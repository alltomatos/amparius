use ed25519_dalek::{Signature, Signer, SigningKey, Verifier, VerifyingKey};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use thiserror::Error;

pub const SNAPSHOT_SCHEMA_VERSION: u16 = 1;

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct SignedSnapshot {
    pub schema_version: u16,
    pub key_id: String,
    pub tenant_id: String,
    pub version: u64,
    pub issued_at: u64,
    pub valid_until: u64,
    pub payload: Value,
    pub signature: Vec<u8>,
}

#[derive(Debug, Clone, PartialEq)]
pub struct VerifiedSnapshot {
    pub key_id: String,
    pub tenant_id: String,
    pub version: u64,
    pub issued_at: u64,
    pub valid_until: u64,
    pub payload: Value,
}

#[derive(Debug)]
pub struct SnapshotSigner {
    key_id: String,
    signing_key: SigningKey,
}

#[derive(Debug, Error)]
pub enum SnapshotError {
    #[error("tenant_id vazio")]
    EmptyTenant,
    #[error("key_id vazio")]
    EmptyKeyId,
    #[error("snapshot deve ter issued_at menor que valid_until")]
    InvalidValidity,
    #[error("snapshot expirado ou ainda não válido")]
    Expired,
    #[error("snapshot pertence a outro tenant")]
    TenantMismatch,
    #[error("schema de snapshot não suportado")]
    UnsupportedSchema,
    #[error("assinatura do snapshot inválida")]
    InvalidSignature,
    #[error("assinatura do snapshot tem tamanho inválido")]
    InvalidSignatureLength,
}

#[derive(Serialize)]
struct UnsignedSnapshot<'a> {
    schema_version: u16,
    key_id: &'a str,
    tenant_id: &'a str,
    version: u64,
    issued_at: u64,
    valid_until: u64,
    payload: &'a Value,
}

impl SnapshotSigner {
    pub fn from_bytes(
        key_id: impl Into<String>,
        key_bytes: &[u8; 32],
    ) -> Result<Self, SnapshotError> {
        let key_id = key_id.into();
        if key_id.trim().is_empty() {
            return Err(SnapshotError::EmptyKeyId);
        }
        Ok(Self {
            key_id,
            signing_key: SigningKey::from_bytes(key_bytes),
        })
    }

    pub fn verifying_key(&self) -> VerifyingKey {
        self.signing_key.verifying_key()
    }

    pub fn sign(
        &self,
        tenant_id: impl Into<String>,
        version: u64,
        issued_at: u64,
        valid_until: u64,
        payload: Value,
    ) -> Result<SignedSnapshot, SnapshotError> {
        let tenant_id = tenant_id.into();
        validate_fields(&self.key_id, &tenant_id, issued_at, valid_until)?;
        let unsigned = UnsignedSnapshot {
            schema_version: SNAPSHOT_SCHEMA_VERSION,
            key_id: &self.key_id,
            tenant_id: &tenant_id,
            version,
            issued_at,
            valid_until,
            payload: &payload,
        };
        let signature = self.signing_key.sign(&canonical_bytes(&unsigned));
        Ok(SignedSnapshot {
            schema_version: SNAPSHOT_SCHEMA_VERSION,
            key_id: self.key_id.clone(),
            tenant_id,
            version,
            issued_at,
            valid_until,
            payload,
            signature: signature.to_bytes().to_vec(),
        })
    }
}

impl SignedSnapshot {
    pub fn verify(
        &self,
        verifying_key: &VerifyingKey,
        expected_tenant: &str,
        now: u64,
    ) -> Result<VerifiedSnapshot, SnapshotError> {
        if self.schema_version != SNAPSHOT_SCHEMA_VERSION {
            return Err(SnapshotError::UnsupportedSchema);
        }
        validate_fields(
            &self.key_id,
            &self.tenant_id,
            self.issued_at,
            self.valid_until,
        )?;
        if self.tenant_id != expected_tenant {
            return Err(SnapshotError::TenantMismatch);
        }
        if self.issued_at > now || self.valid_until <= now {
            return Err(SnapshotError::Expired);
        }
        let signature = Signature::try_from(self.signature.as_slice())
            .map_err(|_| SnapshotError::InvalidSignatureLength)?;
        let unsigned = UnsignedSnapshot {
            schema_version: self.schema_version,
            key_id: &self.key_id,
            tenant_id: &self.tenant_id,
            version: self.version,
            issued_at: self.issued_at,
            valid_until: self.valid_until,
            payload: &self.payload,
        };
        verifying_key
            .verify(&canonical_bytes(&unsigned), &signature)
            .map_err(|_| SnapshotError::InvalidSignature)?;
        Ok(VerifiedSnapshot {
            key_id: self.key_id.clone(),
            tenant_id: self.tenant_id.clone(),
            version: self.version,
            issued_at: self.issued_at,
            valid_until: self.valid_until,
            payload: self.payload.clone(),
        })
    }
}

fn validate_fields(
    key_id: &str,
    tenant_id: &str,
    issued_at: u64,
    valid_until: u64,
) -> Result<(), SnapshotError> {
    if key_id.trim().is_empty() {
        return Err(SnapshotError::EmptyKeyId);
    }
    if tenant_id.trim().is_empty() {
        return Err(SnapshotError::EmptyTenant);
    }
    if issued_at >= valid_until {
        return Err(SnapshotError::InvalidValidity);
    }
    Ok(())
}

fn canonical_bytes(unsigned: &UnsignedSnapshot<'_>) -> Vec<u8> {
    serde_json::to_vec(unsigned).expect("snapshot unsigned fields are serializable")
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    fn signer() -> SnapshotSigner {
        SnapshotSigner::from_bytes("control-plane-key-1", &[7; 32]).expect("signer")
    }

    #[test]
    fn signs_and_verifies_snapshot_for_expected_tenant() {
        let signer = signer();
        let snapshot = signer
            .sign("tenant-a", 3, 100, 200, json!({"modules": ["finance"]}))
            .expect("snapshot");
        let verified = snapshot
            .verify(&signer.verifying_key(), "tenant-a", 150)
            .expect("valid snapshot");
        assert_eq!(verified.version, 3);
        assert_eq!(verified.payload["modules"][0], "finance");
    }

    #[test]
    fn expired_snapshot_fails_closed() {
        let signer = signer();
        let snapshot = signer
            .sign("tenant-a", 1, 100, 200, json!({}))
            .expect("snapshot");
        assert!(matches!(
            snapshot.verify(&signer.verifying_key(), "tenant-a", 200),
            Err(SnapshotError::Expired)
        ));
    }

    #[test]
    fn wrong_tenant_fails_before_signature_acceptance() {
        let signer = signer();
        let snapshot = signer
            .sign("tenant-a", 1, 100, 200, json!({}))
            .expect("snapshot");
        assert!(matches!(
            snapshot.verify(&signer.verifying_key(), "tenant-b", 150),
            Err(SnapshotError::TenantMismatch)
        ));
    }

    #[test]
    fn payload_tampering_invalidates_signature() {
        let signer = signer();
        let mut snapshot = signer
            .sign("tenant-a", 1, 100, 200, json!({"quota": 10}))
            .expect("snapshot");
        snapshot.payload = json!({"quota": 1_000_000});
        assert!(matches!(
            snapshot.verify(&signer.verifying_key(), "tenant-a", 150),
            Err(SnapshotError::InvalidSignature)
        ));
    }
}
