use serde::{Deserialize, Serialize};
use serde_json::{Map, Value};
use sha2::{Digest, Sha256};
use std::time::{SystemTime, UNIX_EPOCH};
use thiserror::Error;

const MAX_IDEMPOTENCY_KEY_LEN: usize = 128;
const MAX_ENTITY_ID_LEN: usize = 128;
const MAX_REASON_LEN: usize = 200;
const MAX_PAYLOAD_BYTES: usize = 64 * 1024;
const MAX_PAYLOAD_DEPTH: usize = 24;

pub fn unix_now() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_or(0, |duration| duration.as_secs())
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum OperationKind {
    MasterUpsert,
    ContractCreate,
    CashOpen,
    CashMovement,
    CashClose,
    FiscalEmission,
}

impl OperationKind {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::MasterUpsert => "master_upsert",
            Self::ContractCreate => "contract_create",
            Self::CashOpen => "cash_open",
            Self::CashMovement => "cash_movement",
            Self::CashClose => "cash_close",
            Self::FiscalEmission => "fiscal_emission",
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum OperationStatus {
    Pending,
    Synced,
    Failed,
    Conflict,
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
    pub actor_subject_id: String,
    pub payload: Value,
    pub content_hash: String,
    pub status: OperationStatus,
    pub created_at: u64,
    pub updated_at: u64,
    pub attempts: u32,
    pub last_error: Option<String>,
    pub synced_at: Option<u64>,
    pub server_cursor: Option<u64>,
    #[serde(default)]
    pub authoritative_version: Option<u64>,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct DeviceSequencer {
    pub tenant_id: String,
    pub node_id: String,
    pub last_seq: u64,
}

#[derive(Debug, Clone, PartialEq)]
pub struct OperationDraft {
    pub kind: OperationKind,
    pub entity_id: String,
    pub idempotency_key: String,
    pub payload: Value,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum RelayMode {
    Online,
    OfflineQueued,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct QueueStatus {
    pub tenant_id: String,
    pub node_id: Option<String>,
    pub pending: usize,
    pub synced: usize,
    pub failed: usize,
    pub conflict: usize,
    pub total: usize,
    pub oldest_pending_at: Option<u64>,
    pub oldest_pending_age_secs: u64,
    pub last_seq: u64,
    pub last_error: Option<String>,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct RelayStatus {
    pub mode: RelayMode,
    pub queue: QueueStatus,
}

#[derive(Debug, Error, PartialEq, Eq)]
pub enum QueueError {
    #[error("tenant_id vazio")]
    EmptyTenant,
    #[error("node_id vazio")]
    EmptyNode,
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
    #[error("transicao de status invalida")]
    InvalidTransition,
    #[error("registro de operacao adulterado")]
    Tampered,
    #[error("sequencia do dispositivo esgotada")]
    SequenceExhausted,
}

impl QueueError {
    pub fn is_client_error(&self) -> bool {
        matches!(
            self,
            Self::EmptyTenant
                | Self::EmptyNode
                | Self::EmptyEntity
                | Self::EmptyIdempotencyKey
                | Self::InvalidIdentifier
                | Self::InvalidPayload(_)
                | Self::PayloadTooLarge
                | Self::PersonalDataInPayload
        )
    }
}

pub fn validate_draft(draft: &OperationDraft) -> Result<(), QueueError> {
    validate_identifier("entity_id", &draft.entity_id, MAX_ENTITY_ID_LEN)?;
    validate_identifier(
        "idempotency_key",
        &draft.idempotency_key,
        MAX_IDEMPOTENCY_KEY_LEN,
    )?;
    let object = draft
        .payload
        .as_object()
        .ok_or(QueueError::InvalidPayload("payload precisa ser objeto"))?;
    match draft.kind {
        OperationKind::MasterUpsert => {
            let base_version = object.get("base_version").and_then(Value::as_u64).ok_or(
                QueueError::InvalidPayload("base_version precisa ser inteiro"),
            )?;
            if base_version > i64::MAX as u64 {
                return Err(QueueError::InvalidPayload("base_version invalida"));
            }
        }
        OperationKind::ContractCreate => {
            require_cents(object, "total_cents", 0)?;
            optional_currency(object)?;
            if let Some(item_count) = object.get("item_count") {
                let count = item_count
                    .as_u64()
                    .ok_or(QueueError::InvalidPayload("item_count precisa ser inteiro"))?;
                if count == 0 {
                    return Err(QueueError::InvalidPayload(
                        "item_count precisa ser maior que zero",
                    ));
                }
            }
        }
        OperationKind::CashOpen => {
            require_cents(object, "opening_amount_cents", 0)?;
        }
        OperationKind::CashMovement => {
            let direction = object
                .get("direction")
                .and_then(Value::as_str)
                .ok_or(QueueError::InvalidPayload("direction obrigatoria"))?;
            if direction != "in" && direction != "out" {
                return Err(QueueError::InvalidPayload(
                    "direction precisa ser in ou out",
                ));
            }
            require_cents(object, "amount_cents", 1)?;
            let reason = object
                .get("reason")
                .and_then(Value::as_str)
                .ok_or(QueueError::InvalidPayload("reason obrigatorio"))?;
            if reason.trim().is_empty() || reason.len() > MAX_REASON_LEN {
                return Err(QueueError::InvalidPayload("reason invalido"));
            }
        }
        OperationKind::CashClose => {
            require_cents(object, "counted_amount_cents", 0)?;
            if object.contains_key("expected_amount_cents") {
                require_cents(object, "expected_amount_cents", 0)?;
            }
        }
        OperationKind::FiscalEmission => {
            let document_ref = object
                .get("document_ref")
                .and_then(Value::as_str)
                .ok_or(QueueError::InvalidPayload("document_ref obrigatorio"))?;
            validate_identifier("entity_id", document_ref, MAX_ENTITY_ID_LEN)?;
        }
    }
    reject_personal_data(&draft.payload, 0)?;
    let encoded = serde_json::to_vec(&draft.payload)
        .map_err(|_| QueueError::InvalidPayload("payload nao serializavel"))?;
    if encoded.len() > MAX_PAYLOAD_BYTES {
        return Err(QueueError::PayloadTooLarge);
    }
    Ok(())
}

pub fn content_hash(draft: &OperationDraft) -> Result<String, QueueError> {
    let canonical = serde_json::to_vec(&(
        draft.kind.as_str(),
        draft.entity_id.as_str(),
        &draft.payload,
    ))
    .map_err(|_| QueueError::InvalidPayload("payload nao serializavel"))?;
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

pub fn verify_operation(operation: &QueuedOperation) -> Result<(), QueueError> {
    if operation.seq == 0 {
        return Err(QueueError::Tampered);
    }
    let draft = OperationDraft {
        kind: operation.kind,
        entity_id: operation.entity_id.clone(),
        idempotency_key: operation.idempotency_key.clone(),
        payload: operation.payload.clone(),
    };
    let expected_hash = content_hash(&draft)?;
    if expected_hash != operation.content_hash {
        return Err(QueueError::Tampered);
    }
    if operation_id(
        &operation.tenant_id,
        &operation.node_id,
        operation.seq,
        &operation.content_hash,
    ) != operation.operation_id
    {
        return Err(QueueError::Tampered);
    }
    Ok(())
}

fn validate_identifier(name: &str, value: &str, max: usize) -> Result<(), QueueError> {
    if value.trim().is_empty() {
        return Err(match name {
            "entity_id" => QueueError::EmptyEntity,
            _ => QueueError::EmptyIdempotencyKey,
        });
    }
    if value.len() > max
        || !value
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || b"-_.:".contains(&byte))
    {
        return Err(QueueError::InvalidIdentifier);
    }
    Ok(())
}

fn require_cents(
    object: &Map<String, Value>,
    field: &'static str,
    minimum: i64,
) -> Result<(), QueueError> {
    let value = object
        .get(field)
        .and_then(Value::as_i64)
        .ok_or(QueueError::InvalidPayload(field))?;
    if value < minimum {
        return Err(QueueError::InvalidPayload(field));
    }
    Ok(())
}

fn optional_currency(object: &Map<String, Value>) -> Result<(), QueueError> {
    if let Some(currency) = object.get("currency") {
        let currency = currency
            .as_str()
            .ok_or(QueueError::InvalidPayload("currency precisa ser texto"))?;
        if currency != "BRL" {
            return Err(QueueError::InvalidPayload("currency precisa ser BRL"));
        }
    }
    Ok(())
}

fn reject_personal_data(value: &Value, depth: usize) -> Result<(), QueueError> {
    if depth > MAX_PAYLOAD_DEPTH {
        return Err(QueueError::InvalidPayload("payload profundo demais"));
    }
    match value {
        Value::Object(object) => {
            for (key, nested) in object {
                if is_personal_data_key(key) {
                    return Err(QueueError::PersonalDataInPayload);
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

fn is_personal_data_key(key: &str) -> bool {
    matches!(
        key.to_ascii_lowercase().as_str(),
        "cpf"
            | "cnpj"
            | "rg"
            | "email"
            | "phone"
            | "telefone"
            | "celular"
            | "name"
            | "nome"
            | "full_name"
            | "nome_completo"
            | "birth_date"
            | "data_nascimento"
            | "address"
            | "endereco"
    )
}

pub fn hex_sha256(bytes: &[u8]) -> String {
    hex(&Sha256::digest(bytes))
}

fn hex(bytes: &[u8]) -> String {
    const DIGITS: &[u8; 16] = b"0123456789abcdef";
    let mut out = String::with_capacity(bytes.len() * 2);
    for byte in bytes {
        out.push(DIGITS[(byte >> 4) as usize] as char);
        out.push(DIGITS[(byte & 0x0f) as usize] as char);
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    fn contract_draft() -> OperationDraft {
        OperationDraft {
            kind: OperationKind::ContractCreate,
            entity_id: "contract-1".to_owned(),
            idempotency_key: "device-op-1".to_owned(),
            payload: json!({"total_cents": 150_000, "currency": "BRL", "item_count": 2}),
        }
    }

    #[test]
    fn accepts_minimal_contract_and_cash_drafts() {
        validate_draft(&contract_draft()).expect("contract");
        validate_draft(&OperationDraft {
            kind: OperationKind::CashOpen,
            entity_id: "cash-1".to_owned(),
            idempotency_key: "device-op-2".to_owned(),
            payload: json!({"opening_amount_cents": 10_000}),
        })
        .expect("cash open");
        validate_draft(&OperationDraft {
            kind: OperationKind::CashMovement,
            entity_id: "cash-1".to_owned(),
            idempotency_key: "device-op-3".to_owned(),
            payload: json!({"direction": "in", "amount_cents": 5_000, "reason": "recebimento"}),
        })
        .expect("cash movement");
        validate_draft(&OperationDraft {
            kind: OperationKind::CashClose,
            entity_id: "cash-1".to_owned(),
            idempotency_key: "device-op-4".to_owned(),
            payload: json!({"counted_amount_cents": 15_000, "expected_amount_cents": 15_000}),
        })
        .expect("cash close");
    }

    #[test]
    fn rejects_negative_money_and_float_amounts() {
        let mut draft = contract_draft();
        draft.payload = json!({"total_cents": -1});
        assert!(matches!(
            validate_draft(&draft),
            Err(QueueError::InvalidPayload("total_cents"))
        ));
        draft.payload = json!({"total_cents": 10.5});
        assert!(matches!(
            validate_draft(&draft),
            Err(QueueError::InvalidPayload("total_cents"))
        ));
    }

    #[test]
    fn rejects_personal_data_in_payload() {
        let mut draft = contract_draft();
        draft.payload = json!({"total_cents": 100, "customer": {"cpf": "000"}});
        assert_eq!(
            validate_draft(&draft),
            Err(QueueError::PersonalDataInPayload)
        );
    }

    #[test]
    fn rejects_unknown_currency() {
        let mut draft = contract_draft();
        draft.payload = json!({"total_cents": 100, "currency": "USD"});
        assert_eq!(
            validate_draft(&draft),
            Err(QueueError::InvalidPayload("currency precisa ser BRL"))
        );
    }

    #[test]
    fn content_hash_is_stable_and_operation_id_is_opaque() {
        let draft = contract_draft();
        assert_eq!(
            content_hash(&draft).expect("hash"),
            content_hash(&draft).expect("hash")
        );
        let id = operation_id("tenant-a", "node-1", 7, "hash-a");
        assert!(id.starts_with("op_"));
        assert!(!id.contains("tenant-a"));
        assert_ne!(id, operation_id("tenant-a", "node-1", 8, "hash-a"));
        assert_ne!(id, operation_id("tenant-b", "node-1", 7, "hash-a"));
        assert_ne!(id, operation_id("tenant-a", "node-1", 7, "hash-b"));
    }
}
