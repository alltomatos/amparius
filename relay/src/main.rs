use std::net::SocketAddr;

use anyhow::Context;
use tokio::net::TcpListener;
use tracing::info;

use amparius_relay::{AppState, LocalStore, RelayConfig, router};

#[tokio::main]
async fn main() -> anyhow::Result<()> {
    tracing_subscriber::fmt::init();
    let config = RelayConfig::from_env().context("carregar configuração do relay")?;
    let address: SocketAddr = config
        .bind_addr
        .parse()
        .context("AMPARIUS_RELAY_BIND_ADDR inválido")?;

    let store = LocalStore::open(&config.store_path).context("abrir store local temporário")?;
    let state = AppState::new(store);
    let app = router(state);
    let listener = TcpListener::bind(address)
        .await
        .with_context(|| format!("escutar em {address}"))?;

    info!(%address, "amparius relay iniciado");
    info!(path = %config.store_path.display(), "store local temporário pronto");
    info!("PostgreSQL local configurado como placeholder; conexão ainda não habilitada");
    axum::serve(listener, app)
        .with_graceful_shutdown(shutdown_signal())
        .await
        .context("servidor HTTP do relay")?;
    Ok(())
}

async fn shutdown_signal() {
    let _ = tokio::signal::ctrl_c().await;
    info!("sinal de término recebido; encerrando relay");
}
