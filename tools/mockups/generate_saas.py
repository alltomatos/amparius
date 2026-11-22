#!/usr/bin/env python3
"""Gera os mockups SVG do painel do admin SaaS (control plane / entitlement).

Uso: python tools/mockups/generate_saas.py
Saida: docs/design/mockups/*.svg

Reutiliza as convencoes de tools/mockups/generate.py (MOCKUPS.md §2):
barra lateral #00274C, destaque #0073CF, densidade 13-14px, assistente contextual.
Nenhum valor fiscal; placeholder ficticio.
"""

import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from generate import (  # noqa: F401
    BG, BLUE, BLUE_L, BLUE_SOFT, BORDER, CHIP, CX, CW, DANGER, DANGER_BG, FAINT,
    FONT, H, INK, MUTED, NAVY, OK, OK_BG, OUT, PANEL, RAIL_X, RW, SW, W, WARN,
    WARN_BG, button, circle, e, line, lines, logo, pill, rail, rect, text,
    topbar,
)

OUT = os.path.join(os.path.dirname(__file__), "..", "..", "docs", "design", "mockups")
os.makedirs(OUT, exist_ok=True)

CORE = ["identity", "party", "catalog", "finance", "fiscal", "sync", "reporting"]
OPT = ["funeral", "cemetery", "cremation", "floriculture", "inventory", "documents", "ai"]

QUOTA = {
    "funeral": (2, 2),
    "cemetery": (2, 1),
    "cremation": (1, 0),
    "floriculture": (3, 2),
    "inventory": (1, 0),
    "documents": (1, 1),
    "ai": (2, 0),
}

SAAS_STATUS = ("SaaS online", OK, OK_BG)


def sidebar_saas(active):
    out = rect(0, 0, SW, H, NAVY)
    out += logo(18, 18, 30, "#ffffff", "#7cc0f8")
    out += text(58, 34, "amparius", 17, "#ffffff", 700, ls="-0.4")
    out += text(58, 48, "control plane", 9, "#9fc3e6", 500, ls="0.4")

    nav = [
        ("grid", "Painel", "painel"),
        ("users", "Tenants", "tenants"),
        ("doc", "Empresas", "empresas"),
        ("chart", "Módulos", "modulos"),
        ("receipt", "Auditoria", "auditoria"),
    ]
    y = 88
    for kind, label, key in nav:
        is_active = key == active
        if is_active:
            out += rect(8, y - 19, SW - 16, 38, "#ffffff", opacity=0.08, rx=8)
            out += rect(8, y - 11, 3, 22, BLUE_L, rx=2)
        color = "#ffffff" if is_active else "#b9cbe0"
        # icons
        x0, y0 = 22, y - 8
        if kind == "grid":
            for dx, dy in ((0, 0), (8, 0), (0, 8), (8, 8)):
                out += rect(x0 + dx, y0 + dy, 6, 6, color, rx=1.5)
        elif kind == "users":
            out += circle(x0 + 5, y0 + 4, 3, "none", color)
            out += f'<path d="M {x0} {y0+14} C {x0} {y0+9}, {x0+10} {y0+9}, {x0+10} {y0+14}" fill="none" stroke="{color}" stroke-width="1.4"/>'
        elif kind == "doc":
            out += rect(x0, y0, 12, 14, "none", color, rx=2)
            out += line(x0 + 3, y0 + 4, x0 + 9, y0 + 4, color)
            out += line(x0 + 3, y0 + 7, x0 + 9, y0 + 7, color)
        elif kind == "chart":
            out += rect(x0 + 1, y0 + 8, 3, 6, color) + rect(x0 + 6, y0 + 4, 3, 10, color) + rect(x0 + 11, y0 + 1, 3, 13, color)
        elif kind == "receipt":
            out += rect(x0 + 1, y0, 12, 14, "none", color, rx=1)
            out += line(x0 + 4, y0 + 4, x0 + 10, y0 + 4, color)
            out += line(x0 + 4, y0 + 7, x0 + 10, y0 + 7, color)
            out += line(x0 + 4, y0 + 10, x0 + 8, y0 + 10, color)
        out += text(48, y + 4, label, 13, color, 600 if is_active else 500)
        y += 42

    out += rect(12, 744, SW - 24, 58, "#ffffff", opacity=0.06, rx=10)
    out += text(24, 764, "CONTROL PLANE", 9, "#8fb4d8", 700, ls="0.8")
    out += text(24, 784, "admin.saas", 12, "#ffffff", 600)
    out += text(24, 797, "Plano SaaS ativo", 10, "#9fc3e6")

    out += circle(30, 840, 14, BLUE_L)
    out += text(30, 845, "AS", 11, "#ffffff", 700, "middle")
    out += text(52, 836, "admin.saas", 12, "#ffffff", 600)
    out += text(52, 850, "Admin SaaS", 10, "#9fc3e6")
    return out


def warn_icon(x, y, color, size=18):
    out = f'<path d="M {x} {y+size} L {x+size/2} {y} L {x+size} {y+size} Z" fill="none" stroke="{color}" stroke-width="1.6" stroke-linejoin="round"/>'
    out += line(x + size / 2, y + size * 0.4, x + size / 2, y + size * 0.72, color, sw=1.6)
    out += circle(x + size / 2, y + size * 0.86, 1, color)
    return out


def check_icon(x, y, color, size=16):
    out = circle(x + size / 2, y + size / 2, size / 2 - 0.5, color)
    out += f'<path d="M {x+size*0.28} {y+size*0.52} l 3 3 5 -6" fill="none" stroke="#ffffff" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/>'
    return out


def quota_table(x, y, w, opacity=1.0, show_header=True):
    """Tabela de licencas por modulo. Retorna o SVG e a altura usada."""
    out = ""
    row_h = 30
    mod_x = x + 16
    tipo_x = x + 168
    cota_x = x + 258
    uso_x = x + 340
    disp_x = x + 424
    acao_r = x + w - 16

    if show_header:
        heads = [("Módulo", mod_x, "start"), ("Tipo", tipo_x, "start"),
                 ("Cota", cota_x, "end"), ("Em uso", uso_x, "end"),
                 ("Disponível", disp_x, "end"), ("Ação", acao_r, "end")]
        for label, hx, anchor in heads:
            out += text(hx, y, label, 9.5, FAINT, 700, anchor, ls="0.6", opacity=opacity)
        y += 18

    def section(label):
        nonlocal y
        out2 = rect(x + 12, y + 6, w - 24, 20, "#f6f9fc", rx=6)
        out2 += text(x + 22, y + 20, label, 9.5, MUTED, 700, ls="0.6")
        y += 28
        return out2

    out += section("OPCIONAIS · NA COTA")
    for code in OPT:
        quota, used = QUOTA[code]
        avail = quota - used
        exhausted = avail == 0
        out += text(mod_x, y + 12, code, 12.5, INK, 600, opacity=opacity)
        p, _ = pill(tipo_x, y + 1, "Opcional", MUTED, CHIP, h=18, size=9.5)
        out += p
        out += text(cota_x, y + 12, str(quota), 12, INK, 600, "end", opacity=opacity)
        out += text(uso_x, y + 12, str(used), 12, INK, 400, "end", opacity=opacity)
        col = DANGER if exhausted else (OK if avail > 0 else INK)
        out += text(disp_x, y + 12, str(avail), 12, col, 700, "end", opacity=opacity)
        if exhausted:
            p, _ = pill(acao_r - 78, y + 1, "esgotada", DANGER, DANGER_BG, h=18, size=9.5)
            out += p
        else:
            out += button(acao_r - 84, y, 84, 22, "+ liberar", primary=True, size=10)
        out += line(x + 12, y + row_h - 7, x + w - 12, y + row_h - 7, BORDER, opacity=0.6 * opacity)
        y += row_h

    out += section("NÚCLEO · SEMPRE LIGADO · FORA DA COTA")
    for code in CORE:
        out += text(mod_x, y + 12, code, 12.5, INK, 600, opacity=opacity)
        p, _ = pill(tipo_x, y + 1, "Núcleo", NAVY, BLUE_SOFT, h=18, size=9.5, border=BLUE_SOFT)
        out += p
        out += text(cota_x, y + 12, "—", 12, FAINT, 400, "end", opacity=opacity)
        out += text(uso_x, y + 12, "—", 12, FAINT, 400, "end", opacity=opacity)
        out += text(disp_x, y + 12, "—", 12, FAINT, 400, "end", opacity=opacity)
        out += text(acao_r, y + 12, "sempre ligado", 10.5, FAINT, 500, "end", opacity=opacity)
        out += line(x + 12, y + row_h - 7, x + w - 12, y + row_h - 7, BORDER, opacity=0.6 * opacity)
        y += row_h
    return out, y


def screen_cotas():
    b = rect(0, 0, W, H, BG)
    b += sidebar_saas("painel")
    b += topbar("Cotas do tenant", "Control plane · Tenants · Funerária Exemplo",
                status=SAAS_STATUS, unit="Control plane")

    # seletor de tenant
    b += rect(CX, 80, CW, 56, PANEL, BORDER, rx=10)
    b += text(CX + 16, 102, "TENANT", 9, FAINT, 700, ls="0.8")
    b += text(CX + 16, 124, "Funerária Exemplo (grupo)", 14, INK, 700)
    p, _ = pill(CX + CW - 120, 98, "trocar ▾", NAVY, CHIP, h=26, size=11, border=BORDER)
    b += p
    b += text(CX + 280, 102, "2 empresas · 10 licenças em uso", 10.5, FAINT)
    b += text(CX + 280, 122, "Vigência do grupo: até 31/12/2026", 10.5, FAINT)

    # aviso de cota esgotada (estado)
    b += rect(CX, 144, CW, 40, WARN_BG, "#f2ddbd", rx=8)
    b += warn_icon(CX + 14, 154, WARN)
    b += text(CX + 40, 169, "2 módulos com cota esgotada: funeral e documents. "
                            "Liberar nova licença exige encerrar ou reatribuir uma existente.", 11.5, WARN)
    b += text(CX + CW - 14, 169, "Ver auditoria", 10.5, WARN, 600, "end")

    # card licencas
    ty = 200
    b += rect(CX, ty, CW, 560, PANEL, BORDER, rx=12)
    b += text(CX + 16, ty + 28, "Licenças por módulo", 14, INK, 700)
    b += text(CX + 16, ty + 46, "14 módulos · 7 núcleo (sempre ligados) · 7 opcionais (na cota)", 10.5, FAINT)
    b += button(CX + CW - 132, ty + 14, 116, 28, "Exportar", primary=False, size=11)
    tbl, _ = quota_table(CX, ty + 58, CW)
    b += tbl

    # card acessos
    ay = 776
    b += rect(CX, ay, CW, 92, PANEL, BORDER, rx=12)
    b += text(CX + 16, ay + 28, "Acessos do tenant", 13.5, INK, 700)
    b += text(CX + 16, ay + 54, "Usuários", 10.5, MUTED, 600)
    b += text(CX + 108, ay + 55, "12 / 20", 15, INK, 700)
    # barra
    bar_x, bar_y, bar_w = CX + 190, ay + 48, 360
    b += rect(bar_x, bar_y, bar_w, 10, "#e6edf5", rx=5)
    b += rect(bar_x, bar_y, bar_w * 12 / 20, 10, BLUE, rx=5)
    b += text(bar_x + bar_w + 14, ay + 57, "8 livres", 10.5, FAINT)
    b += button(CX + CW - 132, ay + 30, 116, 30, "Ajustar cota", primary=False, size=11)

    b += rail(
        "Control plane · Cotas do tenant",
        "Admin SaaS",
        "SaaS online",
        [
            ("user", ["O que está travado", "neste tenant?"]),
            ("agent", ["funeral e documents estão com a", "cota esgotada: 2 de 2 e 1 de 1", "em uso. Para liberar outra,", "encerre ou reatribua uma licença."]),
            ("user", ["Me mostre quem liberou."]),
            ("agent", ["Abri a trilha de auditoria deste", "tenant. As mudanças de 21/11 foram", "feitas por admin.saas e ronaldo", "em self-service."]),
        ],
        ["Abrir auditoria", "Liberar funeral", "Ajustar cota de usuários"],
        "Respostas limitadas ao perfil Admin SaaS · sem dado fiscal · sem PII",
    )
    return b


def screen_licenca():
    b = rect(0, 0, W, H, BG)
    b += sidebar_saas("modulos")
    b += topbar("Liberar / revogar licença", "Control plane · Tenants · Funerária Exemplo · Licença",
                status=SAAS_STATUS, unit="Control plane")

    # fundo esmaecido: painel de cotas
    b += rect(CX, 80, CW, 40, PANEL, BORDER, rx=10)
    b += text(CX + 16, 104, "Licenças por módulo", 13, INK, 700)
    tbl, _ = quota_table(CX, 140, CW, opacity=0.5, show_header=True)
    b += tbl
    b += rect(CX, 80, CW, H - 96, "none", None)
    b += rect(SW, 64, RAIL_X - SW, H - 64, "#0d1b2a", opacity=0.42)

    # modal liberar (estado: cota esgotada)
    mx, my, mw, mh = 232, 150, 500, 600
    b += rect(mx, my, mw, mh, PANEL, BORDER, rx=16)
    b += text(mx + 24, my + 36, "Liberar licença", 17, INK, 700, ls="-0.3")
    b += text(mx + 24, my + 56, "1 licença = 1 módulo em 1 empresa (CNPJ)", 10.5, FAINT)
    b += line(mx + 24, my + 72, mx + mw - 24, my + 72)

    # campo modulo
    b += text(mx + 24, my + 96, "MÓDULO", 9, FAINT, 700, ls="0.7")
    b += rect(mx + 24, my + 104, mw - 48, 38, PANEL, BORDER, rx=8)
    b += text(mx + 36, my + 128, "funeral", 12.5, INK, 600)
    b += text(mx + mw - 40, my + 128, "▾", 11, MUTED, 400, "end")

    # campo empresa
    b += text(mx + 24, my + 166, "EMPRESA (CNPJ)", 9, FAINT, 700, ls="0.7")
    b += rect(mx + 24, my + 174, mw - 48, 38, PANEL, BORDER, rx=8)
    b += text(mx + 36, my + 198, "Funerária Exemplo · CNPJ 00.000.000/0001-00", 11.5, INK, 600)
    b += text(mx + mw - 40, my + 198, "▾", 11, MUTED, 400, "end")

    # vigencia
    b += text(mx + 24, my + 236, "VIGÊNCIA", 9, FAINT, 700, ls="0.7")
    b += rect(mx + 24, my + 244, 150, 38, PANEL, BORDER, rx=8)
    b += text(mx + 36, my + 268, "01/12/2026", 12, INK)
    b += text(mx + 182, my + 268, "até", 10.5, FAINT)
    b += rect(mx + 212, my + 244, 150, 38, PANEL, BORDER, rx=8)
    b += text(mx + 224, my + 268, "31/12/2026", 12, INK)
    b += text(mx + 372, my + 268, "comercial", 9.5, FAINT)

    # resumo da cota
    ry = my + 302
    b += rect(mx + 24, ry, mw - 48, 56, "#f6f9fc", rx=8)
    for i, (label, val, col) in enumerate([("Cota", "2", INK), ("Em uso", "2", INK), ("Disponível", "0", DANGER)]):
        cx0 = mx + 48 + i * 150
        b += text(cx0, ry + 22, label, 9, FAINT, 700, ls="0.6")
        b += text(cx0, ry + 44, val, 18, col, 700)

    # aviso heranca
    hy = ry + 68
    b += rect(mx + 24, hy, mw - 48, 34, BLUE_SOFT, "#cfe2f5", rx=8)
    b += check_icon(mx + 34, hy + 9, BLUE)
    b += text(mx + 58, hy + 22, "Esta licença habilita o módulo nesta empresa. Filiais dela herdam automaticamente.", 10.5, NAVY)

    # erro cota esgotada
    ey = hy + 46
    b += rect(mx + 24, ey, mw - 48, 92, DANGER_BG, "#f3c9c4", rx=8)
    b += warn_icon(mx + 36, ey + 14, DANGER)
    b += text(mx + 62, ey + 26, "Licenças de funeral esgotadas", 12.5, DANGER, 700)
    b += text(mx + 62, ey + 46, "As 2 licenças de funeral já estão em uso. Encerre ou reatribua", 10.5, INK)
    b += text(mx + 62, ey + 62, "uma licença antes de liberar outra.", 10.5, INK)
    b += text(mx + 62, ey + 80, "Detalhes: LICENSE_POOL_EXHAUSTED", 9.5, FAINT)

    # botoes
    b += button(mx + mw - 272, my + mh - 56, 112, 34, "Cancelar", primary=False, size=11.5)
    # confirmar desabilitado
    b += rect(mx + mw - 148, my + mh - 56, 124, 34, "#c7d2dd", rx=8)
    b += text(mx + mw - 86, my + mh - 34, "Confirmar licença", 11, "#ffffff", 600, "middle")

    # variacao: revogar
    vx, vy, vw, vh = 760, 230, 300, 320
    b += text(vx, vy - 12, "VARIAÇÃO · REVOGAR LICENÇA", 9, "#c7d6e6", 700, ls="0.8")
    b += rect(vx, vy, vw, vh, PANEL, BORDER, rx=16)
    b += text(vx + 20, vy + 34, "Revogar licença", 15, INK, 700, ls="-0.2")
    b += text(vx + 20, vy + 56, "cemetery · Funerária Exemplo", 10.5, FAINT)
    b += line(vx + 20, vy + 72, vx + vw - 20, vy + 72)
    b += text(vx + 20, vy + 102, "Efeito da revogação", 9, FAINT, 700, ls="0.7")
    b += text(vx + 20, vy + 126, "A empresa perde o acesso ao módulo.", 11.5, INK)
    b += text(vx + 20, vy + 146, "Os dados permanecem intactos e a", 11.5, INK)
    b += text(vx + 20, vy + 166, "leitura congelada é mantida.", 11.5, INK)
    b += rect(vx + 20, vy + 190, vw - 40, 44, WARN_BG, "#f2ddbd", rx=8)
    b += warn_icon(vx + 30, vy + 202, WARN, size=16)
    b += text(vx + 54, vy + 210, "Ação auditada e irreversível", 10, WARN, 600)
    b += text(vx + 54, vy + 224, "para o tenant.", 10, WARN)
    b += button(vx + vw - 188, vy + vh - 52, 84, 32, "Cancelar", primary=False, size=10.5)
    b += button(vx + vw - 96, vy + vh - 52, 76, 32, "Revogar", primary=True, size=10.5)

    b += rail(
        "Control plane · Licença",
        "Admin SaaS",
        "SaaS online",
        [
            ("user", ["O que acontece se eu", "revogar o cemetery?"]),
            ("agent", ["A empresa perde o acesso ao módulo", "imediatamente, mas nenhum dado é", "apagado. A leitura congelada de", "fiscal e reporting é mantida."]),
            ("user", ["E se faltar cota?"]),
            ("agent", ["O sistema bloqueia com o aviso de", "cota esgotada e não consome", "licença. A filial herda o", "entitlement da empresa."]),
        ],
        ["Ver herança da empresa", "Revogar licença", "Cancelar"],
        "Respostas limitadas ao perfil Admin SaaS · ação auditada",
    )
    return b


def screen_auditoria():
    b = rect(0, 0, W, H, BG)
    b += sidebar_saas("auditoria")
    b += topbar("Auditoria de entitlement", "Control plane · Tenants · Funerária Exemplo · Auditoria",
                status=SAAS_STATUS, unit="Control plane")

    # filtros
    fy = 80
    b += rect(CX, fy, CW, 56, PANEL, BORDER, rx=10)
    b += text(CX + 16, 102, "Filtros", 10, FAINT, 700, ls="0.7")
    b += rect(CX + 16, 108, 150, 26, PANEL, BORDER, rx=6)
    b += text(CX + 26, 125, "Módulo: todos ▾", 10.5, INK)
    b += rect(CX + 174, 108, 140, 26, PANEL, BORDER, rx=6)
    b += text(CX + 184, 125, "Origem: todas ▾", 10.5, INK)
    b += rect(CX + 322, 108, 160, 26, PANEL, BORDER, rx=6)
    b += text(CX + 332, 125, "Período: 30 dias ▾", 10.5, INK)
    b += text(CX + CW - 16, 125, "6 eventos", 10.5, FAINT, 400, "end")

    # tabela
    tx, ty, tw = CX, 168, CW
    b += rect(tx, ty, tw, 520, PANEL, BORDER, rx=12)
    b += text(tx + 16, ty + 28, "Quem ligou e desligou, quando e por qual origem", 14, INK, 700)
    b += text(tx + 16, ty + 46, "ENTITLEMENT_EVENT · append-only · 6 eventos no período", 10.5, FAINT)
    b += button(CX + CW - 132, ty + 14, 116, 30, "Exportar", primary=False, size=11)

    cols = [tx + 16, tx + 150, tx + 260, tx + 500, tx + 700]
    heads = ["Quando", "Módulo", "Empresa", "Quem", "Origem"]
    for i, head in enumerate(heads):
        b += text(cols[i], ty + 86, head, 9.5, FAINT, 700, "start", ls="0.6")

    rows = [
        ("21/11 14:02", "cemetery", "Funerária Exemplo", "admin.saas", "admin", NAVY, BLUE_SOFT),
        ("21/11 11:20", "funeral", "Funerária Exemplo", "ronaldo", "self-service", MUTED, CHIP),
        ("20/11 18:44", "floriculture", "Floricultura Exemplo", "ronaldo", "self-service", MUTED, CHIP),
        ("20/11 09:15", "documents", "Funerária Exemplo", "admin.saas", "admin", NAVY, BLUE_SOFT),
        ("19/11 16:30", "ai", "Funerária Exemplo", "maria.souza", "self-service", MUTED, CHIP),
        ("18/11 10:05", "cemetery", "Funerária Exemplo", "admin.saas", "admin", NAVY, BLUE_SOFT),
    ]
    y = ty + 116
    for quando, modulo, empresa, quem, origem, fg, bg in rows:
        b += text(cols[0], y, quando, 11.5, INK, 400)
        b += text(cols[1], y, modulo, 11.5, INK, 600)
        b += text(cols[2], y, empresa, 11.5, MUTED)
        b += text(cols[3], y, quem, 11.5, INK, 600)
        p, _ = pill(cols[4], y - 14, origem, fg, bg, h=20, size=9.5)
        b += p
        b += line(tx + 16, y + 16, tx + tw - 16, y + 16, BORDER)
        y += 52

    # resumo
    sy = ty + 452
    b += line(tx + 16, sy, tx + tw - 16, sy)
    facts = [("Admin SaaS", "3"), ("Self-service", "3"), ("Módulos tocados", "5"), ("Último evento", "21/11 14:02")]
    x = tx + 16
    for label, val in facts:
        b += text(x, sy + 24, label, 9.5, FAINT, 700, ls="0.5")
        b += text(x, sy + 44, val, 15, INK, 700)
        x += 180

    b += rail(
        "Control plane · Auditoria",
        "Admin SaaS",
        "SaaS online",
        [
            ("user", ["Quem mexeu no funeral", "esta semana?"]),
            ("agent", ["ronaldo ativou funeral em 21/11", "às 11:20, em self-service. Nenhuma", "revogação de admin no período."]),
            ("user", ["Exporte o período."]),
            ("agent", ["Posso gerar o CSV dos 6 eventos", "com quando, módulo, empresa, quem", "e origem. O download começa", "após sua confirmação."]),
        ],
        ["Filtrar por admin", "Ver funeral", "Exportar CSV"],
        "Respostas limitadas ao perfil Admin SaaS · trilha append-only",
    )
    return b


def build():
    defs = f'''<defs>
<linearGradient id="lg" x1="0%" y1="0%" x2="100%" y2="100%">
  <stop offset="0%" stop-color="{BLUE_L}"/><stop offset="100%" stop-color="{BLUE}"/>
</linearGradient>
</defs>'''
    screens = [
        ("05-painel-saas-cotas.svg", "Painel SaaS · Cotas do tenant", "Admin SaaS", screen_cotas()),
        ("06-painel-saas-licenca.svg", "Painel SaaS · Liberar/revogar licença", "Admin SaaS", screen_licenca()),
        ("07-painel-saas-auditoria.svg", "Painel SaaS · Auditoria de entitlement", "Admin SaaS", screen_auditoria()),
    ]
    for name, _, _, body in screens:
        svg = (f'<svg xmlns="http://www.w3.org/2000/svg" width="{W}" height="{H}" '
               f'viewBox="0 0 {W} {H}" role="img" aria-label="Mockup Amparius">' + defs + body + "</svg>")
        with open(os.path.join(OUT, name), "w", encoding="utf-8", newline="\n") as fh:
            fh.write(svg)
        print("gerado:", os.path.join(OUT, name))

    cards = "".join(
        f'''<a class="card" href="{name}"><img src="{name}" alt="{title}"/>
        <div class="meta"><b>{title}</b><span>{role}</span></div></a>'''
        for name, title, role, _ in screens
    )
    index = f'''<!doctype html><html lang="pt-BR"><head><meta charset="utf-8"/>
<meta name="viewport" content="width=device-width,initial-scale=1"/>
<title>Amparius · mockups · painel SaaS</title>
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
<header><img src="../../../assets/brand/amparius-icon.svg" alt=""/><div><h1>Mockups · Painel SaaS</h1><p>Control plane · entitlement por licença · v0.1</p></div></header>
<div class="wrap">
<div class="note">Três telas do admin SaaS: cotas do tenant, liberar/revogar licença e auditoria. Nenhum valor fiscal; placeholders fictícios.</div>
<div class="grid">{cards}</div>
</div></body></html>'''
    with open(os.path.join(OUT, "index.html"), "w", encoding="utf-8", newline="\n") as fh:
        fh.write(index)

    print("galeria em", os.path.abspath(os.path.join(OUT, "index.html")))


if __name__ == "__main__":
    build()
