//! Modelo de apresentação do painel do dia (CL-01).
//!
//! Este módulo não desenha nada: transforma `ClientQueue` + estado de tela em um
//! `DayPanelView` determinístico. A renderização (`ui::render`) e o aplicativo
//! (`ui::app`) apenas consomem esse modelo, o que mantém a regra testável e
//! permite outra superfície (web/LAN) reutilizar o mesmo contrato.
//!
//! Regras: sem payload, sem PII, sem token, sem tenant/dispositivo visível;
//! conflito e falha nunca são apresentados como sucesso nem sobrescritos.

use crate::{
    ClientError, ClientQueue, ConnectivityState, OperationKind, OperationStatus, ReviewTaskKind,
};
use chrono::{DateTime, Utc};
use chrono_tz::America::Sao_Paulo;
use std::collections::HashSet;

/// Identificador estável da tela, usado em observabilidade e testes.
pub const SCREEN_ID: &str = "CL-01";

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Severity {
    Neutral,
    Info,
    Success,
    Warning,
    Danger,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum PanelPhase {
    Loading,
    Ready,
    Error,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct StatusChip {
    pub label: &'static str,
    pub symbol: &'static str,
    pub severity: Severity,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ConnectivityBanner {
    pub state: ConnectivityState,
    pub title: &'static str,
    pub detail: &'static str,
    pub symbol: &'static str,
    pub severity: Severity,
    /// A fila local aceita operações nos três estados; isto é explícito na tela.
    pub queue_available: bool,
}

impl ConnectivityBanner {
    pub fn for_state(state: ConnectivityState) -> Self {
        match state {
            ConnectivityState::Online => Self {
                state,
                title: "Online",
                detail: "Conectado. A fila sincroniza com o relay quando não há pendências travadas.",
                symbol: "+",
                severity: Severity::Success,
                queue_available: true,
            },
            ConnectivityState::Offline => Self {
                state,
                title: "Offline",
                detail: "Sem conexão. A fila continua local, com sequência preservada; nada é perdido.",
                symbol: "-",
                severity: Severity::Warning,
                queue_available: true,
            },
            ConnectivityState::Contingency => Self {
                state,
                title: "Contingência",
                detail: "Contingência declarada: a operação local continua, mas contingência não autoriza emissão fiscal.",
                symbol: "~",
                severity: Severity::Warning,
                queue_available: true,
            },
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
pub struct Counters {
    pub pending: usize,
    pub synced: usize,
    pub failed: usize,
    pub conflict: usize,
    pub retrying: usize,
    pub review_open: usize,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct OperationRow {
    pub operation_id: String,
    /// Referência opaca já validada por `lib.rs`; nunca payload.
    pub entity_ref: String,
    pub seq: u64,
    pub kind: &'static str,
    pub status: StatusChip,
    pub attempts: u32,
    pub created_local: String,
    pub age: String,
    pub error_code: Option<String>,
    pub needs_review: bool,
    /// Menor = mais prioritário na lista do dia.
    pub priority: u8,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ReviewRow {
    pub task_id: String,
    pub operation_id: String,
    pub kind: &'static str,
    pub code: String,
    pub guidance: &'static str,
    pub severity: Severity,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct PanelError {
    pub code: String,
    pub message: String,
    pub hint: Option<String>,
}

impl PanelError {
    pub fn new(code: impl Into<String>, message: impl Into<String>, hint: Option<&str>) -> Self {
        Self {
            code: code.into(),
            message: message.into(),
            hint: hint.map(str::to_owned),
        }
    }

    /// Traduz a falha de abertura/leitura da fila em um código estável, sem
    /// vazar caminho, payload ou identidade. O texto explica a ação possível.
    pub fn from_queue_error(error: &ClientError) -> Self {
        match error {
            ClientError::EmptyTenant => Self::new(
                "empty_tenant",
                "O tenant da estação não está definido.",
                Some("Informe o tenant na configuração da estação."),
            ),
            ClientError::EmptyNode => Self::new(
                "empty_node",
                "O dispositivo da estação não está definido.",
                Some("Informe o identificador do dispositivo na configuração."),
            ),
            ClientError::UnsupportedStoreVersion(version) => Self::new(
                "unsupported_store_version",
                format!("A fila local usa uma versão não suportada ({version})."),
                Some("Atualize o cliente; a fila não será reinterpretada."),
            ),
            ClientError::CorruptStore(reason) => Self::new(
                "corrupt_store",
                format!("A fila local falhou na verificação ({reason})."),
                Some("Preserve o arquivo e acione o suporte; nada foi reconstruído."),
            ),
            ClientError::TenantMismatch => Self::new(
                "tenant_mismatch",
                "A fila local pertence a outro tenant.",
                Some("Abra a fila com a identidade correta da estação."),
            ),
            ClientError::NodeMismatch => Self::new(
                "node_mismatch",
                "A fila local pertence a outro dispositivo.",
                Some("Abra a fila com o identificador do dispositivo correto."),
            ),
            ClientError::Serialization(_) => Self::new(
                "invalid_store",
                "A fila local não pôde ser lida.",
                Some("Preserve o arquivo e acione o suporte."),
            ),
            ClientError::Io(_) => Self::new(
                "io_error",
                "Não foi possível acessar o arquivo da fila local.",
                Some("Verifique permissão e espaço em disco."),
            ),
            _ => Self::new(
                "queue_error",
                "A fila local não pôde ser aberta.",
                Some("Acione o suporte com o código exibido."),
            ),
        }
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Notice {
    pub severity: Severity,
    pub text: String,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct SyncView {
    pub label: String,
    pub detail: Option<String>,
    pub severity: Severity,
    pub can_start: bool,
    pub running: bool,
    /// Indica que a ação disponível reapresenta operações ainda `pending`.
    /// Nunca reabre conflito ou falha de negócio.
    pub retry: bool,
}

impl SyncView {
    pub fn idle(pending: usize, configured: bool) -> Self {
        if !configured {
            return Self::unavailable("Relay ou credencial de sincronização não configurados.");
        }
        if pending == 0 {
            Self {
                label: "Nada pendente".to_owned(),
                detail: Some("A fila local não tem operações aguardando envio.".to_owned()),
                severity: Severity::Neutral,
                can_start: false,
                running: false,
                retry: false,
            }
        } else {
            Self {
                label: "Pronto para sincronizar".to_owned(),
                detail: Some(format!(
                    "{pending} operação(ões) pendente(s) na fila, em ordem de sequência."
                )),
                severity: Severity::Info,
                can_start: true,
                running: false,
                retry: false,
            }
        }
    }

    pub fn running() -> Self {
        Self {
            label: "Sincronizando".to_owned(),
            detail: Some("Enviando operações pendentes em ordem de sequência.".to_owned()),
            severity: Severity::Info,
            can_start: false,
            running: true,
            retry: false,
        }
    }

    pub fn unavailable(reason: &str) -> Self {
        Self {
            label: "Sincronização indisponível".to_owned(),
            detail: Some(reason.to_owned()),
            severity: Severity::Warning,
            can_start: false,
            running: false,
            retry: false,
        }
    }

    pub fn failed(code: &str) -> Self {
        Self {
            label: "Sincronização falhou".to_owned(),
            detail: Some(format!("Código: {}", safe_code(code))),
            severity: Severity::Danger,
            can_start: true,
            running: false,
            retry: true,
        }
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct SyncSummary {
    pub synced: u32,
    pub duplicates: u32,
    pub conflicts: u32,
    pub rejected: u32,
    pub retried: u32,
    pub remaining_pending: usize,
    pub stopped: Option<&'static str>,
}

impl SyncSummary {
    pub fn view(&self) -> SyncView {
        let severity = if self.conflicts > 0 || self.rejected > 0 || self.stopped.is_some() {
            Severity::Warning
        } else {
            Severity::Success
        };
        let mut detail = format!(
            "{} sincronizada(s), {} duplicada(s), {} conflito(s), {} rejeitada(s), {} pendente(s).",
            self.synced, self.duplicates, self.conflicts, self.rejected, self.remaining_pending
        );
        if let Some(reason) = self.stopped {
            detail.push_str(&format!(" Interrompida: {}.", safe_code(reason)));
        }
        SyncView {
            label: "Sincronização concluída".to_owned(),
            detail: Some(detail),
            severity,
            can_start: self.remaining_pending > 0,
            running: false,
            retry: self.stopped.is_some(),
        }
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct DayPanelView {
    pub phase: PanelPhase,
    pub banner: ConnectivityBanner,
    pub counters: Counters,
    pub operations: Vec<OperationRow>,
    pub reviews: Vec<ReviewRow>,
    pub empty: bool,
    pub error: Option<PanelError>,
    pub notice: Option<Notice>,
    pub sync: SyncView,
    pub last_sync: Option<String>,
    /// Resumo de uma frase, usado por leitores de tela e pelo título da janela.
    pub status_line: String,
}

impl DayPanelView {
    pub fn loading(state: ConnectivityState) -> Self {
        Self {
            phase: PanelPhase::Loading,
            banner: ConnectivityBanner::for_state(state),
            counters: Counters::default(),
            operations: Vec::new(),
            reviews: Vec::new(),
            empty: false,
            error: None,
            notice: None,
            sync: SyncView::idle(0, false),
            last_sync: None,
            status_line: "Carregando o estado da estação.".to_owned(),
        }
    }

    pub fn fatal(error: PanelError, state: ConnectivityState) -> Self {
        Self {
            phase: PanelPhase::Error,
            banner: ConnectivityBanner::for_state(state),
            counters: Counters::default(),
            operations: Vec::new(),
            reviews: Vec::new(),
            empty: false,
            error: Some(error),
            notice: None,
            sync: SyncView::unavailable("Fila local indisponível; a sincronização está bloqueada."),
            last_sync: None,
            status_line: "Erro ao abrir a fila local.".to_owned(),
        }
    }

    /// Todas as strings visíveis, na ordem de leitura. Serve para conferir
    /// ausência de PII/identidade e para testes de conteúdo.
    pub fn visible_text(&self) -> Vec<String> {
        let mut out = Vec::new();
        out.push("Painel do dia".to_owned());
        out.push(format!("{}: {}", self.banner.title, self.banner.detail));
        if let Some(error) = &self.error {
            out.push(error.message.clone());
            out.push(format!("Código: {}", error.code));
            if let Some(hint) = &error.hint {
                out.push(hint.clone());
            }
        }
        if let Some(notice) = &self.notice {
            out.push(notice.text.clone());
        }
        out.push(format!("Pendentes: {}", self.counters.pending));
        out.push(format!("Sincronizadas: {}", self.counters.synced));
        out.push(format!("Falhas: {}", self.counters.failed));
        out.push(format!("Conflitos: {}", self.counters.conflict));
        out.push(format!("Novas tentativas: {}", self.counters.retrying));
        out.push(format!("Revisões abertas: {}", self.counters.review_open));
        for row in &self.operations {
            out.push(format!("#{} {} {}", row.seq, row.kind, row.status.label));
            out.push(format!("Referência: {}", row.entity_ref));
            out.push(format!("Criada em {} ({})", row.created_local, row.age));
            if let Some(code) = &row.error_code {
                out.push(format!("Código: {code}"));
            }
            if row.needs_review {
                out.push("Requer revisão; o valor local foi preservado.".to_owned());
            }
        }
        for review in &self.reviews {
            out.push(format!(
                "Revisão {} — {} — código {}",
                review.kind, review.operation_id, review.code
            ));
            out.push(review.guidance.to_owned());
        }
        out.push(self.sync.label.clone());
        if let Some(detail) = &self.sync.detail {
            out.push(detail.clone());
        }
        if self.empty {
            out.push("Nenhuma operação na fila desta estação.".to_owned());
        }
        if let Some(last) = &self.last_sync {
            out.push(format!("Última sincronização: {last}"));
        }
        out.push(self.status_line.clone());
        out
    }
}

pub struct PanelContext<'a> {
    pub queue: Option<&'a ClientQueue>,
    pub phase: PanelPhase,
    pub error: Option<PanelError>,
    pub notice: Option<Notice>,
    pub sync: SyncView,
    pub last_sync_at: Option<u64>,
    pub now: u64,
}

pub fn present(ctx: PanelContext<'_>) -> DayPanelView {
    let state = ctx
        .queue
        .map_or(ConnectivityState::Offline, |queue| queue.connectivity());
    let banner = ConnectivityBanner::for_state(state);

    let Some(queue) = ctx.queue else {
        let error = ctx.error.unwrap_or_else(|| {
            PanelError::new("queue_unavailable", "Fila local indisponível.", None)
        });
        return DayPanelView {
            phase: PanelPhase::Error,
            banner,
            counters: Counters::default(),
            operations: Vec::new(),
            reviews: Vec::new(),
            empty: false,
            error: Some(error),
            notice: ctx.notice,
            sync: SyncView::unavailable("Fila local indisponível; a sincronização está bloqueada."),
            last_sync: None,
            status_line: "Erro ao abrir a fila local.".to_owned(),
        };
    };

    let status = queue.status();
    let counters = Counters {
        pending: status.queue.pending,
        synced: status.queue.synced,
        failed: status.queue.failed,
        conflict: status.queue.conflict,
        retrying: status.queue.retrying,
        review_open: status.review_tasks_open,
    };

    let mut review_ops: HashSet<&str> = HashSet::new();
    for task in queue.review_tasks() {
        review_ops.insert(task.operation_id.as_str());
    }

    let mut operations: Vec<OperationRow> = queue
        .operations()
        .iter()
        .map(|operation| {
            let needs_review = review_ops.contains(operation.operation_id.as_str());
            let (status_chip, priority) = operation_status(operation.status, operation.attempts);
            OperationRow {
                operation_id: operation.operation_id.clone(),
                entity_ref: operation.entity_id.clone(),
                seq: operation.seq,
                kind: operation_kind_label(operation.kind),
                status: status_chip,
                attempts: operation.attempts,
                created_local: format_local(operation.created_at),
                age: age_label(ctx.now, operation.created_at),
                error_code: operation.last_error.as_deref().map(safe_code),
                needs_review,
                priority,
            }
        })
        .collect();
    operations.sort_by(|left, right| {
        left.priority
            .cmp(&right.priority)
            .then(left.seq.cmp(&right.seq))
    });

    let reviews = queue
        .review_tasks()
        .iter()
        .map(|task| {
            let (kind, severity) = review_kind_label(task.kind);
            ReviewRow {
                task_id: task.task_id.clone(),
                operation_id: task.operation_id.clone(),
                kind,
                code: safe_code(&task.code),
                guidance: "Revisão manual obrigatória. O valor local não foi sobrescrito; abra a sincronização para revisar.",
                severity,
            }
        })
        .collect::<Vec<_>>();

    let empty = operations.is_empty() && reviews.is_empty();
    let phase = if ctx.phase == PanelPhase::Loading {
        PanelPhase::Loading
    } else {
        PanelPhase::Ready
    };

    let status_line = compose_status_line(banner.title, &counters, state);

    DayPanelView {
        phase,
        banner,
        counters,
        operations,
        reviews,
        empty,
        error: ctx.error,
        notice: ctx.notice,
        sync: ctx.sync,
        last_sync: ctx.last_sync_at.map(format_local),
        status_line,
    }
}

fn compose_status_line(
    banner_title: &str,
    counters: &Counters,
    state: ConnectivityState,
) -> String {
    let mut line = match state {
        ConnectivityState::Online => format!("{banner_title}."),
        ConnectivityState::Offline => format!("{banner_title}. A fila permanece local."),
        ConnectivityState::Contingency => {
            format!("{banner_title}. Operação local ativa; emissão fiscal não autorizada.")
        }
    };
    line.push_str(&format!(
        " {} pendente(s), {} conflito(s), {} revisão(ões) aberta(s).",
        counters.pending, counters.conflict, counters.review_open
    ));
    line
}

fn operation_status(status: OperationStatus, attempts: u32) -> (StatusChip, u8) {
    match status {
        OperationStatus::Pending if attempts > 0 => (
            StatusChip {
                label: "Nova tentativa",
                symbol: "~",
                severity: Severity::Warning,
            },
            2,
        ),
        OperationStatus::Pending => (
            StatusChip {
                label: "Pendente",
                symbol: ".",
                severity: Severity::Info,
            },
            3,
        ),
        OperationStatus::Synced => (
            StatusChip {
                label: "Sincronizada",
                symbol: "+",
                severity: Severity::Success,
            },
            4,
        ),
        OperationStatus::Failed => (
            StatusChip {
                label: "Falhou",
                symbol: "x",
                severity: Severity::Danger,
            },
            1,
        ),
        OperationStatus::Conflict => (
            StatusChip {
                label: "Conflito",
                symbol: "!",
                severity: Severity::Danger,
            },
            0,
        ),
    }
}

fn operation_kind_label(kind: OperationKind) -> &'static str {
    match kind {
        OperationKind::MasterUpsert => "Cadastro mestre",
        OperationKind::ContractCreate => "Contrato",
        OperationKind::FuneralJourneyCreate => "Jornada funerária",
        OperationKind::FuneralServiceOrderAdvance => "Avanço de OS funerária",
        OperationKind::FuneralServiceOrderPause => "Pausa de OS funerária",
        OperationKind::FuneralServiceOrderComplete => "Conclusão de OS funerária",
        OperationKind::CashOpen => "Abertura de caixa",
        OperationKind::CashMovement => "Movimento de caixa",
        OperationKind::CashClose => "Fechamento de caixa",
        OperationKind::FiscalEmission => "Emissão fiscal",
    }
}

fn review_kind_label(kind: ReviewTaskKind) -> (&'static str, Severity) {
    match kind {
        ReviewTaskKind::SyncFailure => ("Falha de sincronização", Severity::Danger),
        ReviewTaskKind::FuneralConflict => ("Conflito de ordem funerária", Severity::Danger),
        ReviewTaskKind::MasterConflict => ("Conflito de cadastro", Severity::Warning),
        ReviewTaskKind::MoneyConflict => ("Conflito financeiro", Severity::Danger),
        ReviewTaskKind::FiscalConflict => ("Conflito fiscal", Severity::Danger),
        ReviewTaskKind::FiscalFailure => ("Rejeição fiscal", Severity::Danger),
    }
}

/// Formata um instante Unix em `America/Sao_Paulo` (borda de apresentação).
pub fn format_local(timestamp: u64) -> String {
    match DateTime::<Utc>::from_timestamp(timestamp as i64, 0) {
        Some(utc) => utc
            .with_timezone(&Sao_Paulo)
            .format("%d/%m %H:%M")
            .to_string(),
        None => "--/-- --:--".to_owned(),
    }
}

/// Idade legível, sem depender de relógio global.
pub fn age_label(now: u64, then: u64) -> String {
    if now <= then {
        return "agora".to_owned();
    }
    let seconds = now - then;
    if seconds < 60 {
        format!("há {seconds}s")
    } else if seconds < 3_600 {
        format!("há {}min", seconds / 60)
    } else if seconds < 86_400 {
        format!("há {}h", seconds / 3_600)
    } else {
        format!("há {}d", seconds / 86_400)
    }
}

/// Defesa em profundidade: a UI só mostra códigos técnicos filtrados, mesmo que
/// uma origem futura grave algo inesperado em `last_error`.
pub fn safe_code(code: &str) -> String {
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

#[cfg(test)]
mod tests {
    use super::*;
    use crate::{OperationDraft, ReconciliationOutcome};
    use serde_json::json;

    fn queue() -> ClientQueue {
        ClientQueue::new("tenant-secreto", "node-secreto").expect("queue")
    }

    fn contract(key: &str) -> OperationDraft {
        OperationDraft {
            kind: OperationKind::ContractCreate,
            entity_id: "contract-1".to_owned(),
            idempotency_key: key.to_owned(),
            payload: json!({"total_cents": 100, "currency": "BRL"}),
        }
    }

    fn sync_idle() -> SyncView {
        SyncView::idle(0, true)
    }

    fn present_queue(queue: &ClientQueue, now: u64) -> DayPanelView {
        present(PanelContext {
            queue: Some(queue),
            phase: PanelPhase::Ready,
            error: None,
            notice: None,
            sync: sync_idle(),
            last_sync_at: None,
            now,
        })
    }

    #[test]
    fn empty_queue_shows_explicit_empty_state() {
        let queue = queue();
        let view = present_queue(&queue, 1_000);
        assert_eq!(view.phase, PanelPhase::Ready);
        assert!(view.empty);
        assert!(view.operations.is_empty());
        assert_eq!(view.counters.pending, 0);
        assert_eq!(view.banner.state, ConnectivityState::Online);
        assert!(
            view.visible_text()
                .iter()
                .any(|line| line.contains("Nenhuma operação"))
        );
    }

    #[test]
    fn offline_banner_is_distinct_and_keeps_queue_available() {
        let mut queue = queue();
        queue
            .set_connectivity(ConnectivityState::Offline)
            .expect("offline");
        queue
            .enqueue("operator-1", contract("op-1"), 100)
            .expect("enqueue");
        let view = present_queue(&queue, 160);
        assert_eq!(view.banner.state, ConnectivityState::Offline);
        assert!(view.banner.queue_available);
        assert!(view.banner.detail.contains("fila continua local"));
        assert_eq!(view.operations.len(), 1);
        assert!(view.status_line.contains("fila permanece local"));
    }

    #[test]
    fn contingency_never_claims_fiscal_authorization() {
        let mut queue = queue();
        queue
            .set_connectivity(ConnectivityState::Contingency)
            .expect("contingency");
        let view = present_queue(&queue, 10);
        assert!(view.banner.detail.contains("não autoriza emissão fiscal"));
        assert!(view.status_line.contains("emissão fiscal não autorizada"));
        assert_eq!(view.banner.severity, Severity::Warning);
    }

    #[test]
    fn conflict_and_failure_are_prioritized_and_require_review() {
        let mut queue = queue();
        let pending = queue
            .enqueue("operator-1", contract("op-1"), 10)
            .expect("pending");
        let conflict = queue
            .enqueue("operator-1", contract("op-2"), 20)
            .expect("conflict");
        let failed = queue
            .enqueue("operator-1", contract("op-3"), 30)
            .expect("failed");
        queue
            .reconcile(
                &conflict.operation.operation_id,
                ReconciliationOutcome::Conflict {
                    code: "server_version_conflict<script>".to_owned(),
                },
                40,
            )
            .expect("conflict");
        queue
            .reconcile(
                &failed.operation.operation_id,
                ReconciliationOutcome::Retry {
                    reason: crate::RetryReason::TransportTimeout,
                },
                41,
            )
            .expect("retry");
        queue
            .reconcile(
                &failed.operation.operation_id,
                ReconciliationOutcome::Retry {
                    reason: crate::RetryReason::TransportTimeout,
                },
                42,
            )
            .expect("retry");
        queue
            .reconcile(
                &failed.operation.operation_id,
                ReconciliationOutcome::Retry {
                    reason: crate::RetryReason::TransportTimeout,
                },
                43,
            )
            .expect("exhausted");

        let view = present_queue(&queue, 100);
        assert_eq!(view.counters.conflict, 1);
        assert_eq!(view.counters.failed, 1);
        assert_eq!(view.counters.review_open, 2);
        assert_eq!(view.operations[0].status.label, "Conflito");
        assert_eq!(view.operations[1].status.label, "Falhou");
        assert!(view.operations[0].needs_review);
        assert!(view.operations[1].needs_review);
        assert_eq!(
            view.operations[2].operation_id,
            pending.operation.operation_id
        );
        assert_eq!(view.reviews.len(), 2);
        assert!(view.reviews.iter().all(|review| !review.code.contains('<')));
        assert!(
            view.visible_text()
                .iter()
                .any(|line| line.contains("valor local foi preservado"))
        );
    }

    #[test]
    fn visible_text_never_leaks_identity_payload_or_token() {
        let mut queue = queue();
        queue
            .enqueue("operator-1", contract("op-1"), 10)
            .expect("enqueue");
        let view = present_queue(&queue, 20);
        let text = view.visible_text().join("\n");
        assert!(!text.contains("tenant-secreto"));
        assert!(!text.contains("node-secreto"));
        assert!(!text.contains("total_cents"));
        assert!(!text.contains("operator-1"));
        assert!(text.contains("contract-1"));
    }

    #[test]
    fn sync_unavailable_explains_instead_of_simulating_success() {
        let queue = queue();
        let view = present(PanelContext {
            queue: Some(&queue),
            phase: PanelPhase::Ready,
            error: None,
            notice: None,
            sync: SyncView::unavailable("Relay não configurado."),
            last_sync_at: None,
            now: 10,
        });
        assert!(!view.sync.can_start);
        assert_eq!(view.sync.severity, Severity::Warning);
        assert!(view.sync.detail.as_deref().unwrap_or("").contains("Relay"));
    }

    #[test]
    fn fatal_error_has_code_and_hint_without_payload() {
        let error = PanelError::from_queue_error(&ClientError::CorruptStore("hash de integridade"));
        let view = DayPanelView::fatal(error, ConnectivityState::Offline);
        assert_eq!(view.phase, PanelPhase::Error);
        assert_eq!(view.error.as_ref().expect("error").code, "corrupt_store");
        assert!(!view.sync.can_start);
        let text = view.visible_text().join("\n");
        assert!(text.contains("suporte"));
        assert!(!text.contains("total_cents"));
    }

    #[test]
    fn local_time_and_age_are_deterministic() {
        // 2026-11-25T21:00:00Z = 18:00 em America/Sao_Paulo (UTC-3).
        let timestamp = 1_795_640_400;
        assert_eq!(format_local(timestamp), "25/11 18:00");
        assert_eq!(age_label(timestamp + 45, timestamp), "há 45s");
        assert_eq!(age_label(timestamp + 120, timestamp), "há 2min");
        assert_eq!(age_label(timestamp + 7_200, timestamp), "há 2h");
        assert_eq!(age_label(timestamp + 172_800, timestamp), "há 2d");
        assert_eq!(age_label(timestamp, timestamp), "agora");
    }

    #[test]
    fn safe_code_strips_anything_that_is_not_technical() {
        assert_eq!(safe_code("email a@b.com"), "emailab.com");
        assert_eq!(safe_code(""), "unknown");
        assert_eq!(safe_code("http_409_conflict"), "http_409_conflict");
    }
}
