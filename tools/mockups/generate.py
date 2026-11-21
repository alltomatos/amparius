#!/usr/bin/env python3
"""Gera os mockups SVG do Amparius (telas + assistente contextual).

Uso: python tools/mockups/generate.py
Saída: mockups/*.svg e mockups/index.html
"""

import html
import os

W, H = 1440, 900
SW = 200
RW = 340
RAIL_X = W - RW
CX = SW + 24
CW = RAIL_X - CX - 24

NAVY = "#00274C"
BLUE = "#0073CF"
BLUE_L = "#3898EC"
BLUE_SOFT = "#e8f1fa"
BG = "#f3f6fa"
PANEL = "#ffffff"
BORDER = "#dbe4ee"
INK = "#0d1b2a"
MUTED = "#56697c"
FAINT = "#8a99a8"
OK = "#0f7a4d"
OK_BG = "#e6f4ec"
WARN = "#a25a00"
WARN_BG = "#fdf3e3"
DANGER = "#b3261e"
DANGER_BG = "#fdecea"
CHIP = "#eef3f9"
FONT = "'Segoe UI', system-ui, -apple-system, 'Helvetica Neue', Arial, sans-serif"

OUT = os.path.join(os.path.dirname(__file__), "..", "..", "mockups")
os.makedirs(OUT, exist_ok=True)

ICON_D = [
    ("M 32 76 C 32 34, 68 8, 112 12 C 138 14, 158 28, 166 44 C 145 34, 118 32, 92 40 C 66 48, 46 62, 32 76 Z", "light"),
    ("M 105 46 L 124 46 L 156 154 C 146 156, 134 152, 127 142 L 105 78 Z", "light"),
    ("M 136 28 C 104 16, 72 34, 58 68 L 40 112 L 62 112 L 86 58 C 96 38, 116 30, 136 28 Z", "dark"),
    ("M 24 64 C 10 92, 16 132, 42 152 C 56 162, 74 160, 92 148 L 148 106 C 155 100, 161 93, 165 86 L 174 92 L 174 62 L 146 72 L 154 78 C 144 90, 128 102, 108 112 C 94 119, 80 120, 68 116 C 74 111, 86 108, 98 108 C 104 108, 107 103, 102 100 C 88 95, 70 100, 56 112 C 44 122, 36 120, 32 106 C 28 92, 32 76, 40 62 C 34 62, 28 63, 24 64 Z", "dark"),
    ("M 152 72 L 175 61 L 173 88 L 165 82 C 158 92, 146 102, 134 110 C 145 98, 155 86, 159 77 Z", "light"),
]


def e(value):
    return html.escape(str(value), quote=True)


def rect(x, y, w, h, fill=PANEL, stroke=None, rx=0, sw=1, opacity=None):
    parts = [f'x="{x}" y="{y}" width="{w}" height="{h}" fill="{fill}"']
    if rx:
        parts.append(f'rx="{rx}"')
    if stroke:
        parts.append(f'stroke="{stroke}" stroke-width="{sw}"')
    if opacity is not None:
        parts.append(f'opacity="{opacity}"')
    return f'<rect {" ".join(parts)}/>'


def line(x1, y1, x2, y2, stroke=BORDER, sw=1, dash=None, opacity=None):
    extra = ""
    if dash:
        extra += f' stroke-dasharray="{dash}"'
    if opacity is not None:
        extra += f' opacity="{opacity}"'
    return f'<line x1="{x1}" y1="{y1}" x2="{x2}" y2="{y2}" stroke="{stroke}" stroke-width="{sw}"{extra}/>'


def circle(cx, cy, r, fill=BLUE, stroke=None, sw=1, opacity=None):
    parts = [f'cx="{cx}" cy="{cy}" r="{r}" fill="{fill}"']
    if stroke:
        parts.append(f'stroke="{stroke}" stroke-width="{sw}"')
    if opacity is not None:
        parts.append(f'opacity="{opacity}"')
    return f'<circle {" ".join(parts)}/>'


def text(x, y, value, size=13, fill=INK, weight=400, anchor="start", ls=None, opacity=None):
    extra = ""
    if ls:
        extra += f' letter-spacing="{ls}"'
    if opacity is not None:
        extra += f' opacity="{opacity}"'
    return (f'<text x="{x}" y="{y}" font-family="{FONT}" font-size="{size}" fill="{fill}" '
            f'font-weight="{weight}" text-anchor="{anchor}"{extra}>{e(value)}</text>')


def lines(x, y, values, size=13, fill=INK, weight=400, lh=None, anchor="start"):
    lh = lh or int(size * 1.45)
    return "".join(text(x, y + i * lh, v, size, fill, weight, anchor) for i, v in enumerate(values))


def pill(x, y, label, fg=MUTED, bg=CHIP, h=22, size=11, pad=9, border=None):
    w = int(len(label) * size * 0.56) + pad * 2
    stroke = border or bg
    out = rect(x, y, w, h, bg, stroke, rx=h / 2)
    out += text(x + w / 2, y + h / 2 + size * 0.36, label, size, fg, 600, "middle")
    return out, w


def button(x, y, w, h, label, primary=True, size=12):
    if primary:
        out = rect(x, y, w, h, NAVY, rx=8)
        out += text(x + w / 2, y + h / 2 + size * 0.36, label, size, "#ffffff", 600, "middle")
    else:
        out = rect(x, y, w, h, PANEL, BORDER, rx=8)
        out += text(x + w / 2, y + h / 2 + size * 0.36, label, size, INK, 600, "middle")
    return out


def logo(x, y, s, dark, light):
    k = s / 200.0
    out = f'<g transform="translate({x},{y}) scale({k})"><g transform="translate(5,15)">'
    for d, kind in ICON_D:
        color = light if kind == "light" else dark
        out += f'<path d="{d}" fill="{color}"/>'
    out += "</g></g>"
    return out


def icon(kind, x, y, color):
    if kind == "grid":
        out = ""
        for dx, dy in ((0, 0), (8, 0), (0, 8), (8, 8)):
            out += rect(x + dx, y + dy, 6, 6, color, rx=1.5)
        return out
    if kind == "doc":
        return rect(x, y, 12, 14, "none", color, rx=2) + line(x + 3, y + 4, x + 9, y + 4, color) + line(x + 3, y + 7, x + 9, y + 7, color)
    if kind == "users":
        return circle(x + 5, y + 4, 3, "none", color) + f'<path d="M {x} {y+14} C {x} {y+9}, {x+10} {y+9}, {x+10} {y+14}" fill="none" stroke="{color}" stroke-width="1.4"/>'
    if kind == "map":
        return rect(x, y, 14, 14, "none", color, rx=2) + line(x + 7, y + 1, x + 7, y + 13, color) + line(x + 1, y + 7, x + 13, y + 7, color)
    if kind == "flower":
        out = circle(x + 7, y + 7, 2, color)
        for dx, dy in ((0, -5), (0, 5), (-5, 0), (5, 0)):
            out += circle(x + 7 + dx, y + 7 + dy, 2.2, "none", color)
        return out
    if kind == "wallet":
        return rect(x, y + 2, 14, 10, "none", color, rx=2) + line(x + 9, y + 7, x + 13, y + 7, color)
    if kind == "receipt":
        return rect(x + 1, y, 12, 14, "none", color, rx=1) + line(x + 4, y + 4, x + 10, y + 4, color) + line(x + 4, y + 7, x + 10, y + 7, color) + line(x + 4, y + 10, x + 8, y + 10, color)
    if kind == "chart":
        return rect(x + 1, y + 8, 3, 6, color) + rect(x + 6, y + 4, 3, 10, color) + rect(x + 11, y + 1, 3, 13, color)
    return ""


NAV = [
    ("grid", "Painel do dia", "painel"),
    ("doc", "Atendimento", "atendimento"),
    ("users", "Contratos", "contratos"),
    ("map", "Cemitério", "cemiterio"),
    ("flower", "Flores", "flores"),
    ("wallet", "Financeiro", "financeiro"),
    ("receipt", "Fiscal", "fiscal"),
    ("chart", "Relatórios", "relatorios"),
]


def sidebar(active, user="Maria Souza", role="Atendente"):
    out = rect(0, 0, SW, H, NAVY)
    out += logo(18, 18, 30, "#ffffff", "#7cc0f8")
    out += text(58, 34, "amparius", 17, "#ffffff", 700, ls="-0.4")
    out += text(58, 48, "gestão funerária", 9, "#9fc3e6", 500, ls="0.4")

    y = 88
    for kind, label, key in NAV:
        is_active = key == active
        if is_active:
            out += rect(8, y - 19, SW - 16, 38, "#ffffff", opacity=0.08, rx=8)
            out += rect(8, y - 11, 3, 22, BLUE_L, rx=2)
        color = "#ffffff" if is_active else "#b9cbe0"
        out += icon(kind, 22, y - 8, color)
        out += text(48, y + 4, label, 13, color, 600 if is_active else 500)
        y += 42

    out += rect(12, 744, SW - 24, 58, "#ffffff", opacity=0.06, rx=10)
    out += text(24, 764, "UNIDADE", 9, "#8fb4d8", 700, ls="0.8")
    out += text(24, 784, "Centro · Fortaleza", 12, "#ffffff", 600)
    out += text(24, 797, "Servidor da loja", 10, "#9fc3e6")

    out += circle(30, 840, 14, BLUE_L)
    out += text(30, 845, "MS", 11, "#ffffff", 700, "middle")
    out += text(52, 836, user, 12, "#ffffff", 600)
    out += text(52, 850, role, 10, "#9fc3e6")
    return out


def topbar(title, crumb, status=("Online", OK, OK_BG), unit="Centro", date="sábado, 21 de novembro"):
    out = rect(SW, 0, RAIL_X - SW, 64, PANEL)
    out += line(SW, 64, RAIL_X, 64)
    out += text(CX, 32, title, 19, INK, 700, ls="-0.3")
    out += text(CX, 50, crumb, 11.5, FAINT)

    right = RAIL_X - 16
    out += circle(right - 14, 32, 15, NAVY)
    out += text(right - 14, 36, "MS", 11, "#ffffff", 700, "middle")

    label, fg, bg = status
    w = int(len(label) * 11 * 0.56) + 18
    sx = right - 29 - 12 - w
    p, _ = pill(sx, 21, label, fg, bg, border=bg)
    out += p
    out += text(sx - 14, 36, unit, 11.5, MUTED, 500, "end")
    out += text(sx - 14 - 76, 36, date, 11.5, FAINT, 400, "end")
    return out


def rail(context, role, mode, messages, suggestions, note):
    out = rect(RAIL_X, 0, RW, H, "#f8fbff")
    out += line(RAIL_X, 0, RAIL_X, H, BORDER)

    out += rect(RAIL_X, 0, RW, 60, PANEL)
    out += line(RAIL_X, 60, W, 60)
    out += logo(RAIL_X + 16, 16, 26, NAVY, BLUE)
    out += text(RAIL_X + 52, 28, "Assistente", 15, INK, 700)
    out += text(RAIL_X + 52, 45, "Ajuda nesta tela", 10.5, FAINT)
    bw, _ = pill(W - 24 - 28, 20, "IA", BLUE, BLUE_SOFT, h=20, size=10, border=BLUE_SOFT)
    out += bw

    cy = 76
    out += rect(RAIL_X + 16, cy, RW - 32, 66, PANEL, BORDER, rx=10)
    out += text(RAIL_X + 28, cy + 18, "VOCÊ ESTÁ EM", 9, FAINT, 700, ls="0.9")
    out += text(RAIL_X + 28, cy + 36, context, 12.5, INK, 700)
    x = RAIL_X + 28
    p, pw = pill(x, cy + 44, role, BLUE, BLUE_SOFT, h=18, size=10, border=BLUE_SOFT)
    out += p
    p2, _ = pill(x + pw + 6, cy + 44, mode, MUTED, CHIP, h=18, size=10)
    out += p2

    y = cy + 84
    for who, body in messages:
        if who == "agent":
            bw_ = 268
            hgt = 18 + len(body) * 18
            out += circle(RAIL_X + 24, y + 12, 8, BLUE_SOFT)
            out += text(RAIL_X + 24, y + 16, "A", 9, BLUE, 700, "middle")
            out += rect(RAIL_X + 40, y, bw_, hgt, PANEL, BORDER, rx=10)
            out += lines(RAIL_X + 54, y + 22, body, 11.5, INK, 400, lh=17)
            y += hgt + 12
        else:
            bw_ = 250
            hgt = 18 + len(body) * 18
            out += rect(W - 24 - bw_, y, bw_, hgt, NAVY, rx=10)
            out += lines(W - 24 - bw_ + 14, y + 22, body, 11.5, "#ffffff", 400, lh=17)
            y += hgt + 12

    sy = 690
    out += text(RAIL_X + 16, sy, "PERGUNTAS SUGERIDAS", 9, FAINT, 700, ls="0.9")
    sy += 12
    x = RAIL_X + 16
    for label in suggestions:
        p, pw = pill(x, sy, label, NAVY, CHIP, h=24, size=10.5, border=BORDER)
        if x + pw > W - 16:
            x = RAIL_X + 16
            sy += 30
            p, pw = pill(x, sy, label, NAVY, CHIP, h=24, size=10.5, border=BORDER)
        out += p
        x += pw + 6

    iy = 796
    out += rect(RAIL_X + 16, iy, RW - 32, 44, PANEL, BORDER, rx=12)
    out += text(RAIL_X + 30, iy + 27, "Pergunte sobre esta tela…", 11.5, FAINT)
    out += circle(W - 40, iy + 22, 14, NAVY)
    out += f'<path d="M {W-46} {iy+22} l 11 0 M {W-39} {iy+18} l 4 4 -4 4" fill="none" stroke="#ffffff" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/>'
    out += text(RAIL_X + 16, 866, note, 9.5, FAINT)
    return out


def kpi(x, y, label, value, detail, w=201, accent=BLUE):
    out = rect(x, y, w, 92, PANEL, BORDER, rx=12)
    out += rect(x, y, 4, 92, accent, rx=2)
    out += text(x + 16, y + 24, label, 10.5, MUTED, 600)
    out += text(x + 16, y + 54, value, 24, INK, 700, ls="-0.6")
    out += text(x + 16, y + 76, detail, 10.5, FAINT)
    return out


def screen_painel():
    b = rect(0, 0, W, H, BG)
    b += sidebar("painel")
    b += topbar("Painel do dia", "Início · Unidade Centro")

    metrics = [
        ("Contratos hoje", "7", "2 desde as 8h", BLUE),
        ("A receber hoje", "R$ 12.480", "9 títulos · 3 vencidos", OK),
        ("Emissões pendentes", "2", "1 rejeitada · 1 na fila", WARN),
        ("Jazigos livres", "38", "de 120 na quadra C", NAVY),
    ]
    x = CX
    for label, value, detail, accent in metrics:
        b += kpi(x, 88, label, value, detail, accent=accent)
        x += 201 + 16

    ax, ay, aw, ah = CX, 196, 520, 430
    b += rect(ax, ay, aw, ah, PANEL, BORDER, rx=12)
    b += text(ax + 16, ay + 28, "Agenda de hoje", 14, INK, 700)
    b += text(ax + 16, ay + 46, "5 compromissos · 2 em andamento", 10.5, FAINT)
    b += button(ax + aw - 100, ay + 16, 84, 28, "Ver semana", primary=False, size=11)

    items = [
        ("08:00", "Velório · Sala A", "Família Ribeiro · contrato #4821", "Em andamento", WARN, WARN_BG),
        ("10:30", "Sepultamento · C-019", "Cemitério Jardim da Paz", "Confirmado", OK, OK_BG),
        ("13:00", "Cremação · Unidade Norte", "Família Alves · traslado", "Agendado", BLUE, BLUE_SOFT),
        ("15:30", "Reunião de plano", "Sr. Carlos · pré-need", "Agendado", BLUE, BLUE_SOFT),
        ("17:00", "Fechamento de caixa", "Maria Souza", "Pendente", MUTED, CHIP),
    ]
    y = ay + 70
    for hour, title, sub, status, fg, bg in items:
        b += text(ax + 16, y + 14, hour, 11, FAINT, 600)
        b += rect(ax + 66, y, 2, 44, BORDER)
        b += text(ax + 84, y + 14, title, 12.5, INK, 600)
        b += text(ax + 84, y + 31, sub, 10.5, FAINT)
        p, _ = pill(ax + aw - 16 - 86, y + 8, status, fg, bg, h=20, size=10)
        b += p
        y += 66

    px, py, pw_, ph = 760, 196, 316, 430
    b += rect(px, py, pw_, ph, PANEL, BORDER, rx=12)
    b += text(px + 16, py + 28, "Precisa de atenção", 14, INK, 700)
    b += text(px + 16, py + 46, "4 itens", 10.5, FAINT)

    alerts = [
        ("2 notas rejeitadas", "Motivo fiscal · desde 09:12", DANGER, DANGER_BG),
        ("3 títulos vencidos", "R$ 4.860 · cobrar hoje", WARN, WARN_BG),
        ("1 contrato sem assinatura", "Família Alves · 2 h", WARN, WARN_BG),
        ("Fila de sincronização", "0 operações · saudável", OK, OK_BG),
    ]
    y = py + 70
    for title, sub, fg, bg in alerts:
        b += circle(px + 24, y + 12, 5, fg)
        b += text(px + 40, y + 10, title, 12, INK, 600)
        b += text(px + 40, y + 26, sub, 10.5, FAINT)
        y += 62

    fy = 642
    b += rect(CX, fy, CW, 58, BLUE_SOFT, "#cfe2f5", rx=10)
    b += circle(CX + 22, fy + 29, 5, OK)
    b += text(CX + 38, fy + 24, "Servidor da loja conectado", 12, NAVY, 700)
    b += text(CX + 38, fy + 40, "Última sincronização há 2 min · fila 0 · base local íntegra", 10.5, MUTED)
    b += text(CX + CW - 16, fy + 33, "Ver diagnóstico", 11, BLUE, 600, "end")

    ry = 716
    b += rect(CX, ry, CW, 144, PANEL, BORDER, rx=12)
    b += text(CX + 16, ry + 28, "Resumo do mês", 14, INK, 700)
    b += text(CX + 16, ry + 46, "Novembro · até hoje", 10.5, FAINT)
    facts = [("Faturamento", "R$ 184.320", "+8% vs. outubro"), ("Recebido", "R$ 156.900", "85% do total"), ("Em aberto", "R$ 27.420", "12 títulos")]
    x = CX + 24
    for label, value, detail in facts:
        b += text(x, ry + 78, label, 10.5, MUTED, 600)
        b += text(x, ry + 104, value, 20, INK, 700)
        b += text(x, ry + 122, detail, 10, FAINT)
        x += 210
    b += button(CX + CW - 132, ry + 84, 116, 32, "Abrir relatório", primary=False, size=11.5)

    b += rail(
        "Painel do dia",
        "Atendente",
        "Online",
        [
            ("user", ["O que precisa da minha", "atenção agora?"]),
            ("agent", ["2 notas rejeitadas e 3 títulos", "vencidos. A nota da família Alves", "é a mais urgente: rejeição de", "código de serviço."]),
            ("user", ["Me mostre a nota."]),
            ("agent", ["Posso abrir a nota 000412 na tela", "de Fiscal. Você tem permissão", "para corrigir o código."]),
        ],
        ["Abrir nota 000412", "Cobrar vencidos", "Resumo do caixa"],
        "Respostas limitadas ao perfil Atendente · dados desta unidade",
    )
    return b


def field(x, y, w, label, value, hint=None, h=40):
    out = text(x, y - 8, label, 10, MUTED, 600)
    out += rect(x, y, w, h, PANEL, BORDER, rx=8)
    out += text(x + 12, y + 25, value, 12, INK if value else FAINT)
    if hint:
        out += text(x + w - 10, y + 25, hint, 9.5, FAINT, 400, "end")
    return out


def screen_atendimento():
    b = rect(0, 0, W, H, BG)
    b += sidebar("atendimento")
    b += topbar("Novo contrato", "Atendimento · Contratos · Novo", status=("Offline · fila 2", WARN, WARN_BG))

    steps = [("1", "Família", True), ("2", "Serviço", True), ("3", "Pagamento", False), ("4", "Confirmar", False)]
    x = CX
    for num, label, done in steps:
        color = BLUE if done else FAINT
        b += circle(x + 12, 108, 12, BLUE_SOFT if done else "#ffffff", color)
        b += text(x + 12, 112, num, 10.5, color, 700, "middle")
        b += text(x + 32, 112, label, 12, INK if done else MUTED, 600)
        if label != "Confirmar":
            b += line(x + 92, 108, x + 182, 108, BORDER, dash="3 3")
        x += 200

    b += rect(CX, 136, CW, 40, WARN_BG, "#f2ddbd", rx=8)
    b += circle(CX + 18, 156, 4.5, WARN)
    b += text(CX + 32, 161, "Operando no servidor da loja. O contrato vale agora; a nota entra na fila e será emitida ao reconectar.", 11.5, WARN)
    b += text(CX + CW - 14, 161, "2 operações na fila", 10.5, WARN, 600, "end")

    fx, fy, fw, fh = CX, 192, 520, 598
    b += rect(fx, fy, fw, fh, PANEL, BORDER, rx=12)
    b += text(fx + 16, fy + 28, "Dados do atendimento", 14, INK, 700)
    b += text(fx + 16, fy + 46, "Campos com * são obrigatórios", 10.5, FAINT)

    b += field(fx + 16, fy + 78, fw - 32, "Responsável pela família *", "Ana Paula Ribeiro", "buscar")
    b += field(fx + 16, fy + 138, 240, "Telefone *", "(85) 98812-4477")
    b += field(fx + 272, fy + 138, fw - 288, "Parentesco", "Filha")
    b += field(fx + 16, fy + 198, fw - 32, "Falecido *", "João Ribeiro dos Santos")
    b += field(fx + 16, fy + 258, 240, "Data do óbito *", "21/11/2026")
    b += field(fx + 272, fy + 258, fw - 288, "Hora", "06:40")

    b += text(fx + 16, fy + 322, "Serviço", 10, MUTED, 600)
    b += rect(fx + 16, fy + 330, fw - 32, 44, PANEL, BORDER, rx=8)
    b += text(fx + 28, fy + 357, "Plano Essencial + traslado", 12.5, INK, 600)
    b += text(fx + fw - 28, fy + 357, "alterar", 10.5, BLUE, 600, "end")

    b += text(fx + 16, fy + 398, "Adicionais", 10, MUTED, 600)
    x = fx + 16
    for label in ["Coroa de flores", "Sala VIP", "Documentação", "Cremação"]:
        p, pw = pill(x, fy + 406, label, NAVY, CHIP, h=24, size=10.5, border=BORDER)
        b += p
        x += pw + 6

    b += text(fx + 16, fy + 462, "Observações", 10, MUTED, 600)
    b += rect(fx + 16, fy + 470, fw - 32, 64, PANEL, BORDER, rx=8)
    b += text(fx + 28, fy + 492, "Família pediu discrição; velório na Sala A.", 11.5, INK)
    b += text(fx + 28, fy + 512, "Autorização de cremação assinada em papel.", 11.5, MUTED)

    b += button(fx + fw - 148, fy + fh - 48, 132, 34, "Salvar e avançar", primary=True, size=11.5)
    b += button(fx + fw - 282, fy + fh - 48, 122, 34, "Salvar rascunho", primary=False, size=11.5)

    sx, sy, sw_, sh = 760, 192, 316, 268
    b += rect(sx, sy, sw_, sh, PANEL, BORDER, rx=12)
    b += text(sx + 16, sy + 28, "Resumo", 14, INK, 700)
    b += text(sx + 16, sy + 46, "Plano Essencial", 10.5, FAINT)
    rows = [("Plano", "R$ 4.800,00"), ("Traslado", "R$ 650,00"), ("Taxa municipal", "R$ 120,00")]
    y = sy + 78
    for label, value in rows:
        b += text(sx + 16, y, label, 11.5, MUTED)
        b += text(sx + sw_ - 16, y, value, 11.5, INK, 600, "end")
        y += 26
    b += line(sx + 16, y + 4, sx + sw_ - 16, y + 4)
    b += text(sx + 16, y + 30, "Total", 12.5, INK, 700)
    b += text(sx + sw_ - 16, y + 30, "R$ 5.570,00", 16, NAVY, 700, "end")
    b += text(sx + 16, y + 52, "Tributos calculados na emissão fiscal.", 9.5, FAINT)

    px, py, pw_, ph = 760, 476, 316, 314
    b += rect(px, py, pw_, ph, PANEL, BORDER, rx=12)
    b += text(px + 16, py + 28, "Pagamento", 14, INK, 700)
    b += field(px + 16, py + 62, pw_ - 32, "Entrada", "R$ 1.570,00")
    b += field(px + 16, py + 122, pw_ - 32, "Saldo", "3x de R$ 1.333,33")
    b += text(px + 16, py + 182, "Forma", 10, MUTED, 600)
    x = px + 16
    for label, active in [("PIX", True), ("Cartão", False), ("Dinheiro", False)]:
        p, pw2 = pill(x, py + 190, label, "#ffffff" if active else NAVY, NAVY if active else CHIP, h=26, size=10.5, border=BORDER)
        b += p
        x += pw2 + 6
    b += rect(px + 16, py + 232, pw_ - 32, 34, BLUE_SOFT, "#cfe2f5", rx=8)
    b += text(px + 28, py + 253, "Emitir nota após confirmação", 11, NAVY, 600)
    b += circle(px + pw_ - 30, py + 249, 7, OK)
    b += f'<path d="M {px+pw_-34} {py+249} l 3 3 5 -6" fill="none" stroke="#ffffff" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/>'

    b += rail(
        "Atendimento · Contrato novo",
        "Atendente",
        "Offline",
        [
            ("user", ["A família escolheu cremação.", "O que falta?"]),
            ("agent", ["Faltam dois itens: documento", "com foto do responsável e a", "autorização de cremação assinada.", "Os demais campos estão completos."]),
            ("user", ["Posso emitir a nota agora?"]),
            ("agent", ["Sim, mas ela ficará na fila até a", "conexão voltar. O contrato e o", "recibo local valem imediatamente."]),
        ],
        ["Ver checklist", "Anexar documentos", "Calcular taxa"],
        "Respostas limitadas ao perfil Atendente · modo offline",
    )
    return b


def plot_cell(x, y, code, status, selected=False):
    fills = {
        "livre": (PANEL, BORDER),
        "ocupado": ("#eaf2fb", "#bcd4ec"),
        "reservado": ("#e3f1fb", BLUE),
        "manutencao": (WARN_BG, "#f0d6ad"),
        "vencendo": (WARN_BG, WARN),
    }
    fill, stroke = fills[status]
    sw = 2 if selected else 1
    out = rect(x, y, 96, 58, fill, stroke, rx=6, sw=sw)
    out += text(x + 10, y + 22, code, 10.5, NAVY, 700)
    caption = {"livre": "livre", "ocupado": "ocupado", "reservado": "reservado", "manutencao": "manutenção", "vencendo": "vence em 30d"}[status]
    out += text(x + 10, y + 42, caption, 9, MUTED if status in ("livre", "ocupado") else (WARN if status == "vencendo" else MUTED))
    if selected:
        out += circle(x + 84, y + 14, 3.5, WARN)
    return out


def screen_cemiterio():
    b = rect(0, 0, W, H, BG)
    b += sidebar("cemiterio", user="Carlos Menezes", role="Administrador")
    b += topbar("Mapa do cemitério", "Cemitério · Jardim da Paz · Quadra C", status=("Servidor da loja · LAN", BLUE, BLUE_SOFT))

    b += rect(CX, 88, CW, 56, PANEL, BORDER, rx=10)
    x = CX + 16
    for label, active in [("Jardim da Paz", True), ("Quadra C", True), ("Todos os status", False)]:
        p, pw = pill(x, 104, label, "#ffffff" if active else NAVY, NAVY if active else CHIP, h=26, size=10.5, border=BORDER)
        b += p
        x += pw + 8
    b += text(CX + 560, 120, "Buscar jazigo", 11, FAINT)
    b += rect(CX + 640, 104, CW - 656, 26, PANEL, BORDER, rx=6)
    b += text(CX + 652, 121, "C-014", 11, INK)

    mx, my, mw, mh = CX, 160, CW, 520
    b += rect(mx, my, mw, mh, PANEL, BORDER, rx=12)
    b += text(mx + 16, my + 28, "Quadra C · 120 jazigos", 14, INK, 700)
    b += text(mx + 16, my + 46, "40 de 120 exibidos · ocupação 68%", 10.5, FAINT)
    b += text(mx + mw - 16, my + 28, "Arraste para navegar", 10, FAINT, 400, "end")

    statuses = ["ocupado", "ocupado", "livre", "ocupado", "reservado", "livre", "ocupado", "manutencao",
                "livre", "ocupado", "ocupado", "livre", "reservado", "ocupado", "livre", "ocupado",
                "ocupado", "livre", "vencendo", "livre", "ocupado", "reservado", "livre", "ocupado",
                "livre", "ocupado", "ocupado", "livre", "manutencao", "ocupado", "livre", "reservado",
                "ocupado", "livre", "ocupado", "ocupado", "livre", "ocupado", "livre", "manutencao"]
    gx, gy = mx + 16, my + 62
    for i, status in enumerate(statuses):
        row, col = divmod(i, 8)
        code = f"C-{i+1:03d}"
        b += plot_cell(gx + col * 104, gy + row * 64, code, status, selected=(code == "C-019"))

    sy = my + mh - 100
    b += line(mx + 16, sy, mx + mw - 16, sy)
    facts = [("Ocupados", "23"), ("Livres", "10"), ("Reservados", "4"), ("Manutenção", "3"), ("Concessões vencendo", "4")]
    x = mx + 16
    for label, value in facts:
        b += text(x, sy + 22, label, 9.5, FAINT, 700, ls="0.5")
        b += text(x, sy + 42, value, 16, INK, 700)
        x += 164

    ly = my + mh - 34
    x = mx + 16
    for label, status in [("Livre", "livre"), ("Ocupado", "ocupado"), ("Reservado", "reservado"), ("Manutenção", "manutencao"), ("Vencendo", "vencendo")]:
        fill, stroke = {"livre": (PANEL, BORDER), "ocupado": ("#eaf2fb", "#bcd4ec"), "reservado": ("#e3f1fb", BLUE), "manutencao": (WARN_BG, "#f0d6ad"), "vencendo": (WARN_BG, WARN)}[status]
        b += rect(x, ly, 12, 12, fill, stroke, rx=3)
        b += text(x + 18, ly + 10, label, 10.5, MUTED)
        x += 112

    dx, dy, dw, dh = CX, 696, CW, 160
    b += rect(dx, dy, dw, dh, PANEL, BORDER, rx=12)
    b += text(dx + 16, dy + 26, "Jazigo C-019", 14, INK, 700)
    p, _ = pill(dx + 112, dy + 11, "concessão vencendo", WARN, WARN_BG, h=20, size=10)
    b += p
    facts2 = [("Concessionário", "Família Ribeiro"), ("Vigência", "até 21/12/2026"), ("Último sepultamento", "12/03/2019"), ("Manutenção", "em dia")]
    x = dx + 16
    for label, value in facts2:
        b += text(x, dy + 62, label, 9.5, FAINT, 700, ls="0.6")
        b += text(x, dy + 82, value, 12, INK, 600)
        x += 190
    b += button(dx + dw - 260, dy + 104, 112, 32, "Ver histórico", primary=False, size=11)
    b += button(dx + dw - 136, dy + 104, 120, 32, "Renovar concessão", primary=True, size=11)

    b += rail(
        "Cemitério · Mapa",
        "Administrador",
        "Online",
        [
            ("user", ["O que vence este mês?"]),
            ("agent", ["4 concessões vencem em até 30", "dias. A C-019 é a mais próxima:", "21/12, família Ribeiro."]),
            ("user", ["Prepare a renovação."]),
            ("agent", ["Posso gerar a proposta de", "renovação com a taxa vigente.", "O envio depende da sua confirmação."]),
        ],
        ["Ver as 4 concessões", "Gerar proposta", "Abrir C-019"],
        "Respostas limitadas ao perfil Administrador · sem dados de outras unidades",
    )
    return b


def screen_financeiro():
    b = rect(0, 0, W, H, BG)
    b += sidebar("financeiro", user="Paulo Nunes", role="Financeiro")
    b += topbar("Contas a receber", "Financeiro · Títulos · Vencimentos", status=("Online", OK, OK_BG), unit="Centro")

    metrics = [
        ("A receber (30 dias)", "R$ 184.320", "148 títulos", BLUE),
        ("Vencido", "R$ 18.420", "12 títulos", DANGER),
        ("Recebido hoje", "R$ 6.240", "7 baixas", OK),
        ("Caixa atual", "R$ 9.810", "fechado às 17h", NAVY),
    ]
    x = CX
    for label, value, detail, accent in metrics:
        b += kpi(x, 88, label, value, detail, accent=accent)
        x += 201 + 16

    b += rect(CX, 196, CW, 52, PANEL, BORDER, rx=10)
    x = CX + 14
    for label, active in [("Todos", True), ("Vencidos 12", False), ("Hoje 8", False), ("Esta semana 23", False)]:
        p, pw = pill(x, 209, label, "#ffffff" if active else NAVY, NAVY if active else CHIP, h=26, size=10.5, border=BORDER)
        b += p
        x += pw + 8
    b += button(CX + CW - 132, 208, 118, 28, "Novo título", primary=True, size=11)

    tx, ty, tw, th = CX, 264, CW, 500
    b += rect(tx, ty, tw, th, PANEL, BORDER, rx=12)
    b += text(tx + 16, ty + 28, "Títulos a receber", 14, INK, 700)
    b += text(tx + 16, ty + 46, "Ordenado por vencimento", 10.5, FAINT)

    cols_x = [tx + 16, tx + 240, tx + 390, tx + 620, tx + 650]
    heads = ["Cliente", "Serviço", "Vencimento", "Valor", "Status"]
    for i, head in enumerate(heads):
        anchor = "end" if i == 3 else "start"
        b += text(cols_x[i], ty + 74, head, 9.5, FAINT, 700, anchor, ls="0.6")

    rows = [
        ("Família Ribeiro", "Plano Essencial", "10/11/2026", "R$ 1.333,33", "Vencido", DANGER, DANGER_BG),
        ("Família Alves", "Cremação + traslado", "18/11/2026", "R$ 2.480,00", "Vencido", DANGER, DANGER_BG),
        ("Sr. Carlos Andrade", "Plano pré-need", "21/11/2026", "R$ 389,90", "Vence hoje", WARN, WARN_BG),
        ("Família Souza", "Sepultamento C-042", "21/11/2026", "R$ 1.120,00", "Vence hoje", WARN, WARN_BG),
        ("Floricultura Bem-Viver", "Arranjos · pedido 882", "24/11/2026", "R$ 640,00", "Programado", MUTED, CHIP),
        ("Família Lima", "Plano Essencial", "28/11/2026", "R$ 1.333,33", "Programado", MUTED, CHIP),
        ("Família Costa", "Taxa de manutenção", "02/11/2026", "R$ 250,00", "Pago", OK, OK_BG),
    ]
    y = ty + 104
    for client, service, due, value, status, fg, bg in rows:
        b += text(cols_x[0], y, client, 11.5, INK, 600)
        b += text(cols_x[1], y, service, 11, MUTED)
        b += text(cols_x[2], y, due, 11, MUTED)
        b += text(cols_x[3], y, value, 11.5, INK, 600, "end")
        p, _ = pill(cols_x[4], y - 14, status, fg, bg, h=20, size=10)
        b += p
        b += line(tx + 16, y + 18, tx + tw - 16, y + 18, BORDER)
        y += 48

    fy = ty + th - 64
    b += line(tx + 16, fy, tx + tw - 16, fy)
    b += text(tx + 16, fy + 22, "Total filtrado", 9.5, FAINT, 700, ls="0.5")
    b += text(tx + 16, fy + 42, "R$ 184.320", 15, INK, 700)
    b += text(tx + tw - 212, fy + 26, "1–7 de 148", 10.5, FAINT, 400, "end")
    b += button(tx + tw - 196, fy + 10, 78, 28, "Anterior", primary=False, size=10.5)
    b += button(tx + tw - 110, fy + 10, 94, 28, "Próxima", primary=False, size=10.5)

    b += rail(
        "Financeiro · Títulos",
        "Financeiro",
        "Online",
        [
            ("user", ["Monte a régua de cobrança", "dos vencidos."]),
            ("agent", ["São 12 títulos, R$ 18.420.", "Sugiro lembrete hoje, WhatsApp", "em 3 dias e ligação em 7."]),
            ("user", ["Pode enviar?"]),
            ("agent", ["Posso preparar as mensagens, mas", "o envio exige sua confirmação.", "Quer revisar antes?"]),
        ],
        ["Ver régua", "Revisar mensagens", "Separar por cliente"],
        "Respostas limitadas ao perfil Financeiro · envio sempre confirmado",
    )
    return b


def build():
    defs = f'''<defs>
<linearGradient id="lg" x1="0%" y1="0%" x2="100%" y2="100%">
  <stop offset="0%" stop-color="{BLUE_L}"/><stop offset="100%" stop-color="{BLUE}"/>
</linearGradient>
</defs>'''
    screens = [
        ("01-painel-do-dia.svg", "Painel do dia", "Atendente", screen_painel()),
        ("02-atendimento-contrato.svg", "Atendimento · Novo contrato", "Atendente · offline", screen_atendimento()),
        ("03-cemiterio-mapa.svg", "Cemitério · Mapa", "Administrador", screen_cemiterio()),
        ("04-financeiro-titulos.svg", "Financeiro · Títulos", "Financeiro", screen_financeiro()),
    ]
    for name, _, _, body in screens:
        svg = (f'<svg xmlns="http://www.w3.org/2000/svg" width="{W}" height="{H}" '
               f'viewBox="0 0 {W} {H}" role="img" aria-label="Mockup Amparius">' + defs + body + "</svg>")
        with open(os.path.join(OUT, name), "w", encoding="utf-8", newline="\n") as fh:
            fh.write(svg)

    cards = "".join(
        f'''<a class="card" href="{name}"><img src="{name}" alt="{title}"/>
        <div class="meta"><b>{title}</b><span>{role}</span></div></a>'''
        for name, title, role, _ in screens
    )
    index = f'''<!doctype html><html lang="pt-BR"><head><meta charset="utf-8"/>
<meta name="viewport" content="width=device-width,initial-scale=1"/>
<title>Amparius · mockups</title>
<style>
body{{margin:0;background:#f3f6fa;color:#0d1b2a;font-family:{FONT}}}
header{{background:{NAVY};color:#fff;padding:28px 32px;display:flex;align-items:center;gap:14px}}
header img{{width:42px;height:42px}}
header h1{{margin:0;font-size:20px;letter-spacing:-.3px}}
header p{{margin:2px 0 0;color:#9fc3e6;font-size:12px}}
.wrap{{max-width:1100px;margin:0 auto;padding:28px 20px 60px}}
.note{{background:#fff;border:1px solid #dbe4ee;border-radius:12px;padding:14px 16px;font-size:13px;color:#56697c;margin-bottom:22px}}
.grid{{display:grid;grid-template-columns:1fr 1fr;gap:22px}}
.card{{display:block;background:#fff;border:1px solid #dbe4ee;border-radius:14px;overflow:hidden;text-decoration:none;color:inherit;box-shadow:0 8px 24px rgba(0,39,76,.06)}}
.card img{{width:100%;display:block;border-bottom:1px solid #dbe4ee}}
.meta{{display:flex;justify-content:space-between;align-items:center;padding:12px 14px;font-size:12px}}
.meta b{{font-size:13px}} .meta span{{color:#8a99a8}}
@media(max-width:900px){{.grid{{grid-template-columns:1fr}}}}
</style></head><body>
<header><img src="../assets/brand/amparius-icon.svg" alt=""/><div><h1>Mockups · Amparius</h1><p>Telas com assistente contextual · v0.1</p></div></header>
<div class="wrap">
<div class="note">Cada tela tem o mesmo assistente à direita, ciente da <b>tela</b>, do <b>registro aberto</b> e do <b>perfil</b> de quem está usando. Ele sugere, explica e prepara ações; executar exige confirmação e permissão.</div>
<div class="grid">{cards}</div>
</div></body></html>'''
    with open(os.path.join(OUT, "index.html"), "w", encoding="utf-8", newline="\n") as fh:
        fh.write(index)

    print("gerado em", os.path.abspath(OUT))


if __name__ == "__main__":
    build()
