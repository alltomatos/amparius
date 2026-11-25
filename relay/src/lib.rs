mod auth;
mod http;
mod protocol;
mod queue;
mod reconciliation;
mod snapshot;
mod store;
mod sync;

pub use auth::{AuthCache, AuthError, AuthenticatedSubject, CachedCredential};
pub use http::{ApiError, AppState, router};
pub use protocol::{
    AppliedEvent, ApplyOutcome, EnvelopeError, SYNC_EVENT_VERSION, SYNC_PROTOCOL_VERSION,
    SyncEnvelope, SyncReceiver,
};
pub use queue::{
    DeviceSequencer, OperationDraft, OperationKind, OperationStatus, QueueError, QueueStatus,
    QueuedOperation, RelayMode, RelayStatus, content_hash, operation_id, unix_now,
};
pub use reconciliation::{ReconciliationDomain, domain_for};
pub use snapshot::{SignedSnapshot, SnapshotError, SnapshotSigner, VerifiedSnapshot};
pub use store::{
    LocalStore, ReviewTask, ReviewTaskKind, ReviewTaskStatus, StoreError, TemporaryRecord,
};
pub use sync::{SyncEngine, SyncError, SyncOutcome, SyncReport, SyncTransport, TransportError};

use serde::Deserialize;
use std::path::PathBuf;
use thiserror::Error;

#[derive(Debug, Clone, Deserialize)]
pub struct RelayConfig {
    pub bind_addr: String,
    pub postgres_url: String,
    pub store_path: PathBuf,
}

impl Default for RelayConfig {
    fn default() -> Self {
        Self {
            bind_addr: "127.0.0.1:8080".to_owned(),
            postgres_url: "postgres://localhost/amparius_relay".to_owned(),
            store_path: PathBuf::from("relay-data/store.json"),
        }
    }
}

#[derive(Debug, Error)]
pub enum ConfigError {
    #[error("endereço de bind vazio")]
    EmptyBindAddress,
    #[error("URL do PostgreSQL vazia")]
    EmptyPostgresUrl,
    #[error("caminho do store local vazio")]
    EmptyStorePath,
}

impl RelayConfig {
    pub fn from_env() -> Result<Self, ConfigError> {
        let defaults = Self::default();
        let config = Self {
            bind_addr: std::env::var("AMPARIUS_RELAY_BIND_ADDR").unwrap_or(defaults.bind_addr),
            postgres_url: std::env::var("AMPARIUS_RELAY_POSTGRES_URL")
                .unwrap_or(defaults.postgres_url),
            store_path: std::env::var_os("AMPARIUS_RELAY_STORE_PATH")
                .map(PathBuf::from)
                .unwrap_or(defaults.store_path),
        };
        config.validate()?;
        Ok(config)
    }

    fn validate(&self) -> Result<(), ConfigError> {
        if self.bind_addr.trim().is_empty() {
            return Err(ConfigError::EmptyBindAddress);
        }
        if self.postgres_url.trim().is_empty() {
            return Err(ConfigError::EmptyPostgresUrl);
        }
        if self.store_path.as_os_str().is_empty() {
            return Err(ConfigError::EmptyStorePath);
        }
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::RelayConfig;

    #[test]
    fn defaults_are_development_safe() {
        let config = RelayConfig::default();
        assert_eq!(config.bind_addr, "127.0.0.1:8080");
        assert!(config.postgres_url.starts_with("postgres://localhost/"));
        assert_eq!(
            config.store_path,
            std::path::Path::new("relay-data/store.json")
        );
    }
}
