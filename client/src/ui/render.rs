//! Renderização do painel do dia em `egui`. Sem regra de negócio: só desenha o
//! `DayPanelView` e devolve intenções em `DayPanelActions`.

use eframe::egui::{self, Color32, RichText};
use std::collections::HashSet;

use super::attendance::{
    AttendanceAction, AttendanceForm, AttendanceServiceState, AttendanceStatus, AttendanceView,
    ServiceOrderStatus, ServiceOrderSyncState,
};
use super::day_panel::{DayPanelView, Notice, PanelPhase, Severity, StatusChip, SyncView};
use super::theme::{Palette, palette, status_colors};

#[derive(Debug, Default, Clone, PartialEq, Eq)]
pub struct DayPanelActions {
    pub sync: bool,
    pub reload: bool,
    pub set_connectivity: Option<crate::ConnectivityState>,
    pub open_attendance: bool,
}

#[derive(Debug, Default, Clone, PartialEq, Eq)]
pub struct AttendanceActions {
    pub back: bool,
    pub create_journey: bool,
    pub advance_order: bool,
    pub pause_order: bool,
    pub complete_order: bool,
}

pub fn render(ui: &mut egui::Ui, view: &DayPanelView, actions: &mut DayPanelActions) {
    let palette = palette(ui.visuals().dark_mode);
    ui.set_max_width(1200.0);
    egui::ScrollArea::vertical()
        .auto_shrink([false, false])
        .show(ui, |ui| {
            ui.add_space(4.0);
            header(ui, view, &palette, actions);
            ui.add_space(12.0);
            connectivity(ui, view, &palette, actions);
            ui.add_space(12.0);

            if let Some(notice) = &view.notice {
                notice_banner(ui, notice, &palette);
                ui.add_space(8.0);
            }

            if view.phase == PanelPhase::Error {
                fatal(ui, view, &palette, actions);
                footer(ui, view, &palette, actions);
                return;
            }

            counters(ui, view, &palette);
            ui.add_space(12.0);
            sync_area(ui, view, &palette, actions);
            ui.add_space(16.0);

            if view.empty {
                empty(ui, &palette, actions);
            } else {
                operations(ui, view, &palette);
                reviews(ui, view, &palette);
            }

            footer(ui, view, &palette, actions);
        });
}

fn header(
    ui: &mut egui::Ui,
    view: &DayPanelView,
    palette: &Palette,
    actions: &mut DayPanelActions,
) {
    ui.horizontal_wrapped(|ui| {
        ui.heading(RichText::new("Painel do dia").color(palette.brand_900));
        ui.label(
            RichText::new("CL-01 · estado da estação e fila offline")
                .small()
                .color(palette.text_muted),
        );
        if ui
            .add(egui::Button::new("Atendimento funeral").min_size(egui::vec2(190.0, 44.0)))
            .clicked()
        {
            actions.open_attendance = true;
        }
    });
    ui.add_space(4.0);
    ui.label(
        RichText::new(&view.status_line)
            .color(palette.text)
            .size(16.0),
    );
    ui.add_space(8.0);
    ui.horizontal_wrapped(|ui| {
        ui.label(RichText::new("Conexão:").color(palette.text_muted));
        for (state, label) in [
            (crate::ConnectivityState::Online, "Online"),
            (crate::ConnectivityState::Offline, "Offline"),
            (crate::ConnectivityState::Contingency, "Contingência"),
        ] {
            let selected = view.banner.state == state;
            let (fg, bg) = if selected {
                status_colors(Severity::Info, palette)
            } else {
                (palette.text_muted, palette.surface_alt)
            };
            let button = egui::Button::new(RichText::new(label).color(fg))
                .fill(bg)
                .min_size(egui::vec2(120.0, 44.0));
            if ui.add(button).clicked() && !selected {
                actions.set_connectivity = Some(state);
            }
        }
    });
}

pub fn render_attendance(
    ui: &mut egui::Ui,
    form: &mut AttendanceForm,
    view: &AttendanceView,
    actions: &mut AttendanceActions,
) {
    let palette = palette(ui.visuals().dark_mode);
    egui::ScrollArea::vertical()
        .auto_shrink([false, false])
        .show(ui, |ui| {
            ui.horizontal_wrapped(|ui| {
                ui.heading(RichText::new("Atendimento funeral").color(palette.brand_900));
                ui.label(
                        RichText::new("CL-02/CL-03 · jornada e ordens de serviço")
                        .small()
                        .color(palette.text_muted),
                );
            });
            ui.add_space(8.0);
            let (fg, bg) = match view.service_state {
                AttendanceServiceState::Online => status_colors(Severity::Success, &palette),
                AttendanceServiceState::Offline | AttendanceServiceState::Contingency => {
                    status_colors(Severity::Warning, &palette)
                }
                AttendanceServiceState::ServiceUnavailable
                | AttendanceServiceState::EntitlementBlocked => {
                    status_colors(Severity::Danger, &palette)
                }
            };
            panel_frame(bg, fg).show(ui, |ui| {
                ui.set_width(ui.available_width());
                ui.label(RichText::new(view.service_state.label()).strong().color(fg));
                ui.label(RichText::new(view.detail).color(palette.text));
            });
            ui.add_space(12.0);

            if let AttendanceStatus::Error { code, message } = &view.status {
                panel_frame(palette.danger_bg, palette.danger).show(ui, |ui| {
                    ui.set_width(ui.available_width());
                    ui.label(RichText::new(message).strong().color(palette.danger));
                    ui.label(
                        RichText::new(format!("Código: {code}"))
                            .monospace()
                            .small()
                            .color(palette.text_muted),
                    );
                });
                ui.add_space(8.0);
            }
            if let AttendanceStatus::Queued { operation_id, seq } = &view.status {
                panel_frame(palette.success_bg, palette.success).show(ui, |ui| {
                    ui.set_width(ui.available_width());
                    ui.label(
                        RichText::new("Enfileirada").strong().color(palette.success),
                    );
                    ui.label(
                        RichText::new(format!("Sequência local {seq} · operação {operation_id}"))
                            .monospace()
                            .small()
                            .color(palette.text_muted),
                    );
                    ui.label(
                        RichText::new("A sincronização não foi simulada; use o painel do dia quando o relay estiver disponível.")
                            .small()
                            .color(palette.text),
                    );
                });
                ui.add_space(8.0);
            }
            if let AttendanceStatus::Conflict { operation_id, code } = &view.status {
                panel_frame(palette.danger_bg, palette.danger).show(ui, |ui| {
                    ui.set_width(ui.available_width());
                    ui.label(RichText::new("Conflito; revisão necessária").strong().color(palette.danger));
                    ui.label(
                        RichText::new(format!("Operação {operation_id} · código {code}"))
                            .monospace()
                            .small()
                            .color(palette.text_muted),
                    );
                    ui.label(RichText::new("O estado local foi preservado; não houve last-write-wins.").small().color(palette.text));
                });
                ui.add_space(8.0);
            }

            service_orders(ui, form, view, &palette);
            ui.add_space(8.0);

            ui.label(RichText::new("Tipo de operação").strong().color(palette.text));
            ui.horizontal_wrapped(|ui| {
                for (action, label) in [
                    (AttendanceAction::CreateJourney, "Criar jornada"),
                    (AttendanceAction::AdvanceOrder, "Avançar ordem"),
                    (AttendanceAction::PauseOrder, "Pausar ordem"),
                    (AttendanceAction::CompleteOrder, "Concluir ordem"),
                ] {
                    ui.radio_value(&mut form.action, action, label);
                }
            });
            ui.add_space(8.0);

            match form.action {
                AttendanceAction::CreateJourney => journey_fields(ui, form, &palette),
                AttendanceAction::AdvanceOrder
                | AttendanceAction::PauseOrder
                | AttendanceAction::CompleteOrder => {
                    order_fields(ui, form, &palette)
                }
            }

            ui.add_space(12.0);
            ui.horizontal_wrapped(|ui| {
                if ui
                    .add(egui::Button::new("Voltar ao painel").min_size(egui::vec2(180.0, 44.0)))
                    .clicked()
                {
                    actions.back = true;
                }
                let submit_label = form.action.label();
                if ui
                    .add_enabled(
                        view.can_submit,
                        egui::Button::new(submit_label).min_size(egui::vec2(180.0, 44.0)),
                    )
                    .clicked()
                {
                    match form.action {
                        AttendanceAction::CreateJourney => actions.create_journey = true,
                        AttendanceAction::AdvanceOrder => actions.advance_order = true,
                        AttendanceAction::PauseOrder => actions.pause_order = true,
                        AttendanceAction::CompleteOrder => actions.complete_order = true,
                    }
                }
            });
            ui.add_space(8.0);
            ui.label(
                RichText::new("Use somente referências opacas. Nome, documento, contato e texto livre não são aceitos nem registrados em log.")
                    .small()
                    .color(palette.text_muted),
            );
        });
}

fn journey_fields(ui: &mut egui::Ui, form: &mut AttendanceForm, palette: &Palette) {
    field(
        ui,
        "Referência da parte (opcional)",
        &mut form.party_ref,
        palette,
    );
    field(ui, "Contrato active", &mut form.contract_ref, palette);
    ui.checkbox(
        &mut form.contract_active_confirmed,
        "Confirmo que o contrato está active no contexto autorizado",
    );
    field(ui, "Tipo de serviço", &mut form.service_kind, palette);
    field(ui, "Referência do local", &mut form.location_ref, palette);
    field(
        ui,
        "Referência do recurso (opcional)",
        &mut form.resource_ref,
        palette,
    );
    field(ui, "Início RFC3339", &mut form.starts_at, palette);
    field(ui, "Fim RFC3339", &mut form.ends_at, palette);
    field(ui, "Fuso IANA", &mut form.time_zone, palette);
}

fn order_fields(ui: &mut egui::Ui, form: &mut AttendanceForm, palette: &Palette) {
    field(
        ui,
        "Referência da ordem de serviço",
        &mut form.order_ref,
        palette,
    );
    field(ui, "Versão esperada", &mut form.order_version, palette);
}

fn service_orders(
    ui: &mut egui::Ui,
    form: &mut AttendanceForm,
    view: &AttendanceView,
    palette: &Palette,
) {
    ui.label(
        RichText::new("Ordens da jornada local")
            .strong()
            .color(palette.text),
    );
    if view.service_orders.is_empty() {
        ui.label(
            RichText::new("Nenhuma mutação de OS nesta estação; informe uma referência para iniciar a execução.")
                .small()
                .color(palette.text_muted),
        );
        return;
    }
    for order in &view.service_orders {
        let (fg, bg) = match order.sync_state {
            ServiceOrderSyncState::Queued => status_colors(Severity::Warning, palette),
            ServiceOrderSyncState::Done => status_colors(Severity::Success, palette),
            ServiceOrderSyncState::Error | ServiceOrderSyncState::Conflict => {
                status_colors(Severity::Danger, palette)
            }
        };
        panel_frame(palette.surface_alt, palette.border).show(ui, |ui| {
            ui.set_width(ui.available_width());
            ui.horizontal_wrapped(|ui| {
                ui.label(
                    RichText::new(format!("OS {}", order.order_id))
                        .monospace()
                        .strong()
                        .color(palette.text),
                );
                ui.label(
                    RichText::new(order.status.label())
                        .strong()
                        .color(palette.text),
                );
                chip(
                    ui,
                    &super::day_panel::StatusChip {
                        symbol: "•",
                        label: order.sync_state.label(),
                        severity: Severity::Info,
                    },
                    fg,
                    bg,
                );
            });
            ui.label(
                RichText::new(format!(
                    "Versão {} · seq {} · estado {}",
                    order.version,
                    order.seq,
                    order.sync_state.label()
                ))
                .small()
                .color(palette.text_muted),
            );
            if let Some(error) = &order.error_code {
                ui.label(
                    RichText::new(format!("Código: {error}"))
                        .monospace()
                        .small()
                        .color(palette.text_muted),
                );
            }
            if matches!(order.sync_state, ServiceOrderSyncState::Conflict) {
                ui.label(
                    RichText::new("Revisão necessária; a ordem não foi sobrescrita.")
                        .small()
                        .color(palette.danger),
                );
            }
            if ui
                .add(egui::Button::new("Selecionar ordem").min_size(egui::vec2(160.0, 44.0)))
                .clicked()
            {
                form.order_ref = order.order_id.clone();
                form.order_version = order.version.saturating_sub(1).to_string();
                form.action = match order.status {
                    ServiceOrderStatus::Open => AttendanceAction::AdvanceOrder,
                    ServiceOrderStatus::InProgress => AttendanceAction::PauseOrder,
                    ServiceOrderStatus::Paused => AttendanceAction::AdvanceOrder,
                    ServiceOrderStatus::Completed => AttendanceAction::CompleteOrder,
                };
            }
        });
        ui.add_space(6.0);
    }
}

fn field(ui: &mut egui::Ui, label: &str, value: &mut String, palette: &Palette) {
    ui.horizontal(|ui| {
        ui.label(RichText::new(label).color(palette.text));
        ui.add(
            egui::TextEdit::singleline(value)
                .desired_width(360.0)
                .hint_text("referência opaca"),
        );
    });
}

fn connectivity(
    ui: &mut egui::Ui,
    view: &DayPanelView,
    palette: &Palette,
    _actions: &mut DayPanelActions,
) {
    let banner = &view.banner;
    let (fg, bg) = status_colors(banner.severity, palette);
    panel_frame(bg, palette.brand_500).show(ui, |ui| {
        ui.set_width(ui.available_width());
        ui.horizontal_wrapped(|ui| {
            ui.label(RichText::new(banner.symbol).strong().color(fg));
            ui.label(RichText::new(banner.title).strong().color(fg));
            if banner.queue_available {
                ui.label(
                    RichText::new("fila local disponível")
                        .small()
                        .color(palette.text_muted),
                );
            }
        });
        ui.label(RichText::new(banner.detail).color(palette.text));
    });
}

fn notice_banner(ui: &mut egui::Ui, notice: &Notice, palette: &Palette) {
    let (fg, bg) = status_colors(notice.severity, palette);
    panel_frame(bg, fg).show(ui, |ui| {
        ui.set_width(ui.available_width());
        ui.label(RichText::new(&notice.text).color(fg));
    });
}

fn counters(ui: &mut egui::Ui, view: &DayPanelView, palette: &Palette) {
    ui.label(
        RichText::new("Fila desta estação")
            .strong()
            .color(palette.text),
    );
    ui.add_space(4.0);
    ui.horizontal_wrapped(|ui| {
        counter(
            ui,
            palette,
            "Pendentes",
            view.counters.pending,
            Severity::Info,
        );
        counter(
            ui,
            palette,
            "Novas tentativas",
            view.counters.retrying,
            Severity::Warning,
        );
        counter(
            ui,
            palette,
            "Conflitos",
            view.counters.conflict,
            Severity::Danger,
        );
        counter(
            ui,
            palette,
            "Falhas",
            view.counters.failed,
            Severity::Danger,
        );
        counter(
            ui,
            palette,
            "Sincronizadas",
            view.counters.synced,
            Severity::Success,
        );
        counter(
            ui,
            palette,
            "Revisões abertas",
            view.counters.review_open,
            Severity::Warning,
        );
    });
}

fn counter(ui: &mut egui::Ui, palette: &Palette, label: &str, value: usize, severity: Severity) {
    let (fg, bg) = status_colors(severity, palette);
    panel_frame(bg, palette.border).show(ui, |ui| {
        ui.vertical(|ui| {
            ui.label(RichText::new(label).small().color(palette.text_muted));
            ui.label(
                RichText::new(value.to_string())
                    .size(22.0)
                    .strong()
                    .color(fg),
            );
        });
    });
}

fn sync_area(
    ui: &mut egui::Ui,
    view: &DayPanelView,
    palette: &Palette,
    actions: &mut DayPanelActions,
) {
    sync_panel(ui, &view.sync, palette, actions);
}

fn sync_panel(
    ui: &mut egui::Ui,
    sync: &SyncView,
    palette: &Palette,
    actions: &mut DayPanelActions,
) {
    let (fg, _) = status_colors(sync.severity, palette);
    panel_frame(palette.surface_alt, palette.border).show(ui, |ui| {
        ui.set_width(ui.available_width());
        ui.horizontal_wrapped(|ui| {
            ui.label(RichText::new(&sync.label).strong().color(fg));
            if sync.running {
                ui.add(egui::Spinner::new().size(16.0));
            }
        });
        if let Some(detail) = &sync.detail {
            ui.label(RichText::new(detail).small().color(palette.text_muted));
        }
        ui.add_space(6.0);
        let button = egui::Button::new(if sync.running {
            "Sincronizando…"
        } else {
            "Sincronizar agora"
        })
        .min_size(egui::vec2(200.0, 44.0));
        if ui.add_enabled(sync.can_start, button).clicked() {
            actions.sync = true;
        }
    });
}

fn operations(ui: &mut egui::Ui, view: &DayPanelView, palette: &Palette) {
    ui.label(RichText::new("Operações").strong().color(palette.text));
    ui.add_space(4.0);
    for row in &view.operations {
        let (status_fg, status_bg) = status_colors(row.status.severity, palette);
        panel_frame(palette.surface_alt, palette.border).show(ui, |ui| {
            ui.set_width(ui.available_width());
            ui.horizontal_wrapped(|ui| {
                ui.label(
                    RichText::new(format!("#{}", row.seq))
                        .monospace()
                        .strong()
                        .color(palette.text_muted),
                );
                ui.label(RichText::new(row.kind).strong().color(palette.text));
                chip(ui, &row.status, status_fg, status_bg);
            });
            ui.label(
                RichText::new(format!("Referência: {}", row.entity_ref))
                    .monospace()
                    .small()
                    .color(palette.text_muted),
            );
            ui.label(
                RichText::new(format!(
                    "Criada em {} ({}) · tentativas: {}",
                    row.created_local, row.age, row.attempts
                ))
                .small()
                .color(palette.text_muted),
            );
            if let Some(code) = &row.error_code {
                ui.label(
                    RichText::new(format!("Código: {code}"))
                        .monospace()
                        .small()
                        .color(palette.text_muted),
                );
            }
            if row.needs_review {
                ui.label(
                    RichText::new("Requer revisão; o valor local foi preservado.")
                        .small()
                        .color(palette.danger),
                );
            }
        });
        ui.add_space(6.0);
    }
}

fn chip(ui: &mut egui::Ui, chip: &StatusChip, fg: Color32, bg: Color32) {
    egui::Frame::new()
        .fill(bg)
        .corner_radius(8)
        .inner_margin(egui::Margin::symmetric(10, 4))
        .show(ui, |ui| {
            ui.horizontal(|ui| {
                ui.label(RichText::new(chip.symbol).small().strong().color(fg));
                ui.label(RichText::new(chip.label).small().strong().color(fg));
            });
        });
}

fn reviews(ui: &mut egui::Ui, view: &DayPanelView, palette: &Palette) {
    if view.reviews.is_empty() {
        return;
    }
    ui.add_space(12.0);
    ui.label(
        RichText::new("Revisão necessária")
            .strong()
            .color(palette.text),
    );
    ui.add_space(4.0);
    for review in &view.reviews {
        let (fg, bg) = status_colors(review.severity, palette);
        panel_frame(bg, fg).show(ui, |ui| {
            ui.set_width(ui.available_width());
            ui.horizontal_wrapped(|ui| {
                ui.label(RichText::new("!").color(fg));
                ui.label(RichText::new(review.kind).strong().color(palette.text));
            });
            ui.label(
                RichText::new(format!(
                    "Operação {} · código {}",
                    review.operation_id, review.code
                ))
                .monospace()
                .small()
                .color(palette.text_muted),
            );
            ui.label(RichText::new(review.guidance).small().color(palette.text));
            let _ = &review.task_id;
        });
        ui.add_space(6.0);
    }
}

fn empty(ui: &mut egui::Ui, palette: &Palette, actions: &mut DayPanelActions) {
    panel_frame(palette.surface_alt, palette.border).show(ui, |ui| {
        ui.set_width(ui.available_width());
        ui.label(RichText::new("Nenhuma operação na fila desta estação.").strong().color(palette.text));
        ui.label(
            RichText::new("O painel fica pronto para receber contrato, caixa e emissão quando a operação começar.")
                .small()
                .color(palette.text_muted),
        );
        ui.add_space(6.0);
        if ui
            .add(egui::Button::new("Recarregar estado").min_size(egui::vec2(200.0, 44.0)))
            .clicked()
        {
            actions.reload = true;
        }
    });
}

fn fatal(
    ui: &mut egui::Ui,
    view: &DayPanelView,
    palette: &Palette,
    _actions: &mut DayPanelActions,
) {
    let Some(error) = &view.error else {
        return;
    };
    let (fg, bg) = status_colors(Severity::Danger, palette);
    let response = panel_frame(bg, fg).show(ui, |ui| {
        ui.set_width(ui.available_width());
        ui.label(
            RichText::new("Não foi possível abrir a fila local")
                .strong()
                .color(fg),
        );
        ui.label(RichText::new(&error.message).color(palette.text));
        ui.label(
            RichText::new(format!("Código: {}", error.code))
                .monospace()
                .small()
                .color(palette.text_muted),
        );
        if let Some(hint) = &error.hint {
            ui.label(RichText::new(hint).small().color(palette.text_muted));
        }
        ui.add_space(6.0);
        ui.add(egui::Button::new("Tentar novamente").min_size(egui::vec2(200.0, 44.0)))
    });

    // Ao aparecer (ou mudar o código), leva o foco ao botão de ação para que o
    // teclado e o leitor de tela encontrem a saída sem caçar na tela.
    let focus_key = ui.make_persistent_id(("cl01-error-focus", error.code.as_str()));
    let already = ui.data(|data| data.get_temp::<bool>(focus_key).unwrap_or(false));
    if !already {
        response.inner.request_focus();
        ui.data_mut(|data| data.insert_temp(focus_key, true));
    }
}

fn footer(
    ui: &mut egui::Ui,
    view: &DayPanelView,
    palette: &Palette,
    actions: &mut DayPanelActions,
) {
    ui.add_space(16.0);
    ui.separator();
    ui.horizontal_wrapped(|ui| {
        if ui
            .add(egui::Button::new("Atualizar").min_size(egui::vec2(120.0, 44.0)))
            .clicked()
        {
            actions.reload = true;
        }
        let last = view.last_sync.as_deref().map_or_else(
            || "Última sincronização: ainda não executada".to_owned(),
            |last| format!("Última sincronização: {last}"),
        );
        ui.label(RichText::new(last).small().color(palette.text_muted));
    });
}

fn panel_frame(fill: Color32, border: Color32) -> egui::Frame {
    egui::Frame::new()
        .fill(fill)
        .stroke(egui::Stroke::new(1.0, border))
        .corner_radius(8)
        .inner_margin(egui::Margin::symmetric(12, 10))
}

/// Conjunto de rótulos que devem existir na árvore de acessibilidade desta
/// tela; usado em teste para falhar se um bloco obrigatório desaparecer.
pub fn required_labels(view: &DayPanelView) -> HashSet<String> {
    let mut labels = HashSet::new();
    labels.insert("Painel do dia".to_owned());
    labels.insert(view.banner.title.to_owned());
    labels.insert(view.sync.label.clone());
    if let Some(error) = &view.error {
        labels.insert(error.code.clone());
    }
    labels
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::ui::day_panel::{PanelContext, PanelError, present};
    use crate::{ClientQueue, ConnectivityState};
    use egui_kittest::kittest::Queryable;

    fn empty_view() -> DayPanelView {
        let queue = ClientQueue::new("tenant-teste", "node-teste").expect("queue");
        present(PanelContext {
            queue: Some(&queue),
            phase: PanelPhase::Ready,
            error: None,
            notice: None,
            sync: SyncView::idle(0, true),
            last_sync_at: None,
            now: 10,
        })
    }

    #[test]
    fn renders_empty_panel_and_exposes_required_labels() {
        let view = empty_view();
        let mut harness = egui_kittest::Harness::new_ui(move |ui| {
            let mut actions = DayPanelActions::default();
            render(ui, &view, &mut actions);
        });
        harness.run();
        harness.get_by_label("Painel do dia");
        harness.get_by_label("Nenhuma operação na fila desta estação.");
        harness.get_by_label("Sincronizar agora");
        let required = required_labels(&empty_view());
        assert!(required.contains("Painel do dia"));
    }

    #[test]
    fn renders_conflict_and_review_without_payload_text() {
        let view = conflict_view();
        let text = view.visible_text().join("\n");
        assert!(!text.contains("total_cents"));
        let mut harness = egui_kittest::Harness::new_ui(move |ui| {
            let mut actions = DayPanelActions::default();
            render(ui, &view, &mut actions);
        });
        harness.run();
        harness.get_by_label("Conflito");
        harness.get_by_label("Revisão necessária");
        harness.get_by_label("Requer revisão; o valor local foi preservado.");
    }

    fn conflict_view() -> DayPanelView {
        let mut queue = ClientQueue::new("tenant-teste", "node-teste").expect("queue");
        queue
            .set_connectivity(ConnectivityState::Offline)
            .expect("offline");
        let draft = crate::OperationDraft {
            kind: crate::OperationKind::ContractCreate,
            entity_id: "contract-9".to_owned(),
            idempotency_key: "op-9".to_owned(),
            payload: serde_json::json!({"total_cents": 100, "currency": "BRL"}),
        };
        let operation = queue.enqueue("operator-1", draft, 100).expect("enqueue");
        queue
            .reconcile(
                &operation.operation.operation_id,
                crate::ReconciliationOutcome::Conflict {
                    code: "server_version_conflict".to_owned(),
                },
                110,
            )
            .expect("conflict");
        present(PanelContext {
            queue: Some(&queue),
            phase: PanelPhase::Ready,
            error: None,
            notice: None,
            sync: SyncView::idle(0, true),
            last_sync_at: None,
            now: 200,
        })
    }

    #[test]
    fn fatal_view_is_reachable_and_has_action() {
        let view = DayPanelView::fatal(
            PanelError::new(
                "corrupt_store",
                "A fila local falhou.",
                Some("Acione o suporte."),
            ),
            ConnectivityState::Offline,
        );
        let mut harness = egui_kittest::Harness::new_ui(move |ui| {
            let mut actions = DayPanelActions::default();
            render(ui, &view, &mut actions);
        });
        harness.run();
        harness.get_by_label("Não foi possível abrir a fila local");
        harness.get_by_label("Tentar novamente");
    }

    #[test]
    fn attendance_blocked_state_is_visible_without_success_action() {
        let view = AttendanceView::new(
            crate::ConnectivityState::Online,
            crate::ui::attendance::EntitlementState::Blocked,
            true,
            true,
            crate::ui::attendance::AttendanceStatus::Ready,
            Vec::new(),
        );
        let mut form = crate::ui::attendance::AttendanceForm::default();
        let mut harness = egui_kittest::Harness::new_ui(move |ui| {
            let mut actions = AttendanceActions::default();
            render_attendance(ui, &mut form, &view, &mut actions);
        });
        harness.run();
        harness.get_by_label("Atendimento funeral");
        harness.get_by_label("Módulo bloqueado");
        harness.get_by_label("Voltar ao painel");
    }

    #[test]
    fn attendance_queued_state_exposes_sequence_and_not_fake_sync() {
        let view = AttendanceView::new(
            crate::ConnectivityState::Offline,
            crate::ui::attendance::EntitlementState::Active,
            false,
            true,
            crate::ui::attendance::AttendanceStatus::Queued {
                operation_id: "op_opaque".to_owned(),
                seq: 7,
            },
            Vec::new(),
        );
        let mut form = crate::ui::attendance::AttendanceForm::default();
        let mut harness = egui_kittest::Harness::new_ui(move |ui| {
            let mut actions = AttendanceActions::default();
            render_attendance(ui, &mut form, &view, &mut actions);
        });
        harness.run();
        harness.get_by_label("Enfileirada");
        harness.get_by_label("Sequência local 7 · operação op_opaque");
    }

    #[test]
    fn service_order_cards_expose_status_version_and_conflict_without_payload() {
        let view = AttendanceView::new(
            crate::ConnectivityState::Offline,
            crate::ui::attendance::EntitlementState::Active,
            false,
            true,
            crate::ui::attendance::AttendanceStatus::Ready,
            vec![crate::ui::attendance::ServiceOrderCard {
                order_id: "order-1".to_owned(),
                status: crate::ui::attendance::ServiceOrderStatus::Paused,
                version: 2,
                sync_state: crate::ui::attendance::ServiceOrderSyncState::Conflict,
                operation_id: "op-opaque".to_owned(),
                seq: 4,
                error_code: Some("conflict:CONFLICT_UNREVIEWED".to_owned()),
            }],
        );
        let mut form = crate::ui::attendance::AttendanceForm::default();
        let mut harness = egui_kittest::Harness::new_ui(move |ui| {
            let mut actions = AttendanceActions::default();
            render_attendance(ui, &mut form, &view, &mut actions);
        });
        harness.run();
        harness.get_by_label("Ordens da jornada local");
        harness.get_by_label("OS order-1");
        harness.get_by_label("paused");
        harness.get_by_label("Versão 2 · seq 4 · estado conflict");
        harness.get_by_label("Revisão necessária; a ordem não foi sobrescrita.");
    }
}
