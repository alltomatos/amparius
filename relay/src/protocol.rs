use crate::queue::{content_hash, operation_id, validate_draft, verify_operation};
use crate::{OperationDraft, OperationKind, QueuedOperation};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::collections::BTreeMap;
use thiserror::Error;

pub const SYNC_PROTOCOL_VERSION: u16 = 1;
pub const SYNC_EVENT_VERSION: u16 = 1;

const MAX_IDEMPOTENCY_KEY_LEN: usize = 128;
const MAX_ENTITY_ID_LEN: usize = 128;
const MAX_HASH_LEN: usize = 64;

#[derive(Debug, Error, PartialEq, Eq)]
pub enum EnvelopeError {
    #[error("versao de protocolo nao suportada: {0}")]
    UnsupportedProtocolVersion(u16),
    #[error("versao de evento nao suportada: {0}")]
    UnsupportedEventVersion(u16),
    #[error("envelope de sincronizacao adulterado")]
    Tampered,
    #[error("campo obrigatorio ausente ou invalido: {0}")]
    InvalidField(&'static str),
    #[error("payload do envelope invalido")]
    InvalidPayload,
}

impl EnvelopeError {
    pub fn code(&self) -> &'static str {
        match self {
            Self::UnsupportedProtocolVersion(_) => "unsupported_protocol_version",
            Self::UnsupportedEventVersion(_) => "unsupported_event_version",
            Self::Tampered => "tampered",
            Self::InvalidField(_) => "invalid_envelope",
            Self::InvalidPayload => "invalid_payload",
        }
    }
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct SyncEnvelope {
    pub protocol_version: u16,
    pub event_version: u16,
    pub event_id: String,
    pub tenant_id: String,
    pub origin_node: String,
    pub seq: u64,
    pub kind: OperationKind,
    pub entity_id: String,
    pub idempotency_key: String,
    pub actor_subject_id: String,
    pub content_hash: String,
    pub occurred_at: u64,
    pub payload: Value,
}

impl SyncEnvelope {
    pub fn from_operation(operation: &QueuedOperation) -> Result<Self, EnvelopeError> {
        verify_operation(operation).map_err(|_| EnvelopeError::Tampered)?;
        let envelope = Self {
            protocol_version: SYNC_PROTOCOL_VERSION,
            event_version: SYNC_EVENT_VERSION,
            event_id: operation.operation_id.clone(),
            tenant_id: operation.tenant_id.clone(),
            origin_node: operation.node_id.clone(),
            seq: operation.seq,
            kind: operation.kind,
            entity_id: operation.entity_id.clone(),
            idempotency_key: operation.idempotency_key.clone(),
            actor_subject_id: operation.actor_subject_id.clone(),
            content_hash: operation.content_hash.clone(),
            occurred_at: operation.created_at,
            payload: operation.payload.clone(),
        };
        envelope.validate()?;
        Ok(envelope)
    }

    pub fn validate(&self) -> Result<(), EnvelopeError> {
        if self.protocol_version != SYNC_PROTOCOL_VERSION {
            return Err(EnvelopeError::UnsupportedProtocolVersion(
                self.protocol_version,
            ));
        }
        if self.event_version != SYNC_EVENT_VERSION {
            return Err(EnvelopeError::UnsupportedEventVersion(self.event_version));
        }
        if self.tenant_id.trim().is_empty() {
            return Err(EnvelopeError::InvalidField("tenant_id"));
        }
        if self.origin_node.trim().is_empty() {
            return Err(EnvelopeError::InvalidField("origin_node"));
        }
        if self.seq == 0 {
            return Err(EnvelopeError::InvalidField("seq"));
        }
        if self.actor_subject_id.trim().is_empty() {
            return Err(EnvelopeError::InvalidField("actor_subject_id"));
        }
        if !valid_identifier(&self.entity_id, MAX_ENTITY_ID_LEN) {
            return Err(EnvelopeError::InvalidField("entity_id"));
        }
        if !valid_identifier(&self.idempotency_key, MAX_IDEMPOTENCY_KEY_LEN) {
            return Err(EnvelopeError::InvalidField("idempotency_key"));
        }
        if !valid_identifier(&self.event_id, MAX_HASH_LEN + 3) || !self.event_id.starts_with("op_")
        {
            return Err(EnvelopeError::InvalidField("event_id"));
        }
        if self.content_hash.len() != MAX_HASH_LEN
            || !self
                .content_hash
                .bytes()
                .all(|byte| byte.is_ascii_hexdigit())
        {
            return Err(EnvelopeError::InvalidField("content_hash"));
        }
        let draft = OperationDraft {
            kind: self.kind,
            entity_id: self.entity_id.clone(),
            idempotency_key: self.idempotency_key.clone(),
            payload: self.payload.clone(),
        };
        validate_draft(&draft).map_err(|_| EnvelopeError::InvalidPayload)?;
        let expected_hash = content_hash(&draft).map_err(|_| EnvelopeError::Tampered)?;
        if expected_hash != self.content_hash {
            return Err(EnvelopeError::Tampered);
        }
        if operation_id(
            &self.tenant_id,
            &self.origin_node,
            self.seq,
            &self.content_hash,
        ) != self.event_id
        {
            return Err(EnvelopeError::Tampered);
        }
        Ok(())
    }
}

fn valid_identifier(value: &str, max: usize) -> bool {
    !value.trim().is_empty()
        && value.len() <= max
        && value
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || b"-_.:".contains(&byte))
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct AppliedEvent {
    pub event_id: String,
    pub origin_node: String,
    pub seq: u64,
    pub idempotency_key: String,
    pub content_hash: String,
    pub cursor: u64,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum ApplyOutcome {
    Applied {
        cursor: u64,
    },
    Duplicate {
        cursor: u64,
    },
    OutOfOrder {
        expected_seq: u64,
        received_seq: u64,
    },
    Conflict {
        code: String,
    },
    Rejected {
        code: String,
    },
}

#[derive(Debug, Clone)]
pub struct SyncReceiver {
    tenant_id: String,
    cursor: u64,
    next_seq: BTreeMap<String, u64>,
    by_position: BTreeMap<(String, u64), AppliedEvent>,
    by_event: BTreeMap<String, AppliedEvent>,
    by_idempotency: BTreeMap<(String, String), AppliedEvent>,
}

impl SyncReceiver {
    pub fn new(tenant_id: impl Into<String>) -> Self {
        Self {
            tenant_id: tenant_id.into(),
            cursor: 0,
            next_seq: BTreeMap::new(),
            by_position: BTreeMap::new(),
            by_event: BTreeMap::new(),
            by_idempotency: BTreeMap::new(),
        }
    }

    pub fn tenant_id(&self) -> &str {
        &self.tenant_id
    }

    pub fn cursor(&self) -> u64 {
        self.cursor
    }

    pub fn applied_count(&self) -> usize {
        self.by_event.len()
    }

    pub fn next_expected_seq(&self, origin_node: &str) -> u64 {
        self.next_seq.get(origin_node).copied().unwrap_or(1)
    }

    pub fn applied_event(&self, event_id: &str) -> Option<&AppliedEvent> {
        self.by_event.get(event_id)
    }

    pub fn apply(&mut self, envelope: &SyncEnvelope) -> ApplyOutcome {
        if let Err(error) = envelope.validate() {
            return ApplyOutcome::Rejected {
                code: error.code().to_owned(),
            };
        }
        if envelope.tenant_id != self.tenant_id {
            return ApplyOutcome::Rejected {
                code: "tenant_mismatch".to_owned(),
            };
        }

        if let Some(existing) = self.by_event.get(&envelope.event_id) {
            if same_content(existing, envelope) {
                return ApplyOutcome::Duplicate {
                    cursor: existing.cursor,
                };
            }
            return ApplyOutcome::Conflict {
                code: "event_id_content_mismatch".to_owned(),
            };
        }

        let idempotency_key = (
            envelope.origin_node.clone(),
            envelope.idempotency_key.clone(),
        );
        if let Some(existing) = self.by_idempotency.get(&idempotency_key) {
            if same_content(existing, envelope) {
                return ApplyOutcome::Duplicate {
                    cursor: existing.cursor,
                };
            }
            return ApplyOutcome::Conflict {
                code: "idempotency_conflict".to_owned(),
            };
        }

        let position = (envelope.origin_node.clone(), envelope.seq);
        if let Some(existing) = self.by_position.get(&position) {
            if same_content(existing, envelope) {
                return ApplyOutcome::Duplicate {
                    cursor: existing.cursor,
                };
            }
            return ApplyOutcome::Conflict {
                code: "sequence_conflict".to_owned(),
            };
        }

        let expected_seq = self.next_expected_seq(&envelope.origin_node);
        if envelope.seq > expected_seq {
            return ApplyOutcome::OutOfOrder {
                expected_seq,
                received_seq: envelope.seq,
            };
        }
        if envelope.seq < expected_seq {
            return ApplyOutcome::Conflict {
                code: "stale_sequence".to_owned(),
            };
        }

        self.cursor = self.cursor.saturating_add(1);
        let applied = AppliedEvent {
            event_id: envelope.event_id.clone(),
            origin_node: envelope.origin_node.clone(),
            seq: envelope.seq,
            idempotency_key: envelope.idempotency_key.clone(),
            content_hash: envelope.content_hash.clone(),
            cursor: self.cursor,
        };
        self.by_event
            .insert(envelope.event_id.clone(), applied.clone());
        self.by_position.insert(position, applied.clone());
        self.by_idempotency.insert(idempotency_key, applied);
        self.next_seq
            .insert(envelope.origin_node.clone(), envelope.seq + 1);
        ApplyOutcome::Applied {
            cursor: self.cursor,
        }
    }
}

fn same_content(existing: &AppliedEvent, envelope: &SyncEnvelope) -> bool {
    existing.event_id == envelope.event_id
        && existing.origin_node == envelope.origin_node
        && existing.seq == envelope.seq
        && existing.idempotency_key == envelope.idempotency_key
        && existing.content_hash == envelope.content_hash
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::{AuthCache, LocalStore};
    use serde_json::json;
    use std::{
        fs,
        path::PathBuf,
        time::{SystemTime, UNIX_EPOCH},
    };

    fn path() -> PathBuf {
        let nonce = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .expect("clock")
            .as_nanos();
        std::env::temp_dir().join(format!("amparius-relay-protocol-{nonce}.json"))
    }

    fn store_with_operations(
        tenant: &str,
        node: &str,
        drafts: Vec<OperationDraft>,
    ) -> Vec<QueuedOperation> {
        let file = path();
        let mut store = LocalStore::open(&file).expect("store");
        let credential = AuthCache
            .issue(tenant, "operator-1", "secret-token", 90, 1_000)
            .expect("credential");
        store.cache_credential(credential).expect("cache");
        let subject = store
            .authenticate(tenant, "operator-1", "secret-token", 100)
            .expect("auth");
        let operations = drafts
            .into_iter()
            .map(|draft| {
                store
                    .append_operation(&subject, node, draft, 100)
                    .expect("append")
                    .0
            })
            .collect();
        drop(store);
        let _ = fs::remove_file(file);
        operations
    }

    fn contract(key: &str, total: i64) -> OperationDraft {
        OperationDraft {
            kind: OperationKind::ContractCreate,
            entity_id: format!("contract-{key}"),
            idempotency_key: key.to_owned(),
            payload: json!({"total_cents": total, "currency": "BRL"}),
        }
    }

    fn envelopes(count: usize) -> Vec<SyncEnvelope> {
        let drafts = (0..count)
            .map(|index| contract(&format!("op-{index}"), 10_000 + index as i64))
            .collect();
        let operations = store_with_operations("tenant-a", "node-1", drafts);
        operations
            .iter()
            .map(|operation| SyncEnvelope::from_operation(operation).expect("envelope"))
            .collect()
    }

    #[test]
    fn envelope_round_trips_and_carries_versions_origin_and_idempotency() {
        let envelopes = envelopes(1);
        let envelope = &envelopes[0];
        assert_eq!(envelope.protocol_version, SYNC_PROTOCOL_VERSION);
        assert_eq!(envelope.event_version, SYNC_EVENT_VERSION);
        assert_eq!(envelope.origin_node, "node-1");
        assert_eq!(envelope.seq, 1);
        assert_eq!(envelope.idempotency_key, "op-0");
        envelope.validate().expect("valid");

        let encoded = serde_json::to_string(envelope).expect("serialize");
        let decoded: SyncEnvelope = serde_json::from_str(&encoded).expect("deserialize");
        assert_eq!(&decoded, envelope);
    }

    #[test]
    fn unsupported_versions_are_rejected_without_touching_state() {
        let mut receiver = SyncReceiver::new("tenant-a");
        let mut envelope = envelopes(1).remove(0);

        envelope.protocol_version = SYNC_PROTOCOL_VERSION + 1;
        assert_eq!(
            receiver.apply(&envelope),
            ApplyOutcome::Rejected {
                code: "unsupported_protocol_version".to_owned()
            }
        );

        envelope.protocol_version = SYNC_PROTOCOL_VERSION;
        envelope.event_version = SYNC_EVENT_VERSION + 1;
        assert_eq!(
            receiver.apply(&envelope),
            ApplyOutcome::Rejected {
                code: "unsupported_event_version".to_owned()
            }
        );
        assert_eq!(receiver.applied_count(), 0);
        assert_eq!(receiver.cursor(), 0);
    }

    #[test]
    fn tampered_envelope_is_rejected_and_tenant_is_enforced() {
        let mut receiver = SyncReceiver::new("tenant-a");
        let mut envelope = envelopes(1).remove(0);

        let original = envelope.payload.clone();
        envelope.payload = json!({"total_cents": 1, "currency": "BRL"});
        assert_eq!(
            receiver.apply(&envelope),
            ApplyOutcome::Rejected {
                code: "tampered".to_owned()
            }
        );

        envelope.payload = original;
        let foreign = {
            let operations =
                store_with_operations("tenant-b", "node-1", vec![contract("op-0", 10_000)]);
            SyncEnvelope::from_operation(&operations[0]).expect("envelope")
        };
        assert_eq!(
            receiver.apply(&foreign),
            ApplyOutcome::Rejected {
                code: "tenant_mismatch".to_owned()
            }
        );
        assert_eq!(receiver.applied_count(), 0);
    }

    #[test]
    fn applies_in_sequence_and_replay_of_the_same_event_is_idempotent() {
        let envelopes = envelopes(3);
        let mut receiver = SyncReceiver::new("tenant-a");

        for (index, envelope) in envelopes.iter().enumerate() {
            assert_eq!(
                receiver.apply(envelope),
                ApplyOutcome::Applied {
                    cursor: index as u64 + 1
                }
            );
        }
        assert_eq!(receiver.cursor(), 3);
        assert_eq!(receiver.applied_count(), 3);

        let duplicate = receiver.apply(&envelopes[1]);
        assert_eq!(duplicate, ApplyOutcome::Duplicate { cursor: 2 });
        assert_eq!(receiver.cursor(), 3);
        assert_eq!(receiver.applied_count(), 3);
    }

    #[test]
    fn whole_batch_replay_after_reconnect_does_not_duplicate() {
        let envelopes = envelopes(3);
        let mut receiver = SyncReceiver::new("tenant-a");
        for envelope in &envelopes {
            receiver.apply(envelope);
        }

        for envelope in &envelopes {
            assert!(matches!(
                receiver.apply(envelope),
                ApplyOutcome::Duplicate { .. }
            ));
        }
        assert_eq!(receiver.applied_count(), 3);
        assert_eq!(receiver.cursor(), 3);
    }

    #[test]
    fn gap_is_rejected_and_expected_sequence_is_reported() {
        let envelopes = envelopes(3);
        let mut receiver = SyncReceiver::new("tenant-a");

        assert_eq!(
            receiver.apply(&envelopes[1]),
            ApplyOutcome::OutOfOrder {
                expected_seq: 1,
                received_seq: 2
            }
        );
        assert_eq!(receiver.applied_count(), 0);
        assert_eq!(receiver.next_expected_seq("node-1"), 1);

        assert_eq!(
            receiver.apply(&envelopes[0]),
            ApplyOutcome::Applied { cursor: 1 }
        );
        assert_eq!(
            receiver.apply(&envelopes[1]),
            ApplyOutcome::Applied { cursor: 2 }
        );
    }

    #[test]
    fn sequence_conflict_does_not_overwrite_applied_event() {
        let envelopes = envelopes(2);
        let mut receiver = SyncReceiver::new("tenant-a");
        receiver.apply(&envelopes[0]);

        let mut forged = envelopes[0].clone();
        forged.content_hash = envelopes[1].content_hash.clone();
        forged.payload = envelopes[1].payload.clone();
        forged.entity_id = envelopes[1].entity_id.clone();
        forged.idempotency_key = "op-9".to_owned();
        forged.event_id = operation_id(
            &forged.tenant_id,
            &forged.origin_node,
            forged.seq,
            &forged.content_hash,
        );
        assert_eq!(
            receiver.apply(&forged),
            ApplyOutcome::Conflict {
                code: "sequence_conflict".to_owned()
            }
        );
        assert_eq!(
            receiver
                .applied_event(&envelopes[0].event_id)
                .expect("original")
                .content_hash,
            envelopes[0].content_hash
        );
    }

    #[test]
    fn idempotency_key_reused_with_different_content_is_a_conflict() {
        let mut receiver = SyncReceiver::new("tenant-a");
        let first = envelopes(1).remove(0);
        receiver.apply(&first);

        let mut second = {
            let operations =
                store_with_operations("tenant-a", "node-1", vec![contract("op-0", 99_999)]);
            SyncEnvelope::from_operation(&operations[0]).expect("envelope")
        };
        second.seq = first.seq;
        second.event_id = operation_id(
            &second.tenant_id,
            &second.origin_node,
            second.seq,
            &second.content_hash,
        );
        assert_eq!(
            receiver.apply(&second),
            ApplyOutcome::Conflict {
                code: "idempotency_conflict".to_owned()
            }
        );
    }

    #[test]
    fn origins_are_ordered_independently() {
        let mut receiver = SyncReceiver::new("tenant-a");
        let node_one = envelopes(1).remove(0);
        let operations =
            store_with_operations("tenant-a", "node-2", vec![contract("other", 12_000)]);
        let node_two = SyncEnvelope::from_operation(&operations[0]).expect("envelope");

        assert_eq!(
            receiver.apply(&node_one),
            ApplyOutcome::Applied { cursor: 1 }
        );
        assert_eq!(
            receiver.apply(&node_two),
            ApplyOutcome::Applied { cursor: 2 }
        );
        assert_eq!(receiver.next_expected_seq("node-1"), 2);
        assert_eq!(receiver.next_expected_seq("node-2"), 2);
    }

    #[test]
    fn envelope_with_personal_data_fails_closed_even_with_recomputed_hash() {
        let mut envelope = envelopes(1).remove(0);
        envelope.payload = json!({"total_cents": 10_000, "customer": {"cpf": "000"}});
        let draft = OperationDraft {
            kind: envelope.kind,
            entity_id: envelope.entity_id.clone(),
            idempotency_key: envelope.idempotency_key.clone(),
            payload: envelope.payload.clone(),
        };
        envelope.content_hash = content_hash(&draft).expect("hash");
        envelope.event_id = operation_id(
            &envelope.tenant_id,
            &envelope.origin_node,
            envelope.seq,
            &envelope.content_hash,
        );
        assert_eq!(envelope.validate(), Err(EnvelopeError::InvalidPayload));
        assert_eq!(
            SyncReceiver::new("tenant-a").apply(&envelope),
            ApplyOutcome::Rejected {
                code: "invalid_payload".to_owned()
            }
        );
    }

    #[test]
    fn envelope_from_tampered_operation_is_refused() {
        let operations =
            store_with_operations("tenant-a", "node-1", vec![contract("op-0", 10_000)]);
        let mut operation = operations[0].clone();
        operation.payload = json!({"total_cents": 1});
        assert_eq!(
            SyncEnvelope::from_operation(&operation),
            Err(EnvelopeError::Tampered)
        );
    }
}
