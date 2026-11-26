//! Modelo puro do atendimento funerário (CL-02).
//!
//! O formulário aceita somente referências opacas e valores operacionais. Não há
//! nome, documento, contato ou texto livre no payload enfileirado. A API do
//! servidor continua sendo a autoridade para entitlement, contrato `active` e
//! transições válidas.

use crate::{
    ClientError, ConnectivityState, OperationDraft, OperationKind, QueuedOperation, validate_draft,
};
use serde_json::json;

pub const SCREEN_ID: &str = "CL-02";

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum AttendanceAction {
    CreateJourney,
    AdvanceOrder,
    CompleteOrder,
}

impl AttendanceAction {
    pub const fn label(self) -> &'static str {
        match self {
            Self::CreateJourney => "Criar jornada",
            Self::AdvanceOrder => "Avançar ordem",
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
    Error {
        code: String,
        message: String,
    },
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
}

impl AttendanceView {
    pub fn new(
        connectivity: ConnectivityState,
        entitlement: EntitlementState,
        relay_configured: bool,
        operator_configured: bool,
        status: AttendanceStatus,
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
            (
                OperationKind::FuneralServiceOrderAdvance,
                order.to_owned(),
                format!("cl02-advance-{order}"),
                json!({"status": "in_progress"}),
            )
        }
        AttendanceAction::CompleteOrder => {
            let order = required_ref(&form.order_ref, "order_ref")?;
            (
                OperationKind::FuneralServiceOrderComplete,
                order.to_owned(),
                format!("cl02-complete-{order}"),
                json!({"status": "completed"}),
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
            );
            assert!(view.can_submit);
        }
    }
}
