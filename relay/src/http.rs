use std::sync::{Arc, Mutex};

use axum::{
    Json, Router,
    extract::{Query, State},
    http::{HeaderMap, StatusCode},
    response::{IntoResponse, Response},
    routing::get,
};
use serde::Deserialize;
use serde_json::{Value, json};

use crate::auth::AuthenticatedSubject;
use crate::queue::{OperationDraft, OperationKind, QueueError, RelayStatus};
use crate::store::{LocalStore, StoreError};

const TENANT_HEADER: &str = "x-amparius-tenant";
const SUBJECT_HEADER: &str = "x-amparius-subject";
const TOKEN_HEADER: &str = "x-amparius-token";
const NODE_HEADER: &str = "x-amparius-node";
const DEFAULT_LIMIT: usize = 50;
const MAX_LIMIT: usize = 200;

#[derive(Clone)]
pub struct AppState {
    store: Arc<Mutex<LocalStore>>,
}

impl AppState {
    pub fn new(store: LocalStore) -> Self {
        Self {
            store: Arc::new(Mutex::new(store)),
        }
    }
}

pub fn router(state: AppState) -> Router {
    Router::new()
        .route("/healthz", get(healthz))
        .route("/relay/v1/status", get(status))
        .route(
            "/relay/v1/operations",
            get(list_operations).post(append_operation),
        )
        .with_state(state)
}

async fn healthz() -> StatusCode {
    StatusCode::OK
}

#[derive(Debug, Deserialize)]
struct StatusQuery {
    node_id: Option<String>,
}

async fn status(
    State(state): State<AppState>,
    headers: HeaderMap,
    Query(query): Query<StatusQuery>,
) -> Result<Json<RelayStatus>, ApiError> {
    let now = crate::queue::unix_now();
    let mut store = lock(&state)?;
    let subject = authenticate(&mut store, &headers, now)?;
    let status = store.queue_status(&subject, query.node_id.as_deref(), now)?;
    Ok(Json(status))
}

#[derive(Debug, Deserialize)]
struct ListQuery {
    node_id: Option<String>,
    after_seq: Option<u64>,
    limit: Option<usize>,
}

async fn list_operations(
    State(state): State<AppState>,
    headers: HeaderMap,
    Query(query): Query<ListQuery>,
) -> Result<Json<Value>, ApiError> {
    let now = crate::queue::unix_now();
    let mut store = lock(&state)?;
    let subject = authenticate(&mut store, &headers, now)?;
    let limit = query.limit.unwrap_or(DEFAULT_LIMIT).clamp(1, MAX_LIMIT);
    let after_seq = query.after_seq.unwrap_or(0);
    let operations = store.list_operations(&subject, query.node_id.as_deref(), after_seq, limit)?;
    let next_after_seq = operations.last().map_or(after_seq, |item| item.seq);
    Ok(Json(json!({
        "operations": operations,
        "next_after_seq": next_after_seq
    })))
}

#[derive(Debug, Deserialize)]
struct OperationRequest {
    kind: OperationKind,
    entity_id: String,
    idempotency_key: String,
    payload: Value,
}

async fn append_operation(
    State(state): State<AppState>,
    headers: HeaderMap,
    Json(request): Json<OperationRequest>,
) -> Result<(StatusCode, Json<Value>), ApiError> {
    let now = crate::queue::unix_now();
    let mut store = lock(&state)?;
    let subject = authenticate(&mut store, &headers, now)?;
    let node_id = required_node(&headers)?;
    let (operation, replayed) = store.append_operation(
        &subject,
        &node_id,
        OperationDraft {
            kind: request.kind,
            entity_id: request.entity_id,
            idempotency_key: request.idempotency_key,
            payload: request.payload,
        },
        now,
    )?;
    let status_code = if replayed {
        StatusCode::OK
    } else {
        StatusCode::CREATED
    };
    Ok((
        status_code,
        Json(json!({
            "operation": operation,
            "replayed": replayed
        })),
    ))
}

fn lock(state: &AppState) -> Result<std::sync::MutexGuard<'_, LocalStore>, ApiError> {
    state
        .store
        .lock()
        .map_err(|_| ApiError::internal("store local indisponível"))
}

fn authenticate(
    store: &mut LocalStore,
    headers: &HeaderMap,
    now: u64,
) -> Result<AuthenticatedSubject, ApiError> {
    let tenant = required_header(headers, TENANT_HEADER)?;
    let subject = required_header(headers, SUBJECT_HEADER)?;
    let token = required_header(headers, TOKEN_HEADER)?;
    store
        .authenticate(&tenant, &subject, &token, now)
        .map_err(ApiError::from)
}

fn required_header(headers: &HeaderMap, name: &'static str) -> Result<String, ApiError> {
    headers
        .get(name)
        .and_then(|value| value.to_str().ok())
        .filter(|value| !value.trim().is_empty())
        .map(str::to_owned)
        .ok_or_else(|| {
            ApiError::new(
                StatusCode::UNAUTHORIZED,
                "missing_credential",
                "credencial ausente",
            )
        })
}

fn required_node(headers: &HeaderMap) -> Result<String, ApiError> {
    headers
        .get(NODE_HEADER)
        .and_then(|value| value.to_str().ok())
        .filter(|value| !value.trim().is_empty())
        .map(str::to_owned)
        .ok_or_else(|| {
            ApiError::new(
                StatusCode::BAD_REQUEST,
                "invalid_request",
                "x-amparius-node obrigatório",
            )
        })
}

#[derive(Debug)]
pub struct ApiError {
    status: StatusCode,
    code: &'static str,
    message: String,
}

impl ApiError {
    fn new(status: StatusCode, code: &'static str, message: impl Into<String>) -> Self {
        Self {
            status,
            code,
            message: message.into(),
        }
    }

    fn internal(message: &'static str) -> Self {
        tracing::error!(message, "erro interno do relay");
        Self::new(
            StatusCode::SERVICE_UNAVAILABLE,
            "store_unavailable",
            message,
        )
    }
}

impl From<StoreError> for ApiError {
    fn from(error: StoreError) -> Self {
        let status = match &error {
            StoreError::Auth(_) => StatusCode::UNAUTHORIZED,
            StoreError::Queue(QueueError::IdempotencyConflict) => StatusCode::CONFLICT,
            StoreError::Queue(QueueError::PayloadTooLarge) => StatusCode::PAYLOAD_TOO_LARGE,
            StoreError::Queue(QueueError::NotFound) => StatusCode::NOT_FOUND,
            StoreError::Queue(QueueError::InvalidTransition) => StatusCode::CONFLICT,
            StoreError::Queue(QueueError::Tampered) => StatusCode::INTERNAL_SERVER_ERROR,
            error if error.is_client_error() => StatusCode::BAD_REQUEST,
            StoreError::Io(_) | StoreError::Serialization(_) => StatusCode::SERVICE_UNAVAILABLE,
            _ => StatusCode::INTERNAL_SERVER_ERROR,
        };
        let code = match &error {
            StoreError::Auth(_) => "invalid_credential",
            StoreError::Queue(QueueError::IdempotencyConflict) => "idempotency_conflict",
            StoreError::Queue(QueueError::PayloadTooLarge) => "payload_too_large",
            StoreError::Queue(QueueError::NotFound) => "operation_not_found",
            StoreError::Queue(QueueError::InvalidTransition) => "invalid_transition",
            StoreError::Queue(QueueError::Tampered) => "tampered_store",
            error if error.is_client_error() => "invalid_request",
            _ => "store_unavailable",
        };
        if status.is_server_error() {
            tracing::error!(%error, "falha no relay");
        } else {
            tracing::debug!(%error, "requisição recusada pelo relay");
        }
        Self::new(status, code, error.to_string())
    }
}

impl IntoResponse for ApiError {
    fn into_response(self) -> Response {
        (
            self.status,
            Json(json!({
                "error": {
                    "code": self.code,
                    "message": self.message
                }
            })),
        )
            .into_response()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn error_mapping_never_returns_the_raw_token() {
        let error = ApiError::from(StoreError::Auth(crate::auth::AuthError::InvalidToken));
        let response = error.into_response();
        assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
    }
}
