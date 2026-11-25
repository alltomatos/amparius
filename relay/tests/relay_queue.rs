use std::time::{SystemTime, UNIX_EPOCH};

use amparius_relay::{AppState, AuthCache, LocalStore, OperationDraft, OperationKind, router};
use axum::{
    Router,
    body::Body,
    http::{Request, StatusCode},
};
use http_body_util::BodyExt;
use serde_json::{Value, json};
use tower::ServiceExt;

fn temp_path() -> std::path::PathBuf {
    let nonce = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .expect("clock")
        .as_nanos();
    std::env::temp_dir().join(format!("amparius-relay-http-{nonce}.json"))
}

fn app(path: &std::path::Path, tenant: &str, subject: &str) -> Router {
    let mut store = LocalStore::open(path).expect("store");
    let now = amparius_relay::unix_now();
    let credential = AuthCache
        .issue(tenant, subject, "secret-token", now - 10, now + 3_600)
        .expect("credential");
    store.cache_credential(credential).expect("cache");
    router(AppState::new(store))
}

fn operation_request(kind: &str, entity: &str, key: &str, payload: Value) -> Request<Body> {
    let body = json!({
        "kind": kind,
        "entity_id": entity,
        "idempotency_key": key,
        "payload": payload
    });
    Request::builder()
        .method("POST")
        .uri("/relay/v1/operations")
        .header("content-type", "application/json")
        .header("x-amparius-tenant", "tenant-a")
        .header("x-amparius-subject", "operator-1")
        .header("x-amparius-token", "secret-token")
        .header("x-amparius-node", "node-1")
        .body(Body::from(body.to_string()))
        .expect("request")
}

async fn body_json(response: axum::response::Response) -> Value {
    let bytes = response
        .into_body()
        .collect()
        .await
        .expect("body")
        .to_bytes();
    serde_json::from_slice(&bytes).expect("json body")
}

#[tokio::test]
async fn contract_and_cash_are_queued_idempotently_and_visible() {
    let path = temp_path();
    let app = app(&path, "tenant-a", "operator-1");

    let health = app
        .clone()
        .oneshot(
            Request::builder()
                .uri("/healthz")
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .expect("health");
    assert_eq!(health.status(), StatusCode::OK);

    let missing = app
        .clone()
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/relay/v1/operations")
                .header("content-type", "application/json")
                .body(Body::from(
                    json!({
                        "kind": "contract_create",
                        "entity_id": "contract-1",
                        "idempotency_key": "device-op-1",
                        "payload": {"total_cents": 100}
                    })
                    .to_string(),
                ))
                .unwrap(),
        )
        .await
        .expect("missing headers");
    assert_eq!(missing.status(), StatusCode::UNAUTHORIZED);

    let contract = operation_request(
        "contract_create",
        "contract-1",
        "device-op-1",
        json!({"total_cents": 150_000, "currency": "BRL", "item_count": 2}),
    );
    let created = app.clone().oneshot(contract).await.expect("create");
    assert_eq!(created.status(), StatusCode::CREATED);
    let created = body_json(created).await;
    assert_eq!(created["operation"]["seq"], 1);
    assert_eq!(created["operation"]["status"], "pending");
    assert_eq!(created["replayed"], false);

    let replay = operation_request(
        "contract_create",
        "contract-1",
        "device-op-1",
        json!({"total_cents": 150_000, "currency": "BRL", "item_count": 2}),
    );
    let replay = app.clone().oneshot(replay).await.expect("replay");
    assert_eq!(replay.status(), StatusCode::OK);
    let replay = body_json(replay).await;
    assert_eq!(replay["operation"]["seq"], 1);
    assert_eq!(replay["replayed"], true);

    let cash = operation_request(
        "cash_open",
        "cash-1",
        "device-op-2",
        json!({"opening_amount_cents": 50_000}),
    );
    let cash = app.clone().oneshot(cash).await.expect("cash");
    assert_eq!(cash.status(), StatusCode::CREATED);
    assert_eq!(body_json(cash).await["operation"]["seq"], 2);

    let movement = operation_request(
        "cash_movement",
        "cash-1",
        "device-op-3",
        json!({"direction": "in", "amount_cents": 5_000, "reason": "recebimento"}),
    );
    let movement = app.clone().oneshot(movement).await.expect("movement");
    assert_eq!(movement.status(), StatusCode::CREATED);
    assert_eq!(body_json(movement).await["operation"]["seq"], 3);

    let close = operation_request(
        "cash_close",
        "cash-1",
        "device-op-4",
        json!({"counted_amount_cents": 55_000, "expected_amount_cents": 55_000}),
    );
    let close = app.clone().oneshot(close).await.expect("close");
    assert_eq!(close.status(), StatusCode::CREATED);
    assert_eq!(body_json(close).await["operation"]["seq"], 4);

    let status = app
        .clone()
        .oneshot(
            Request::builder()
                .uri("/relay/v1/status?node_id=node-1")
                .header("x-amparius-tenant", "tenant-a")
                .header("x-amparius-subject", "operator-1")
                .header("x-amparius-token", "secret-token")
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .expect("status");
    assert_eq!(status.status(), StatusCode::OK);
    let status = body_json(status).await;
    assert_eq!(status["mode"], "offline_queued");
    assert_eq!(status["queue"]["pending"], 4);
    assert_eq!(status["queue"]["total"], 4);
    assert_eq!(status["queue"]["last_seq"], 4);
    assert_eq!(status["fiscal"]["total"], 0);
    assert_eq!(status["review_tasks_open"], 0);

    let list = app
        .clone()
        .oneshot(
            Request::builder()
                .uri("/relay/v1/operations?node_id=node-1&after_seq=0")
                .header("x-amparius-tenant", "tenant-a")
                .header("x-amparius-subject", "operator-1")
                .header("x-amparius-token", "secret-token")
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .expect("list");
    assert_eq!(list.status(), StatusCode::OK);
    let list = body_json(list).await;
    assert_eq!(list["operations"].as_array().expect("array").len(), 4);
    assert_eq!(list["next_after_seq"], 4);

    let raw = std::fs::read_to_string(&path).expect("store file");
    assert!(!raw.contains("secret-token"));
    assert!(raw.contains("contract-1"));

    let _ = std::fs::remove_file(path);
}

#[tokio::test]
async fn review_tasks_are_readable_only_inside_the_authenticated_tenant() {
    let path = temp_path();
    let mut store = LocalStore::open(&path).expect("store");
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
    let subject = store
        .authenticate("tenant-a", "operator-1", "secret-token", now)
        .expect("auth");
    let (operation, _) = store
        .append_operation(
            &subject,
            "node-1",
            OperationDraft {
                kind: OperationKind::FiscalEmission,
                entity_id: "document-1".to_owned(),
                idempotency_key: "fiscal-1".to_owned(),
                payload: json!({"document_ref": "document-1"}),
            },
            now,
        )
        .expect("append");
    store
        .mark_fiscal_failure(&subject, &operation.operation_id, "schema_invalid", now + 1)
        .expect("failure");
    let app = router(AppState::new(store));

    let response = app
        .clone()
        .oneshot(
            Request::builder()
                .uri("/relay/v1/review-tasks")
                .header("x-amparius-tenant", "tenant-a")
                .header("x-amparius-subject", "operator-1")
                .header("x-amparius-token", "secret-token")
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .expect("review tasks");
    assert_eq!(response.status(), StatusCode::OK);
    let body = body_json(response).await;
    assert_eq!(body["tasks"].as_array().expect("tasks").len(), 1);
    assert_eq!(body["tasks"][0]["operation_id"], operation.operation_id);
    assert_eq!(body["tasks"][0]["code"], "schema_invalid");

    let _ = std::fs::remove_file(path);
}

#[tokio::test]
async fn status_and_operations_are_tenant_bound() {
    let path = temp_path();
    let app_a = app(&path, "tenant-a", "operator-1");
    let created = app_a
        .clone()
        .oneshot(operation_request(
            "contract_create",
            "contract-1",
            "device-op-1",
            json!({"total_cents": 100}),
        ))
        .await
        .expect("create");
    assert_eq!(created.status(), StatusCode::CREATED);

    let tenant_b = app(&path, "tenant-b", "operator-2");
    let status = tenant_b
        .clone()
        .oneshot(
            Request::builder()
                .uri("/relay/v1/status?node_id=node-1")
                .header("x-amparius-tenant", "tenant-b")
                .header("x-amparius-subject", "operator-2")
                .header("x-amparius-token", "secret-token")
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .expect("status");
    assert_eq!(status.status(), StatusCode::OK);
    assert_eq!(body_json(status).await["queue"]["total"], 0);

    let wrong_token = app_a
        .clone()
        .oneshot(
            Request::builder()
                .uri("/relay/v1/status?node_id=node-1")
                .header("x-amparius-tenant", "tenant-a")
                .header("x-amparius-subject", "operator-1")
                .header("x-amparius-token", "wrong-token")
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .expect("wrong token");
    assert_eq!(wrong_token.status(), StatusCode::UNAUTHORIZED);

    let _ = std::fs::remove_file(path);
}
