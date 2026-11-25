use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use thiserror::Error;

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct CachedCredential {
    pub tenant_id: String,
    pub subject_id: String,
    pub issued_at: u64,
    pub valid_until: u64,
    #[serde(with = "hex_bytes")]
    pub token_hash: Vec<u8>,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct AuthenticatedSubject {
    pub tenant_id: String,
    pub subject_id: String,
}

#[derive(Debug, Default, Clone)]
pub struct AuthCache;

#[derive(Debug, Error, PartialEq, Eq)]
pub enum AuthError {
    #[error("tenant_id vazio")]
    EmptyTenant,
    #[error("subject_id vazio")]
    EmptySubject,
    #[error("token vazio")]
    EmptyToken,
    #[error("validade da credencial inválida")]
    InvalidValidity,
    #[error("credencial ausente")]
    NotFound,
    #[error("credencial expirada")]
    Expired,
    #[error("credencial pertence a outro tenant")]
    TenantMismatch,
    #[error("token inválido")]
    InvalidToken,
}

impl AuthCache {
    pub fn issue(
        &self,
        tenant_id: impl Into<String>,
        subject_id: impl Into<String>,
        token: &str,
        issued_at: u64,
        valid_until: u64,
    ) -> Result<CachedCredential, AuthError> {
        let tenant_id = tenant_id.into();
        let subject_id = subject_id.into();
        validate(&tenant_id, &subject_id, token, issued_at, valid_until)?;
        Ok(CachedCredential {
            tenant_id,
            subject_id,
            issued_at,
            valid_until,
            token_hash: hash_token(token),
        })
    }

    pub fn authenticate(
        &self,
        credential: &CachedCredential,
        expected_tenant: &str,
        token: &str,
        now: u64,
    ) -> Result<AuthenticatedSubject, AuthError> {
        if credential.tenant_id != expected_tenant {
            return Err(AuthError::TenantMismatch);
        }
        if credential.valid_until <= now || credential.issued_at > now {
            return Err(AuthError::Expired);
        }
        if token.is_empty() {
            return Err(AuthError::EmptyToken);
        }
        if !constant_time_eq(&credential.token_hash, &hash_token(token)) {
            return Err(AuthError::InvalidToken);
        }
        Ok(AuthenticatedSubject {
            tenant_id: credential.tenant_id.clone(),
            subject_id: credential.subject_id.clone(),
        })
    }
}

fn validate(
    tenant_id: &str,
    subject_id: &str,
    token: &str,
    issued_at: u64,
    valid_until: u64,
) -> Result<(), AuthError> {
    if tenant_id.trim().is_empty() {
        return Err(AuthError::EmptyTenant);
    }
    if subject_id.trim().is_empty() {
        return Err(AuthError::EmptySubject);
    }
    if token.is_empty() {
        return Err(AuthError::EmptyToken);
    }
    if issued_at >= valid_until {
        return Err(AuthError::InvalidValidity);
    }
    Ok(())
}

fn hash_token(token: &str) -> Vec<u8> {
    Sha256::digest(token.as_bytes()).to_vec()
}

fn constant_time_eq(left: &[u8], right: &[u8]) -> bool {
    if left.len() != right.len() {
        return false;
    }
    let mut difference = 0u8;
    for (a, b) in left.iter().zip(right) {
        difference |= a ^ b;
    }
    difference == 0
}

mod hex_bytes {
    use serde::{Deserialize, Deserializer, Serializer};

    pub fn serialize<S>(value: &[u8], serializer: S) -> Result<S::Ok, S::Error>
    where
        S: Serializer,
    {
        serializer.serialize_bytes(value)
    }

    pub fn deserialize<'de, D>(deserializer: D) -> Result<Vec<u8>, D::Error>
    where
        D: Deserializer<'de>,
    {
        <Vec<u8>>::deserialize(deserializer)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn cache_never_serializes_the_raw_token() {
        let credential = AuthCache
            .issue("tenant-a", "operator-1", "secret-token", 100, 200)
            .expect("credential");
        let encoded = serde_json::to_string(&credential).expect("json");
        assert!(!encoded.contains("secret-token"));
        assert!(encoded.contains("token_hash"));
    }

    #[test]
    fn expired_credential_fails_closed() {
        let credential = AuthCache
            .issue("tenant-a", "operator-1", "secret-token", 100, 200)
            .expect("credential");
        assert_eq!(
            AuthCache.authenticate(&credential, "tenant-a", "secret-token", 200),
            Err(AuthError::Expired)
        );
    }

    #[test]
    fn tenant_and_token_are_bound_to_the_cached_credential() {
        let credential = AuthCache
            .issue("tenant-a", "operator-1", "secret-token", 100, 200)
            .expect("credential");
        assert_eq!(
            AuthCache.authenticate(&credential, "tenant-b", "secret-token", 150),
            Err(AuthError::TenantMismatch)
        );
        assert_eq!(
            AuthCache.authenticate(&credential, "tenant-a", "other-token", 150),
            Err(AuthError::InvalidToken)
        );
    }
}
