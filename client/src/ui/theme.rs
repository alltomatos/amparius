//! Tokens visuais do cliente (frontend-spec §7.1). Nada copiado de tema externo.
//!
//! Cada par texto/fundo usado em chip ou cartão mantém contraste de texto
//! confortável (≥ 4.5:1) nos temas claro e escuro.

use eframe::egui::{self, Color32, Stroke};

use super::day_panel::Severity;

#[derive(Debug, Clone, Copy)]
pub struct Palette {
    pub brand_900: Color32,
    pub brand_500: Color32,
    pub surface: Color32,
    pub surface_alt: Color32,
    pub text: Color32,
    pub text_muted: Color32,
    pub border: Color32,
    pub success: Color32,
    pub success_bg: Color32,
    pub warning: Color32,
    pub warning_bg: Color32,
    pub danger: Color32,
    pub danger_bg: Color32,
    pub info: Color32,
    pub info_bg: Color32,
}

pub fn palette(dark_mode: bool) -> Palette {
    if dark_mode {
        Palette {
            brand_900: Color32::from_rgb(0x9C, 0xC7, 0xEE),
            brand_500: Color32::from_rgb(0x4D, 0xA3, 0xE8),
            surface: Color32::from_rgb(0x0F, 0x18, 0x21),
            surface_alt: Color32::from_rgb(0x18, 0x24, 0x2F),
            text: Color32::from_rgb(0xED, 0xF2, 0xF7),
            text_muted: Color32::from_rgb(0xB6, 0xC4, 0xD1),
            border: Color32::from_rgb(0x33, 0x42, 0x51),
            success: Color32::from_rgb(0x9B, 0xE3, 0xB8),
            success_bg: Color32::from_rgb(0x16, 0x30, 0x1F),
            warning: Color32::from_rgb(0xF5, 0xCF, 0x7A),
            warning_bg: Color32::from_rgb(0x32, 0x27, 0x0E),
            danger: Color32::from_rgb(0xFF, 0xA0, 0xA0),
            danger_bg: Color32::from_rgb(0x35, 0x1A, 0x1A),
            info: Color32::from_rgb(0xA9, 0xD2, 0xF5),
            info_bg: Color32::from_rgb(0x14, 0x28, 0x38),
        }
    } else {
        Palette {
            brand_900: Color32::from_rgb(0x00, 0x27, 0x4C),
            brand_500: Color32::from_rgb(0x00, 0x73, 0xCF),
            surface: Color32::from_rgb(0xFF, 0xFF, 0xFF),
            surface_alt: Color32::from_rgb(0xF2, 0xF5, 0xF9),
            text: Color32::from_rgb(0x14, 0x20, 0x2B),
            text_muted: Color32::from_rgb(0x46, 0x56, 0x66),
            border: Color32::from_rgb(0xC5, 0xD1, 0xDD),
            success: Color32::from_rgb(0x14, 0x5A, 0x30),
            success_bg: Color32::from_rgb(0xE4, 0xF4, 0xEA),
            warning: Color32::from_rgb(0x6E, 0x48, 0x00),
            warning_bg: Color32::from_rgb(0xFF, 0xF3, 0xD6),
            danger: Color32::from_rgb(0x92, 0x1B, 0x1B),
            danger_bg: Color32::from_rgb(0xFD, 0xE8, 0xE8),
            info: Color32::from_rgb(0x00, 0x45, 0x7A),
            info_bg: Color32::from_rgb(0xE6, 0xF0, 0xFA),
        }
    }
}

/// Par (texto, fundo) de um chip de estado. Cor nunca é o único sinal: o texto e
/// o símbolo acompanham sempre.
pub fn status_colors(severity: Severity, palette: &Palette) -> (Color32, Color32) {
    match severity {
        Severity::Neutral => (palette.text_muted, palette.surface_alt),
        Severity::Info => (palette.info, palette.info_bg),
        Severity::Success => (palette.success, palette.success_bg),
        Severity::Warning => (palette.warning, palette.warning_bg),
        Severity::Danger => (palette.danger, palette.danger_bg),
    }
}

/// Aplica tipografia, espaçamento e foco visível. As cores de superfície vêm do
/// tema do sistema; os tokens de marca são usados explicitamente nos widgets.
pub fn apply(ctx: &egui::Context) {
    for theme in [egui::Theme::Light, egui::Theme::Dark] {
        apply_theme(ctx, theme);
    }
}

fn apply_theme(ctx: &egui::Context, theme: egui::Theme) {
    let dark = matches!(theme, egui::Theme::Dark);
    let mut style = (*ctx.style_of(theme)).clone();
    style.spacing.item_spacing = egui::vec2(8.0, 8.0);
    style.spacing.button_padding = egui::vec2(14.0, 10.0);
    style.spacing.interact_size = egui::vec2(44.0, 44.0);
    style.interaction.selectable_labels = false;
    style
        .text_styles
        .insert(egui::TextStyle::Body, egui::FontId::proportional(16.0));
    style
        .text_styles
        .insert(egui::TextStyle::Button, egui::FontId::proportional(16.0));
    style
        .text_styles
        .insert(egui::TextStyle::Heading, egui::FontId::proportional(24.0));
    let focus = Stroke::new(2.0, palette(dark).brand_500);
    style.visuals.selection.stroke = focus;
    for widget in [
        &mut style.visuals.widgets.inactive,
        &mut style.visuals.widgets.hovered,
        &mut style.visuals.widgets.active,
        &mut style.visuals.widgets.open,
    ] {
        widget.bg_stroke.width = widget.bg_stroke.width.max(1.0);
    }
    ctx.set_style_of(theme, style);
}
