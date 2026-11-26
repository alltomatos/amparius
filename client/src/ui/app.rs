//! Aplicativo de janela do cliente e ponte entre a UI e a fila persistida.
//!
//! A UI nunca bloqueia no socket: a sincronização roda em uma thread com um
//! runtime próprio e devolve um `SyncReport` por canal. A fila vive em
//! `Arc<Mutex<ClientQueue>>` e toda leitura de tela é uma cópia curta do estado.

use eframe::egui;
use std::path::PathBuf;
use std::sync::mpsc::{self, Receiver, TryRecvError};
use std::sync::{Arc, Mutex, MutexGuard};
use std::time::{Duration, Instant};
use tracing::{info, warn};

use crate::transport::{
    Credentials, HttpTransport, SyncError, SyncReport, SyncStop, TransportConfig, sync_pending,
};
use crate::ui::attendance::{
    AttendanceAction, AttendanceForm, AttendanceServiceState, AttendanceStatus, AttendanceView,
    EntitlementState, SCREEN_ID as ATTENDANCE_SCREEN_ID, build_draft, queued_status,
};
use crate::ui::day_panel::{
    Notice, PanelContext, PanelError, PanelPhase, SCREEN_ID, Severity, SyncSummary, SyncView,
    present,
};
use crate::ui::render::{DayPanelActions, render};
use crate::ui::theme;
use crate::{ClientQueue, ConnectivityState, unix_now};

#[derive(Clone)]
pub struct RelayConfig {
    pub base_url: String,
    pub subject_id: String,
    pub token: String,
}

impl std::fmt::Debug for RelayConfig {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        formatter
            .debug_struct("RelayConfig")
            .field("base_url", &self.base_url)
            .field("subject_id", &self.subject_id)
            .field("token", &"<redacted>")
            .finish()
    }
}

#[derive(Debug, Clone)]
pub struct ClientConfig {
    pub tenant_id: String,
    pub node_id: String,
    pub queue_path: PathBuf,
    pub relay: Option<RelayConfig>,
    pub operator_id: Option<String>,
    pub funeral_entitlement: EntitlementState,
    /// Motivo pelo qual o relay não está utilizável; a UI explica em vez de
    /// fingir sucesso.
    pub relay_issue: Option<&'static str>,
}

#[derive(Debug, Clone)]
pub struct SetupProblem {
    pub missing: Vec<&'static str>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
struct SyncFailure {
    code: &'static str,
}

impl SyncFailure {
    const fn new(code: &'static str) -> Self {
        Self { code }
    }
}

enum SyncOutcome {
    Done(SyncReport),
    Failed(SyncFailure),
}

pub struct ClientApp {
    config: ClientConfig,
    queue: Option<Arc<Mutex<ClientQueue>>>,
    open_error: Option<PanelError>,
    notice: Option<Notice>,
    sync: SyncView,
    sync_rx: Option<Receiver<SyncOutcome>>,
    last_sync_at: Option<u64>,
    started: Instant,
    first_frame: bool,
    last_title: String,
    screen: Screen,
    attendance_form: AttendanceForm,
    attendance_status: AttendanceStatus,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum Screen {
    Day,
    Attendance,
}

impl ClientApp {
    pub fn new(cc: &eframe::CreationContext<'_>, config: ClientConfig) -> Self {
        theme::apply(&cc.egui_ctx);
        let started = Instant::now();
        let (queue, open_error) =
            match ClientQueue::open(&config.queue_path, &config.tenant_id, &config.node_id) {
                Ok(queue) => (Some(Arc::new(Mutex::new(queue))), None),
                Err(error) => {
                    warn!(
                        screen = SCREEN_ID,
                        code = %PanelError::from_queue_error(&error).code,
                        "fila local nao abriu; painel em estado de erro"
                    );
                    (None, Some(PanelError::from_queue_error(&error)))
                }
            };

        let pending = queue
            .as_ref()
            .map(|queue| lock(queue).status().queue.pending)
            .unwrap_or(0);
        let sync = initial_sync_view(&config, pending);

        Self {
            config,
            queue,
            open_error,
            notice: None,
            sync,
            sync_rx: None,
            last_sync_at: None,
            started,
            first_frame: true,
            last_title: String::new(),
            screen: Screen::Day,
            attendance_form: AttendanceForm::default(),
            attendance_status: AttendanceStatus::Ready,
        }
    }

    fn poll_sync(&mut self) {
        let Some(rx) = &self.sync_rx else {
            return;
        };
        match rx.try_recv() {
            Ok(SyncOutcome::Done(report)) => {
                self.last_sync_at = Some(unix_now());
                self.sync = report_view(&report);
                self.notice = None;
                info!(
                    screen = SCREEN_ID,
                    synced = report.synced,
                    duplicates = report.duplicates,
                    conflicts = report.conflicts,
                    rejected = report.rejected,
                    retried = report.retried,
                    remaining_pending = report.remaining_pending,
                    "sincronizacao concluida"
                );
                self.sync_rx = None;
            }
            Ok(SyncOutcome::Failed(failure)) => {
                self.sync = SyncView::failed(failure.code);
                self.notice = Some(Notice {
                    severity: Severity::Danger,
                    text: format!(
                        "A sincronização falhou ({}) e não alterou a fila.",
                        failure.code
                    ),
                });
                warn!(
                    screen = SCREEN_ID,
                    code = failure.code,
                    "sincronizacao falhou"
                );
                self.sync_rx = None;
            }
            Err(TryRecvError::Empty) => {}
            Err(TryRecvError::Disconnected) => {
                self.sync = SyncView::failed("sync_worker_stopped");
                self.sync_rx = None;
            }
        }
    }

    fn start_sync(&mut self) {
        if self.sync_rx.is_some() {
            return;
        }
        let Some(relay) = self.config.relay.clone() else {
            self.sync = SyncView::unavailable(
                self.config
                    .relay_issue
                    .unwrap_or("Relay ou credencial de sincronização não configurados."),
            );
            return;
        };
        let Some(queue) = self.queue.clone() else {
            return;
        };
        let (tx, rx) = mpsc::channel();
        self.sync_rx = Some(rx);
        self.sync = SyncView::running();
        std::thread::spawn(move || {
            let outcome = match run_sync(queue, relay) {
                Ok(report) => SyncOutcome::Done(report),
                Err(failure) => SyncOutcome::Failed(failure),
            };
            let _ = tx.send(outcome);
        });
    }

    fn set_connectivity(&mut self, state: ConnectivityState) {
        let Some(queue) = &self.queue else {
            return;
        };
        let mut guard = lock(queue);
        match guard.set_connectivity(state) {
            Ok(()) => {
                self.notice = Some(Notice {
                    severity: Severity::Info,
                    text: format!(
                        "Conectividade registrada como {}.",
                        match state {
                            ConnectivityState::Online => "online",
                            ConnectivityState::Offline => "offline",
                            ConnectivityState::Contingency => "contingência",
                        }
                    ),
                });
            }
            Err(error) => {
                let panel_error = PanelError::from_queue_error(&error);
                self.notice = Some(Notice {
                    severity: Severity::Danger,
                    text: format!(
                        "Não foi possível registrar a conectividade ({}). A fila não foi alterada.",
                        panel_error.code
                    ),
                });
            }
        }
    }

    fn reload(&mut self) {
        if self.sync_rx.is_some() {
            self.notice = Some(Notice {
                severity: Severity::Warning,
                text: "Aguarde a sincronização em andamento para recarregar a fila.".to_owned(),
            });
            return;
        }
        match ClientQueue::open(
            &self.config.queue_path,
            &self.config.tenant_id,
            &self.config.node_id,
        ) {
            Ok(queue) => {
                let pending = queue.status().queue.pending;
                self.queue = Some(Arc::new(Mutex::new(queue)));
                self.open_error = None;
                self.sync = initial_sync_view(&self.config, pending);
            }
            Err(error) => {
                self.queue = None;
                self.open_error = Some(PanelError::from_queue_error(&error));
                self.sync = SyncView::unavailable(
                    "Fila local indisponível; a sincronização está bloqueada.",
                );
            }
        }
    }

    fn handle(&mut self, actions: DayPanelActions) {
        if let Some(state) = actions.set_connectivity {
            self.set_connectivity(state);
        }
        if actions.sync {
            self.start_sync();
        }
        if actions.reload {
            self.reload();
        }
        if actions.open_attendance {
            self.screen = Screen::Attendance;
            self.attendance_status = AttendanceStatus::Ready;
        }
    }

    fn attendance_view(&self) -> AttendanceView {
        if self.queue.is_none() {
            return AttendanceView {
                service_state: AttendanceServiceState::ServiceUnavailable,
                status: AttendanceStatus::Error {
                    code: "QUEUE_UNAVAILABLE".to_owned(),
                    message: "A fila local está indisponível; nenhuma operação será criada."
                        .to_owned(),
                },
                can_submit: false,
                detail: "Corrija a fila local antes de iniciar o atendimento.",
            };
        }
        let (connectivity, operator_configured) = self
            .queue
            .as_ref()
            .map(|queue| {
                let queue = lock(queue);
                (queue.connectivity(), self.config.operator_id.is_some())
            })
            .unwrap_or((ConnectivityState::Offline, false));
        AttendanceView::new(
            connectivity,
            self.config.funeral_entitlement,
            self.config.relay.is_some(),
            operator_configured,
            self.attendance_status.clone(),
        )
    }

    fn submit_attendance(&mut self, action: AttendanceAction) {
        self.attendance_form.action = action;
        let view = self.attendance_view();
        if self.config.funeral_entitlement == EntitlementState::Blocked {
            self.attendance_status = AttendanceStatus::Error {
                code: "LICENSE_INACTIVE".to_owned(),
                message: "O módulo funeral está bloqueado por entitlement.".to_owned(),
            };
            return;
        }
        if view.service_state == AttendanceServiceState::ServiceUnavailable {
            self.attendance_status = AttendanceStatus::Error {
                code: "SERVICE_UNAVAILABLE".to_owned(),
                message: "O relay não está disponível para esta estação.".to_owned(),
            };
            return;
        }
        let Some(queue) = self.queue.clone() else {
            self.attendance_status = AttendanceStatus::Error {
                code: "QUEUE_UNAVAILABLE".to_owned(),
                message: "A fila local está indisponível; nada foi criado.".to_owned(),
            };
            return;
        };
        let Some(operator_id) = self.config.operator_id.clone() else {
            self.attendance_status = AttendanceStatus::Error {
                code: "OPERATOR_UNAVAILABLE".to_owned(),
                message: "A identidade do operador é necessária para enfileirar.".to_owned(),
            };
            return;
        };
        let draft = match build_draft(&self.attendance_form) {
            Ok(draft) => draft,
            Err(error) => {
                self.attendance_status = AttendanceStatus::Error {
                    code: attendance_error_code(&error).to_owned(),
                    message: attendance_error_message(&error).to_owned(),
                };
                return;
            }
        };
        let mut queue = lock(&queue);
        match queue.enqueue(operator_id, draft, unix_now()) {
            Ok(result) => {
                self.attendance_status = queued_status(&result.operation);
                info!(
                    screen = ATTENDANCE_SCREEN_ID,
                    operation_kind = result.operation.kind.as_str(),
                    seq = result.operation.seq,
                    replayed = result.replayed,
                    "atendimento enfileirado"
                );
                self.notice = Some(Notice {
                    severity: Severity::Success,
                    text: if result.replayed {
                        "A operação já estava na fila; nenhuma duplicata foi criada.".to_owned()
                    } else {
                        "Atendimento enfileirado com idempotência; o estado local foi persistido."
                            .to_owned()
                    },
                });
            }
            Err(error) => {
                self.attendance_status = AttendanceStatus::Error {
                    code: attendance_error_code(&error).to_owned(),
                    message: attendance_error_message(&error).to_owned(),
                };
            }
        }
    }
}

impl eframe::App for ClientApp {
    fn ui(&mut self, ui: &mut egui::Ui, _frame: &mut eframe::Frame) {
        let ctx = ui.ctx().clone();
        self.poll_sync();

        if self.screen == Screen::Attendance {
            let view = self.attendance_view();
            let mut actions = crate::ui::render::AttendanceActions::default();
            egui::CentralPanel::default()
                .frame(
                    egui::Frame::new()
                        .fill(theme::palette(ui.visuals().dark_mode).surface)
                        .inner_margin(egui::Margin::symmetric(16, 12)),
                )
                .show(ui, |ui| {
                    crate::ui::render::render_attendance(
                        ui,
                        &mut self.attendance_form,
                        &view,
                        &mut actions,
                    );
                });
            if actions.back {
                self.screen = Screen::Day;
                self.attendance_status = AttendanceStatus::Ready;
            }
            if actions.create_journey {
                self.submit_attendance(AttendanceAction::CreateJourney);
            }
            if actions.advance_order {
                self.submit_attendance(AttendanceAction::AdvanceOrder);
            }
            if actions.complete_order {
                self.submit_attendance(AttendanceAction::CompleteOrder);
            }
            ctx.request_repaint_after(Duration::from_millis(120));
            let title = format!(
                "Amparius — Atendimento funeral — {}",
                view.service_state.label()
            );
            if title != self.last_title {
                ctx.send_viewport_cmd(egui::ViewportCommand::Title(title.clone()));
                self.last_title = title;
            }
            return;
        }

        let now = unix_now();
        let guard = self.queue.as_ref().map(|queue| lock(queue));
        let queue_ref = guard.as_deref();

        let phase = if queue_ref.is_some() {
            PanelPhase::Ready
        } else {
            PanelPhase::Error
        };
        let view = present(PanelContext {
            queue: queue_ref,
            phase,
            error: self.open_error.clone(),
            notice: self.notice.clone(),
            sync: self.sync.clone(),
            last_sync_at: self.last_sync_at,
            now,
        });

        let mut actions = DayPanelActions::default();
        egui::CentralPanel::default()
            .frame(
                egui::Frame::new()
                    .fill(theme::palette(ui.visuals().dark_mode).surface)
                    .inner_margin(egui::Margin::symmetric(16, 12)),
            )
            .show(ui, |ui| {
                render(ui, &view, &mut actions);
            });
        drop(guard);

        self.handle(actions);

        if self.sync.running {
            ctx.request_repaint_after(Duration::from_millis(120));
        }

        let title = format!(
            "Amparius — {} — {} pendente(s)",
            view.banner.title, view.counters.pending
        );
        if title != self.last_title {
            ctx.send_viewport_cmd(egui::ViewportCommand::Title(title.clone()));
            self.last_title = title;
        }

        if self.first_frame {
            self.first_frame = false;
            info!(
                screen = SCREEN_ID,
                boot_ms = self.started.elapsed().as_millis() as u64,
                "client first frame"
            );
        }
    }
}

fn initial_sync_view(config: &ClientConfig, pending: usize) -> SyncView {
    match config.relay_issue {
        Some(reason) => SyncView::unavailable(reason),
        None => SyncView::idle(pending, config.relay.is_some()),
    }
}

fn lock(queue: &Arc<Mutex<ClientQueue>>) -> MutexGuard<'_, ClientQueue> {
    queue
        .lock()
        .unwrap_or_else(|poisoned| poisoned.into_inner())
}

fn report_view(report: &SyncReport) -> SyncView {
    SyncSummary {
        synced: report.synced,
        duplicates: report.duplicates,
        conflicts: report.conflicts,
        rejected: report.rejected,
        retried: report.retried,
        remaining_pending: report.remaining_pending,
        stopped: report.stopped.map(stop_code),
    }
    .view()
}

fn stop_code(stop: SyncStop) -> &'static str {
    match stop {
        SyncStop::TransportUnavailable => "transport_unavailable",
        SyncStop::TransportTimeout => "transport_timeout",
        SyncStop::Unauthorized => "unauthorized",
    }
}

fn attendance_error_code(error: &crate::ClientError) -> &'static str {
    match error {
        crate::ClientError::InvalidPayload(field) => field,
        crate::ClientError::IdempotencyConflict => "IDEMPOTENCY_CONFLICT",
        crate::ClientError::PersonalDataInPayload => "PII_NOT_ALLOWED",
        crate::ClientError::InvalidIdentifier => "INVALID_REFERENCE",
        _ => "ATTENDANCE_QUEUE_ERROR",
    }
}

fn attendance_error_message(error: &crate::ClientError) -> &'static str {
    match error {
        crate::ClientError::InvalidPayload("contract_active_required") => {
            "Confirme que o contrato está active antes de criar a jornada."
        }
        crate::ClientError::InvalidPayload(_) => "Revise os campos obrigatórios do atendimento.",
        crate::ClientError::IdempotencyConflict => {
            "A mesma chave já foi usada com outro conteúdo; a operação não foi substituída."
        }
        crate::ClientError::PersonalDataInPayload => {
            "Use referências opacas; nome, documento e contato não entram na fila."
        }
        crate::ClientError::InvalidIdentifier => "A referência contém caracteres não permitidos.",
        _ => "A operação não foi enfileirada; a fila local foi preservada.",
    }
}

fn run_sync(queue: Arc<Mutex<ClientQueue>>, relay: RelayConfig) -> Result<SyncReport, SyncFailure> {
    let config = TransportConfig::new(relay.base_url)
        .map_err(|_| SyncFailure::new("transport_config_invalid"))?;
    let credentials = Credentials::new(relay.subject_id, relay.token)
        .map_err(|_| SyncFailure::new("credentials_invalid"))?;
    let transport =
        HttpTransport::new(config).map_err(|_| SyncFailure::new("transport_build_failed"))?;
    let runtime = tokio::runtime::Builder::new_current_thread()
        .enable_all()
        .build()
        .map_err(|_| SyncFailure::new("runtime_unavailable"))?;
    let mut guard = lock(&queue);
    runtime
        .block_on(sync_pending(
            &mut guard,
            &transport,
            &credentials,
            unix_now(),
        ))
        .map_err(|error| match error {
            SyncError::Transport(_) => SyncFailure::new("sync_transport_error"),
            SyncError::Queue(_) => SyncFailure::new("sync_queue_error"),
        })
}

/// Tela de configuração obrigatória: sem tenant/dispositivo não existe fila e
/// nada é criado com identidade inventada.
pub struct SetupApp {
    problem: SetupProblem,
    started: Instant,
}

impl SetupApp {
    pub fn new(problem: SetupProblem) -> Self {
        Self {
            problem,
            started: Instant::now(),
        }
    }
}

impl eframe::App for SetupApp {
    fn ui(&mut self, ui: &mut egui::Ui, _frame: &mut eframe::Frame) {
        let ctx = ui.ctx().clone();
        let palette = theme::palette(ui.visuals().dark_mode);
        egui::CentralPanel::default()
            .frame(
                egui::Frame::new()
                    .fill(palette.surface)
                    .inner_margin(egui::Margin::symmetric(20, 16)),
            )
            .show(ui, |ui| {
                ui.set_max_width(760.0);
                ui.heading(egui::RichText::new("Configuração necessária").color(palette.brand_900));
                ui.label(
                    egui::RichText::new(
                        "A estação precisa de identidade antes de abrir a fila local. Nenhum dado é criado com tenant inventado.",
                    )
                    .color(palette.text),
                );
                ui.add_space(8.0);
                for missing in &self.problem.missing {
                    ui.label(
                        egui::RichText::new(format!("• {}", missing))
                            .strong()
                            .color(palette.danger),
                    );
                }
                ui.add_space(8.0);
                ui.label(
                    egui::RichText::new("Exemplo no PowerShell, a partir da raiz do repositório:")
                        .color(palette.text_muted),
                );
                ui.add(
                    egui::Label::new(
                        egui::RichText::new(
                            "$env:AMPARIUS_TENANT=\"loja-1\"; $env:AMPARIUS_NODE=\"estacao-1\"; cargo run --manifest-path client/Cargo.toml",
                        )
                        .monospace(),
                    )
                    .wrap(),
                );
                ui.add_space(8.0);
                ui.label(
                    egui::RichText::new(
                        "Opcional para sincronizar: AMPARIUS_RELAY_URL, AMPARIUS_SUBJECT e AMPARIUS_TOKEN. A credencial nunca é exibida nem gravada.",
                    )
                    .small()
                    .color(palette.text_muted),
                );
            });

        if self.started.elapsed() < Duration::from_millis(500) {
            ctx.request_repaint_after(Duration::from_millis(500));
        }
    }
}

pub fn run(config: Result<ClientConfig, SetupProblem>) -> Result<(), eframe::Error> {
    let options = eframe::NativeOptions {
        viewport: egui::ViewportBuilder::default()
            .with_inner_size([1024.0, 720.0])
            .with_min_inner_size([360.0, 480.0])
            .with_title("Amparius — Painel do dia"),
        ..Default::default()
    };
    eframe::run_native(
        "Amparius — Painel do dia",
        options,
        Box::new(move |cc| {
            let app: Box<dyn eframe::App> = match config {
                Ok(config) => Box::new(ClientApp::new(cc, config)),
                Err(problem) => Box::new(SetupApp::new(problem)),
            };
            Ok(app)
        }),
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn report_view_marks_stop_and_counts_without_pii() {
        let report = SyncReport {
            attempted: 3,
            http_attempts: 4,
            synced: 1,
            duplicates: 1,
            conflicts: 0,
            rejected: 0,
            retried: 1,
            remaining_pending: 2,
            stopped: Some(SyncStop::TransportTimeout),
        };
        let view = report_view(&report);
        assert!(!view.can_start);
        assert_eq!(view.severity, Severity::Warning);
        let detail = view.detail.unwrap_or_default();
        assert!(detail.contains("transport_timeout"));
        assert!(!detail.contains("tenant"));
    }

    #[test]
    fn initial_sync_explains_missing_credentials() {
        let config = ClientConfig {
            tenant_id: "tenant-a".to_owned(),
            node_id: "node-1".to_owned(),
            queue_path: PathBuf::from("unused.json"),
            relay: None,
            operator_id: None,
            funeral_entitlement: EntitlementState::Blocked,
            relay_issue: Some("Credencial de sincronização ausente."),
        };
        let view = initial_sync_view(&config, 4);
        assert!(!view.can_start);
        assert_eq!(view.severity, Severity::Warning);
        assert!(view.detail.unwrap_or_default().contains("Credencial"));
    }
}
