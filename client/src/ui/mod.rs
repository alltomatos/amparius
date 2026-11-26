//! Superfície gráfica do cliente de estação (ADR 0036).
//!
//! `day_panel` é o modelo puro; `render` desenha; `app` liga a fila persistida,
//! o transporte e a janela. Nenhum módulo aqui contém regra fiscal ou de dinheiro.

pub mod app;
pub mod attendance;
pub mod day_panel;
pub mod render;
pub mod theme;
