use anyhow::Context;
use tracing::info;

#[tokio::main]
async fn main() -> anyhow::Result<()> {
    tracing_subscriber::fmt::init();
    info!("amparius client iniciado no papel estação");
    info!("interface gráfica ainda não habilitada nesta fase");
    tokio::signal::ctrl_c()
        .await
        .context("aguardar sinal de término")?;
    info!("sinal de término recebido; encerrando client");
    Ok(())
}
