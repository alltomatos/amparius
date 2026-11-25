use amparius_client::unix_now;
use anyhow::Context;
use tracing::info;

#[tokio::main]
async fn main() -> anyhow::Result<()> {
    tracing_subscriber::fmt::init();
    info!("amparius client iniciado no papel estação");
    info!(
        at = unix_now(),
        "modelo de fila local carregado; UI ainda não habilitada"
    );
    tokio::signal::ctrl_c()
        .await
        .context("aguardar sinal de término")?;
    info!("sinal de término recebido; encerrando client");
    Ok(())
}
