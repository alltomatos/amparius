use crate::auth::{AuthCache, AuthError, AuthenticatedSubject, CachedCredential};
use crate::snapshot::{SignedSnapshot, SnapshotError, VerifiedSnapshot};
use ed25519_dalek::VerifyingKey;
use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::{
    fs, io,
    path::{Path, PathBuf},
};
use thiserror::Error;

const STORE_SCHEMA_VERSION: u16 = 1;

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct TemporaryRecord {
    pub tenant_id: String,
    pub key: String,
    pub expires_at: u64,
    pub payload: Value,
}

#[derive(Debug, Serialize, Deserialize)]
struct PersistedStore {
    schema_version: u16,
    records: Vec<TemporaryRecord>,
    credentials: Vec<CachedCredential>,
    snapshot: Option<SignedSnapshot>,
}

impl Default for PersistedStore {
    fn default() -> Self {
        Self {
            schema_version: STORE_SCHEMA_VERSION,
            records: Vec::new(),
            credentials: Vec::new(),
            snapshot: None,
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
    #[error("store local inválido: {0}")]
    Serialization(#[from] serde_json::Error),
    #[error("tenant_id vazio")]
    EmptyTenant,
    #[error("chave do registro vazia")]
    EmptyKey,
    #[error("registro expirado")]
    Expired,
    #[error("registro não encontrado")]
    NotFound,
    #[error("schema do store não suportado")]
    UnsupportedSchema,
    #[error("snapshot recusado por versão não monotônica")]
    SnapshotVersionRollback,
    #[error(transparent)]
    Auth(#[from] AuthError),
    #[error(transparent)]
    Snapshot(#[from] SnapshotError),
}

impl LocalStore {
    pub fn open(path: impl AsRef<Path>) -> Result<Self, StoreError> {
        let path = path.as_ref().to_path_buf();
        recover_backup(&path)?;
        let data = if path.exists() {
            let bytes = fs::read(&path)?;
            let data: PersistedStore = serde_json::from_slice(&bytes)?;
            if data.schema_version != STORE_SCHEMA_VERSION {
                return Err(StoreError::UnsupportedSchema);
            }
            data
        } else {
            PersistedStore::default()
        };
        Ok(Self { path, data })
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
    use serde_json::json;
    use std::time::{SystemTime, UNIX_EPOCH};

    fn temp_store_path() -> PathBuf {
        let nonce = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .expect("clock")
            .as_nanos();
        std::env::temp_dir().join(format!("amparius-relay-test-{nonce}.json"))
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
}
