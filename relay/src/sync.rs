use crate::{
    AuthenticatedSubject, LocalStore, OperationStatus, QueuedOperation, StoreError, SyncEnvelope,
};
use thiserror::Error;

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum SyncOutcome {
    Applied { server_cursor: u64 },
    FiscalRejected { code: String },
    Conflict { code: String },
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Error)]
pub enum TransportError {
    #[error("transporte de sincronizacao indisponivel")]
    Unavailable,
    #[error("timeout do transporte de sincronizacao")]
    Timeout,
}

pub trait SyncTransport {
    fn submit(&mut self, operation: &QueuedOperation) -> Result<SyncOutcome, TransportError>;

    fn submit_versioned(
        &mut self,
        operation: &QueuedOperation,
        envelope: &SyncEnvelope,
    ) -> Result<SyncOutcome, TransportError> {
        let _ = envelope;
        self.submit(operation)
    }
}

#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct SyncReport {
    pub attempted: usize,
    pub synced: usize,
    pub failed: usize,
    pub review_tasks: usize,
    pub last_cursor: Option<u64>,
}

#[derive(Debug, Error)]
pub enum SyncError {
    #[error("falha no transporte durante a sincronizacao")]
    Transport(#[source] TransportError),
    #[error(transparent)]
    Store(#[from] StoreError),
}

pub struct SyncEngine<T> {
    transport: T,
}

impl<T> SyncEngine<T>
where
    T: SyncTransport,
{
    pub fn new(transport: T) -> Self {
        Self { transport }
    }

    pub fn sync_once(
        &mut self,
        store: &mut LocalStore,
        subject: &AuthenticatedSubject,
        node_id: &str,
        now: u64,
    ) -> Result<SyncReport, SyncError> {
        let operations = store.list_operations(subject, Some(node_id), 0, 200)?;
        let mut report = SyncReport::default();

        for operation in operations
            .into_iter()
            .filter(|operation| operation.status == OperationStatus::Pending)
        {
            report.attempted += 1;
            let envelope = match SyncEnvelope::from_operation(&operation) {
                Ok(envelope) => envelope,
                Err(_) => {
                    store.mark_operation_conflict(
                        subject,
                        &operation.operation_id,
                        "sync_conflict:envelope_invalid",
                        now,
                    )?;
                    report.failed += 1;
                    continue;
                }
            };
            match self.transport.submit_versioned(&operation, &envelope) {
                Ok(SyncOutcome::Applied { server_cursor }) => {
                    store.mark_operation_synced(
                        subject,
                        &operation.operation_id,
                        server_cursor,
                        now,
                    )?;
                    report.synced += 1;
                    report.last_cursor = Some(server_cursor);
                }
                Ok(SyncOutcome::FiscalRejected { code }) => {
                    store.mark_fiscal_failure(subject, &operation.operation_id, &code, now)?;
                    report.failed += 1;
                    report.review_tasks += 1;
                }
                Ok(SyncOutcome::Conflict { code }) => {
                    store.mark_operation_conflict(
                        subject,
                        &operation.operation_id,
                        &format!("sync_conflict:{}", safe_code(&code)),
                        now,
                    )?;
                    report.failed += 1;
                }
                Err(error) => {
                    store.mark_operation_retryable(
                        subject,
                        &operation.operation_id,
                        match error {
                            TransportError::Unavailable => "transport_unavailable",
                            TransportError::Timeout => "transport_timeout",
                        },
                        now,
                    )?;
                    return Err(SyncError::Transport(error));
                }
            }
        }

        Ok(report)
    }
}

fn safe_code(value: &str) -> String {
    let code: String = value
        .chars()
        .filter(|character| {
            character.is_ascii_alphanumeric() || matches!(character, '_' | '-' | '.')
        })
        .take(64)
        .collect();
    if code.is_empty() {
        "unspecified".to_owned()
    } else {
        code
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::{AuthCache, OperationDraft, OperationKind};
    use serde_json::json;
    use std::{
        collections::VecDeque,
        fs,
        path::PathBuf,
        time::{SystemTime, UNIX_EPOCH},
    };

    struct ScriptedTransport {
        outcomes: VecDeque<Result<SyncOutcome, TransportError>>,
        submitted: Vec<String>,
    }

    impl SyncTransport for ScriptedTransport {
        fn submit(&mut self, operation: &QueuedOperation) -> Result<SyncOutcome, TransportError> {
            self.submitted.push(operation.operation_id.clone());
            self.outcomes
                .pop_front()
                .expect("scripted transport outcome")
        }
    }

    struct EnvelopeTransport {
        envelopes: Vec<SyncEnvelope>,
    }

    impl SyncTransport for EnvelopeTransport {
        fn submit(&mut self, _operation: &QueuedOperation) -> Result<SyncOutcome, TransportError> {
            panic!("o adaptador versionado deve receber o envelope");
        }

        fn submit_versioned(
            &mut self,
            _operation: &QueuedOperation,
            envelope: &SyncEnvelope,
        ) -> Result<SyncOutcome, TransportError> {
            self.envelopes.push(envelope.clone());
            Ok(SyncOutcome::Applied { server_cursor: 7 })
        }
    }

    fn path() -> PathBuf {
        let nonce = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .expect("clock")
            .as_nanos();
        std::env::temp_dir().join(format!("amparius-relay-sync-{nonce}.json"))
    }

    fn subject(store: &mut LocalStore) -> AuthenticatedSubject {
        let credential = AuthCache
            .issue("tenant-a", "operator-1", "secret-token", 90, 1_000)
            .expect("credential");
        store.cache_credential(credential).expect("cache");
        store
            .authenticate("tenant-a", "operator-1", "secret-token", 100)
            .expect("auth")
    }

    fn contract() -> OperationDraft {
        OperationDraft {
            kind: OperationKind::ContractCreate,
            entity_id: "contract-1".to_owned(),
            idempotency_key: "op-1".to_owned(),
            payload: json!({"total_cents": 10_000, "currency": "BRL"}),
        }
    }

    fn fiscal() -> OperationDraft {
        OperationDraft {
            kind: OperationKind::FiscalEmission,
            entity_id: "document-1".to_owned(),
            idempotency_key: "fiscal-1".to_owned(),
            payload: json!({"document_ref": "document-1"}),
        }
    }

    #[test]
    fn network_drop_keeps_operation_pending_and_reconnect_replays_same_id() {
        let file = path();
        let mut store = LocalStore::open(&file).expect("store");
        let operator = subject(&mut store);
        let (operation, _) = store
            .append_operation(&operator, "node-1", contract(), 100)
            .expect("append");
        let transport = ScriptedTransport {
            outcomes: VecDeque::from([
                Err(TransportError::Unavailable),
                Ok(SyncOutcome::Applied { server_cursor: 41 }),
            ]),
            submitted: Vec::new(),
        };
        let mut engine = SyncEngine::new(transport);

        assert!(matches!(
            engine.sync_once(&mut store, &operator, "node-1", 110),
            Err(SyncError::Transport(TransportError::Unavailable))
        ));
        let status = store
            .queue_status(&operator, Some("node-1"), 110)
            .expect("status after drop");
        assert_eq!(status.queue.pending, 1);
        assert_eq!(status.queue.failed, 0);
        assert_eq!(
            status.queue.last_error.as_deref(),
            Some("sync_retry:transport_unavailable")
        );

        let report = engine
            .sync_once(&mut store, &operator, "node-1", 120)
            .expect("reconnect");
        assert_eq!(report.synced, 1);
        let operations = store
            .list_operations(&operator, Some("node-1"), 0, 10)
            .expect("operations");
        assert_eq!(operations[0].operation_id, operation.operation_id);
        assert_eq!(operations[0].server_cursor, Some(41));
        assert_eq!(
            engine.transport.submitted,
            vec![operation.operation_id.clone(), operation.operation_id]
        );
        let _ = fs::remove_file(file);
    }

    #[test]
    fn versioned_transport_receives_protocol_envelope_without_resend_after_sync() {
        let file = path();
        let mut store = LocalStore::open(&file).expect("store");
        let operator = subject(&mut store);
        let (operation, _) = store
            .append_operation(&operator, "node-1", contract(), 100)
            .expect("append");
        let transport = EnvelopeTransport {
            envelopes: Vec::new(),
        };
        let mut engine = SyncEngine::new(transport);

        let report = engine
            .sync_once(&mut store, &operator, "node-1", 110)
            .expect("sync");
        assert_eq!(report.synced, 1);
        assert_eq!(engine.transport.envelopes.len(), 1);
        let envelope = &engine.transport.envelopes[0];
        assert_eq!(envelope.protocol_version, crate::SYNC_PROTOCOL_VERSION);
        assert_eq!(envelope.event_version, crate::SYNC_EVENT_VERSION);
        assert_eq!(envelope.event_id, operation.operation_id);
        assert_eq!(envelope.origin_node, "node-1");
        assert_eq!(envelope.seq, operation.seq);
        assert_eq!(envelope.idempotency_key, operation.idempotency_key);
        assert_eq!(envelope.content_hash, operation.content_hash);

        engine
            .sync_once(&mut store, &operator, "node-1", 120)
            .expect("second sync");
        assert_eq!(engine.transport.envelopes.len(), 1);
        let _ = fs::remove_file(file);
    }

    #[test]
    fn fiscal_rejection_is_failed_once_and_creates_tenant_scoped_review_task() {
        let file = path();
        let mut store = LocalStore::open(&file).expect("store");
        let operator = subject(&mut store);
        let (operation, _) = store
            .append_operation(&operator, "node-1", fiscal(), 100)
            .expect("append");
        let transport = ScriptedTransport {
            outcomes: VecDeque::from([Ok(SyncOutcome::FiscalRejected {
                code: "schema_invalid".to_owned(),
            })]),
            submitted: Vec::new(),
        };
        let mut engine = SyncEngine::new(transport);
        let report = engine
            .sync_once(&mut store, &operator, "node-1", 110)
            .expect("fiscal rejection");
        assert_eq!(report.review_tasks, 1);
        let tasks = store.list_review_tasks(&operator).expect("tasks");
        assert_eq!(tasks.len(), 1);
        assert_eq!(tasks[0].operation_id, operation.operation_id);
        assert_eq!(tasks[0].code, "schema_invalid");
        assert_eq!(
            store
                .mark_fiscal_failure(&operator, &operation.operation_id, "schema_invalid", 120)
                .expect("idempotent task"),
            tasks[0]
        );
        assert_eq!(
            store
                .queue_status(&operator, Some("node-1"), 110)
                .expect("status")
                .queue
                .failed,
            1
        );
        drop(store);
        let mut reopened = LocalStore::open(&file).expect("reopen");
        let operator = subject(&mut reopened);
        assert_eq!(
            reopened.list_review_tasks(&operator).expect("tasks").len(),
            1
        );
        let _ = fs::remove_file(file);
    }
}
