use amparius_client::ui::app::{self, ClientConfig, RelayConfig, SetupProblem};
use amparius_client::ui::attendance::EntitlementState;
use std::path::PathBuf;
use tracing::info;

fn main() -> anyhow::Result<()> {
    tracing_subscriber::fmt()
        .with_writer(std::io::stderr)
        .init();
    let args: Vec<String> = std::env::args().collect();
    if args.iter().any(|arg| arg == "--help" || arg == "-h") {
        print_usage();
        return Ok(());
    }

    let config = load_config(&args);
    info!(
        configured = config.is_ok(),
        "abrindo painel do dia do cliente"
    );
    app::run(config).map_err(|error| anyhow::anyhow!("falha ao abrir a janela: {error}"))?;
    Ok(())
}

fn load_config(args: &[String]) -> Result<ClientConfig, SetupProblem> {
    let tenant_id = setting(args, "--tenant", "AMPARIUS_TENANT");
    let node_id = setting(args, "--node", "AMPARIUS_NODE");

    let mut missing = Vec::new();
    if tenant_id.is_none() {
        missing.push("Tenant: use --tenant <id> ou a variável AMPARIUS_TENANT.");
    }
    if node_id.is_none() {
        missing.push("Dispositivo: use --node <id> ou a variável AMPARIUS_NODE.");
    }
    let (Some(tenant_id), Some(node_id)) = (tenant_id, node_id) else {
        return Err(SetupProblem { missing });
    };

    let queue_path = setting(args, "--queue", "AMPARIUS_QUEUE_PATH")
        .map(PathBuf::from)
        .unwrap_or_else(|| default_queue_path(&tenant_id, &node_id));

    let base_url = setting(args, "--relay", "AMPARIUS_RELAY_URL");
    let subject_id = setting(args, "--subject", "AMPARIUS_SUBJECT");
    let operator_id = subject_id.clone();
    // A credencial entra apenas por variável de ambiente: argumento de linha de
    // comando ficaria visível no gerenciador de processos.
    let token = env_value("AMPARIUS_TOKEN");

    let (relay, relay_issue) = match (base_url, subject_id, token) {
        (Some(base_url), Some(subject_id), Some(token)) => (
            Some(RelayConfig {
                base_url,
                subject_id,
                token,
            }),
            None,
        ),
        (None, None, None) => (
            None,
            Some("Relay não configurado; a fila permanece local até haver relay."),
        ),
        (None, _, _) => (
            None,
            Some("Endereço do relay ausente (AMPARIUS_RELAY_URL)."),
        ),
        (_, None, _) => (
            None,
            Some("Identidade do operador ausente (AMPARIUS_SUBJECT)."),
        ),
        (_, _, None) => (
            None,
            Some("Credencial de sessão ausente (AMPARIUS_TOKEN); nada é enviado sem ela."),
        ),
    };

    Ok(ClientConfig {
        tenant_id,
        node_id,
        queue_path,
        relay,
        operator_id,
        // CL-02 is dev-only nesta fatia; produção deve substituir este flag por
        // snapshot de entitlement assinado antes de habilitar a ação.
        funeral_entitlement: match setting(
            args,
            "--funeral-entitlement",
            "AMPARIUS_FUNERAL_ENTITLEMENT",
        )
        .as_deref()
        {
            Some("active") => EntitlementState::Active,
            _ => EntitlementState::Blocked,
        },
        relay_issue,
    })
}

fn setting(args: &[String], flag: &str, env_key: &str) -> Option<String> {
    arg_value(args, flag).or_else(|| env_value(env_key))
}

fn arg_value(args: &[String], flag: &str) -> Option<String> {
    args.iter()
        .position(|argument| argument == flag)
        .and_then(|index| args.get(index + 1))
        .map(|value| value.trim().to_owned())
        .filter(|value| !value.is_empty())
}

fn env_value(key: &str) -> Option<String> {
    std::env::var(key)
        .ok()
        .map(|value| value.trim().to_owned())
        .filter(|value| !value.is_empty())
}

fn default_queue_path(tenant_id: &str, node_id: &str) -> PathBuf {
    let base = std::env::var_os("LOCALAPPDATA")
        .map(PathBuf::from)
        .or_else(|| std::env::var_os("APPDATA").map(PathBuf::from))
        .or_else(|| std::env::current_dir().ok())
        .unwrap_or_else(std::env::temp_dir);
    base.join("Amparius").join("client").join(format!(
        "{}-{}.json",
        file_part(tenant_id),
        file_part(node_id)
    ))
}

fn file_part(value: &str) -> String {
    value
        .chars()
        .map(|character| {
            if character.is_ascii_alphanumeric() || matches!(character, '-' | '_' | '.') {
                character
            } else {
                '_'
            }
        })
        .collect()
}

fn print_usage() {
    println!(
        "Amparius — painel do dia (CL-01)\n\
         \n\
         Uso:\n\
           amparius-client --tenant <id> --node <id> [--queue <arquivo>]\n\
         \n\
         Configuração por ambiente:\n\
           AMPARIUS_TENANT        tenant da estação (obrigatório)\n\
           AMPARIUS_NODE          identificador do dispositivo (obrigatório)\n\
           AMPARIUS_QUEUE_PATH    arquivo da fila local (opcional)\n\
           AMPARIUS_RELAY_URL     origem do relay, ex. http://127.0.0.1:8787\n\
           AMPARIUS_SUBJECT       identidade do operador no relay\n\
           AMPARIUS_TOKEN         credencial de sessão (nunca exibida)\n\
         \n\
         Sem tenant e dispositivo a janela abre em modo de configuração e nenhuma\n\
         fila é criada."
    );
}
