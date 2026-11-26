//! Modelo puro do atendimento funerário (CL-02) e das ordens de serviço (CL-03).
//!
//! O formulário aceita somente referências opacas e valores operacionais. Não há
//! nome, documento, contato ou texto livre no payload enfileirado. A API do
//! servidor continua sendo a autoridade para entitlement, contrato `active` e
//! transições válidas.

use crate::{
    ClientError, ClientQueue, ConnectivityState, OperationDraft, OperationKind, OperationStatus,
    QueuedOperation, validate_draft,
};
use serde_json::json;

pub const SCREEN_ID: &str = "CL-02";
pub const SERVICE_ORDER_SCREEN_ID: &str = "CL-03";

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum AttendanceAction {
    CreateJourney,
    AdvanceOrder,
    PauseOrder,
    CompleteOrder,
}

impl AttendanceAction {
    pub const fn label(self) -> &'static str {
        match self {
            Self::CreateJourney => "Criar jornada",
            Self::AdvanceOrder => "Avançar ordem",
            Self::PauseOrder => "Pausar ordem",
            Self::CompleteOrder => "Concluir ordem",
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum EntitlementState {
    Active,
    Blocked,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum AttendanceServiceState {
    Online,
    Offline,
    Contingency,
    ServiceUnavailable,
    EntitlementBlocked,
}

impl AttendanceServiceState {
    pub const fn label(self) -> &'static str {
        match self {
            Self::Online => "Online",
            Self::Offline => "Offline",
            Self::Contingency => "Contingência",
            Self::ServiceUnavailable => "Serviço indisponível",
            Self::EntitlementBlocked => "Módulo bloqueado",
        }
    }
}

#[derive(Debug, Clone, PartialEq, Eq, Default)]
pub enum AttendanceStatus {
    #[default]
    Ready,
    Queued {
        operation_id: String,
        seq: u64,
    },
    Conflict {
        operation_id: String,
        code: String,
    },
    Error {
        code: String,
        message: String,
    },
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ServiceOrderStatus {
    Open,
    InProgress,
    Paused,
    Completed,
}

impl ServiceOrderStatus {
    pub const fn label(self) -> &'static str {
        match self {
            Self::Open => "open",
            Self::InProgress => "in_progress",
            Self::Paused => "paused",
            Self::Completed => "completed",
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ServiceOrderSyncState {
    Queued,
    Done,
    Error,
    Conflict,
}

impl ServiceOrderSyncState {
    pub const fn label(self) -> &'static str {
        match self {
            Self::Queued => "queued",
            Self::Done => "done",
            Self::Error => "error",
            Self::Conflict => "conflict",
        }
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ServiceOrderCard {
    pub order_id: String,
    pub status: ServiceOrderStatus,
    pub version: u64,
    pub sync_state: ServiceOrderSyncState,
    pub operation_id: String,
    pub seq: u64,
    pub error_code: Option<String>,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct AttendanceForm {
    pub action: AttendanceAction,
    pub party_ref: String,
    pub contract_ref: String,
    pub service_kind: String,
    pub location_ref: String,
    pub resource_ref: String,
    pub starts_at: String,
    pub ends_at: String,
    pub time_zone: String,
    pub order_ref: String,
    pub order_version: String,
    pub contract_active_confirmed: bool,
}

impl Default for AttendanceForm {
    fn default() -> Self {
        Self {
            action: AttendanceAction::CreateJourney,
            party_ref: String::new(),
            contract_ref: String::new(),
            service_kind: "wake".to_owned(),
            location_ref: String::new(),
            resource_ref: String::new(),
            starts_at: String::new(),
            ends_at: String::new(),
            time_zone: "America/Fortaleza".to_owned(),
            order_ref: String::new(),
            order_version: "0".to_owned(),
            contract_active_confirmed: false,
        }
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct AttendanceView {
    pub service_state: AttendanceServiceState,
    pub status: AttendanceStatus,
    pub can_submit: bool,
    pub detail: &'static str,
    pub service_orders: Vec<ServiceOrderCard>,
}

impl AttendanceView {
    pub fn new(
        connectivity: ConnectivityState,
        entitlement: EntitlementState,
        relay_configured: bool,
        operator_configured: bool,
        status: AttendanceStatus,
        service_orders: Vec<ServiceOrderCard>,
    ) -> Self {
        let service_state = if entitlement == EntitlementState::Blocked {
            AttendanceServiceState::EntitlementBlocked
        } else if !operator_configured
            || (connectivity == ConnectivityState::Online && !relay_configured)
        {
            AttendanceServiceState::ServiceUnavailable
        } else {
            match connectivity {
                ConnectivityState::Online => AttendanceServiceState::Online,
                ConnectivityState::Offline => AttendanceServiceState::Offline,
                ConnectivityState::Contingency => AttendanceServiceState::Contingency,
            }
        };
        let detail = match service_state {
            AttendanceServiceState::Online => {
                "A operação será persistida na fila e poderá ser sincronizada pelo painel do dia."
            }
            AttendanceServiceState::Offline => {
                "A operação será enfileirada localmente e enviada quando houver conexão."
            }
            AttendanceServiceState::Contingency => {
                "A operação será enfileirada em contingência; nenhuma emissão fiscal é autorizada aqui."
            }
            AttendanceServiceState::ServiceUnavailable => {
                "Relay ou identidade do operador indisponível; nenhuma operação será simulada."
            }
            AttendanceServiceState::EntitlementBlocked => {
                "O módulo funeral não está habilitado para esta estação; nenhuma operação será criada."
            }
        };
        let status_ready = matches!(status, AttendanceStatus::Ready);
        let can_submit = status_ready
            && entitlement == EntitlementState::Active
            && operator_configured
            && service_state != AttendanceServiceState::ServiceUnavailable;
        Self {
            service_state,
            status,
            can_submit,
            detail,
            service_orders,
        }
    }
}

pub fn build_draft(form: &AttendanceForm) -> Result<OperationDraft, ClientError> {
    let (kind, entity_id, idempotency_key, payload) = match form.action {
        AttendanceAction::CreateJourney => {
            if !form.contract_active_confirmed {
                return Err(ClientError::InvalidPayload("contract_active_required"));
            }
            let contract = required_ref(&form.contract_ref, "contract_ref")?;
            let service_kind = required_ref(&form.service_kind, "service_kind")?;
            let location = required_ref(&form.location_ref, "location_id")?;
            let starts = required_ref(&form.starts_at, "starts_at")?;
            let ends = required_ref(&form.ends_at, "ends_at")?;
            let time_zone = required_ref(&form.time_zone, "time_zone")?;
            let mut service_order = serde_json::Map::new();
            service_order.insert("kind".to_owned(), json!(service_kind));
            service_order.insert("description".to_owned(), json!("atendimento operacional"));
            service_order.insert("scheduled_for".to_owned(), json!(starts));
            let mut schedule = serde_json::Map::new();
            schedule.insert("kind".to_owned(), json!(service_kind));
            schedule.insert("location_id".to_owned(), json!(location));
            schedule.insert("starts_at".to_owned(), json!(starts));
            schedule.insert("ends_at".to_owned(), json!(ends));
            schedule.insert("time_zone".to_owned(), json!(time_zone));
            if !form.resource_ref.trim().is_empty() {
                schedule.insert("resource_id".to_owned(), json!(form.resource_ref.trim()));
            }
            let mut payload = json!({
                "service_order": service_order,
                "schedule": schedule,
            });
            if !form.party_ref.trim().is_empty() {
                payload["party_ref"] = json!(form.party_ref.trim());
            }
            (
                OperationKind::FuneralJourneyCreate,
                contract.to_owned(),
                format!("cl02-journey-{contract}"),
                payload,
            )
        }
        AttendanceAction::AdvanceOrder => {
            let order = required_ref(&form.order_ref, "order_ref")?;
            let expected_version = parse_version(&form.order_version)?;
            (
                OperationKind::FuneralServiceOrderAdvance,
                order.to_owned(),
                format!("cl03-advance-{order}-{expected_version}"),
                json!({"status": "in_progress", "expected_version": expected_version}),
            )
        }
        AttendanceAction::PauseOrder => {
            let order = required_ref(&form.order_ref, "order_ref")?;
            let expected_version = parse_version(&form.order_version)?;
            (
                OperationKind::FuneralServiceOrderPause,
                order.to_owned(),
                format!("cl03-pause-{order}-{expected_version}"),
                json!({"status": "paused", "expected_version": expected_version}),
            )
        }
        AttendanceAction::CompleteOrder => {
            let order = required_ref(&form.order_ref, "order_ref")?;
            let expected_version = parse_version(&form.order_version)?;
            (
                OperationKind::FuneralServiceOrderComplete,
                order.to_owned(),
                format!("cl03-complete-{order}-{expected_version}"),
                json!({"status": "completed", "expected_version": expected_version}),
            )
        }
    };
    let draft = OperationDraft {
        kind,
        entity_id,
        idempotency_key,
        payload,
    };
    validate_draft(&draft)?;
    Ok(draft)
}

fn parse_version(value: &str) -> Result<u64, ClientError> {
    let value = value.trim();
    let version = value
        .parse::<u64>()
        .map_err(|_| ClientError::InvalidPayload("expected_version invalido"))?;
    if version > i64::MAX as u64 {
        return Err(ClientError::InvalidPayload("expected_version invalido"));
    }
    Ok(version)
}

pub fn required_ref<'a>(value: &'a str, field: &'static str) -> Result<&'a str, ClientError> {
    let value = value.trim();
    if value.is_empty() {
        return Err(ClientError::InvalidPayload(field));
    }
    if value.len() > 128
        || !value
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || b"-_.:/+TZ".contains(&byte))
    {
        return Err(ClientError::InvalidPayload(field));
    }
    Ok(value)
}

pub fn queued_status(operation: &QueuedOperation) -> AttendanceStatus {
    AttendanceStatus::Queued {
        operation_id: operation.operation_id.clone(),
        seq: operation.seq,
    }
}

pub fn service_order_cards(queue: &ClientQueue) -> Vec<ServiceOrderCard> {
    let mut cards: Vec<ServiceOrderCard> = Vec::new();
    for operation in queue.operations().iter().filter(|operation| {
        matches!(
            operation.kind,
            OperationKind::FuneralServiceOrderAdvance
                | OperationKind::FuneralServiceOrderPause
                | OperationKind::FuneralServiceOrderComplete
        )
    }) {
        let Some(status) = operation
            .payload
            .get("status")
            .and_then(|value| value.as_str())
            .and_then(parse_order_status)
        else {
            continue;
        };
        let version = operation
            .payload
            .get("expected_version")
            .and_then(|value| value.as_u64())
            .unwrap_or(0)
            .saturating_add(1);
        let sync_state = match operation.status {
            OperationStatus::Pending => ServiceOrderSyncState::Queued,
            OperationStatus::Synced => ServiceOrderSyncState::Done,
            OperationStatus::Failed => ServiceOrderSyncState::Error,
            OperationStatus::Conflict => ServiceOrderSyncState::Conflict,
        };
        let card = ServiceOrderCard {
            order_id: operation.entity_id.clone(),
            status,
            version,
            sync_state,
            operation_id: operation.operation_id.clone(),
            seq: operation.seq,
            error_code: operation.last_error.clone(),
        };
        if let Some(previous) = cards
            .iter_mut()
            .find(|previous| previous.order_id == card.order_id)
        {
            if card.seq > previous.seq {
                *previous = card;
            }
        } else {
            cards.push(card);
        }
    }
    cards.sort_by(|left, right| left.order_id.cmp(&right.order_id));
    cards
}

fn parse_order_status(value: &str) -> Option<ServiceOrderStatus> {
    match value {
        "open" => Some(ServiceOrderStatus::Open),
        "in_progress" => Some(ServiceOrderStatus::InProgress),
        "paused" => Some(ServiceOrderStatus::Paused),
        "completed" => Some(ServiceOrderStatus::Completed),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn journey_form() -> AttendanceForm {
        AttendanceForm {
            contract_ref: "contract-1".to_owned(),
            service_kind: "wake".to_owned(),
            location_ref: "location-1".to_owned(),
            resource_ref: "resource-1".to_owned(),
            starts_at: "2026-11-25T12:00:00-03:00".to_owned(),
            ends_at: "2026-11-25T14:00:00-03:00".to_owned(),
            contract_active_confirmed: true,
            ..AttendanceForm::default()
        }
    }

    #[test]
    fn journey_draft_is_idempotent_and_has_no_personal_data() {
        let draft = build_draft(&journey_form()).expect("draft");
        assert_eq!(draft.kind, OperationKind::FuneralJourneyCreate);
        assert_eq!(draft.idempotency_key, "cl02-journey-contract-1");
        assert!(
            serde_json::to_string(&draft.payload)
                .expect("json")
                .contains("atendimento operacional")
        );
    }

    #[test]
    fn inactive_contract_is_rejected_locally() {
        let error = build_draft(&journey_form_with_active(false)).expect_err("must reject");
        assert_eq!(
            error,
            ClientError::InvalidPayload("contract_active_required")
        );
    }

    fn journey_form_with_active(active: bool) -> AttendanceForm {
        AttendanceForm {
            contract_active_confirmed: active,
            ..journey_form()
        }
    }

    #[test]
    fn unavailable_online_does_not_allow_submit() {
        let view = AttendanceView::new(
            ConnectivityState::Online,
            EntitlementState::Active,
            false,
            true,
            AttendanceStatus::Ready,
            Vec::new(),
        );
        assert_eq!(
            view.service_state,
            AttendanceServiceState::ServiceUnavailable
        );
        assert!(!view.can_submit);
    }

    #[test]
    fn offline_and_contingency_allow_queued_work_when_entitled() {
        for state in [ConnectivityState::Offline, ConnectivityState::Contingency] {
            let view = AttendanceView::new(
                state,
                EntitlementState::Active,
                false,
                true,
                AttendanceStatus::Ready,
                Vec::new(),
            );
            assert!(view.can_submit);
        }
    }

    #[test]
    fn order_mutations_carry_version_and_distinct_idempotency_keys() {
        let mut form = AttendanceForm {
            order_ref: "order-1".to_owned(),
            order_version: "3".to_owned(),
            action: AttendanceAction::AdvanceOrder,
            ..AttendanceForm::default()
        };
        let advance = build_draft(&form).expect("advance");
        assert_eq!(advance.idempotency_key, "cl03-advance-order-1-3");
        assert_eq!(advance.payload["expected_version"], 3);

        form.action = AttendanceAction::PauseOrder;
        form.order_version = "4".to_owned();
        let pause = build_draft(&form).expect("pause");
        assert_eq!(pause.kind, OperationKind::FuneralServiceOrderPause);
        assert_eq!(pause.idempotency_key, "cl03-pause-order-1-4");
        assert_ne!(advance.idempotency_key, pause.idempotency_key);

        form.action = AttendanceAction::CompleteOrder;
        form.order_version = "5".to_owned();
        let complete = build_draft(&form).expect("complete");
        assert_eq!(complete.payload["expected_version"], 5);
        assert_eq!(complete.payload["status"], "completed");
    }

    #[test]
    fn service_order_cards_rebuild_from_persisted_queue_and_preserve_conflict() {
        let mut queue = ClientQueue::new("tenant-a", "node-1").expect("queue");
        let first = queue
            .enqueue(
                "operator-1",
                OperationDraft {
                    kind: OperationKind::FuneralServiceOrderAdvance,
                    entity_id: "order-1".to_owned(),
                    idempotency_key: "cl03-advance-order-1-0".to_owned(),
                    payload: json!({"status": "in_progress", "expected_version": 0}),
                },
                10,
            )
            .expect("advance");
        let second = queue
            .enqueue(
                "operator-1",
                OperationDraft {
                    kind: OperationKind::FuneralServiceOrderPause,
                    entity_id: "order-1".to_owned(),
                    idempotency_key: "cl03-pause-order-1-1".to_owned(),
                    payload: json!({"status": "paused", "expected_version": 1}),
                },
                11,
            )
            .expect("pause");
        let cards = service_order_cards(&queue);
        assert_eq!(cards.len(), 1);
        assert_eq!(cards[0].status, ServiceOrderStatus::Paused);
        assert_eq!(cards[0].version, 2);
        assert_eq!(cards[0].sync_state, ServiceOrderSyncState::Queued);

        queue
            .reconcile(
                &first.operation.operation_id,
                crate::ReconciliationOutcome::Applied { server_cursor: 1 },
                12,
            )
            .expect("advance synced");
        queue
            .reconcile(
                &second.operation.operation_id,
                crate::ReconciliationOutcome::Conflict {
                    code: "CONFLICT_UNREVIEWED".to_owned(),
                },
                13,
            )
            .expect("pause conflict");
        let cards = service_order_cards(&queue);
        assert_eq!(cards[0].sync_state, ServiceOrderSyncState::Conflict);
        assert_eq!(
            cards[0].error_code.as_deref(),
            Some("conflict:CONFLICT_UNREVIEWED")
        );
    }

    #[test]
    fn service_order_projection_survives_queue_reopen() {
        let path =
            std::env::temp_dir().join(format!("amparius-client-cl03-{}.json", crate::unix_now()));
        {
            let mut queue = ClientQueue::open(&path, "tenant-a", "node-1").expect("open");
            queue
                .set_connectivity(ConnectivityState::Contingency)
                .expect("contingency");
            queue
                .enqueue(
                    "operator-1",
                    OperationDraft {
                        kind: OperationKind::FuneralServiceOrderComplete,
                        entity_id: "order-2".to_owned(),
                        idempotency_key: "cl03-complete-order-2-4".to_owned(),
                        payload: json!({"status": "completed", "expected_version": 4}),
                    },
                    10,
                )
                .expect("complete");
        }
        let queue = ClientQueue::open(&path, "tenant-a", "node-1").expect("reopen");
        let cards = service_order_cards(&queue);
        assert_eq!(queue.connectivity(), ConnectivityState::Contingency);
        assert_eq!(cards[0].order_id, "order-2");
        assert_eq!(cards[0].version, 5);
        assert_eq!(cards[0].sync_state, ServiceOrderSyncState::Queued);
        let _ = std::fs::remove_file(&path);
        let _ = std::fs::remove_file(path.with_extension("tmp"));
        let _ = std::fs::remove_file(path.with_extension("bak"));
    }
}
