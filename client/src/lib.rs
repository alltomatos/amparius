pub mod transport;
pub mod ui;

use serde::{Deserialize, Serialize};
use serde_json::{Map, Value};
use sha2::{Digest, Sha256};
use std::collections::HashSet;
use std::fs;
use std::io::{self, Write};
use std::path::{Path, PathBuf};
use std::time::{SystemTime, UNIX_EPOCH};
use thiserror::Error;

const DEFAULT_RETRY_LIMIT: u32 = 3;
const STORE_SCHEMA_VERSION: u16 = 1;
const MAX_IDENTIFIER_LEN: usize = 128;
const MAX_REASON_LEN: usize = 200;
const MAX_PAYLOAD_BYTES: usize = 64 * 1024;
const MAX_PAYLOAD_DEPTH: usize = 24;

/// Estado de conectividade exibido pelo cliente. A fila permanece disponível nos
/// três estados; contingência é deliberadamente distinta de uma simples queda.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum ConnectivityState {
    Online,
    Offline,
    Contingency,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum OperationKind {
    MasterUpsert,
    ContractCreate,
    FuneralJourneyCreate,
    FuneralServiceOrderAdvance,
    FuneralServiceOrderPause,
    FuneralServiceOrderComplete,
    CashOpen,
    CashMovement,
    CashClose,
    FiscalEmission,
}

impl OperationKind {
    pub const fn as_str(self) -> &'static str {
        match self {
            Self::MasterUpsert => "master_upsert",
            Self::ContractCreate => "contract_create",
            Self::FuneralJourneyCreate => "funeral_journey_create",
            Self::FuneralServiceOrderAdvance => "funeral_service_order_advance",
            Self::FuneralServiceOrderPause => "funeral_service_order_pause",
            Self::FuneralServiceOrderComplete => "funeral_service_order_complete",
            Self::CashOpen => "cash_open",
            Self::CashMovement => "cash_movement",
            Self::CashClose => "cash_close",
            Self::FiscalEmission => "fiscal_emission",
        }
    }

    pub const fn domain(self) -> OperationDomain {
        match self {
            Self::MasterUpsert => OperationDomain::Master,
            Self::ContractCreate | Self::CashOpen | Self::CashMovement | Self::CashClose => {
                OperationDomain::Money
            }
            Self::FuneralJourneyCreate
            | Self::FuneralServiceOrderAdvance
            | Self::FuneralServiceOrderPause
            | Self::FuneralServiceOrderComplete => OperationDomain::Funeral,
            Self::FiscalEmission => OperationDomain::Fiscal,
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum OperationDomain {
    Master,
    Money,
    Funeral,
    Fiscal,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum OperationStatus {
    Pending,
    Synced,
    Failed,
    Conflict,
}

#[derive(Debug, Clone, PartialEq)]
pub struct OperationDraft {
    pub kind: OperationKind,
    pub entity_id: String,
    pub idempotency_key: String,
    pub payload: Value,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct QueuedOperation {
    pub operation_id: String,
    pub tenant_id: String,
    pub node_id: String,
    pub seq: u64,
    pub kind: OperationKind,
    pub entity_id: String,
    pub idempotency_key: String,
    pub payload: Value,
    pub content_hash: String,
    pub status: OperationStatus,
    pub created_at: u64,
    pub updated_at: u64,
    pub attempts: u32,
    pub last_error: Option<String>,
    pub server_cursor: Option<u64>,
    pub authoritative_version: Option<u64>,
}

#[derive(Debug, Clone, PartialEq)]
pub struct EnqueueResult {
    pub operation: QueuedOperation,
    pub replayed: bool,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum RetryReason {
    TransportUnavailable,
    TransportTimeout,
}

impl RetryReason {
    pub const fn code(self) -> &'static str {
        match self {
            Self::TransportUnavailable => "transport_unavailable",
            Self::TransportTimeout => "transport_timeout",
        }
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum ReconciliationOutcome {
    Applied {
        server_cursor: u64,
    },
    MasterApplied {
        server_cursor: u64,
        authoritative_version: u64,
    },
    Duplicate {
        server_cursor: u64,
    },
    Retry {
        reason: RetryReason,
    },
    FiscalRejected {
        code: String,
    },
    Conflict {
        code: String,
    },
    OutOfOrder {
        expected_seq: u64,
    },
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ReconciliationResult {
    pub status: OperationStatus,
    pub review_task_created: bool,
    pub terminal: bool,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum ReviewTaskKind {
    SyncFailure,
    FuneralConflict,
    MasterConflict,
    MoneyConflict,
    FiscalConflict,
    FiscalFailure,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct ReviewTask {
    pub task_id: String,
    pub operation_id: String,
    pub kind: ReviewTaskKind,
    pub code: String,
    pub status: ReviewTaskStatus,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum ReviewTaskStatus {
    Open,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct QueueSummary {
    pub pending: usize,
    pub synced: usize,
    pub failed: usize,
    pub conflict: usize,
    pub retrying: usize,
    pub total: usize,
    pub last_seq: u64,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct ClientStatus {
    pub connectivity: ConnectivityState,
    pub queue: QueueSummary,
    pub review_tasks_open: usize,
}

#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct ClientMetrics {
    pub enqueued: u64,
    pub replayed: u64,
    pub synced: u64,
    pub retried: u64,
    pub failed: u64,
    pub conflicts: u64,
    pub review_tasks: u64,
}

#[derive(Debug, Serialize, Deserialize)]
struct PersistedQueue {
    schema_version: u16,
    tenant_id: String,
    node_id: String,
    connectivity: ConnectivityState,
    retry_limit: u32,
    last_seq: u64,
    operations: Vec<QueuedOperation>,
    review_tasks: Vec<ReviewTask>,
    metrics: ClientMetrics,
    integrity_hash: String,
}

#[derive(Debug, Serialize)]
struct QueueIntegrity<'a> {
    schema_version: u16,
    tenant_id: &'a str,
    node_id: &'a str,
    connectivity: ConnectivityState,
    retry_limit: u32,
    last_seq: u64,
    operations: &'a [QueuedOperation],
    review_tasks: &'a [ReviewTask],
    metrics: &'a ClientMetrics,
}

#[derive(Debug, Error, PartialEq, Eq)]
pub enum ClientError {
    #[error("tenant_id vazio")]
    EmptyTenant,
    #[error("node_id vazio")]
    EmptyNode,
    #[error("actor_subject_id vazio")]
    EmptyActor,
    #[error("entity_id vazio")]
    EmptyEntity,
    #[error("idempotency_key vazia")]
    EmptyIdempotencyKey,
    #[error("identificador local invalido")]
    InvalidIdentifier,
    #[error("payload da operacao invalido: {0}")]
    InvalidPayload(&'static str),
    #[error("payload excede o limite local")]
    PayloadTooLarge,
    #[error("payload contem dado pessoal; envie apenas referencias opacas")]
    PersonalDataInPayload,
    #[error("operacao com a mesma chave e conteudo diferente")]
    IdempotencyConflict,
    #[error("operacao nao encontrada")]
    NotFound,
    #[error("transicao de reconciliacao invalida")]
    InvalidReconciliation,
    #[error("limite de retry invalido")]
    InvalidRetryLimit,
    #[error("sequencia do dispositivo esgotada")]
    SequenceExhausted,
    #[error("falha de I/O no store local: {0}")]
    Io(String),
    #[error("store local invalido: {0}")]
    Serialization(String),
    #[error("schema do store local nao suportado: {0}")]
    UnsupportedStoreVersion(u16),
    #[error("store local corrompido: {0}")]
    CorruptStore(&'static str),
    #[error("tenant_id do store local nao corresponde ao contexto")]
    TenantMismatch,
    #[error("node_id do store local nao corresponde ao contexto")]
    NodeMismatch,
}

#[derive(Debug, Clone)]
pub struct ClientQueue {
    path: Option<PathBuf>,
    tenant_id: String,
    node_id: String,
    connectivity: ConnectivityState,
    retry_limit: u32,
    last_seq: u64,
    operations: Vec<QueuedOperation>,
    review_tasks: Vec<ReviewTask>,
    metrics: ClientMetrics,
}

impl ClientQueue {
    pub fn new(
        tenant_id: impl Into<String>,
        node_id: impl Into<String>,
    ) -> Result<Self, ClientError> {
        Self::with_retry_limit(tenant_id, node_id, DEFAULT_RETRY_LIMIT)
    }

    pub fn with_retry_limit(
        tenant_id: impl Into<String>,
        node_id: impl Into<String>,
        retry_limit: u32,
    ) -> Result<Self, ClientError> {
        let tenant_id = tenant_id.into();
        let node_id = node_id.into();
        if tenant_id.trim().is_empty() {
            return Err(ClientError::EmptyTenant);
        }
        if node_id.trim().is_empty() {
            return Err(ClientError::EmptyNode);
        }
        if retry_limit == 0 {
            return Err(ClientError::InvalidRetryLimit);
        }
        Ok(Self {
            path: None,
            tenant_id,
            node_id,
            connectivity: ConnectivityState::Online,
            retry_limit,
            last_seq: 0,
            operations: Vec::new(),
            review_tasks: Vec::new(),
            metrics: ClientMetrics::default(),
        })
    }

    /// Abre uma fila persistida no arquivo local indicado.
    ///
    /// O arquivo é criado na primeira abertura. O contexto de tenant e dispositivo
    /// é obrigatório para impedir que uma fila seja reaberta sob outra identidade.
    pub fn open(
        path: impl AsRef<Path>,
        tenant_id: impl Into<String>,
        node_id: impl Into<String>,
    ) -> Result<Self, ClientError> {
        Self::open_with_retry_limit(path, tenant_id, node_id, DEFAULT_RETRY_LIMIT)
    }

    pub fn open_with_retry_limit(
        path: impl AsRef<Path>,
        tenant_id: impl Into<String>,
        node_id: impl Into<String>,
        retry_limit: u32,
    ) -> Result<Self, ClientError> {
        let path = path.as_ref().to_path_buf();
        let tenant_id = tenant_id.into();
        let node_id = node_id.into();
        let mut queue = Self::with_retry_limit(&tenant_id, &node_id, retry_limit)?;
        recover_backup(&path).map_err(io_error)?;

        if path.exists() {
            let bytes = fs::read(&path).map_err(io_error)?;
            let persisted: PersistedQueue = serde_json::from_slice(&bytes)
                .map_err(|error| ClientError::Serialization(error.to_string()))?;
            if persisted.schema_version != STORE_SCHEMA_VERSION {
                return Err(ClientError::UnsupportedStoreVersion(
                    persisted.schema_version,
                ));
            }
            if persisted.tenant_id != tenant_id {
                return Err(ClientError::TenantMismatch);
            }
            if persisted.node_id != node_id {
                return Err(ClientError::NodeMismatch);
            }
            if persisted.integrity_hash != persisted_integrity_hash(&persisted)? {
                return Err(ClientError::CorruptStore("hash de integridade"));
            }
            validate_persisted_queue(&persisted)?;
            queue.path = Some(path);
            queue.connectivity = persisted.connectivity;
            queue.retry_limit = persisted.retry_limit;
            queue.last_seq = persisted.last_seq;
            queue.operations = persisted.operations;
            queue.review_tasks = persisted.review_tasks;
            queue.metrics = persisted.metrics;
        } else {
            queue.path = Some(path);
            queue.persist_if_configured()?;
        }

        Ok(queue)
    }

    pub fn set_connectivity(&mut self, state: ConnectivityState) -> Result<(), ClientError> {
        let before = self.clone();
        self.connectivity = state;
        if let Err(error) = self.persist_if_configured() {
            *self = before;
            return Err(error);
        }
        Ok(())
    }

    pub const fn connectivity(&self) -> ConnectivityState {
        self.connectivity
    }

    /// Identidade do tenant fixada na abertura. Usada pelo transporte para
    /// preservar o contexto e nunca aceitar tenant vindo do corpo da requisição.
    pub fn tenant_id(&self) -> &str {
        &self.tenant_id
    }

    /// Identidade do dispositivo fixada na abertura; a sequência é monotônica
    /// por `(tenant_id, node_id)`.
    pub fn node_id(&self) -> &str {
        &self.node_id
    }

    pub fn enqueue(
        &mut self,
        actor_subject_id: impl Into<String>,
        draft: OperationDraft,
        now: u64,
    ) -> Result<EnqueueResult, ClientError> {
        let before = self.clone();
        let actor_subject_id = actor_subject_id.into();
        if actor_subject_id.trim().is_empty() {
            return Err(ClientError::EmptyActor);
        }
        validate_draft(&draft)?;
        let content_hash = content_hash(&draft)?;

        if let Some(existing) = self
            .operations
            .iter()
            .find(|operation| operation.idempotency_key == draft.idempotency_key)
        {
            if existing.content_hash != content_hash {
                return Err(ClientError::IdempotencyConflict);
            }
            self.metrics.replayed += 1;
            let result = EnqueueResult {
                operation: existing.clone(),
                replayed: true,
            };
            if let Err(error) = self.persist_if_configured() {
                *self = before;
                return Err(error);
            }
            return Ok(result);
        }

        let seq = self
            .last_seq
            .checked_add(1)
            .ok_or(ClientError::SequenceExhausted)?;
        self.last_seq = seq;
        let operation = QueuedOperation {
            operation_id: operation_id(&self.tenant_id, &self.node_id, seq, &content_hash),
            tenant_id: self.tenant_id.clone(),
            node_id: self.node_id.clone(),
            seq,
            kind: draft.kind,
            entity_id: draft.entity_id,
            idempotency_key: draft.idempotency_key,
            payload: draft.payload,
            content_hash,
            status: OperationStatus::Pending,
            created_at: now,
            updated_at: now,
            attempts: 0,
            last_error: None,
            server_cursor: None,
            authoritative_version: None,
        };
        self.operations.push(operation.clone());
        self.metrics.enqueued += 1;
        let result = EnqueueResult {
            operation,
            replayed: false,
        };
        if let Err(error) = self.persist_if_configured() {
            *self = before;
            return Err(error);
        }
        Ok(result)
    }

    pub fn pending(&self) -> impl Iterator<Item = &QueuedOperation> {
        self.operations
            .iter()
            .filter(|operation| operation.status == OperationStatus::Pending)
    }

    pub fn operations(&self) -> &[QueuedOperation] {
        &self.operations
    }

    pub fn review_tasks(&self) -> &[ReviewTask] {
        &self.review_tasks
    }

    pub const fn metrics(&self) -> &ClientMetrics {
        &self.metrics
    }

    pub fn status(&self) -> ClientStatus {
        let mut summary = QueueSummary {
            pending: 0,
            synced: 0,
            failed: 0,
            conflict: 0,
            retrying: 0,
            total: self.operations.len(),
            last_seq: self.last_seq,
        };
        for operation in &self.operations {
            match operation.status {
                OperationStatus::Pending => {
                    summary.pending += 1;
                    if operation.attempts > 0 {
                        summary.retrying += 1;
                    }
                }
                OperationStatus::Synced => summary.synced += 1,
                OperationStatus::Failed => summary.failed += 1,
                OperationStatus::Conflict => summary.conflict += 1,
            }
        }
        ClientStatus {
            connectivity: self.connectivity,
            queue: summary,
            review_tasks_open: self.review_tasks.len(),
        }
    }

    pub fn reconcile(
        &mut self,
        operation_id: &str,
        outcome: ReconciliationOutcome,
        now: u64,
    ) -> Result<ReconciliationResult, ClientError> {
        let before = self.clone();
        let result = self.reconcile_inner(operation_id, outcome, now);
        match result {
            Ok(result) => {
                if let Err(error) = self.persist_if_configured() {
                    *self = before;
                    return Err(error);
                }
                Ok(result)
            }
            Err(error) => {
                *self = before;
                Err(error)
            }
        }
    }

    fn reconcile_inner(
        &mut self,
        operation_id: &str,
        outcome: ReconciliationOutcome,
        now: u64,
    ) -> Result<ReconciliationResult, ClientError> {
        let index = self
            .operations
            .iter()
            .position(|operation| operation.operation_id == operation_id)
            .ok_or(ClientError::NotFound)?;
        let operation = &mut self.operations[index];
        if operation.status != OperationStatus::Pending {
            return Err(ClientError::InvalidReconciliation);
        }

        match outcome {
            ReconciliationOutcome::Applied { server_cursor }
                if operation.kind != OperationKind::MasterUpsert =>
            {
                operation.status = OperationStatus::Synced;
                operation.server_cursor = Some(server_cursor);
                operation.updated_at = now;
                self.metrics.synced += 1;
                Ok(ReconciliationResult {
                    status: OperationStatus::Synced,
                    review_task_created: false,
                    terminal: true,
                })
            }
            ReconciliationOutcome::MasterApplied {
                server_cursor,
                authoritative_version,
            } if operation.kind == OperationKind::MasterUpsert => {
                operation.status = OperationStatus::Synced;
                operation.server_cursor = Some(server_cursor);
                operation.authoritative_version = Some(authoritative_version);
                operation.updated_at = now;
                self.metrics.synced += 1;
                Ok(ReconciliationResult {
                    status: OperationStatus::Synced,
                    review_task_created: false,
                    terminal: true,
                })
            }
            ReconciliationOutcome::Duplicate { server_cursor } => {
                operation.status = OperationStatus::Synced;
                operation.server_cursor = Some(server_cursor);
                operation.updated_at = now;
                self.metrics.synced += 1;
                Ok(ReconciliationResult {
                    status: OperationStatus::Synced,
                    review_task_created: false,
                    terminal: true,
                })
            }
            ReconciliationOutcome::Retry { reason } => {
                operation.attempts = operation.attempts.saturating_add(1);
                operation.updated_at = now;
                let retry_code = format!("sync_retry:{}", reason.code());
                if operation.attempts >= self.retry_limit {
                    operation.status = OperationStatus::Failed;
                    operation.last_error = Some(format!("sync_retry_exhausted:{}", reason.code()));
                    let task =
                        self.add_review_task(index, ReviewTaskKind::SyncFailure, &retry_code);
                    self.metrics.failed += 1;
                    self.metrics.review_tasks += u64::from(task);
                    Ok(ReconciliationResult {
                        status: OperationStatus::Failed,
                        review_task_created: task,
                        terminal: true,
                    })
                } else {
                    operation.last_error = Some(retry_code);
                    self.metrics.retried += 1;
                    Ok(ReconciliationResult {
                        status: OperationStatus::Pending,
                        review_task_created: false,
                        terminal: false,
                    })
                }
            }
            ReconciliationOutcome::FiscalRejected { code }
                if operation.kind == OperationKind::FiscalEmission =>
            {
                operation.attempts = operation.attempts.saturating_add(1);
                operation.status = OperationStatus::Failed;
                operation.last_error = Some(format!("fiscal_rejected:{}", sanitize_code(&code)));
                operation.updated_at = now;
                let task = self.add_review_task(index, ReviewTaskKind::FiscalFailure, &code);
                self.metrics.failed += 1;
                self.metrics.review_tasks += u64::from(task);
                Ok(ReconciliationResult {
                    status: OperationStatus::Failed,
                    review_task_created: task,
                    terminal: true,
                })
            }
            ReconciliationOutcome::Conflict { code } => {
                let domain = operation.kind.domain();
                operation.attempts = operation.attempts.saturating_add(1);
                operation.status = OperationStatus::Conflict;
                operation.last_error = Some(format!("conflict:{}", sanitize_code(&code)));
                operation.updated_at = now;
                let task = self.add_review_task(
                    index,
                    match domain {
                        OperationDomain::Master => ReviewTaskKind::MasterConflict,
                        OperationDomain::Money => ReviewTaskKind::MoneyConflict,
                        OperationDomain::Funeral => ReviewTaskKind::FuneralConflict,
                        OperationDomain::Fiscal => ReviewTaskKind::FiscalConflict,
                    },
                    &code,
                );
                self.metrics.conflicts += 1;
                self.metrics.review_tasks += u64::from(task);
                Ok(ReconciliationResult {
                    status: OperationStatus::Conflict,
                    review_task_created: task,
                    terminal: true,
                })
            }
            ReconciliationOutcome::OutOfOrder { expected_seq } => {
                operation.attempts = operation.attempts.saturating_add(1);
                operation.last_error =
                    Some(format!("sync_reject:out_of_order_expected_{expected_seq}"));
                operation.updated_at = now;
                self.metrics.retried += 1;
                Ok(ReconciliationResult {
                    status: OperationStatus::Pending,
                    review_task_created: false,
                    terminal: false,
                })
            }
            _ => Err(ClientError::InvalidReconciliation),
        }
    }

    fn persist_if_configured(&self) -> Result<(), ClientError> {
        if let Some(path) = &self.path {
            persist_queue(path, self)?;
        }
        Ok(())
    }

    fn add_review_task(
        &mut self,
        operation_index: usize,
        kind: ReviewTaskKind,
        code: &str,
    ) -> bool {
        let operation_id = self.operations[operation_index].operation_id.clone();
        if self
            .review_tasks
            .iter()
            .any(|task| task.operation_id == operation_id && task.status == ReviewTaskStatus::Open)
        {
            return false;
        }
        self.review_tasks.push(ReviewTask {
            task_id: format!("review_{}", operation_id.trim_start_matches("op_")),
            operation_id,
            kind,
            code: sanitize_code(code),
            status: ReviewTaskStatus::Open,
        });
        true
    }
}

fn persisted_integrity_hash(data: &PersistedQueue) -> Result<String, ClientError> {
    let integrity = QueueIntegrity {
        schema_version: data.schema_version,
        tenant_id: &data.tenant_id,
        node_id: &data.node_id,
        connectivity: data.connectivity,
        retry_limit: data.retry_limit,
        last_seq: data.last_seq,
        operations: &data.operations,
        review_tasks: &data.review_tasks,
        metrics: &data.metrics,
    };
    let bytes = serde_json::to_vec(&integrity)
        .map_err(|error| ClientError::Serialization(error.to_string()))?;
    Ok(hex_sha256(&bytes))
}

fn validate_persisted_queue(data: &PersistedQueue) -> Result<(), ClientError> {
    if data.retry_limit == 0 {
        return Err(ClientError::CorruptStore("limite de retry"));
    }

    let mut operation_ids = HashSet::new();
    let mut sequences = HashSet::new();
    let mut idempotency_keys = HashSet::new();
    let mut max_seq = 0;
    for operation in &data.operations {
        if operation.tenant_id != data.tenant_id {
            return Err(ClientError::CorruptStore("tenant da operacao"));
        }
        if operation.node_id != data.node_id {
            return Err(ClientError::CorruptStore("node da operacao"));
        }
        if operation.seq == 0 {
            return Err(ClientError::CorruptStore("sequencia invalida"));
        }
        if !operation_ids.insert(operation.operation_id.as_str()) {
            return Err(ClientError::CorruptStore("operation_id duplicado"));
        }
        if !sequences.insert(operation.seq) {
            return Err(ClientError::CorruptStore("sequencia duplicada"));
        }
        if !idempotency_keys.insert(operation.idempotency_key.as_str()) {
            return Err(ClientError::CorruptStore("idempotency_key duplicada"));
        }

        let draft = OperationDraft {
            kind: operation.kind,
            entity_id: operation.entity_id.clone(),
            idempotency_key: operation.idempotency_key.clone(),
            payload: operation.payload.clone(),
        };
        validate_draft(&draft).map_err(|_| ClientError::CorruptStore("operacao invalida"))?;
        let expected_hash = content_hash(&draft)
            .map_err(|_| ClientError::CorruptStore("hash de conteudo invalido"))?;
        if operation.content_hash != expected_hash {
            return Err(ClientError::CorruptStore("hash de conteudo"));
        }
        if operation.operation_id
            != operation_id(
                &operation.tenant_id,
                &operation.node_id,
                operation.seq,
                &operation.content_hash,
            )
        {
            return Err(ClientError::CorruptStore("operation_id invalido"));
        }
        max_seq = max_seq.max(operation.seq);
    }
    if data.last_seq < max_seq {
        return Err(ClientError::CorruptStore("sequencia final regressiva"));
    }

    for task in &data.review_tasks {
        if !operation_ids.contains(task.operation_id.as_str())
            || task.task_id.trim().is_empty()
            || sanitize_code(&task.code) != task.code
        {
            return Err(ClientError::CorruptStore("tarefa de revisao invalida"));
        }
    }
    Ok(())
}

fn persist_queue(path: &Path, queue: &ClientQueue) -> Result<(), ClientError> {
    let mut data = PersistedQueue {
        schema_version: STORE_SCHEMA_VERSION,
        tenant_id: queue.tenant_id.clone(),
        node_id: queue.node_id.clone(),
        connectivity: queue.connectivity,
        retry_limit: queue.retry_limit,
        last_seq: queue.last_seq,
        operations: queue.operations.clone(),
        review_tasks: queue.review_tasks.clone(),
        metrics: queue.metrics.clone(),
        integrity_hash: String::new(),
    };
    data.integrity_hash = persisted_integrity_hash(&data)?;
    let bytes = serde_json::to_vec_pretty(&data)
        .map_err(|error| ClientError::Serialization(error.to_string()))?;
    if let Some(parent) = path
        .parent()
        .filter(|parent| !parent.as_os_str().is_empty())
    {
        fs::create_dir_all(parent).map_err(io_error)?;
    }
    let temp_path = path.with_extension("tmp");
    let mut file = fs::File::create(&temp_path).map_err(io_error)?;
    file.write_all(&bytes).map_err(io_error)?;
    file.sync_all().map_err(io_error)?;
    replace_file(&temp_path, path).map_err(io_error)
}

fn replace_file(temp_path: &Path, destination: &Path) -> Result<(), io::Error> {
    let backup_path = destination.with_extension("bak");
    if destination.exists() {
        if backup_path.exists() {
            fs::remove_file(&backup_path)?;
        }
        fs::rename(destination, &backup_path)?;
    }
    match fs::rename(temp_path, destination) {
        Ok(()) => {
            let _ = fs::remove_file(backup_path);
            Ok(())
        }
        Err(error) => {
            if !destination.exists() && backup_path.exists() {
                let _ = fs::rename(&backup_path, destination);
            }
            Err(error)
        }
    }
}

fn recover_backup(destination: &Path) -> Result<(), io::Error> {
    let backup_path = destination.with_extension("bak");
    if !destination.exists() && backup_path.exists() {
        fs::rename(backup_path, destination)?;
    }
    Ok(())
}

fn io_error(error: io::Error) -> ClientError {
    ClientError::Io(error.to_string())
}

pub fn unix_now() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_or(0, |duration| duration.as_secs())
}

pub fn validate_draft(draft: &OperationDraft) -> Result<(), ClientError> {
    validate_identifier(&draft.entity_id, ClientError::EmptyEntity)?;
    validate_identifier(&draft.idempotency_key, ClientError::EmptyIdempotencyKey)?;
    let object = draft
        .payload
        .as_object()
        .ok_or(ClientError::InvalidPayload("payload precisa ser objeto"))?;
    match draft.kind {
        OperationKind::MasterUpsert => require_cents(object, "base_version", 0)?,
        OperationKind::ContractCreate => {
            require_cents(object, "total_cents", 0)?;
            if let Some(currency) = object.get("currency") {
                if currency.as_str() != Some("BRL") {
                    return Err(ClientError::InvalidPayload("currency precisa ser BRL"));
                }
            }
            if let Some(item_count) = object.get("item_count") {
                if item_count.as_u64().is_none_or(|count| count == 0) {
                    return Err(ClientError::InvalidPayload("item_count invalido"));
                }
            }
        }
        OperationKind::FuneralJourneyCreate => {
            let service_order = object
                .get("service_order")
                .and_then(Value::as_object)
                .ok_or(ClientError::InvalidPayload("service_order obrigatorio"))?;
            let schedule = object
                .get("schedule")
                .and_then(Value::as_object)
                .ok_or(ClientError::InvalidPayload("schedule obrigatorio"))?;
            validate_opaque_field(service_order, "kind")?;
            validate_opaque_field(service_order, "scheduled_for")?;
            validate_opaque_field(schedule, "kind")?;
            validate_opaque_field(schedule, "location_id")?;
            validate_opaque_field(schedule, "starts_at")?;
            validate_opaque_field(schedule, "ends_at")?;
            validate_opaque_field(schedule, "time_zone")?;
            if let Some(resource_id) = schedule.get("resource_id") {
                if resource_id.as_str().is_none() {
                    return Err(ClientError::InvalidPayload("resource_id invalido"));
                }
            }
        }
        OperationKind::FuneralServiceOrderAdvance => {
            validate_opaque_field(object, "status")?;
            if object.get("status").and_then(Value::as_str) != Some("in_progress") {
                return Err(ClientError::InvalidPayload("status de avancoo invalido"));
            }
            validate_expected_version(object)?;
        }
        OperationKind::FuneralServiceOrderPause => {
            validate_opaque_field(object, "status")?;
            if object.get("status").and_then(Value::as_str) != Some("paused") {
                return Err(ClientError::InvalidPayload("status de pausa invalido"));
            }
            validate_expected_version(object)?;
        }
        OperationKind::FuneralServiceOrderComplete => {
            validate_opaque_field(object, "status")?;
            if object.get("status").and_then(Value::as_str) != Some("completed") {
                return Err(ClientError::InvalidPayload("status de conclusao invalido"));
            }
            validate_expected_version(object)?;
        }
        OperationKind::CashOpen => require_cents(object, "opening_amount_cents", 0)?,
        OperationKind::CashMovement => {
            let direction = object
                .get("direction")
                .and_then(Value::as_str)
                .ok_or(ClientError::InvalidPayload("direction obrigatoria"))?;
            if direction != "in" && direction != "out" {
                return Err(ClientError::InvalidPayload("direction invalida"));
            }
            require_cents(object, "amount_cents", 1)?;
            let reason = object
                .get("reason")
                .and_then(Value::as_str)
                .ok_or(ClientError::InvalidPayload("reason obrigatorio"))?;
            if reason.trim().is_empty() || reason.len() > MAX_REASON_LEN {
                return Err(ClientError::InvalidPayload("reason invalido"));
            }
        }
        OperationKind::CashClose => {
            require_cents(object, "counted_amount_cents", 0)?;
            if object.contains_key("expected_amount_cents") {
                require_cents(object, "expected_amount_cents", 0)?;
            }
        }
        OperationKind::FiscalEmission => {
            let reference = object
                .get("document_ref")
                .and_then(Value::as_str)
                .ok_or(ClientError::InvalidPayload("document_ref obrigatorio"))?;
            validate_identifier(reference, ClientError::EmptyEntity)?;
        }
    }
    reject_personal_data(&draft.payload, 0)?;
    let encoded = serde_json::to_vec(&draft.payload)
        .map_err(|_| ClientError::InvalidPayload("payload nao serializavel"))?;
    if encoded.len() > MAX_PAYLOAD_BYTES {
        return Err(ClientError::PayloadTooLarge);
    }
    Ok(())
}

fn validate_expected_version(object: &Map<String, Value>) -> Result<(), ClientError> {
    let version = object
        .get("expected_version")
        .and_then(Value::as_u64)
        .ok_or(ClientError::InvalidPayload("expected_version invalido"))?;
    if version > i64::MAX as u64 {
        return Err(ClientError::InvalidPayload("expected_version invalido"));
    }
    Ok(())
}

pub fn content_hash(draft: &OperationDraft) -> Result<String, ClientError> {
    let canonical = serde_json::to_vec(&(
        draft.kind.as_str(),
        draft.entity_id.as_str(),
        &draft.payload,
    ))
    .map_err(|_| ClientError::InvalidPayload("payload nao serializavel"))?;
    Ok(hex_sha256(&canonical))
}

pub fn operation_id(tenant_id: &str, node_id: &str, seq: u64, content_hash: &str) -> String {
    let mut hasher = Sha256::new();
    hasher.update(tenant_id.as_bytes());
    hasher.update([0]);
    hasher.update(node_id.as_bytes());
    hasher.update([0]);
    hasher.update(seq.to_be_bytes());
    hasher.update([0]);
    hasher.update(content_hash.as_bytes());
    format!("op_{}", hex(&hasher.finalize()))
}

fn validate_identifier(value: &str, empty: ClientError) -> Result<(), ClientError> {
    if value.trim().is_empty() {
        return Err(empty);
    }
    if value.len() > MAX_IDENTIFIER_LEN
        || !value
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || b"-_.:".contains(&byte))
    {
        return Err(ClientError::InvalidIdentifier);
    }
    Ok(())
}

fn require_cents(
    object: &Map<String, Value>,
    field: &'static str,
    minimum: i64,
) -> Result<(), ClientError> {
    let value = object
        .get(field)
        .and_then(Value::as_i64)
        .ok_or(ClientError::InvalidPayload(field))?;
    if value < minimum {
        return Err(ClientError::InvalidPayload(field));
    }
    Ok(())
}

fn validate_opaque_field(
    object: &Map<String, Value>,
    field: &'static str,
) -> Result<(), ClientError> {
    let value = object
        .get(field)
        .and_then(Value::as_str)
        .ok_or(ClientError::InvalidPayload(field))?;
    if value.trim().is_empty()
        || value.len() > MAX_IDENTIFIER_LEN
        || !value
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || b"-_.:/+TZ".contains(&byte))
    {
        return Err(ClientError::InvalidPayload(field));
    }
    Ok(())
}

fn reject_personal_data(value: &Value, depth: usize) -> Result<(), ClientError> {
    if depth > MAX_PAYLOAD_DEPTH {
        return Err(ClientError::InvalidPayload("payload profundo demais"));
    }
    match value {
        Value::Object(object) => {
            for (key, nested) in object {
                if matches!(
                    key.to_ascii_lowercase().as_str(),
                    "cpf"
                        | "cnpj"
                        | "rg"
                        | "nome"
                        | "name"
                        | "email"
                        | "phone"
                        | "telefone"
                        | "celular"
                        | "endereco"
                        | "address"
                        | "data_nascimento"
                ) {
                    return Err(ClientError::PersonalDataInPayload);
                }
                reject_personal_data(nested, depth + 1)?;
            }
        }
        Value::Array(items) => {
            for item in items {
                reject_personal_data(item, depth + 1)?;
            }
        }
        _ => {}
    }
    Ok(())
}

pub(crate) fn sanitize_code(code: &str) -> String {
    let sanitized: String = code
        .chars()
        .filter(|character| {
            character.is_ascii_alphanumeric() || matches!(character, '-' | '_' | '.' | ':')
        })
        .take(120)
        .collect();
    if sanitized.is_empty() {
        "unknown".to_owned()
    } else {
        sanitized
    }
}

fn hex_sha256(bytes: &[u8]) -> String {
    let mut hasher = Sha256::new();
    hasher.update(bytes);
    hex(&hasher.finalize())
}

fn hex(bytes: &[u8]) -> String {
    const HEX: &[u8; 16] = b"0123456789abcdef";
    let mut output = String::with_capacity(bytes.len() * 2);
    for byte in bytes {
        output.push(HEX[(byte >> 4) as usize] as char);
        output.push(HEX[(byte & 0x0f) as usize] as char);
    }
    output
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;
    use std::fs;
    use std::path::{Path, PathBuf};
    use std::time::{SystemTime, UNIX_EPOCH};

    fn temp_queue_path() -> PathBuf {
        let nonce = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .expect("clock")
            .as_nanos();
        std::env::temp_dir().join(format!("amparius-client-test-{nonce}.json"))
    }

    fn cleanup_queue_path(path: &Path) {
        let _ = fs::remove_file(path);
        let _ = fs::remove_file(path.with_extension("tmp"));
        let _ = fs::remove_file(path.with_extension("bak"));
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
    fn connectivity_states_are_distinct_and_visible_without_identifiers() {
        let mut queue = ClientQueue::new("tenant-a", "node-1").expect("queue");
        assert_eq!(queue.connectivity(), ConnectivityState::Online);
        queue
            .set_connectivity(ConnectivityState::Offline)
            .expect("offline");
        assert_eq!(queue.status().connectivity, ConnectivityState::Offline);
        queue
            .set_connectivity(ConnectivityState::Contingency)
            .expect("contingency");
        let status = serde_json::to_string(&queue.status()).expect("status json");
        assert!(status.contains("contingency"));
        assert!(!status.contains("tenant-a"));
        assert!(!status.contains("node-1"));
    }

    #[test]
    fn sequence_and_idempotency_never_duplicate_or_overwrite() {
        let mut queue = ClientQueue::new("tenant-a", "node-1").expect("queue");
        let first = queue
            .enqueue("operator-1", contract("op-1", 100), 10)
            .expect("first");
        let replay = queue
            .enqueue("operator-1", contract("op-1", 100), 11)
            .expect("replay");
        assert_eq!(first.operation.seq, 1);
        assert!(replay.replayed);
        assert_eq!(replay.operation.operation_id, first.operation.operation_id);
        assert_eq!(queue.status().queue.total, 1);
        assert_eq!(queue.metrics().replayed, 1);
        assert_eq!(
            queue.enqueue("operator-1", contract("op-1", 999), 12),
            Err(ClientError::IdempotencyConflict)
        );
        let second = queue
            .enqueue("operator-1", contract("op-2", 200), 13)
            .expect("second");
        assert_eq!(second.operation.seq, 2);
        assert_eq!(queue.status().queue.last_seq, 2);
    }

    #[test]
    fn money_conflict_creates_review_without_last_write_wins() {
        let mut queue = ClientQueue::new("tenant-a", "node-1").expect("queue");
        let result = queue
            .enqueue("operator-1", contract("op-1", 100), 10)
            .expect("enqueue");
        let reconciliation = queue
            .reconcile(
                &result.operation.operation_id,
                ReconciliationOutcome::Conflict {
                    code: "server_version_conflict".to_owned(),
                },
                20,
            )
            .expect("conflict");
        assert_eq!(reconciliation.status, OperationStatus::Conflict);
        assert_eq!(
            queue.operations()[0].payload,
            json!({"total_cents": 100, "currency": "BRL"})
        );
        assert_eq!(queue.review_tasks()[0].kind, ReviewTaskKind::MoneyConflict);
        assert_eq!(queue.review_tasks()[0].code, "server_version_conflict");
        assert_eq!(queue.metrics().review_tasks, 1);
    }

    #[test]
    fn fiscal_conflict_and_rejection_are_explicit_and_safe() {
        let mut queue = ClientQueue::new("tenant-a", "node-1").expect("queue");
        let conflict = queue
            .enqueue("operator-1", fiscal("fiscal-1"), 10)
            .expect("conflict enqueue");
        queue
            .reconcile(
                &conflict.operation.operation_id,
                ReconciliationOutcome::Conflict {
                    code: "fiscal_conflict<raw>".to_owned(),
                },
                20,
            )
            .expect("fiscal conflict");
        let rejected = queue
            .enqueue("operator-1", fiscal("fiscal-2"), 21)
            .expect("reject enqueue");
        queue
            .reconcile(
                &rejected.operation.operation_id,
                ReconciliationOutcome::FiscalRejected {
                    code: "document_invalid<xml>".to_owned(),
                },
                22,
            )
            .expect("fiscal rejection");
        assert_eq!(queue.status().queue.conflict, 1);
        assert_eq!(queue.status().queue.failed, 1);
        assert_eq!(queue.review_tasks().len(), 2);
        assert_eq!(queue.review_tasks()[0].kind, ReviewTaskKind::FiscalConflict);
        assert_eq!(queue.review_tasks()[1].kind, ReviewTaskKind::FiscalFailure);
        assert!(!queue.review_tasks()[1].code.contains('<'));
    }

    #[test]
    fn retry_limit_fails_without_discarding_operation() {
        let mut queue = ClientQueue::with_retry_limit("tenant-a", "node-1", 2).expect("queue");
        let result = queue
            .enqueue("operator-1", contract("op-1", 100), 10)
            .expect("enqueue");
        let id = result.operation.operation_id;
        let first = queue
            .reconcile(
                &id,
                ReconciliationOutcome::Retry {
                    reason: RetryReason::TransportTimeout,
                },
                11,
            )
            .expect("retry");
        assert_eq!(first.status, OperationStatus::Pending);
        let second = queue
            .reconcile(
                &id,
                ReconciliationOutcome::Retry {
                    reason: RetryReason::TransportTimeout,
                },
                12,
            )
            .expect("exhausted");
        assert_eq!(second.status, OperationStatus::Failed);
        assert_eq!(queue.operations().len(), 1);
        assert_eq!(queue.operations()[0].attempts, 2);
        assert_eq!(queue.review_tasks()[0].kind, ReviewTaskKind::SyncFailure);
    }

    #[test]
    fn master_requires_authoritative_acceptance_and_out_of_order_stays_pending() {
        let mut queue = ClientQueue::new("tenant-a", "node-1").expect("queue");
        let master = OperationDraft {
            kind: OperationKind::MasterUpsert,
            entity_id: "catalog-1".to_owned(),
            idempotency_key: "master-1".to_owned(),
            payload: json!({"base_version": 7, "label_ref": "label-1"}),
        };
        let operation = queue.enqueue("operator-1", master, 10).expect("enqueue");
        let out_of_order = queue
            .reconcile(
                &operation.operation.operation_id,
                ReconciliationOutcome::OutOfOrder { expected_seq: 4 },
                11,
            )
            .expect("out of order");
        assert_eq!(out_of_order.status, OperationStatus::Pending);
        assert_eq!(queue.operations()[0].attempts, 1);
        assert_eq!(
            queue.reconcile(
                &operation.operation.operation_id,
                ReconciliationOutcome::Applied { server_cursor: 9 },
                12,
            ),
            Err(ClientError::InvalidReconciliation)
        );
        let applied = queue
            .reconcile(
                &operation.operation.operation_id,
                ReconciliationOutcome::MasterApplied {
                    server_cursor: 10,
                    authoritative_version: 8,
                },
                13,
            )
            .expect("master applied");
        assert_eq!(applied.status, OperationStatus::Synced);
        assert_eq!(queue.operations()[0].authoritative_version, Some(8));
    }

    #[test]
    fn rejects_personal_data_and_invalid_money() {
        let personal = OperationDraft {
            kind: OperationKind::ContractCreate,
            entity_id: "contract-1".to_owned(),
            idempotency_key: "op-1".to_owned(),
            payload: json!({"total_cents": 100, "nome": "não registrar"}),
        };
        assert_eq!(
            validate_draft(&personal),
            Err(ClientError::PersonalDataInPayload)
        );
        assert_eq!(
            validate_draft(&contract("op-2", -1)),
            Err(ClientError::InvalidPayload("total_cents"))
        );
    }

    #[test]
    fn persistent_queue_reopens_with_sequence_and_replay_intact() {
        let path = temp_queue_path();
        {
            let mut queue = ClientQueue::open(&path, "tenant-a", "node-1").expect("open");
            queue
                .set_connectivity(ConnectivityState::Offline)
                .expect("offline");
            let first = queue
                .enqueue("operator-1", contract("op-1", 100), 10)
                .expect("enqueue");
            assert_eq!(first.operation.seq, 1);
            queue
                .reconcile(
                    &first.operation.operation_id,
                    ReconciliationOutcome::Retry {
                        reason: RetryReason::TransportTimeout,
                    },
                    11,
                )
                .expect("retry");
        }

        let mut reopened = ClientQueue::open(&path, "tenant-a", "node-1").expect("reopen");
        assert_eq!(reopened.connectivity(), ConnectivityState::Offline);
        assert_eq!(reopened.operations().len(), 1);
        assert_eq!(reopened.operations()[0].seq, 1);
        assert_eq!(reopened.operations()[0].attempts, 1);
        let replay = reopened
            .enqueue("operator-1", contract("op-1", 100), 12)
            .expect("replay");
        assert!(replay.replayed);
        assert_eq!(replay.operation.seq, 1);
        let next = reopened
            .enqueue("operator-1", contract("op-2", 200), 13)
            .expect("next");
        assert_eq!(next.operation.seq, 2);
        assert_eq!(reopened.status().queue.last_seq, 2);
        cleanup_queue_path(&path);
    }

    #[test]
    fn persistent_queue_keeps_financial_and_fiscal_review_states() {
        let path = temp_queue_path();
        {
            let mut queue = ClientQueue::open(&path, "tenant-a", "node-1").expect("open");
            let money = queue
                .enqueue("operator-1", contract("money-1", 100), 10)
                .expect("money enqueue");
            queue
                .reconcile(
                    &money.operation.operation_id,
                    ReconciliationOutcome::Conflict {
                        code: "money_conflict".to_owned(),
                    },
                    11,
                )
                .expect("money conflict");
            let fiscal = queue
                .enqueue("operator-1", fiscal("fiscal-1"), 12)
                .expect("fiscal enqueue");
            queue
                .reconcile(
                    &fiscal.operation.operation_id,
                    ReconciliationOutcome::FiscalRejected {
                        code: "fiscal_rejected".to_owned(),
                    },
                    13,
                )
                .expect("fiscal rejection");
        }

        let reopened = ClientQueue::open(&path, "tenant-a", "node-1").expect("reopen");
        assert_eq!(reopened.status().queue.conflict, 1);
        assert_eq!(reopened.status().queue.failed, 1);
        assert_eq!(reopened.review_tasks().len(), 2);
        assert_eq!(
            reopened.review_tasks()[0].kind,
            ReviewTaskKind::MoneyConflict
        );
        assert_eq!(
            reopened.review_tasks()[1].kind,
            ReviewTaskKind::FiscalFailure
        );
        cleanup_queue_path(&path);
    }

    #[test]
    fn persistent_queue_rejects_a_different_tenant_or_node_context() {
        let path = temp_queue_path();
        let queue = ClientQueue::open(&path, "tenant-a", "node-1").expect("open");
        drop(queue);
        assert!(matches!(
            ClientQueue::open(&path, "tenant-b", "node-1"),
            Err(ClientError::TenantMismatch)
        ));
        assert!(matches!(
            ClientQueue::open(&path, "tenant-a", "node-2"),
            Err(ClientError::NodeMismatch)
        ));
        cleanup_queue_path(&path);
    }

    #[test]
    fn corrupted_store_fails_closed_without_reconstructing_queue() {
        let path = temp_queue_path();
        let queue = ClientQueue::open(&path, "tenant-a", "node-1").expect("open");
        drop(queue);
        fs::write(&path, b"{not-json").expect("corrupt");
        assert!(matches!(
            ClientQueue::open(&path, "tenant-a", "node-1"),
            Err(ClientError::Serialization(_))
        ));
        cleanup_queue_path(&path);
    }

    #[test]
    fn unsupported_store_version_fails_closed_before_loading_state() {
        let path = temp_queue_path();
        let queue = ClientQueue::open(&path, "tenant-a", "node-1").expect("open");
        drop(queue);
        let mut document: Value =
            serde_json::from_slice(&fs::read(&path).expect("store bytes")).expect("document");
        document["schema_version"] = json!(999);
        fs::write(
            &path,
            serde_json::to_vec(&document).expect("document bytes"),
        )
        .expect("write invalid version");
        assert!(matches!(
            ClientQueue::open(&path, "tenant-a", "node-1"),
            Err(ClientError::UnsupportedStoreVersion(999))
        ));
        cleanup_queue_path(&path);
    }

    #[test]
    fn tampered_payload_is_rejected_by_integrity_hash() {
        let path = temp_queue_path();
        let mut queue = ClientQueue::open(&path, "tenant-a", "node-1").expect("open");
        queue
            .enqueue("operator-1", contract("op-1", 100), 10)
            .expect("enqueue");
        drop(queue);
        let mut document: Value =
            serde_json::from_slice(&fs::read(&path).expect("store bytes")).expect("document");
        document["operations"][0]["payload"]["total_cents"] = json!(999);
        fs::write(
            &path,
            serde_json::to_vec(&document).expect("document bytes"),
        )
        .expect("write tampered payload");
        assert!(matches!(
            ClientQueue::open(&path, "tenant-a", "node-1"),
            Err(ClientError::CorruptStore("hash de integridade"))
        ));
        cleanup_queue_path(&path);
    }
}
