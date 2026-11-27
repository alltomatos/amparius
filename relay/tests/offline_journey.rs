use std::collections::VecDeque;
use std::time::{SystemTime, UNIX_EPOCH};

use amparius_relay::{
    AuthCache, AuthenticatedSubject, LocalStore, OperationDraft, OperationKind, QueuedOperation,
    SnapshotSigner, SyncEngine, SyncOutcome, SyncTransport, TransportError,
};
use serde_json::json;

struct ScriptedTransport {
    outcomes: VecDeque<Result<SyncOutcome, TransportError>>,
    submitted: usize,
}

impl SyncTransport for ScriptedTransport {
    fn submit(&mut self, _operation: &QueuedOperation) -> Result<SyncOutcome, TransportError> {
        self.submitted += 1;
        self.outcomes.pop_front().expect("scripted outcome")
    }
}

fn temp_path(prefix: &str) -> std::path::PathBuf {
    let nonce = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .expect("clock")
        .as_nanos();
    std::env::temp_dir().join(format!("amparius-{prefix}-{nonce}.json"))
}

fn subject(store: &mut LocalStore) -> AuthenticatedSubject {
    let now = amparius_relay::unix_now();
    let credential = AuthCache
        .issue(
            "tenant-a",
            "operator-1",
            "secret-token",
            now - 10,
            now + 3_600,
        )
        .expect("credential");
    store.cache_credential(credential).expect("cache");
    store
        .authenticate("tenant-a", "operator-1", "secret-token", now)
        .expect("auth")
}

#[test]
fn offline_journey_snapshot_to_replay_and_conflict_review() {
    let path = temp_path("offline-journey");
    let mut store = LocalStore::open(&path).expect("store");

    // 1. Snapshot autorizado carrega cadastros e preço na estação.
    let signer = SnapshotSigner::from_bytes("snap-key", &[7; 32]).expect("signer");
    let key = signer.verifying_key();
    let snapshot = signer
        .sign(
            "tenant-a",
            1,
            100,
            10_000,
            json!({
                "cadastros": [{"id": "party-1", "name": "Família"}],
                "precos": [{"item": "servico-1", "amount_cents": 10000}]
            }),
        )
        .expect("snapshot");
    let loaded = store
        .apply_snapshot(snapshot, &key, "tenant-a", 200)
        .expect("apply snapshot");
    assert!(loaded.payload["cadastros"].is_array());
    assert!(loaded.payload["precos"].is_array());

    // 2. Registra contrato e caixa no relay (LAN), sem conexão com o SaaS.
    let operator = subject(&mut store);
    store
        .append_operation(
            &operator,
            "node-1",
            OperationDraft {
                kind: OperationKind::ContractCreate,
                entity_id: "contract-1".to_owned(),
                idempotency_key: "device-op-1".to_owned(),
                payload: json!({"total_cents": 10_000, "currency": "BRL"}),
            },
            300,
        )
        .expect("contract");
    store
        .append_operation(
            &operator,
            "node-1",
            OperationDraft {
                kind: OperationKind::CashMovement,
                entity_id: "cash-1".to_owned(),
                idempotency_key: "device-op-2".to_owned(),
                payload: json!({"direction": "in", "amount_cents": 5_000, "reason": "recebimento"}),
            },
            320,
        )
        .expect("cash");

    // 3. Queda de WAN: o transporte falha e as operações permanecem pendentes.
    let drop_transport = ScriptedTransport {
        outcomes: VecDeque::from([
            Err(TransportError::Unavailable),
            Err(TransportError::Unavailable),
        ]),
        submitted: 0,
    };
    let mut engine = SyncEngine::new(drop_transport);
    assert!(
        engine
            .sync_once(&mut store, &operator, "node-1", 400)
            .is_err()
    );
    let status = store
        .queue_status(&operator, Some("node-1"), 400)
        .expect("status after drop");
    assert_eq!(status.queue.pending, 2);

    // 4. Reabre com a fila persistida após a queda.
    drop(store);
    let mut store = LocalStore::open(&path).expect("reopen");
    let operator = subject(&mut store);
    let status = store
        .queue_status(&operator, Some("node-1"), 500)
        .expect("status after reopen");
    assert_eq!(status.queue.pending, 2);

    // 5. Reconexão com o SaaS: replay único de cada operação.
    let reconnect = ScriptedTransport {
        outcomes: VecDeque::from([
            Ok(SyncOutcome::Applied { server_cursor: 41 }),
            Ok(SyncOutcome::Applied { server_cursor: 42 }),
        ]),
        submitted: 0,
    };
    let mut engine = SyncEngine::new(reconnect);
    let report = engine
        .sync_once(&mut store, &operator, "node-1", 600)
        .expect("reconnect");
    assert_eq!(report.synced, 2);
    assert_eq!(report.failed, 0);
    assert_eq!(report.conflicts, 0);

    // 6. Revisão de conflito: um mestre em conflito vira tarefa de revisão.
    store
        .append_operation(
            &operator,
            "node-1",
            OperationDraft {
                kind: OperationKind::MasterUpsert,
                entity_id: "catalog-item-1".to_owned(),
                idempotency_key: "master-1".to_owned(),
                payload: json!({"base_version": 3, "record_ref": "catalog-item-1"}),
            },
            700,
        )
        .expect("master");
    let conflict = ScriptedTransport {
        outcomes: VecDeque::from([Ok(SyncOutcome::Conflict {
            code: "server_version_mismatch".to_owned(),
        })]),
        submitted: 0,
    };
    let mut engine = SyncEngine::new(conflict);
    let report = engine
        .sync_once(&mut store, &operator, "node-1", 800)
        .expect("conflict");
    assert_eq!(report.conflicts, 1);
    assert_eq!(report.review_tasks, 1);
    let tasks = store.list_review_tasks(&operator).expect("tasks");
    assert_eq!(tasks.len(), 1);
    assert_eq!(
        tasks[0].kind,
        amparius_relay::ReviewTaskKind::MasterConflict
    );

    drop(store);
    let _ = std::fs::remove_file(path);
}
