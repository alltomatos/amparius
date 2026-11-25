use crate::ReconciliationDomain;
use crate::auth::{AuthCache, AuthError, AuthenticatedSubject, CachedCredential};
use crate::queue::{
    DeviceSequencer, FiscalStatus, OperationDraft, OperationKind, OperationStatus, QueueError,
    QueueStatus, QueuedOperation, RelayMode, RelayStatus, content_hash, operation_id,
    validate_draft, verify_operation,
};
use crate::snapshot::{SignedSnapshot, SnapshotError, VerifiedSnapshot};
use ed25519_dalek::VerifyingKey;
use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::{
    collections::HashSet,
    fs, io,
    path::{Path, PathBuf},
};
use thiserror::Error;

const STORE_SCHEMA_VERSION: u16 = 2;
const PREVIOUS_STORE_SCHEMA_VERSION: u16 = 1;
const MAX_SYNC_ERROR_LEN: usize = 500;
pub const DEFAULT_SYNC_RETRY_LIMIT: u32 = 3;

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct TemporaryRecord {
    pub tenant_id: String,
    pub key: String,
    pub expires_at: u64,
    pub payload: Value,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum ReviewTaskKind {
    FiscalFailure,
    SyncFailure,
    MasterConflict,
    MoneyConflict,
    FiscalConflict,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum ReviewTaskStatus {
    Open,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum RetryDisposition {
    Retried,
    Exhausted,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct ReviewTask {
    pub task_id: String,
    pub tenant_id: String,
    pub operation_id: String,
    pub kind: ReviewTaskKind,
    pub code: String,
    pub status: ReviewTaskStatus,
    pub created_at: u64,
}

#[derive(Debug, Serialize, Deserialize)]
struct PersistedStore {
    schema_version: u16,
    records: Vec<TemporaryRecord>,
    credentials: Vec<CachedCredential>,
    snapshot: Option<SignedSnapshot>,
    #[serde(default)]
    queue: Vec<QueuedOperation>,
    #[serde(default)]
    sequencers: Vec<DeviceSequencer>,
    #[serde(default)]
    review_tasks: Vec<ReviewTask>,
}

impl Default for PersistedStore {
    fn default() -> Self {
        Self {
            schema_version: STORE_SCHEMA_VERSION,
            records: Vec::new(),
            credentials: Vec::new(),
            snapshot: None,
            queue: Vec::new(),
            sequencers: Vec::new(),
            review_tasks: Vec::new(),
        }
    }
}

#[derive(Debug)]
pub struct LocalStore {
    path: PathBuf,
    data: PersistedStore,
}

#[derive(Debug, Error)]
pub enum StoreError {
    #[error("falha de I/O no store local: {0}")]
    Io(#[from] io::Error),
    #[error("store local invÃ¡lido: {0}")]
    Serialization(#[from] serde_json::Error),
    #[error("tenant_id vazio")]
    EmptyTenant,
    #[error("chave do registro vazia")]
    EmptyKey,
    #[error("registro expirado")]
    Expired,
    #[error("registro nÃ£o encontrado")]
    NotFound,
    #[error("schema do store nÃ£o suportado")]
    UnsupportedSchema,
    #[error("snapshot recusado por versÃ£o nÃ£o monotÃ´nica")]
    SnapshotVersionRollback,
    #[error(transparent)]
    Auth(#[from] AuthError),
    #[error(transparent)]
    Snapshot(#[from] SnapshotError),
    #[error(transparent)]
    Queue(#[from] QueueError),
}

impl StoreError {
    pub fn is_auth_error(&self) -> bool {
        matches!(self, Self::Auth(_))
    }

    pub fn is_client_error(&self) -> bool {
        match self {
            Self::EmptyTenant | Self::EmptyKey => true,
            Self::Queue(error) => error.is_client_error(),
            _ => false,
        }
    }
}

impl LocalStore {
    pub fn open(path: impl AsRef<Path>) -> Result<Self, StoreError> {
        let path = path.as_ref().to_path_buf();
        recover_backup(&path)?;
        let (mut data, migrated) = if path.exists() {
            let bytes = fs::read(&path)?;
            let mut data: PersistedStore = serde_json::from_slice(&bytes)?;
            let migrated = match data.schema_version {
                STORE_SCHEMA_VERSION => false,
                PREVIOUS_STORE_SCHEMA_VERSION => {
                    data.schema_version = STORE_SCHEMA_VERSION;
                    true
                }
                _ => return Err(StoreError::UnsupportedSchema),
            };
            (data, migrated)
        } else {
            (PersistedStore::default(), false)
        };
        validate_loaded_queue(&data.queue, &data.sequencers)?;
        refresh_sequencers(&mut data);
        let store = Self { path, data };
        if migrated {
            store.persist()?;
        }
        Ok(store)
    }

    pub fn path(&self) -> &Path {
        &self.path
    }

    pub fn put_record(
        &mut self,
        tenant_id: impl Into<String>,
        key: impl Into<String>,
        expires_at: u64,
        payload: Value,
    ) -> Result<(), StoreError> {
        let tenant_id = tenant_id.into();
        let key = key.into();
        validate_identity(&tenant_id, &key)?;
        let record = TemporaryRecord {
            tenant_id: tenant_id.clone(),
            key: key.clone(),
            expires_at,
            payload,
        };
        if let Some(existing) = self
            .data
            .records
            .iter_mut()
            .find(|item| item.tenant_id == tenant_id && item.key == key)
        {
            *existing = record;
        } else {
            self.data.records.push(record);
        }
        self.persist()
    }

    pub fn get_record(
        &mut self,
        tenant_id: &str,
        key: &str,
        now: u64,
    ) -> Result<Value, StoreError> {
        validate_identity(tenant_id, key)?;
        let index = self
            .data
            .records
            .iter()
            .position(|item| item.tenant_id == tenant_id && item.key == key)
            .ok_or(StoreError::NotFound)?;
        if self.data.records[index].expires_at <= now {
            self.data.records.remove(index);
            self.persist()?;
            return Err(StoreError::Expired);
        }
        Ok(self.data.records[index].payload.clone())
    }

    pub fn purge_expired(&mut self, now: u64) -> Result<usize, StoreError> {
        let before = self.data.records.len();
        self.data.records.retain(|record| record.expires_at > now);
        let removed = before - self.data.records.len();
        if removed > 0 {
            self.persist()?;
        }
        Ok(removed)
    }

    pub fn cache_credential(&mut self, credential: CachedCredential) -> Result<(), StoreError> {
        validate_identity(&credential.tenant_id, &credential.subject_id)?;
        if credential.token_hash.len() != 32 || credential.issued_at >= credential.valid_until {
            return Err(StoreError::Auth(AuthError::InvalidValidity));
        }
        if let Some(existing) = self.data.credentials.iter_mut().find(|item| {
            item.tenant_id == credential.tenant_id && item.subject_id == credential.subject_id
        }) {
            *existing = credential;
        } else {
            self.data.credentials.push(credential);
        }
        self.persist()
    }

    pub fn authenticate(
        &mut self,
        expected_tenant: &str,
        subject_id: &str,
        token: &str,
        now: u64,
    ) -> Result<AuthenticatedSubject, StoreError> {
        let index = self
            .data
            .credentials
            .iter()
            .position(|item| item.tenant_id == expected_tenant && item.subject_id == subject_id)
            .ok_or(StoreError::Auth(AuthError::NotFound))?;
        let result =
            AuthCache.authenticate(&self.data.credentials[index], expected_tenant, token, now);
        if matches!(result, Err(AuthError::Expired)) {
            self.data.credentials.remove(index);
            self.persist()?;
        }
        result.map_err(StoreError::from)
    }

    pub fn append_operation(
        &mut self,
        subject: &AuthenticatedSubject,
        node_id: &str,
        draft: OperationDraft,
        now: u64,
    ) -> Result<(QueuedOperation, bool), StoreError> {
        if subject.tenant_id.trim().is_empty() {
            return Err(StoreError::Queue(QueueError::EmptyTenant));
        }
        if node_id.trim().is_empty() {
            return Err(StoreError::Queue(QueueError::EmptyNode));
        }
        validate_draft(&draft)?;
        let hash = content_hash(&draft)?;
        if let Some(existing) = self.data.queue.iter().find(|item| {
            item.tenant_id == subject.tenant_id
                && item.node_id == node_id
                && item.idempotency_key == draft.idempotency_key
        }) {
            if existing.content_hash == hash {
                return Ok((existing.clone(), true));
            }
            return Err(StoreError::Queue(QueueError::IdempotencyConflict));
        }

        let previous_last_seq = self
            .data
            .sequencers
            .iter()
            .find(|item| item.tenant_id == subject.tenant_id && item.node_id == node_id)
            .map(|item| item.last_seq);
        let highest_existing = self
            .data
            .queue
            .iter()
            .filter(|item| item.tenant_id == subject.tenant_id && item.node_id == node_id)
            .map(|item| item.seq)
            .max()
            .unwrap_or(0);
        let seq = previous_last_seq
            .unwrap_or(0)
            .max(highest_existing)
            .checked_add(1)
            .ok_or(StoreError::Queue(QueueError::SequenceExhausted))?;
        let operation = QueuedOperation {
            operation_id: operation_id(&subject.tenant_id, node_id, seq, &hash),
            tenant_id: subject.tenant_id.clone(),
            node_id: node_id.to_owned(),
            seq,
            kind: draft.kind,
            entity_id: draft.entity_id,
            idempotency_key: draft.idempotency_key,
            actor_subject_id: subject.subject_id.clone(),
            payload: draft.payload,
            content_hash: hash,
            status: OperationStatus::Pending,
            created_at: now,
            updated_at: now,
            attempts: 0,
            last_error: None,
            synced_at: None,
            server_cursor: None,
            authoritative_version: None,
        };

        if let Some(sequencer) = self
            .data
            .sequencers
            .iter_mut()
            .find(|item| item.tenant_id == subject.tenant_id && item.node_id == node_id)
        {
            sequencer.last_seq = seq;
        } else {
            self.data.sequencers.push(DeviceSequencer {
                tenant_id: subject.tenant_id.clone(),
                node_id: node_id.to_owned(),
                last_seq: seq,
            });
        }
        self.data.queue.push(operation.clone());
        if let Err(error) = self.persist() {
            self.data
                .queue
                .retain(|item| item.operation_id != operation.operation_id);
            match previous_last_seq {
                Some(previous) => {
                    if let Some(sequencer) =
                        self.data.sequencers.iter_mut().find(|item| {
                            item.tenant_id == subject.tenant_id && item.node_id == node_id
                        })
                    {
                        sequencer.last_seq = previous;
                    }
                }
                None => self.data.sequencers.retain(|item| {
                    !(item.tenant_id == subject.tenant_id && item.node_id == node_id)
                }),
            }
            return Err(error);
        }
        Ok((operation, false))
    }

    pub fn list_operations(
        &self,
        subject: &AuthenticatedSubject,
        node_id: Option<&str>,
        after_seq: u64,
        limit: usize,
    ) -> Result<Vec<QueuedOperation>, StoreError> {
        if subject.tenant_id.trim().is_empty() {
            return Err(StoreError::Queue(QueueError::EmptyTenant));
        }
        if let Some(node) = node_id {
            if node.trim().is_empty() {
                return Err(StoreError::Queue(QueueError::EmptyNode));
            }
        }
        let mut operations: Vec<QueuedOperation> = self
            .data
            .queue
            .iter()
            .filter(|item| item.tenant_id == subject.tenant_id)
            .filter(|item| node_id.is_none_or(|node| item.node_id == node))
            .filter(|item| item.seq > after_seq)
            .cloned()
            .collect();
        operations.sort_by_key(|item| item.seq);
        operations.truncate(limit);
        Ok(operations)
    }

    pub fn queue_status(
        &self,
        subject: &AuthenticatedSubject,
        node_id: Option<&str>,
        now: u64,
    ) -> Result<RelayStatus, StoreError> {
        if subject.tenant_id.trim().is_empty() {
            return Err(StoreError::Queue(QueueError::EmptyTenant));
        }
        if let Some(node) = node_id {
            if node.trim().is_empty() {
                return Err(StoreError::Queue(QueueError::EmptyNode));
            }
        }
        let mut status = QueueStatus {
            tenant_id: subject.tenant_id.clone(),
            node_id: node_id.map(str::to_owned),
            pending: 0,
            synced: 0,
            failed: 0,
            conflict: 0,
            retrying: 0,
            total: 0,
            oldest_pending_at: None,
            oldest_pending_age_secs: 0,
            last_seq: 0,
            last_error: None,
        };
        let mut fiscal = FiscalStatus {
            total: 0,
            pending: 0,
            synced: 0,
            failed: 0,
            conflict: 0,
            retrying: 0,
            last_error: None,
        };
        let mut latest_error_at = 0;
        let mut latest_fiscal_error_at = 0;
        for operation in self
            .data
            .queue
            .iter()
            .filter(|item| item.tenant_id == subject.tenant_id)
            .filter(|item| node_id.is_none_or(|node| item.node_id == node))
        {
            status.total += 1;
            status.last_seq = status.last_seq.max(operation.seq);
            if operation.kind == OperationKind::FiscalEmission {
                fiscal.total += 1;
            }
            match operation.status {
                OperationStatus::Pending => {
                    status.pending += 1;
                    if operation.attempts > 0 {
                        status.retrying += 1;
                    }
                    status.oldest_pending_at = Some(
                        status
                            .oldest_pending_at
                            .map_or(operation.created_at, |oldest| {
                                oldest.min(operation.created_at)
                            }),
                    );
                }
                OperationStatus::Synced => status.synced += 1,
                OperationStatus::Failed => status.failed += 1,
                OperationStatus::Conflict => status.conflict += 1,
            }
            if operation.kind == OperationKind::FiscalEmission {
                match operation.status {
                    OperationStatus::Pending => {
                        fiscal.pending += 1;
                        if operation.attempts > 0 {
                            fiscal.retrying += 1;
                        }
                    }
                    OperationStatus::Synced => fiscal.synced += 1,
                    OperationStatus::Failed => fiscal.failed += 1,
                    OperationStatus::Conflict => fiscal.conflict += 1,
                }
                if operation.last_error.is_some() && operation.updated_at >= latest_fiscal_error_at
                {
                    latest_fiscal_error_at = operation.updated_at;
                    fiscal.last_error = operation.last_error.clone();
                }
            }
            if operation.last_error.is_some() && operation.updated_at >= latest_error_at {
                latest_error_at = operation.updated_at;
                status.last_error = operation.last_error.clone();
            }
        }
        status.oldest_pending_age_secs = status
            .oldest_pending_at
            .map_or(0, |created_at| now.saturating_sub(created_at));
        let mode = if status.pending > 0 {
            RelayMode::OfflineQueued
        } else if status.failed > 0 || status.conflict > 0 {
            RelayMode::Degraded
        } else {
            RelayMode::Online
        };
        let review_tasks_open = self
            .data
            .review_tasks
            .iter()
            .filter(|task| task.tenant_id == subject.tenant_id)
            .count();
        Ok(RelayStatus {
            mode,
            queue: status,
            fiscal,
            review_tasks_open,
        })
    }

    pub fn mark_operation_synced(
        &mut self,
        subject: &AuthenticatedSubject,
        target_operation_id: &str,
        cursor: u64,
        now: u64,
    ) -> Result<(), StoreError> {
        self.update_operation(
            subject,
            target_operation_id,
            |operation| {
                if operation.status == OperationStatus::Synced {
                    return Ok(());
                }
                if operation.kind == OperationKind::MasterUpsert {
                    return Err(QueueError::InvalidTransition);
                }
                if operation.status == OperationStatus::Conflict {
                    return Err(QueueError::InvalidTransition);
                }
                operation.status = OperationStatus::Synced;
                operation.synced_at = Some(now);
                operation.attempts = operation.attempts.saturating_add(1);
                operation.server_cursor = Some(cursor);
                operation.authoritative_version = None;
                operation.last_error = None;
                Ok(())
            },
            now,
        )
    }

    pub fn mark_operation_synced_authoritative(
        &mut self,
        subject: &AuthenticatedSubject,
        target_operation_id: &str,
        cursor: u64,
        authoritative_version: u64,
        now: u64,
    ) -> Result<(), StoreError> {
        self.update_operation(
            subject,
            target_operation_id,
            |operation| {
                if operation.status == OperationStatus::Synced {
                    return Ok(());
                }
                if operation.kind != OperationKind::MasterUpsert {
                    return Err(QueueError::InvalidTransition);
                }
                if operation.status == OperationStatus::Conflict {
                    return Err(QueueError::InvalidTransition);
                }
                operation.status = OperationStatus::Synced;
                operation.synced_at = Some(now);
                operation.attempts = operation.attempts.saturating_add(1);
                operation.server_cursor = Some(cursor);
                operation.authoritative_version = Some(authoritative_version);
                operation.last_error = None;
                Ok(())
            },
            now,
        )
    }

    pub fn mark_operation_failed(
        &mut self,
        subject: &AuthenticatedSubject,
        target_operation_id: &str,
        reason: &str,
        now: u64,
    ) -> Result<(), StoreError> {
        let reason = truncate(reason, MAX_SYNC_ERROR_LEN);
        self.update_operation(
            subject,
            target_operation_id,
            |operation| {
                if operation.status != OperationStatus::Pending {
                    return Err(QueueError::InvalidTransition);
                }
                operation.status = OperationStatus::Failed;
                operation.attempts = operation.attempts.saturating_add(1);
                operation.last_error = Some(reason);
                Ok(())
            },
            now,
        )
    }

    pub fn mark_operation_retryable(
        &mut self,
        subject: &AuthenticatedSubject,
        target_operation_id: &str,
        code: &str,
        now: u64,
    ) -> Result<(), StoreError> {
        self.retry_or_fail(
            subject,
            target_operation_id,
            code,
            DEFAULT_SYNC_RETRY_LIMIT,
            now,
        )?;
        Ok(())
    }

    pub fn retry_or_fail(
        &mut self,
        subject: &AuthenticatedSubject,
        target_operation_id: &str,
        code: &str,
        retry_limit: u32,
        now: u64,
    ) -> Result<RetryDisposition, StoreError> {
        let code = safe_code(code);
        let limit = retry_limit.max(1);
        let operation = self
            .data
            .queue
            .iter()
            .find(|item| {
                item.tenant_id == subject.tenant_id && item.operation_id == target_operation_id
            })
            .cloned()
            .ok_or(StoreError::Queue(QueueError::NotFound))?;
        if operation.status != OperationStatus::Pending {
            return Err(StoreError::Queue(QueueError::InvalidTransition));
        }
        if operation.attempts.saturating_add(1) < limit {
            self.update_operation(
                subject,
                target_operation_id,
                |operation| {
                    operation.status = OperationStatus::Pending;
                    operation.attempts = operation.attempts.saturating_add(1);
                    operation.last_error = Some(format!("sync_retry:{code}"));
                    Ok(())
                },
                now,
            )?;
            return Ok(RetryDisposition::Retried);
        }

        self.mark_terminal_sync_failure(subject, target_operation_id, &code, now)?;
        Ok(RetryDisposition::Exhausted)
    }

    fn mark_terminal_sync_failure(
        &mut self,
        subject: &AuthenticatedSubject,
        target_operation_id: &str,
        code: &str,
        now: u64,
    ) -> Result<ReviewTask, StoreError> {
        if subject.tenant_id.trim().is_empty() {
            return Err(StoreError::Queue(QueueError::EmptyTenant));
        }
        let index = self
            .data
            .queue
            .iter()
            .position(|item| {
                item.tenant_id == subject.tenant_id && item.operation_id == target_operation_id
            })
            .ok_or(StoreError::Queue(QueueError::NotFound))?;
        if self.data.queue[index].status != OperationStatus::Pending {
            return Err(StoreError::Queue(QueueError::InvalidTransition));
        }
        let task_id = review_task_id(&subject.tenant_id, target_operation_id);
        let task = ReviewTask {
            task_id,
            tenant_id: subject.tenant_id.clone(),
            operation_id: target_operation_id.to_owned(),
            kind: if self.data.queue[index].kind == OperationKind::FiscalEmission {
                ReviewTaskKind::FiscalFailure
            } else {
                ReviewTaskKind::SyncFailure
            },
            code: format!("sync_retry_exhausted:{code}"),
            status: ReviewTaskStatus::Open,
            created_at: now,
        };
        let previous_queue = self.data.queue[index].clone();
        self.data.queue[index].status = OperationStatus::Failed;
        self.data.queue[index].attempts = self.data.queue[index].attempts.saturating_add(1);
        self.data.queue[index].last_error = Some(task.code.clone());
        self.data.queue[index].updated_at = now;
        self.data.review_tasks.push(task.clone());
        if let Err(error) = self.persist() {
            self.data.queue[index] = previous_queue;
            self.data
                .review_tasks
                .retain(|existing| existing.task_id != task.task_id);
            return Err(error);
        }
        Ok(task)
    }

    pub fn mark_fiscal_failure(
        &mut self,
        subject: &AuthenticatedSubject,
        target_operation_id: &str,
        code: &str,
        now: u64,
    ) -> Result<ReviewTask, StoreError> {
        if subject.tenant_id.trim().is_empty() {
            return Err(StoreError::Queue(QueueError::EmptyTenant));
        }
        let index = self
            .data
            .queue
            .iter()
            .position(|item| {
                item.tenant_id == subject.tenant_id && item.operation_id == target_operation_id
            })
            .ok_or(StoreError::Queue(QueueError::NotFound))?;
        let code = safe_code(code);
        let previous_queue = self.data.queue[index].clone();
        let task_id = review_task_id(&subject.tenant_id, target_operation_id);
        let task = self
            .data
            .review_tasks
            .iter()
            .find(|task| task.task_id == task_id)
            .cloned()
            .unwrap_or_else(|| ReviewTask {
                task_id,
                tenant_id: subject.tenant_id.clone(),
                operation_id: target_operation_id.to_owned(),
                kind: ReviewTaskKind::FiscalFailure,
                code: code.clone(),
                status: ReviewTaskStatus::Open,
                created_at: now,
            });

        if matches!(
            self.data.queue[index].status,
            OperationStatus::Synced | OperationStatus::Conflict
        ) {
            return Err(StoreError::Queue(QueueError::InvalidTransition));
        }
        if self.data.queue[index].status == OperationStatus::Failed {
            if let Some(existing) = self
                .data
                .review_tasks
                .iter()
                .find(|existing| existing.task_id == task.task_id)
            {
                return Ok(existing.clone());
            }
        }
        self.data.queue[index].status = OperationStatus::Failed;
        self.data.queue[index].attempts = self.data.queue[index].attempts.saturating_add(1);
        self.data.queue[index].last_error = Some(format!("fiscal_rejected:{code}"));
        self.data.queue[index].updated_at = now;
        let inserted_task = !self
            .data
            .review_tasks
            .iter()
            .any(|existing| existing.task_id == task.task_id)
            && {
                self.data.review_tasks.push(task.clone());
                true
            };
        if let Err(error) = self.persist() {
            self.data.queue[index] = previous_queue;
            if inserted_task {
                self.data
                    .review_tasks
                    .retain(|existing| existing.task_id != task.task_id);
            }
            return Err(error);
        }
        Ok(task)
    }

    /// Bloqueia uma divergência sem escolher vencedor e grava sua revisão uma
    /// única vez. O payload nunca é copiado para a tarefa: a operação opaca e
    /// o código sanitizado são suficientes para o operador localizar o caso.
    pub fn mark_reconciliation_conflict(
        &mut self,
        subject: &AuthenticatedSubject,
        target_operation_id: &str,
        domain: ReconciliationDomain,
        code: &str,
        now: u64,
    ) -> Result<ReviewTask, StoreError> {
        if subject.tenant_id.trim().is_empty() {
            return Err(StoreError::Queue(QueueError::EmptyTenant));
        }
        let index = self
            .data
            .queue
            .iter()
            .position(|item| {
                item.tenant_id == subject.tenant_id && item.operation_id == target_operation_id
            })
            .ok_or(StoreError::Queue(QueueError::NotFound))?;

        if self.data.queue[index].status == OperationStatus::Synced {
            return Err(StoreError::Queue(QueueError::InvalidTransition));
        }
        let task_id = review_task_id(&subject.tenant_id, target_operation_id);
        if self.data.queue[index].status == OperationStatus::Conflict {
            if let Some(existing) = self
                .data
                .review_tasks
                .iter()
                .find(|task| task.task_id == task_id)
            {
                return Ok(existing.clone());
            }
        }
        if self.data.queue[index].status == OperationStatus::Failed {
            return Err(StoreError::Queue(QueueError::InvalidTransition));
        }

        let task = ReviewTask {
            task_id,
            tenant_id: subject.tenant_id.clone(),
            operation_id: target_operation_id.to_owned(),
            kind: match domain {
                ReconciliationDomain::Master => ReviewTaskKind::MasterConflict,
                ReconciliationDomain::Money => ReviewTaskKind::MoneyConflict,
                ReconciliationDomain::Fiscal => ReviewTaskKind::FiscalConflict,
            },
            code: safe_code(code),
            status: ReviewTaskStatus::Open,
            created_at: now,
        };
        let previous_queue = self.data.queue[index].clone();
        self.data.queue[index].status = OperationStatus::Conflict;
        self.data.queue[index].attempts = self.data.queue[index].attempts.saturating_add(1);
        self.data.queue[index].last_error =
            Some(format!("sync_conflict:{}:{}", domain.as_str(), task.code));
        self.data.queue[index].updated_at = now;
        self.data.review_tasks.push(task.clone());
        if let Err(error) = self.persist() {
            self.data.queue[index] = previous_queue;
            self.data
                .review_tasks
                .retain(|existing| existing.task_id != task.task_id);
            return Err(error);
        }
        Ok(task)
    }

    pub fn list_review_tasks(
        &self,
        subject: &AuthenticatedSubject,
    ) -> Result<Vec<ReviewTask>, StoreError> {
        if subject.tenant_id.trim().is_empty() {
            return Err(StoreError::Queue(QueueError::EmptyTenant));
        }
        Ok(self
            .data
            .review_tasks
            .iter()
            .filter(|task| task.tenant_id == subject.tenant_id)
            .cloned()
            .collect())
    }

    pub fn mark_operation_conflict(
        &mut self,
        subject: &AuthenticatedSubject,
        target_operation_id: &str,
        reason: &str,
        now: u64,
    ) -> Result<(), StoreError> {
        let reason = truncate(reason, MAX_SYNC_ERROR_LEN);
        self.update_operation(
            subject,
            target_operation_id,
            |operation| {
                if operation.status == OperationStatus::Synced {
                    return Err(QueueError::InvalidTransition);
                }
                operation.status = OperationStatus::Conflict;
                operation.last_error = Some(reason);
                Ok(())
            },
            now,
        )
    }

    fn update_operation<F>(
        &mut self,
        subject: &AuthenticatedSubject,
        target_operation_id: &str,
        update: F,
        now: u64,
    ) -> Result<(), StoreError>
    where
        F: FnOnce(&mut QueuedOperation) -> Result<(), QueueError>,
    {
        if subject.tenant_id.trim().is_empty() {
            return Err(StoreError::Queue(QueueError::EmptyTenant));
        }
        let index = self
            .data
            .queue
            .iter()
            .position(|item| {
                item.tenant_id == subject.tenant_id && item.operation_id == target_operation_id
            })
            .ok_or(StoreError::Queue(QueueError::NotFound))?;
        let previous = self.data.queue[index].clone();
        update(&mut self.data.queue[index])?;
        self.data.queue[index].updated_at = now;
        if let Err(error) = self.persist() {
            self.data.queue[index] = previous;
            return Err(error);
        }
        Ok(())
    }

    pub fn apply_snapshot(
        &mut self,
        snapshot: SignedSnapshot,
        verifying_key: &VerifyingKey,
        expected_tenant: &str,
        now: u64,
    ) -> Result<VerifiedSnapshot, StoreError> {
        let verified = snapshot.verify(verifying_key, expected_tenant, now)?;
        if let Some(current) = &self.data.snapshot {
            if current.tenant_id == expected_tenant && current.version >= snapshot.version {
                return Err(StoreError::SnapshotVersionRollback);
            }
        }
        self.data.snapshot = Some(snapshot);
        self.persist()?;
        Ok(verified)
    }

    pub fn current_snapshot(
        &self,
        verifying_key: &VerifyingKey,
        expected_tenant: &str,
        now: u64,
    ) -> Result<VerifiedSnapshot, StoreError> {
        self.data
            .snapshot
            .as_ref()
            .ok_or(StoreError::NotFound)?
            .verify(verifying_key, expected_tenant, now)
            .map_err(StoreError::from)
    }

    fn persist(&self) -> Result<(), StoreError> {
        if let Some(parent) = self.path.parent() {
            fs::create_dir_all(parent)?;
        }
        let bytes = serde_json::to_vec_pretty(&self.data)?;
        let temp_path = self.path.with_extension("json.tmp");
        fs::write(&temp_path, bytes)?;
        Ok(replace_file(&temp_path, &self.path)?)
    }
}

fn validate_loaded_queue(
    queue: &[QueuedOperation],
    sequencers: &[DeviceSequencer],
) -> Result<(), StoreError> {
    let mut operation_ids = HashSet::new();
    let mut sequences = HashSet::new();
    let mut idempotency_keys = HashSet::new();
    let mut devices = HashSet::new();
    for sequencer in sequencers {
        if sequencer.tenant_id.trim().is_empty() || sequencer.node_id.trim().is_empty() {
            return Err(StoreError::Queue(QueueError::Tampered));
        }
        if !devices.insert((sequencer.tenant_id.as_str(), sequencer.node_id.as_str())) {
            return Err(StoreError::Queue(QueueError::Tampered));
        }
    }
    for operation in queue {
        verify_operation(operation)?;
        if operation.tenant_id.trim().is_empty() || operation.node_id.trim().is_empty() {
            return Err(StoreError::Queue(QueueError::Tampered));
        }
        if !operation_ids.insert(operation.operation_id.as_str()) {
            return Err(StoreError::Queue(QueueError::Tampered));
        }
        if !sequences.insert((
            operation.tenant_id.as_str(),
            operation.node_id.as_str(),
            operation.seq,
        )) {
            return Err(StoreError::Queue(QueueError::Tampered));
        }
        if !idempotency_keys.insert((
            operation.tenant_id.as_str(),
            operation.node_id.as_str(),
            operation.idempotency_key.as_str(),
        )) {
            return Err(StoreError::Queue(QueueError::Tampered));
        }
    }
    Ok(())
}

fn refresh_sequencers(data: &mut PersistedStore) {
    for operation in &data.queue {
        match data
            .sequencers
            .iter_mut()
            .find(|item| item.tenant_id == operation.tenant_id && item.node_id == operation.node_id)
        {
            Some(sequencer) => sequencer.last_seq = sequencer.last_seq.max(operation.seq),
            None => data.sequencers.push(DeviceSequencer {
                tenant_id: operation.tenant_id.clone(),
                node_id: operation.node_id.clone(),
                last_seq: operation.seq,
            }),
        }
    }
}

fn truncate(value: &str, max: usize) -> String {
    if value.len() <= max {
        return value.to_owned();
    }
    let mut end = max;
    while !value.is_char_boundary(end) {
        end -= 1;
    }
    value[..end].to_owned()
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

fn review_task_id(tenant_id: &str, operation_id: &str) -> String {
    format!(
        "review_{}",
        crate::queue::hex_sha256(format!("{tenant_id}\0{operation_id}").as_bytes())
    )
}

fn replace_file(temp_path: &Path, destination: &Path) -> Result<(), io::Error> {
    let backup_path = destination.with_extension("json.bak");
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
    let backup_path = destination.with_extension("json.bak");
    if !destination.exists() && backup_path.exists() {
        fs::rename(backup_path, destination)?;
    }
    Ok(())
}

fn validate_identity(tenant_id: &str, key: &str) -> Result<(), StoreError> {
    if tenant_id.trim().is_empty() {
        return Err(StoreError::EmptyTenant);
    }
    if key.trim().is_empty() {
        return Err(StoreError::EmptyKey);
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::queue::OperationKind;
    use serde_json::json;
    use std::time::{SystemTime, UNIX_EPOCH};

    fn temp_store_path() -> PathBuf {
        let nonce = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .expect("clock")
            .as_nanos();
        std::env::temp_dir().join(format!("amparius-relay-test-{nonce}.json"))
    }

    fn auth_subject(
        store: &mut LocalStore,
        tenant: &str,
        subject_id: &str,
        now: u64,
    ) -> AuthenticatedSubject {
        let credential = AuthCache
            .issue(tenant, subject_id, "secret-token", now - 10, now + 3_600)
            .expect("credential");
        store.cache_credential(credential).expect("cache");
        store
            .authenticate(tenant, subject_id, "secret-token", now)
            .expect("auth")
    }

    fn contract(entity: &str, key: &str, total: i64) -> OperationDraft {
        OperationDraft {
            kind: OperationKind::ContractCreate,
            entity_id: entity.to_owned(),
            idempotency_key: key.to_owned(),
            payload: json!({"total_cents": total, "currency": "BRL", "item_count": 2}),
        }
    }

    #[test]
    fn records_are_persistent_tenant_scoped_and_expire() {
        let path = temp_store_path();
        let mut store = LocalStore::open(&path).expect("store");
        store
            .put_record("tenant-a", "catalog", 200, json!({"name": "urna"}))
            .expect("put");
        drop(store);
        let mut store = LocalStore::open(&path).expect("reopen store");
        assert_eq!(
            store.get_record("tenant-a", "catalog", 150).expect("get"),
            json!({"name": "urna"})
        );
        assert!(matches!(
            store.get_record("tenant-b", "catalog", 150),
            Err(StoreError::NotFound)
        ));
        assert!(matches!(
            store.get_record("tenant-a", "catalog", 200),
            Err(StoreError::Expired)
        ));
        let _ = fs::remove_file(path);
    }

    #[test]
    fn credential_cache_persists_without_raw_secret_and_expires() {
        let path = temp_store_path();
        let mut store = LocalStore::open(&path).expect("store");
        let credential = AuthCache
            .issue("tenant-a", "operator-1", "secret-token", 100, 200)
            .expect("credential");
        store.cache_credential(credential).expect("cache");
        let bytes = fs::read_to_string(&path).expect("store file");
        assert!(!bytes.contains("secret-token"));
        assert!(matches!(
            store.authenticate("tenant-a", "operator-1", "secret-token", 200),
            Err(StoreError::Auth(AuthError::Expired))
        ));
        let _ = fs::remove_file(path);
    }

    #[test]
    fn snapshot_rejects_version_rollback_and_expiry() {
        let path = temp_store_path();
        let mut store = LocalStore::open(&path).expect("store");
        let signer = crate::SnapshotSigner::from_bytes("key-1", &[9; 32]).expect("signer");
        let key = signer.verifying_key();
        let current = signer
            .sign("tenant-a", 2, 100, 300, json!({"modules": ["finance"]}))
            .expect("snapshot");
        store
            .apply_snapshot(current, &key, "tenant-a", 150)
            .expect("apply");
        let older = signer
            .sign("tenant-a", 1, 100, 300, json!({}))
            .expect("snapshot");
        assert!(matches!(
            store.apply_snapshot(older, &key, "tenant-a", 150),
            Err(StoreError::SnapshotVersionRollback)
        ));
        assert!(matches!(
            store.current_snapshot(&key, "tenant-a", 300),
            Err(StoreError::Snapshot(SnapshotError::Expired))
        ));
        let _ = fs::remove_file(path);
    }

    #[test]
    fn queue_persists_and_sequence_is_monotonic_per_device() {
        let path = temp_store_path();
        let mut store = LocalStore::open(&path).expect("store");
        let subject = auth_subject(&mut store, "tenant-a", "operator-1", 100);
        let (first, replayed) = store
            .append_operation(
                &subject,
                "node-1",
                contract("contract-1", "key-1", 10_000),
                100,
            )
            .expect("append");
        assert!(!replayed);
        assert_eq!(first.seq, 1);
        drop(store);

        let mut store = LocalStore::open(&path).expect("reopen");
        let subject = auth_subject(&mut store, "tenant-a", "operator-1", 200);
        let (second, _) = store
            .append_operation(
                &subject,
                "node-1",
                contract("contract-2", "key-2", 20_000),
                200,
            )
            .expect("append");
        assert_eq!(second.seq, 2);
        let (third, _) = store
            .append_operation(
                &subject,
                "node-1",
                contract("contract-3", "key-3", 30_000),
                210,
            )
            .expect("append");
        assert_eq!(third.seq, 3);
        let (other_node, _) = store
            .append_operation(
                &subject,
                "node-2",
                contract("contract-4", "key-4", 40_000),
                220,
            )
            .expect("append");
        assert_eq!(other_node.seq, 1);
        let listed = store
            .list_operations(&subject, Some("node-1"), 0, 10)
            .expect("list");
        assert_eq!(listed.len(), 3);
        assert_eq!(
            listed.iter().map(|item| item.seq).collect::<Vec<_>>(),
            vec![1, 2, 3]
        );
        let _ = fs::remove_file(path);
    }

    #[test]
    fn replay_same_key_is_idempotent_and_different_content_is_rejected() {
        let path = temp_store_path();
        let mut store = LocalStore::open(&path).expect("store");
        let subject = auth_subject(&mut store, "tenant-a", "operator-1", 100);
        let draft = contract("contract-1", "key-1", 10_000);
        let (first, replayed) = store
            .append_operation(&subject, "node-1", draft.clone(), 100)
            .expect("first");
        assert!(!replayed);
        let (again, replayed) = store
            .append_operation(&subject, "node-1", draft, 120)
            .expect("again");
        assert!(replayed);
        assert_eq!(first, again);
        assert!(matches!(
            store.append_operation(
                &subject,
                "node-1",
                contract("contract-1", "key-1", 99_000),
                130
            ),
            Err(StoreError::Queue(QueueError::IdempotencyConflict))
        ));
        let listed = store
            .list_operations(&subject, Some("node-1"), 0, 10)
            .expect("list");
        assert_eq!(listed.len(), 1);
        let _ = fs::remove_file(path);
    }

    #[test]
    fn queue_is_tenant_scoped() {
        let path = temp_store_path();
        let mut store = LocalStore::open(&path).expect("store");
        let tenant_a = auth_subject(&mut store, "tenant-a", "operator-1", 100);
        let tenant_b = auth_subject(&mut store, "tenant-b", "operator-2", 100);
        store
            .append_operation(
                &tenant_a,
                "node-1",
                contract("contract-1", "key-1", 10_000),
                100,
            )
            .expect("append a");
        assert!(
            store
                .list_operations(&tenant_b, Some("node-1"), 0, 10)
                .expect("list b")
                .is_empty()
        );
        assert_eq!(
            store
                .queue_status(&tenant_b, Some("node-1"), 130)
                .expect("status b")
                .queue
                .total,
            0
        );
        let _ = fs::remove_file(path);
    }

    #[test]
    fn status_reports_visible_backlog_after_24h_offline() {
        let path = temp_store_path();
        let mut store = LocalStore::open(&path).expect("store");
        let subject = auth_subject(&mut store, "tenant-a", "operator-1", 1_000);
        let day = 24 * 60 * 60;
        store
            .append_operation(
                &subject,
                "node-1",
                contract("contract-1", "key-1", 10_000),
                1_000,
            )
            .expect("append");
        let opened = OperationDraft {
            kind: OperationKind::CashOpen,
            entity_id: "cash-1".to_owned(),
            idempotency_key: "key-2".to_owned(),
            payload: json!({"opening_amount_cents": 50_000}),
        };
        store
            .append_operation(&subject, "node-1", opened, 1_000 + day)
            .expect("append cash");
        let status = store
            .queue_status(&subject, Some("node-1"), 2_000 + day)
            .expect("status");
        assert_eq!(status.mode, RelayMode::OfflineQueued);
        assert_eq!(status.queue.pending, 2);
        assert_eq!(status.queue.total, 2);
        assert_eq!(status.queue.last_seq, 2);
        assert_eq!(status.queue.oldest_pending_age_secs, day + 1_000);
        let _ = fs::remove_file(path);
    }

    #[test]
    fn status_exposes_fiscal_backlog_and_terminal_failure_without_payload() {
        let path = temp_store_path();
        let mut store = LocalStore::open(&path).expect("store");
        let subject = auth_subject(&mut store, "tenant-a", "operator-1", 100);
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
                100,
            )
            .expect("append fiscal");
        let pending = store
            .queue_status(&subject, Some("node-1"), 110)
            .expect("pending status");
        assert_eq!(pending.fiscal.total, 1);
        assert_eq!(pending.fiscal.pending, 1);
        assert_eq!(pending.fiscal.failed, 0);
        assert_eq!(pending.review_tasks_open, 0);

        store
            .mark_fiscal_failure(&subject, &operation.operation_id, "schema_invalid", 120)
            .expect("fiscal failure");
        let failed = store
            .queue_status(&subject, Some("node-1"), 130)
            .expect("failed status");
        assert_eq!(failed.mode, RelayMode::Degraded);
        assert_eq!(failed.fiscal.failed, 1);
        assert_eq!(failed.queue.failed, 1);
        assert_eq!(failed.review_tasks_open, 1);
        assert_eq!(
            failed.fiscal.last_error.as_deref(),
            Some("fiscal_rejected:schema_invalid")
        );
        let _ = fs::remove_file(path);
    }

    #[test]
    fn expired_credential_cannot_enqueue() {
        let path = temp_store_path();
        let mut store = LocalStore::open(&path).expect("store");
        let credential = AuthCache
            .issue("tenant-a", "operator-1", "secret-token", 100, 200)
            .expect("credential");
        store.cache_credential(credential).expect("cache");
        assert!(matches!(
            store.authenticate("tenant-a", "operator-1", "secret-token", 200),
            Err(StoreError::Auth(AuthError::Expired))
        ));
        let _ = fs::remove_file(path);
    }

    #[test]
    fn sync_transitions_are_append_only_and_reject_regression() {
        let path = temp_store_path();
        let mut store = LocalStore::open(&path).expect("store");
        let subject = auth_subject(&mut store, "tenant-a", "operator-1", 100);
        let (operation, _) = store
            .append_operation(
                &subject,
                "node-1",
                contract("contract-1", "key-1", 10_000),
                100,
            )
            .expect("append");
        store
            .mark_operation_synced(&subject, &operation.operation_id, 42, 150)
            .expect("synced");
        store
            .mark_operation_synced(&subject, &operation.operation_id, 42, 160)
            .expect("idempotent synced");
        assert!(matches!(
            store.mark_operation_failed(&subject, &operation.operation_id, "late failure", 170),
            Err(StoreError::Queue(QueueError::InvalidTransition))
        ));
        let status = store
            .queue_status(&subject, Some("node-1"), 180)
            .expect("status");
        assert_eq!(status.queue.synced, 1);
        assert_eq!(status.queue.pending, 0);
        assert_eq!(status.mode, RelayMode::Online);
        let _ = fs::remove_file(path);
    }

    #[test]
    fn tampered_operation_fails_closed_on_open() {
        let path = temp_store_path();
        let mut store = LocalStore::open(&path).expect("store");
        let subject = auth_subject(&mut store, "tenant-a", "operator-1", 100);
        store
            .append_operation(
                &subject,
                "node-1",
                contract("contract-1", "key-1", 10_000),
                100,
            )
            .expect("append");
        drop(store);
        let text = fs::read_to_string(&path).expect("read");
        let tampered = text.replace("\"total_cents\": 10000", "\"total_cents\": 99000");
        assert_ne!(text, tampered);
        fs::write(&path, tampered).expect("write");
        assert!(matches!(
            LocalStore::open(&path),
            Err(StoreError::Queue(QueueError::Tampered))
        ));
        let _ = fs::remove_file(path);
    }

    #[test]
    fn offline_queue_survives_simulated_network_downtime_and_restart() {
        let path = temp_store_path();
        let mut store = LocalStore::open(&path).expect("store");
        let subject = auth_subject(&mut store, "tenant-a", "operator-1", 100);
        store
            .append_operation(
                &subject,
                "node-1",
                contract("contract-1", "key-1", 10_000),
                100,
            )
            .expect("contract");
        store
            .append_operation(
                &subject,
                "node-1",
                OperationDraft {
                    kind: OperationKind::CashMovement,
                    entity_id: "cash-1".to_owned(),
                    idempotency_key: "key-2".to_owned(),
                    payload: json!({"direction": "in", "amount_cents": 5_000, "reason": "recebimento"}),
                },
                120,
            )
            .expect("cash");
        drop(store);

        let mut store = LocalStore::open(&path).expect("reopen after outage");
        let subject = auth_subject(&mut store, "tenant-a", "operator-1", 200);
        let status = store
            .queue_status(&subject, Some("node-1"), 200)
            .expect("status");
        assert_eq!(status.mode, RelayMode::OfflineQueued);
        assert_eq!(status.queue.pending, 2);
        assert_eq!(status.queue.last_seq, 2);
        let operations = store
            .list_operations(&subject, Some("node-1"), 0, 10)
            .expect("list");
        assert_eq!(
            operations
                .iter()
                .map(|item| (item.seq, item.status))
                .collect::<Vec<_>>(),
            vec![(1, OperationStatus::Pending), (2, OperationStatus::Pending)]
        );
        let _ = fs::remove_file(path);
    }

    #[test]
    fn legacy_schema_one_migrates_without_losing_records() {
        let path = temp_store_path();
        let legacy = json!({
            "schema_version": 1,
            "records": [{
                "tenant_id": "tenant-a",
                "key": "catalog",
                "expires_at": 9999999999u64,
                "payload": {"name": "urna"}
            }],
            "credentials": [],
            "snapshot": null
        });
        fs::write(&path, serde_json::to_vec_pretty(&legacy).expect("json")).expect("write");
        let mut store = LocalStore::open(&path).expect("open legacy");
        assert_eq!(
            store
                .get_record("tenant-a", "catalog", 100)
                .expect("record"),
            json!({"name": "urna"})
        );
        let persisted: Value =
            serde_json::from_slice(&fs::read(&path).expect("read")).expect("persisted json");
        assert_eq!(persisted["schema_version"], 2);
        assert_eq!(persisted["queue"], json!([]));
        drop(store);
        let reopened = LocalStore::open(&path).expect("reopen");
        assert!(reopened.path().exists());
        let _ = fs::remove_file(path);
    }
}
