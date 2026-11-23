use serde::Deserialize;
use thiserror::Error;

#[derive(Debug, Clone, Deserialize)]
pub struct RelayConfig {
    pub bind_addr: String,
    pub postgres_url: String,
}

impl Default for RelayConfig {
    fn default() -> Self {
        Self {
            bind_addr: "127.0.0.1:8080".to_owned(),
            postgres_url: "postgres://localhost/amparius_relay".to_owned(),
        }
    }
}

#[derive(Debug, Error)]
pub enum ConfigError {
    #[error("endereço de bind vazio")]
    EmptyBindAddress,
    #[error("URL do PostgreSQL vazia")]
    EmptyPostgresUrl,
}

impl RelayConfig {
    pub fn from_env() -> Result<Self, ConfigError> {
        let defaults = Self::default();
        let config = Self {
            bind_addr: std::env::var("AMPARIUS_RELAY_BIND_ADDR").unwrap_or(defaults.bind_addr),
            postgres_url: std::env::var("AMPARIUS_RELAY_POSTGRES_URL")
                .unwrap_or(defaults.postgres_url),
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
    }
}
