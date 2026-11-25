//! Transporte HTTP da fila offline do cliente para o relay local.
//!
//! O adaptador envia somente operações já validadas e persistidas, preservando
//! `(tenant_id, node_id)`, `idempotency_key`, `seq` monotônica, `operation_id` e
//! `content_hash`. Nenhum payload, token ou resposta crua entra em log.
//!
//! Política de rede desta fatia:
//! - toda chamada tem timeout configurável;
//! - retry limitado somente para indisponibilidade (erro de conexão/leitura ou
//!   HTTP 5xx) e timeout, com backoff exponencial e jitter determinístico;
//! - HTTP 409 vira conflito explícito; demais 4xx viram rejeição explícita;
//! - nenhuma rejeição 4xx ou conflito fiscal é repetida;
//! - o transporte não abre TLS: o relay escuta em loopback ou atrás de TLS local,
//!   como exige o contrato de operações.
//!
//! O resultado de cada submissão é aplicado ao `ClientQueue` via
//! `ReconciliationOutcome`, que persiste antes de confirmar em memória. Uma
//! operação já sincronizada não é reenviada, e replay do servidor é sucesso
//! idempotente (`Duplicate`).

use std::time::Duration;

use reqwest::header::{HeaderMap, HeaderValue};
use reqwest::{Client, StatusCode};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use sha2::{Digest, Sha256};
use thiserror::Error;

use crate::{
    ClientError, ClientQueue, OperationDraft, OperationKind, QueuedOperation,
    ReconciliationOutcome, RetryReason, content_hash, operation_id, sanitize_code, validate_draft,
};

/// Caminho do relay que aceita a operação (contrato RELAY-OPERATIONS v1).
pub const OPERATIONS_PATH: &str = "/relay/v1/operations";
/// Cabeçalho de idempotência exigido pelo cliente; o corpo também carrega a chave.
pub const IDEMPOTENCY_HEADER: &str = "idempotency-key";

const TENANT_HEADER: &str = "x-amparius-tenant";
const SUBJECT_HEADER: &str = "x-amparius-subject";
const TOKEN_HEADER: &str = "x-amparius-token";
const NODE_HEADER: &str = "x-amparius-node";

/// Configuração do transporte. `base_url` é a origem do relay, sem o sufixo da
/// rota; `max_attempts` inclui a tentativa inicial e nunca pode ser zero.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct TransportConfig {
    pub base_url: String,
    pub timeout: Duration,
    pub max_attempts: u32,
    pub backoff_base: Duration,
    pub backoff_cap: Duration,
    pub jitter_seed: u64,
}

impl TransportConfig {
    pub fn new(base_url: impl Into<String>) -> Result<Self, TransportError> {
        let base_url = base_url.into();
        Self {
            base_url,
            timeout: Duration::from_secs(10),
            max_attempts: 3,
            backoff_base: Duration::from_millis(200),
            backoff_cap: Duration::from_secs(5),
            jitter_seed: 0x5eed_c0de_1234_5678,
        }
        .validated()
    }

    fn validated(self) -> Result<Self, TransportError> {
        let trimmed = self.base_url.trim();
        if trimmed.is_empty() {
            return Err(TransportError::InvalidConfig("base_url vazia"));
        }
        let lower = trimmed.to_ascii_lowercase();
        if !(lower.starts_with("http://") || lower.starts_with("https://")) {
            return Err(TransportError::InvalidConfig(
                "base_url exige http ou https",
            ));
        }
        if self.timeout.is_zero() {
            return Err(TransportError::InvalidConfig("timeout zero"));
        }
        if self.max_attempts == 0 {
            return Err(TransportError::InvalidConfig("max_attempts zero"));
        }
        Ok(Self {
            base_url: trimmed.trim_end_matches('/').to_owned(),
            ..self
        })
    }

    fn endpoint(&self) -> String {
        format!("{}{}", self.base_url, OPERATIONS_PATH)
    }
}

/// Credencial de transporte. O token só existe em trânsito e nunca é exibido em
/// `Debug`, log ou mensagem de erro.
#[derive(Clone, PartialEq, Eq)]
pub struct Credentials {
    pub subject_id: String,
    pub token: String,
}

impl std::fmt::Debug for Credentials {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        formatter
            .debug_struct("Credentials")
            .field("subject_id", &self.subject_id)
            .field("token", &"<redacted>")
            .finish()
    }
}

impl Credentials {
    pub fn new(
        subject_id: impl Into<String>,
        token: impl Into<String>,
    ) -> Result<Self, TransportError> {
        let subject_id = subject_id.into();
        let token = token.into();
        if subject_id.trim().is_empty() {
            return Err(TransportError::InvalidCredentials("subject_id vazio"));
        }
        if token.trim().is_empty() {
            return Err(TransportError::InvalidCredentials("token vazio"));
        }
        Ok(Self { subject_id, token })
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum SubmitOutcome {
    /// Relay aceitou a operação. `server_cursor` é o `seq` do registro durável.
    /// `authoritative_version` só existe quando o servidor é a autoridade do mestre.
    Accepted {
        server_cursor: u64,
        replayed: bool,
        authoritative_version: Option<u64>,
    },
    /// HTTP 409: mesma chave com conteúdo diferente ou transição inválida.
    Conflict { code: String },
    /// 4xx (exceto 409) ou resposta 2xx ilegível: rejeição sem retry.
    Rejected { status: u16, code: String },
    /// Indisponibilidade ou timeout; pode ser repetido com backoff.
    Transient { reason: RetryReason },
}

/// Resultado de uma submissão com o número de tentativas HTTP usadas.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Submission {
    pub outcome: SubmitOutcome,
    pub attempts: u32,
}

/// Motivo pelo qual uma execução de sincronização parou antes de esvaziar a fila.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum SyncStop {
    TransportUnavailable,
    TransportTimeout,
    /// 401/403: a credencial precisa ser renovada. A operação fica `pending`
    /// intacta e nenhuma revisão de negócio é criada.
    Unauthorized,
}

/// Agregado de uma execução. Contém apenas contagens e códigos técnicos.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct SyncReport {
    pub attempted: u32,
    pub http_attempts: u32,
    pub synced: u32,
    pub duplicates: u32,
    pub conflicts: u32,
    pub rejected: u32,
    pub retried: u32,
    pub remaining_pending: usize,
    pub stopped: Option<SyncStop>,
}

#[derive(Debug, Error, PartialEq, Eq)]
pub enum TransportError {
    #[error("configuracao de transporte invalida: {0}")]
    InvalidConfig(&'static str),
    #[error("credencial de transporte invalida: {0}")]
    InvalidCredentials(&'static str),
    #[error("falha ao montar o cliente HTTP")]
    ClientBuild,
    #[error("falha ao montar a requisicao HTTP")]
    RequestBuild,
    #[error("operacao armazenada inconsistente: {0}")]
    CorruptOperation(&'static str),
}

#[derive(Debug, Error)]
pub enum SyncError {
    #[error(transparent)]
    Transport(#[from] TransportError),
    #[error(transparent)]
    Queue(#[from] ClientError),
}

pub struct HttpTransport {
    client: Client,
    config: TransportConfig,
}

impl HttpTransport {
    pub fn new(config: TransportConfig) -> Result<Self, TransportError> {
        let config = config.validated()?;
        let client = Client::builder()
            .timeout(config.timeout)
            .connect_timeout(config.timeout)
            .redirect(reqwest::redirect::Policy::none())
            .user_agent(concat!("amparius-client/", env!("CARGO_PKG_VERSION")))
            .build()
            .map_err(|_| TransportError::ClientBuild)?;
        Ok(Self { client, config })
    }

    pub fn config(&self) -> &TransportConfig {
        &self.config
    }

    /// Envia uma operação, com retry limitado apenas para falhas transitórias.
    ///
    /// A operação é revalidada (payload, hash e identificador) antes da primeira
    /// tentativa. Divergência é erro fatal e nada é transmitido.
    pub async fn submit(
        &self,
        operation: &QueuedOperation,
        tenant_id: &str,
        node_id: &str,
        credentials: &Credentials,
    ) -> Result<Submission, TransportError> {
        if operation.tenant_id != tenant_id {
            return Err(TransportError::CorruptOperation("tenant divergente"));
        }
        if operation.node_id != node_id {
            return Err(TransportError::CorruptOperation("node divergente"));
        }
        let draft = OperationDraft {
            kind: operation.kind,
            entity_id: operation.entity_id.clone(),
            idempotency_key: operation.idempotency_key.clone(),
            payload: operation.payload.clone(),
        };
        validate_draft(&draft).map_err(|_| TransportError::CorruptOperation("payload invalido"))?;
        let expected_hash = content_hash(&draft)
            .map_err(|_| TransportError::CorruptOperation("hash de conteudo invalido"))?;
        if expected_hash != operation.content_hash {
            return Err(TransportError::CorruptOperation("content_hash divergente"));
        }
        if operation.operation_id != operation_id(tenant_id, node_id, operation.seq, &expected_hash)
        {
            return Err(TransportError::CorruptOperation("operation_id divergente"));
        }

        let request = RequestBody {
            kind: operation.kind.as_str(),
            entity_id: &operation.entity_id,
            idempotency_key: &operation.idempotency_key,
            payload: &operation.payload,
            seq: operation.seq,
            operation_id: &operation.operation_id,
            content_hash: &operation.content_hash,
        };

        let mut attempts = 0;
        let mut last_reason = RetryReason::TransportUnavailable;
        for attempt in 1..=self.config.max_attempts {
            attempts = attempt;
            match self
                .send_once(&request, tenant_id, node_id, credentials)
                .await?
            {
                AttemptResult::Outcome(outcome) => {
                    return Ok(Submission { outcome, attempts });
                }
                AttemptResult::Retry(reason) => {
                    last_reason = reason;
                    if attempt < self.config.max_attempts {
                        let delay = backoff_delay(
                            &self.config,
                            operation.seq,
                            attempt,
                            &operation.operation_id,
                        );
                        tracing::debug!(
                            attempt,
                            delay_ms = delay.as_millis() as u64,
                            code = reason.code(),
                            "retry de transporte agendado"
                        );
                        tokio::time::sleep(delay).await;
                    }
                }
            }
        }
        Ok(Submission {
            outcome: SubmitOutcome::Transient {
                reason: last_reason,
            },
            attempts,
        })
    }

    async fn send_once(
        &self,
        request: &RequestBody<'_>,
        tenant_id: &str,
        node_id: &str,
        credentials: &Credentials,
    ) -> Result<AttemptResult, TransportError> {
        let headers = self.headers(request, tenant_id, node_id, credentials)?;
        let response = match self
            .client
            .post(self.config.endpoint())
            .headers(headers)
            .json(request)
            .send()
            .await
        {
            Ok(response) => response,
            Err(error) => return classify_error(error),
        };

        let status = response.status();
        if status.is_server_error() {
            return Ok(AttemptResult::Retry(RetryReason::TransportUnavailable));
        }

        let body = match response.text().await {
            Ok(body) => body,
            Err(error) => return classify_error(error),
        };
        Ok(AttemptResult::Outcome(classify_body(status, &body)))
    }

    fn headers(
        &self,
        request: &RequestBody<'_>,
        tenant_id: &str,
        node_id: &str,
        credentials: &Credentials,
    ) -> Result<HeaderMap, TransportError> {
        if credentials.subject_id.trim().is_empty() {
            return Err(TransportError::InvalidCredentials("subject_id vazio"));
        }
        if credentials.token.trim().is_empty() {
            return Err(TransportError::InvalidCredentials("token vazio"));
        }
        let mut headers = HeaderMap::new();
        headers.insert(TENANT_HEADER, header_value(tenant_id)?);
        headers.insert(NODE_HEADER, header_value(node_id)?);
        headers.insert(SUBJECT_HEADER, header_value(&credentials.subject_id)?);
        headers.insert(TOKEN_HEADER, header_value(&credentials.token)?);
        headers.insert(IDEMPOTENCY_HEADER, header_value(request.idempotency_key)?);
        Ok(headers)
    }
}

#[derive(Serialize)]
struct RequestBody<'a> {
    kind: &'a str,
    entity_id: &'a str,
    idempotency_key: &'a str,
    payload: &'a Value,
    seq: u64,
    operation_id: &'a str,
    content_hash: &'a str,
}

#[derive(Deserialize)]
struct SuccessBody {
    operation: Option<ServerOperation>,
    #[serde(default)]
    replayed: bool,
}

#[derive(Deserialize)]
struct ServerOperation {
    seq: u64,
    #[serde(default)]
    authoritative_version: Option<u64>,
}

#[derive(Deserialize)]
struct ErrorBody {
    error: Option<ServerError>,
}

#[derive(Deserialize)]
struct ServerError {
    code: Option<String>,
}

enum AttemptResult {
    Outcome(SubmitOutcome),
    Retry(RetryReason),
}

fn header_value(value: &str) -> Result<HeaderValue, TransportError> {
    HeaderValue::from_str(value)
        .map_err(|_| TransportError::InvalidCredentials("cabecalho com caractere invalido"))
}

fn classify_error(error: reqwest::Error) -> Result<AttemptResult, TransportError> {
    if error.is_timeout() {
        return Ok(AttemptResult::Retry(RetryReason::TransportTimeout));
    }
    if error.is_connect() || error.is_body() {
        return Ok(AttemptResult::Retry(RetryReason::TransportUnavailable));
    }
    if error.is_builder() {
        return Err(TransportError::RequestBuild);
    }
    Ok(AttemptResult::Outcome(SubmitOutcome::Rejected {
        status: 0,
        code: "request_error".to_owned(),
    }))
}

fn classify_body(status: StatusCode, body: &str) -> SubmitOutcome {
    if status == StatusCode::CONFLICT {
        return SubmitOutcome::Conflict {
            code: error_code(body).unwrap_or_else(|| "http_409".to_owned()),
        };
    }
    if status.is_client_error() {
        return SubmitOutcome::Rejected {
            status: status.as_u16(),
            code: error_code(body).unwrap_or_else(|| format!("http_{}", status.as_u16())),
        };
    }
    if status.is_success() {
        let parsed = serde_json::from_str::<SuccessBody>(body).ok();
        if let Some(parsed) = parsed {
            if let Some(operation) = parsed.operation {
                return SubmitOutcome::Accepted {
                    server_cursor: operation.seq,
                    replayed: parsed.replayed,
                    authoritative_version: operation.authoritative_version,
                };
            }
        }
        return SubmitOutcome::Rejected {
            status: status.as_u16(),
            code: "invalid_response".to_owned(),
        };
    }
    SubmitOutcome::Rejected {
        status: status.as_u16(),
        code: "unexpected_status".to_owned(),
    }
}

fn error_code(body: &str) -> Option<String> {
    let parsed = serde_json::from_str::<ErrorBody>(body).ok()?;
    let code = parsed.error?.code?;
    let sanitized = sanitize_code(&code);
    if sanitized == "unknown" {
        None
    } else {
        Some(sanitized)
    }
}

/// Backoff exponencial com full jitter determinístico.
///
/// O atraso é derivado de `(jitter_seed, operation_id, seq, attempt)`, portanto o
/// mesmo estado sempre produz o mesmo atraso — testável sem relógio global — e
/// nunca excede `backoff_cap`.
pub fn backoff_delay(
    config: &TransportConfig,
    seq: u64,
    attempt: u32,
    operation_id: &str,
) -> Duration {
    let exponent = attempt.saturating_sub(1).min(20);
    let base = config.backoff_base.as_millis() as u64;
    let cap = config.backoff_cap.as_millis() as u64;
    let nominal = base.saturating_mul(1u64 << exponent).min(cap);
    if nominal == 0 {
        return Duration::ZERO;
    }
    let seed = config.jitter_seed
        ^ stable_seed(operation_id)
        ^ seq.wrapping_mul(0x9e37_79b9_7f4a_7c15)
        ^ u64::from(attempt);
    let spread = splitmix64(seed) % nominal.saturating_add(1);
    Duration::from_millis(spread)
}

fn stable_seed(operation_id: &str) -> u64 {
    let digest = Sha256::digest(operation_id.as_bytes());
    let mut bytes = [0u8; 8];
    bytes.copy_from_slice(&digest[..8]);
    u64::from_be_bytes(bytes)
}

fn splitmix64(mut value: u64) -> u64 {
    value = value.wrapping_add(0x9e37_79b9_7f4a_7c15);
    let mut z = value;
    z = (z ^ (z >> 30)).wrapping_mul(0xbf58_476d_1ce4_e5b9);
    z = (z ^ (z >> 27)).wrapping_mul(0x94d0_49bb_1331_11eb);
    z ^ (z >> 31)
}

/// Sincroniza as operações `pending` em ordem crescente de `seq`.
///
/// Cada resultado é persistido por `ClientQueue::reconcile` antes de o próximo
/// item ser enviado. Em indisponibilidade/timeout a execução para — para não
/// consumir o orçamento de tentativas das demais operações durante uma queda —
/// e a operação corrente permanece `pending` com `attempts` incrementado.
pub async fn sync_pending(
    queue: &mut ClientQueue,
    transport: &HttpTransport,
    credentials: &Credentials,
    now: u64,
) -> Result<SyncReport, SyncError> {
    let mut targets: Vec<(u64, String)> = queue
        .pending()
        .map(|operation| (operation.seq, operation.operation_id.clone()))
        .collect();
    targets.sort_by_key(|(seq, _)| *seq);

    let mut report = SyncReport::default();
    if targets.is_empty() {
        return Ok(report);
    }

    let tenant_id = queue.tenant_id().to_owned();
    let node_id = queue.node_id().to_owned();

    for (_, operation_id) in targets {
        let operation = queue
            .operations()
            .iter()
            .find(|operation| operation.operation_id == operation_id)
            .cloned()
            .ok_or(SyncError::Queue(ClientError::NotFound))?;

        let submission = transport
            .submit(&operation, &tenant_id, &node_id, credentials)
            .await?;
        report.attempted += 1;
        report.http_attempts += submission.attempts;

        match submission.outcome {
            SubmitOutcome::Accepted {
                server_cursor,
                replayed,
                authoritative_version,
            } => {
                let outcome = if operation.kind == OperationKind::MasterUpsert {
                    match authoritative_version {
                        Some(version) => ReconciliationOutcome::MasterApplied {
                            server_cursor,
                            authoritative_version: version,
                        },
                        None => ReconciliationOutcome::Conflict {
                            code: "master_requires_authoritative_version".to_owned(),
                        },
                    }
                } else if replayed {
                    ReconciliationOutcome::Duplicate { server_cursor }
                } else {
                    ReconciliationOutcome::Applied { server_cursor }
                };
                let is_conflict = matches!(outcome, ReconciliationOutcome::Conflict { .. });
                let is_duplicate = matches!(outcome, ReconciliationOutcome::Duplicate { .. });
                queue.reconcile(&operation_id, outcome, now)?;
                if is_conflict {
                    report.conflicts += 1;
                } else if is_duplicate {
                    report.duplicates += 1;
                } else {
                    report.synced += 1;
                }
            }
            SubmitOutcome::Conflict { code } => {
                queue.reconcile(&operation_id, ReconciliationOutcome::Conflict { code }, now)?;
                report.conflicts += 1;
            }
            SubmitOutcome::Rejected { status, code } => {
                if status == 401 || status == 403 {
                    // Credencial recusada não é conflito de negócio: a operação
                    // permanece pendente para uma execução após renovação.
                    report.stopped = Some(SyncStop::Unauthorized);
                    break;
                }
                let safe = format!("http_{status}_{}", sanitize_code(&code));
                let outcome = if operation.kind == OperationKind::FiscalEmission {
                    ReconciliationOutcome::FiscalRejected { code: safe }
                } else {
                    ReconciliationOutcome::Conflict { code: safe }
                };
                queue.reconcile(&operation_id, outcome, now)?;
                report.rejected += 1;
            }
            SubmitOutcome::Transient { reason } => {
                queue.reconcile(&operation_id, ReconciliationOutcome::Retry { reason }, now)?;
                report.retried += 1;
                report.stopped = Some(match reason {
                    RetryReason::TransportTimeout => SyncStop::TransportTimeout,
                    RetryReason::TransportUnavailable => SyncStop::TransportUnavailable,
                });
                break;
            }
        }
    }

    report.remaining_pending = queue.pending().count();
    Ok(report)
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;
    use std::path::{Path, PathBuf};
    use std::sync::{Arc, Mutex};
    use std::time::{SystemTime, UNIX_EPOCH};
    use tokio::io::{AsyncReadExt, AsyncWriteExt};
    use tokio::net::TcpListener;
    use tokio::task::JoinHandle;

    struct MockResponse {
        status: u16,
        body: String,
        delay: Duration,
    }

    impl MockResponse {
        fn json(status: u16, body: &str) -> Self {
            Self {
                status,
                body: body.to_owned(),
                delay: Duration::ZERO,
            }
        }

        fn accepted(seq: u64, replayed: bool) -> Self {
            Self::json(
                if replayed { 200 } else { 201 },
                &json!({
                    "operation": { "seq": seq, "authoritative_version": null },
                    "replayed": replayed
                })
                .to_string(),
            )
        }

        fn error(status: u16, code: &str) -> Self {
            Self::json(status, &json!({ "error": { "code": code } }).to_string())
        }

        fn delayed(mut self, delay: Duration) -> Self {
            self.delay = delay;
            self
        }
    }

    #[derive(Debug, Clone)]
    struct CapturedRequest {
        path: String,
        headers: Vec<(String, String)>,
        body: String,
    }

    impl CapturedRequest {
        fn header(&self, name: &str) -> Option<&str> {
            self.headers
                .iter()
                .find(|(key, _)| key == name)
                .map(|(_, value)| value.as_str())
        }
    }

    struct MockServer {
        base_url: String,
        captured: Arc<Mutex<Vec<CapturedRequest>>>,
        handle: JoinHandle<()>,
    }

    impl MockServer {
        async fn spawn(responses: Vec<MockResponse>) -> Self {
            let listener = TcpListener::bind("127.0.0.1:0").await.expect("bind");
            let address = listener.local_addr().expect("addr");
            let captured = Arc::new(Mutex::new(Vec::new()));
            let captured_task = Arc::clone(&captured);
            let script = Arc::new(Mutex::new(responses.into_iter()));
            let handle = tokio::spawn(async move {
                loop {
                    let Ok((mut socket, _)) = listener.accept().await else {
                        break;
                    };
                    let next = script.lock().expect("script").next();
                    let Some(response) = next else {
                        break;
                    };
                    let captured = Arc::clone(&captured_task);
                    tokio::spawn(async move {
                        let Some((head, body)) = read_request(&mut socket).await else {
                            return;
                        };
                        captured
                            .lock()
                            .expect("captured")
                            .push(parse_request(&head, &body));
                        if !response.delay.is_zero() {
                            tokio::time::sleep(response.delay).await;
                        }
                        let reason = reason_phrase(response.status);
                        let output = format!(
                            "HTTP/1.1 {} {}\r\ncontent-type: application/json\r\ncontent-length: {}\r\nconnection: close\r\n\r\n{}",
                            response.status,
                            reason,
                            response.body.len(),
                            response.body
                        );
                        let _ = socket.write_all(output.as_bytes()).await;
                        let _ = socket.flush().await;
                    });
                }
            });
            Self {
                base_url: format!("http://{address}"),
                captured,
                handle,
            }
        }

        fn requests(&self) -> Vec<CapturedRequest> {
            self.captured.lock().expect("captured").clone()
        }
    }

    impl Drop for MockServer {
        fn drop(&mut self) {
            self.handle.abort();
        }
    }

    async fn read_request(socket: &mut tokio::net::TcpStream) -> Option<(String, String)> {
        let mut buffer = Vec::new();
        let mut chunk = [0u8; 2048];
        loop {
            let read = socket.read(&mut chunk).await.ok()?;
            if read == 0 {
                return None;
            }
            buffer.extend_from_slice(&chunk[..read]);
            if let Some(position) = find(&buffer, b"\r\n\r\n") {
                let head = String::from_utf8_lossy(&buffer[..position]).to_string();
                let mut body = buffer[position + 4..].to_vec();
                let content_length = parse_content_length(&head);
                while body.len() < content_length {
                    let read = socket.read(&mut chunk).await.ok()?;
                    if read == 0 {
                        break;
                    }
                    body.extend_from_slice(&chunk[..read]);
                }
                return Some((head, String::from_utf8_lossy(&body).to_string()));
            }
        }
    }

    fn find(haystack: &[u8], needle: &[u8]) -> Option<usize> {
        haystack
            .windows(needle.len())
            .position(|window| window == needle)
    }

    fn parse_content_length(head: &str) -> usize {
        head.lines()
            .find_map(|line| {
                line.to_ascii_lowercase()
                    .strip_prefix("content-length:")
                    .map(|value| value.trim().parse::<usize>().unwrap_or(0))
            })
            .unwrap_or(0)
    }

    fn parse_request(head: &str, body: &str) -> CapturedRequest {
        let mut lines = head.lines();
        let request_line = lines.next().unwrap_or_default();
        let path = request_line
            .split_whitespace()
            .nth(1)
            .unwrap_or_default()
            .to_owned();
        let headers = lines
            .filter_map(|line| {
                let (key, value) = line.split_once(':')?;
                Some((key.trim().to_ascii_lowercase(), value.trim().to_owned()))
            })
            .collect();
        CapturedRequest {
            path,
            headers,
            body: body.to_owned(),
        }
    }

    fn reason_phrase(status: u16) -> &'static str {
        match status {
            200 => "OK",
            201 => "Created",
            400 => "Bad Request",
            401 => "Unauthorized",
            409 => "Conflict",
            413 => "Payload Too Large",
            422 => "Unprocessable Entity",
            429 => "Too Many Requests",
            500 => "Internal Server Error",
            503 => "Service Unavailable",
            _ => "OK",
        }
    }

    fn temp_queue_path() -> PathBuf {
        let nonce = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .expect("clock")
            .as_nanos();
        std::env::temp_dir().join(format!("amparius-client-transport-{nonce}.json"))
    }

    fn cleanup(path: &Path) {
        let _ = std::fs::remove_file(path);
        let _ = std::fs::remove_file(path.with_extension("tmp"));
        let _ = std::fs::remove_file(path.with_extension("bak"));
    }

    fn config(base_url: &str, timeout: Duration, max_attempts: u32) -> TransportConfig {
        let mut config = TransportConfig::new(base_url).expect("config");
        config.timeout = timeout;
        config.max_attempts = max_attempts;
        config.backoff_base = Duration::from_millis(1);
        config.backoff_cap = Duration::from_millis(5);
        config.jitter_seed = 42;
        config
    }

    fn credentials() -> Credentials {
        Credentials::new("operator-1", "tkn-secret").expect("credentials")
    }

    fn contract(key: &str, total_cents: i64) -> OperationDraft {
        OperationDraft {
            kind: OperationKind::ContractCreate,
            entity_id: "contract-1".to_owned(),
            idempotency_key: key.to_owned(),
            payload: json!({"total_cents": total_cents, "currency": "BRL"}),
        }
    }

    fn fiscal(key: &str) -> OperationDraft {
        OperationDraft {
            kind: OperationKind::FiscalEmission,
            entity_id: "fiscal-1".to_owned(),
            idempotency_key: key.to_owned(),
            payload: json!({"document_ref": "dps-1"}),
        }
    }

    #[test]
    fn backoff_is_deterministic_bounded_and_varies_by_attempt() {
        let config = config("http://127.0.0.1:1", Duration::from_secs(1), 3);
        let first = backoff_delay(&config, 1, 1, "op_abc");
        let again = backoff_delay(&config, 1, 1, "op_abc");
        assert_eq!(first, again);
        assert!(first <= config.backoff_cap);

        let mut seen = std::collections::HashSet::new();
        for attempt in 1..=8 {
            let delay = backoff_delay(&config, 7, attempt, "op_abc");
            assert!(delay <= config.backoff_cap);
            seen.insert(delay.as_millis());
        }
        assert!(seen.len() > 1, "jitter deve variar entre tentativas");
    }

    #[tokio::test]
    async fn headers_preserve_context_and_idempotency_without_pii() {
        let server = MockServer::spawn(vec![MockResponse::accepted(1, false)]).await;
        let transport = HttpTransport::new(config(&server.base_url, Duration::from_secs(2), 1))
            .expect("transport");
        let mut queue = ClientQueue::new("tenant-a", "node-1").expect("queue");
        let enqueued = queue
            .enqueue("operator-1", contract("op-1", 15_000), 10)
            .expect("enqueue");

        let report = sync_pending(&mut queue, &transport, &credentials(), 20)
            .await
            .expect("sync");
        assert_eq!(report.synced, 1);
        assert_eq!(queue.operations()[0].status, crate::OperationStatus::Synced);

        let requests = server.requests();
        assert_eq!(requests.len(), 1);
        let request = &requests[0];
        assert_eq!(request.path, OPERATIONS_PATH);
        assert_eq!(request.header(TENANT_HEADER), Some("tenant-a"));
        assert_eq!(request.header(NODE_HEADER), Some("node-1"));
        assert_eq!(request.header(SUBJECT_HEADER), Some("operator-1"));
        assert_eq!(request.header(TOKEN_HEADER), Some("tkn-secret"));
        assert_eq!(request.header(IDEMPOTENCY_HEADER), Some("op-1"));

        let body: Value = serde_json::from_str(&request.body).expect("body json");
        assert_eq!(body["seq"], json!(1));
        assert_eq!(body["idempotency_key"], json!("op-1"));
        assert_eq!(body["operation_id"], json!(enqueued.operation.operation_id));
        assert_eq!(body["content_hash"], json!(enqueued.operation.content_hash));
        assert_eq!(body["payload"]["total_cents"], json!(15_000));
        let text = request.body.to_ascii_lowercase();
        assert!(!text.contains("cpf"));
        assert!(!text.contains("nome"));
    }

    #[tokio::test]
    async fn retries_transient_5xx_then_syncs_without_duplicate_operation() {
        let server = MockServer::spawn(vec![
            MockResponse::error(503, "store_unavailable"),
            MockResponse::error(500, "internal"),
            MockResponse::accepted(5, false),
        ])
        .await;
        let transport = HttpTransport::new(config(&server.base_url, Duration::from_secs(2), 3))
            .expect("transport");
        let mut queue = ClientQueue::new("tenant-a", "node-1").expect("queue");
        queue
            .enqueue("operator-1", contract("op-1", 100), 10)
            .expect("enqueue");

        let report = sync_pending(&mut queue, &transport, &credentials(), 20)
            .await
            .expect("sync");
        assert_eq!(report.synced, 1);
        assert_eq!(report.http_attempts, 3);
        assert_eq!(report.stopped, None);
        assert_eq!(server.requests().len(), 3);
        assert_eq!(queue.operations().len(), 1);
        assert_eq!(queue.operations()[0].server_cursor, Some(5));
        assert_eq!(queue.operations()[0].attempts, 0);
    }

    #[tokio::test]
    async fn timeout_retries_only_up_to_limit_and_keeps_operation_pending() {
        let server = MockServer::spawn(vec![
            MockResponse::accepted(1, false).delayed(Duration::from_millis(400)),
            MockResponse::accepted(1, false).delayed(Duration::from_millis(400)),
        ])
        .await;
        let transport = HttpTransport::new(config(&server.base_url, Duration::from_millis(60), 2))
            .expect("transport");
        let mut queue = ClientQueue::new("tenant-a", "node-1").expect("queue");
        queue
            .enqueue("operator-1", contract("op-1", 100), 10)
            .expect("enqueue");

        let report = sync_pending(&mut queue, &transport, &credentials(), 20)
            .await
            .expect("sync");
        assert_eq!(report.retried, 1);
        assert_eq!(report.synced, 0);
        assert_eq!(report.http_attempts, 2);
        assert_eq!(report.stopped, Some(SyncStop::TransportTimeout));
        assert_eq!(report.remaining_pending, 1);
        assert_eq!(
            queue.operations()[0].status,
            crate::OperationStatus::Pending
        );
        assert_eq!(queue.operations()[0].attempts, 1);
        assert_eq!(
            queue.operations()[0].last_error.as_deref(),
            Some("sync_retry:transport_timeout")
        );
        assert_eq!(server.requests().len(), 2);
    }

    #[tokio::test]
    async fn client_rejection_4xx_is_not_retried_and_creates_review() {
        let server = MockServer::spawn(vec![MockResponse::error(422, "invalid_request")]).await;
        let transport = HttpTransport::new(config(&server.base_url, Duration::from_secs(2), 3))
            .expect("transport");
        let mut queue = ClientQueue::new("tenant-a", "node-1").expect("queue");
        queue
            .enqueue("operator-1", contract("op-1", 100), 10)
            .expect("enqueue");

        let report = sync_pending(&mut queue, &transport, &credentials(), 20)
            .await
            .expect("sync");
        assert_eq!(report.rejected, 1);
        assert_eq!(report.http_attempts, 1);
        assert_eq!(server.requests().len(), 1);
        assert_eq!(
            queue.operations()[0].status,
            crate::OperationStatus::Conflict
        );
        assert_eq!(
            queue.review_tasks()[0].kind,
            crate::ReviewTaskKind::MoneyConflict
        );
        assert!(queue.review_tasks()[0].code.contains("invalid_request"));
        assert!(!queue.review_tasks()[0].code.contains('<'));
    }

    #[tokio::test]
    async fn fiscal_4xx_is_fiscal_rejected_without_retry() {
        let server = MockServer::spawn(vec![MockResponse::error(400, "invalid_request")]).await;
        let transport = HttpTransport::new(config(&server.base_url, Duration::from_secs(2), 3))
            .expect("transport");
        let mut queue = ClientQueue::new("tenant-a", "node-1").expect("queue");
        queue
            .enqueue("operator-1", fiscal("fiscal-1"), 10)
            .expect("enqueue");

        let report = sync_pending(&mut queue, &transport, &credentials(), 20)
            .await
            .expect("sync");
        assert_eq!(report.rejected, 1);
        assert_eq!(server.requests().len(), 1);
        assert_eq!(queue.operations()[0].status, crate::OperationStatus::Failed);
        assert_eq!(
            queue.review_tasks()[0].kind,
            crate::ReviewTaskKind::FiscalFailure
        );
    }

    #[tokio::test]
    async fn conflict_409_is_explicit_and_never_retried() {
        let server =
            MockServer::spawn(vec![MockResponse::error(409, "idempotency_conflict")]).await;
        let transport = HttpTransport::new(config(&server.base_url, Duration::from_secs(2), 3))
            .expect("transport");
        let mut queue = ClientQueue::new("tenant-a", "node-1").expect("queue");
        queue
            .enqueue("operator-1", contract("op-1", 100), 10)
            .expect("enqueue");

        let report = sync_pending(&mut queue, &transport, &credentials(), 20)
            .await
            .expect("sync");
        assert_eq!(report.conflicts, 1);
        assert_eq!(report.http_attempts, 1);
        assert_eq!(server.requests().len(), 1);
        assert_eq!(
            queue.operations()[0].status,
            crate::OperationStatus::Conflict
        );
        assert_eq!(queue.operations()[0].payload["total_cents"], json!(100));
        assert_eq!(
            queue.review_tasks()[0].kind,
            crate::ReviewTaskKind::MoneyConflict
        );
    }

    #[tokio::test]
    async fn duplicate_server_response_is_idempotent_success() {
        let server = MockServer::spawn(vec![MockResponse::accepted(2, true)]).await;
        let transport = HttpTransport::new(config(&server.base_url, Duration::from_secs(2), 1))
            .expect("transport");
        let mut queue = ClientQueue::new("tenant-a", "node-1").expect("queue");
        queue
            .enqueue("operator-1", contract("op-1", 100), 10)
            .expect("enqueue");
        let original_len = queue.operations().len();

        let report = sync_pending(&mut queue, &transport, &credentials(), 20)
            .await
            .expect("sync");
        assert_eq!(report.duplicates, 1);
        assert_eq!(report.synced, 0);
        assert_eq!(queue.operations().len(), original_len);
        assert_eq!(queue.operations()[0].status, crate::OperationStatus::Synced);
        assert_eq!(queue.operations()[0].server_cursor, Some(2));
    }

    #[tokio::test]
    async fn master_accept_without_authoritative_version_requires_review() {
        let server = MockServer::spawn(vec![MockResponse::accepted(3, false)]).await;
        let transport = HttpTransport::new(config(&server.base_url, Duration::from_secs(2), 1))
            .expect("transport");
        let mut queue = ClientQueue::new("tenant-a", "node-1").expect("queue");
        let master = OperationDraft {
            kind: OperationKind::MasterUpsert,
            entity_id: "catalog-1".to_owned(),
            idempotency_key: "master-1".to_owned(),
            payload: json!({"base_version": 7, "label_ref": "label-1"}),
        };
        queue.enqueue("operator-1", master, 10).expect("enqueue");

        let report = sync_pending(&mut queue, &transport, &credentials(), 20)
            .await
            .expect("sync");
        assert_eq!(report.conflicts, 1);
        assert_eq!(report.synced, 0);
        assert_eq!(
            queue.operations()[0].status,
            crate::OperationStatus::Conflict
        );
        assert_eq!(
            queue.review_tasks()[0].kind,
            crate::ReviewTaskKind::MasterConflict
        );
    }

    #[tokio::test]
    async fn unauthorized_stops_without_conflict_or_data_loss() {
        let server = MockServer::spawn(vec![MockResponse::error(401, "invalid_credential")]).await;
        let transport = HttpTransport::new(config(&server.base_url, Duration::from_secs(2), 1))
            .expect("transport");
        let mut queue = ClientQueue::new("tenant-a", "node-1").expect("queue");
        queue
            .enqueue("operator-1", contract("op-1", 100), 10)
            .expect("enqueue");

        let report = sync_pending(&mut queue, &transport, &credentials(), 20)
            .await
            .expect("sync");
        assert_eq!(report.stopped, Some(SyncStop::Unauthorized));
        assert_eq!(report.conflicts, 0);
        assert_eq!(report.rejected, 0);
        assert_eq!(report.remaining_pending, 1);
        assert_eq!(
            queue.operations()[0].status,
            crate::OperationStatus::Pending
        );
        assert_eq!(queue.operations()[0].attempts, 0);
        assert!(queue.review_tasks().is_empty());
    }

    #[tokio::test]
    async fn synced_operation_is_persisted_and_not_resent_after_reopen() {
        let path = temp_queue_path();
        let server = MockServer::spawn(vec![MockResponse::accepted(1, false)]).await;
        let transport = HttpTransport::new(config(&server.base_url, Duration::from_secs(2), 1))
            .expect("transport");
        {
            let mut queue = ClientQueue::open(&path, "tenant-a", "node-1").expect("open");
            queue
                .enqueue("operator-1", contract("op-1", 100), 10)
                .expect("enqueue");
            let report = sync_pending(&mut queue, &transport, &credentials(), 20)
                .await
                .expect("sync");
            assert_eq!(report.synced, 1);
        }

        let mut reopened = ClientQueue::open(&path, "tenant-a", "node-1").expect("reopen");
        assert_eq!(reopened.status().queue.synced, 1);
        assert_eq!(reopened.status().queue.pending, 0);
        let second = sync_pending(&mut reopened, &transport, &credentials(), 30)
            .await
            .expect("second sync");
        assert_eq!(second.attempted, 0);
        assert_eq!(second.remaining_pending, 0);
        assert_eq!(server.requests().len(), 1);
        assert_eq!(reopened.operations().len(), 1);
        cleanup(&path);
    }

    #[tokio::test]
    async fn invalid_credentials_fail_before_any_network_call() {
        let server = MockServer::spawn(vec![MockResponse::accepted(1, false)]).await;
        let transport = HttpTransport::new(config(&server.base_url, Duration::from_secs(2), 1))
            .expect("transport");
        let mut queue = ClientQueue::new("tenant-a", "node-1").expect("queue");
        let enqueued = queue
            .enqueue("operator-1", contract("op-1", 100), 10)
            .expect("enqueue");
        let empty = Credentials {
            subject_id: "operator-1".to_owned(),
            token: String::new(),
        };
        let error = transport
            .submit(
                &enqueued.operation,
                queue.tenant_id(),
                queue.node_id(),
                &empty,
            )
            .await;
        assert_eq!(
            error,
            Err(TransportError::InvalidCredentials("token vazio"))
        );
        assert!(server.requests().is_empty());
        assert_eq!(
            format!("{:?}", credentials()),
            "Credentials { subject_id: \"operator-1\", token: \"<redacted>\" }"
        );
    }
}
