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
    WARN_BG, button, circle, e, kpi, line, lines, logo, pill, rail, rect, text,
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


def sidebar_saas(active, ia=False):
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
    if ia:
        nav += [
            ("divider", "", ""),
            ("chip", "Provedores", "ia-provedores"),
            ("layers", "Modelos e cota", "ia-modelos"),
            ("chart", "Consumo", "ia-consumo"),
        ]
    y = 88
    for kind, label, key in nav:
        if kind == "divider":
            out += line(18, y - 6, SW - 18, y - 6, "#33517a")
            y += 14
            continue
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
        elif kind == "chip":
            out += rect(x0 + 1, y0 + 2, 14, 10, "none", color, rx=2)
            out += line(x0 + 4, y0 + 2, x0 + 4, y0 - 1, color, sw=1.2)
            out += line(x0 + 11, y0 + 2, x0 + 11, y0 - 1, color, sw=1.2)
            out += line(x0 + 4, y0 + 12, x0 + 4, y0 + 15, color, sw=1.2)
            out += line(x0 + 11, y0 + 12, x0 + 11, y0 + 15, color, sw=1.2)
        elif kind == "layers":
            out += rect(x0, y0 + 8, 14, 4, color, rx=1)
            out += rect(x0 + 2, y0 + 4, 10, 4, color, rx=1)
            out += rect(x0 + 4, y0, 6, 4, color, rx=1)
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


def toggle(x, y, on=True, w=36, h=20):
    out = rect(x, y, w, h, BLUE if on else "#c7d2dd", rx=h / 2)
    cx = x + w - h / 2 - 2 if on else x + h / 2 + 2
    out += circle(cx, y + h / 2, h / 2 - 2, "#ffffff")
    return out


def seg(x, y, w, h, options, active_idx, size=11):
    """Controle segmentado (tenant/CNPJ, overage/hard_stop, dia/mês)."""
    out = rect(x, y, w, h, CHIP, BORDER, rx=8)
    sw = w / len(options)
    for i, (label, _) in enumerate(options):
        if i == active_idx:
            out += rect(x + i * sw, y, sw, h, PANEL, BORDER, rx=8)
            out += text(x + i * sw + sw / 2, y + h / 2 + size * 0.36, label, size, NAVY, 700, "middle")
        else:
            out += text(x + i * sw + sw / 2, y + h / 2 + size * 0.36, label, size, MUTED, 600, "middle")
    return out


def bar(x, y, w, h, pct, color=BLUE, track="#e6edf5"):
    out = rect(x, y, w, h, track, rx=h / 2)
    fw = max(h, w * min(max(pct, 0.0), 1.0))
    out += rect(x, y, fw, h, color, rx=h / 2)
    return out


def alert(x, y, w, h, fg, bg, border, title, body):
    out = rect(x, y, w, h, bg, border, rx=8)
    out += warn_icon(x + 12, y + (h - 18) / 2, fg, size=18)
    out += text(x + 38, y + 20, title, 11, fg, 700)
    out += text(x + 38, y + 36, body, 9.5, INK)
    return out


def screen_ia_provedores():
    b = rect(0, 0, W, H, BG)
    b += sidebar_saas("ia-provedores", ia=True)
    b += topbar("Provedores de IA", "Control plane · Tenants · IA · Provedores",
                status=SAAS_STATUS, unit="Control plane")

    # seletor de tenant + escopo
    b += rect(CX, 80, CW, 56, PANEL, BORDER, rx=10)
    b += text(CX + 16, 102, "TENANT", 9, FAINT, 700, ls="0.8")
    b += text(CX + 16, 124, "Funerária Exemplo (grupo)", 14, INK, 700)
    b += text(CX + 280, 102, "Catálogo global · configuração por tenant ou CNPJ", 10.5, FAINT)
    b += text(CX + 280, 122, "Chave de API centralizada no cofre do admin SaaS", 10.5, FAINT)
    b += text(CX + CW - 250, 102, "ESCOPO", 9, FAINT, 700, ls="0.7")
    b += seg(CX + CW - 250, 108, 234, 26, [("Tenant", "t"), ("CNPJ", "c")], 0, size=10.5)

    # card provedores
    px, py, pw, ph = CX, 152, CW, 416
    b += rect(px, py, pw, ph, PANEL, BORDER, rx=12)
    b += text(px + 16, py + 28, "Provedores de IA", 14, INK, 700)
    b += text(px + 16, py + 46, "4 provedores no catálogo · 2 ativos nesta conta · reordenação de fallback", 10.5, FAINT)
    b += button(px + pw - 292, py + 14, 132, 30, "Testar conexão", primary=False, size=11)
    b += button(px + pw - 148, py + 14, 132, 30, "Adicionar provedor", primary=True, size=11)

    name_x = px + 16
    kind_x = px + 170
    cat_x = px + 240
    scope_x = px + 320
    fb_x = px + 430
    key_x = px + 540
    act_r = px + pw - 16
    heads = [("Provedor", name_x), ("Tipo", kind_x), ("Catálogo", cat_x),
             ("Neste escopo", scope_x), ("Fallback", fb_x), ("Chave (cofre)", key_x),
             ("Ação", act_r)]
    for label, hx in heads:
        b += text(hx, py + 74, label, 9.5, FAINT, 700, "end" if hx == act_r else "start", ls="0.6")

    rows = [
        ("openai", "llm", "ativo", "ativo", "1", True),
        ("anthropic", "llm", "ativo", "ativo", "2", False),
        ("google", "llm", "inativo", "—", "—", False),
        ("deepseek", "llm", "inativo", "—", "—", False),
    ]
    y = py + 88
    for code, kind, cat, scope, fb, expanded in rows:
        rh = 52 if expanded else 48
        b += text(name_x, y + 18, code, 12.5, INK, 700)
        b += text(name_x, y + 34, f"code {code} · 3 modelos", 9.5, FAINT)
        p, _ = pill(kind_x, y + 10, kind, MUTED, CHIP, h=20, size=9.5)
        b += p
        cat_fg, cat_bg = (OK, OK_BG) if cat == "ativo" else (MUTED, CHIP)
        p, _ = pill(cat_x, y + 10, cat, cat_fg, cat_bg, h=20, size=9.5)
        b += p
        if scope == "ativo":
            p, _ = pill(scope_x, y + 10, scope, NAVY, BLUE_SOFT, h=20, size=9.5, border=BLUE_SOFT)
            b += p
        else:
            b += text(scope_x, y + 24, scope, 11, FAINT)
        b += text(fb_x, y + 20, fb, 12, INK, 700)
        if fb != "—":
            b += text(fb_x + 22, y + 18, "↕", 11, BLUE, 700)
        b += text(key_x, y + 18, "cofre · ai/" + code, 10.5, MUTED)
        b += text(key_x, y + 34, "••••••••", 11, INK, 700, ls="1")
        b += text(act_r, y + 24, "desativar" if scope == "ativo" else "ativar", 10.5, BLUE, 600, "end")
        if expanded:
            b += text(name_x - 2, y + 8, "▾", 10, MUTED)
        else:
            b += text(name_x - 2, y + 20, "▸", 10, FAINT)
        b += line(px + 16, y + rh - 8, px + pw - 16, y + rh - 8, BORDER, opacity=0.6)
        y += rh

        if expanded:
            b += line(px + 24, y + 4, px + 24, y + 74, BORDER)
            models = [
                ("gpt-4o", "chat", "US$ 2,50 / 10,00"),
                ("gpt-4o-mini", "chat", "US$ 0,15 / 0,60"),
                ("o4-mini", "chat", "US$ 10,00 / 40,00"),
            ]
            for mcode, mkind, price in models:
                b += text(px + 40, y + 22, mcode, 11.5, INK, 600)
                b += text(px + 200, y + 22, mkind, 10.5, MUTED)
                b += text(px + pw - 16, y + 22, price + " por 1M", 10.5, MUTED, 400, "end")
                y += 26
            b += text(px + 40, y + 22, "preço de tabela por 1M tokens · só estimativa de painel", 9.5, FAINT)
            y += 30

    # rodapé do card
    b += line(px + 16, py + ph - 40, px + pw - 16, py + ph - 40)
    b += text(px + 16, py + ph - 18, "Fallback: openai → anthropic · nunca sai da allowlist · falha de um provedor desce para o próximo habilitado", 10.5, MUTED)

    # card offline + snapshot
    ox, oy, ow, oh = CX, 584, 512, 208
    b += rect(ox, oy, ow, oh, PANEL, BORDER, rx=12)
    b += text(ox + 16, oy + 28, "Offline e snapshot de enforcement", 14, INK, 700)
    b += text(ox + 16, oy + 46, "O assistente nunca consome cota sem aferição", 10.5, FAINT)
    b += rect(ox + 16, oy + 60, ow - 32, 44, WARN_BG, "#f2ddbd", rx=8)
    b += warn_icon(ox + 28, oy + 70, WARN)
    b += text(ox + 52, oy + 78, "Offline: assistente desabilitado", 11.5, WARN, 700)
    b += text(ox + 52, oy + 94, "só textos fixos, sem LLM e sem consumo de cota", 10, WARN)
    b += rect(ox + 16, oy + 114, ow - 32, 34, BLUE_SOFT, "#cfe2f5", rx=8)
    b += text(ox + 28, oy + 135, "Snapshot de IA v12 · válido até 23:40 · fail-closed ao expirar", 10.5, NAVY)
    b += text(ox + 16, oy + 170, "Sem renovação, o assistente bloqueia em vez de continuar sem teto.", 10.5, MUTED)

    # variacao: nenhum provedor ativo
    vx, vy, vw, vh = 752, 584, 324, 208
    b += text(vx, vy - 10, "VARIAÇÃO · NENHUM PROVEDOR ATIVO", 8.5, FAINT, 700, ls="0.7")
    b += rect(vx, vy, vw, vh, PANEL, BORDER, rx=12)
    b += text(vx + 16, vy + 28, "Estado do assistente", 13.5, INK, 700)
    b += rect(vx + 16, vy + 44, vw - 32, 62, DANGER_BG, "#f3c9c4", rx=8)
    b += warn_icon(vx + 28, vy + 54, DANGER, size=16)
    b += text(vx + 52, vy + 66, "Assistente desligado", 11.5, DANGER, 700)
    b += text(vx + 52, vy + 84, "Nenhum provedor ativo neste", 10, INK)
    b += text(vx + 52, vy + 98, "escopo. Ative um para ligar.", 10, INK)
    b += text(vx + 16, vy + 126, "Enquanto isso a operação segue sem IA:", 10.5, MUTED)
    b += text(vx + 16, vy + 142, "nenhum fluxo crítico depende do assistente.", 10.5, MUTED)
    b += button(vx + 16, vy + vh - 48, vw - 32, 32, "Ativar openai", primary=True, size=11)

    b += rail(
        "Control plane · Provedores de IA",
        "Admin SaaS",
        "SaaS online",
        [
            ("user", ["O fallback cai fora da", "allowlist?"]),
            ("agent", ["Nunca. A ordem só usa provedores", "habilitados no escopo. Se openai", "falhar, o run desce para", "anthropic; se ambos falharem, o", "assistente bloqueia."]),
            ("user", ["Onde fica a chave?"]),
            ("agent", ["No cofre do admin SaaS. A tela", "só mostra a referência", "ai/openai e a chave mascarada.", "Nunca a chave em si."]),
        ],
        ["Reordenar fallback", "Testar conexão", "Ativar anthropic", "Ver snapshot"],
        "Chave sempre no cofre e mascarada · sem valor fiscal · sem PII",
    )
    return b


def screen_ia_modelos():
    b = rect(0, 0, W, H, BG)
    b += sidebar_saas("ia-modelos", ia=True)
    b += topbar("Modelos e cota de tokens", "Control plane · Tenants · IA · Modelos e cota",
                status=SAAS_STATUS, unit="Control plane")

    b += rect(CX, 80, CW, 56, PANEL, BORDER, rx=10)
    b += text(CX + 16, 102, "TENANT", 9, FAINT, 700, ls="0.8")
    b += text(CX + 16, 124, "Funerária Exemplo (grupo)", 14, INK, 700)
    b += text(CX + 280, 102, "Allowlist por escopo · liga/desliga sem apagar", 10.5, FAINT)
    b += text(CX + 280, 122, "CNPJ: Floricultura Exemplo · 00.000.000/0002-00", 10.5, FAINT)
    b += text(CX + CW - 250, 102, "ESCOPO", 9, FAINT, 700, ls="0.7")
    b += seg(CX + CW - 250, 108, 234, 26, [("Tenant", "t"), ("CNPJ", "c")], 1, size=10.5)

    # allowlist
    px, py, pw, ph = CX, 152, CW, 360
    b += rect(px, py, pw, ph, PANEL, BORDER, rx=12)
    b += text(px + 16, py + 28, "Allowlist de modelos", 14, INK, 700)
    b += text(px + 16, py + 46, "6 modelos no catálogo · escopo CNPJ · 4 habilitados", 10.5, FAINT)
    b += button(px + pw - 148, py + 14, 132, 30, "Adicionar modelo", primary=True, size=11)

    cols = [px + 16, px + 230, px + 350, px + 470, px + 590]
    heads = ["Modelo", "Provedor", "Tipo", "Fallback", "Habilitado"]
    for i, head in enumerate(heads):
        b += text(cols[i], py + 74, head, 9.5, FAINT, 700, "start", ls="0.6")

    models = [
        ("gpt-4o", "openai", "chat", "1", True),
        ("claude-sonnet-4", "anthropic", "chat", "2", True),
        ("gemini-2.5-flash", "google", "chat", "3", False),
        ("text-embedding-3-large", "openai", "embedding", "1", True),
        ("deepseek-chat", "deepseek", "chat", "4", False),
        ("rerank-3", "anthropic", "rerank", "2", True),
    ]
    y = py + 88
    for code, prov, kind, fb, on in models:
        b += text(cols[0], y + 20, code, 11.5, INK, 600)
        b += text(cols[1], y + 20, prov, 11, MUTED)
        b += text(cols[2], y + 20, kind, 11, MUTED)
        b += text(cols[3], y + 18, fb, 12, INK, 700)
        b += text(cols[3] + 22, y + 16, "↕", 10, BLUE, 700)
        b += toggle(cols[4], y + 6, on=on)
        b += line(px + 16, y + 34, px + pw - 16, y + 34, BORDER, opacity=0.6)
        y += 40

    b += line(px + 16, py + ph - 34, px + pw - 16, py + ph - 34)
    b += text(px + 16, py + ph - 14, "Modelo fora da allowlist vigente faz o run falhar fechado, sem fallback fora do snapshot.", 10.5, MUTED)

    # cota + politica
    cx0, cy, cw0, ch = CX, 528, CW, 264
    b += rect(cx0, cy, cw0, ch, PANEL, BORDER, rx=12)
    b += text(cx0 + 16, cy + 28, "Cota em R$ (teto real)", 14, INK, 700)
    b += text(cx0 + 16, cy + 46, "Tokens aparecem só como estimativa de referência", 10.5, FAINT)

    # teto global
    b += text(cx0 + 16, cy + 72, "Teto global do tenant · mensal", 10.5, MUTED, 600)
    b += text(cx0 + 420, cy + 72, "R$ 5.000,00", 14, INK, 700, "end")
    b += bar(cx0 + 16, cy + 84, 404, 12, 0.62, BLUE)
    b += text(cx0 + 16, cy + 112, "Usado R$ 3.100,00", 10.5, INK, 600)
    b += text(cx0 + 420, cy + 112, "saldo R$ 1.900,00", 10.5, OK, 700, "end")

    # sub-cota
    b += text(cx0 + 16, cy + 140, "Sub-cota · Floricultura Exemplo", 10.5, MUTED, 600)
    b += text(cx0 + 420, cy + 140, "R$ 2.000,00", 14, INK, 700, "end")
    b += bar(cx0 + 16, cy + 152, 404, 12, 1.0, DANGER)
    b += text(cx0 + 16, cy + 180, "Usado R$ 2.000,00", 10.5, INK, 600)
    b += text(cx0 + 420, cy + 180, "saldo R$ 0,00", 10.5, DANGER, 700, "end")
    b += text(cx0 + 16, cy + 208, "Sub-cota só existe dentro do teto global e não pode excedê-lo.", 10, FAINT)
    b += text(cx0 + 16, cy + 224, "Sem sub-cota definida, o CNPJ usa o teto global.", 10, FAINT)

    # politica de estouro
    rx0 = cx0 + 452
    rw = cw0 - 452 - 16
    b += line(cx0 + 436, cy + 56, cx0 + 436, cy + ch - 16)
    b += text(rx0, cy + 28, "Política de estouro", 14, INK, 700)
    b += text(rx0, cy + 46, "configurável por tenant", 10.5, FAINT)
    b += seg(rx0, cy + 58, rw, 30, [("overage · excede cobrando", "o"), ("hard_stop · bloqueia", "h")], 0, size=10.5)
    b += text(rx0, cy + 104, "Exceder cobra o excedente na fatura do tenant,", 10.5, MUTED)
    b += text(rx0, cy + 120, "com aviso a cada novo run acima do teto.", 10.5, MUTED)

    b += alert(rx0, cy + 140, rw, 34, WARN, WARN_BG, "#f2ddbd",
               "Atenção · 80% do teto global", "projeção: estoura em 12 dias")
    b += alert(rx0, cy + 180, rw, 34, DANGER, DANGER_BG, "#f3c9c4",
               "Limite · sub-cota do CNPJ estourada", "100% · policy overage: cobra o excedente")

    b += rail(
        "Control plane · Modelos e cota",
        "Admin SaaS",
        "SaaS online",
        [
            ("user", ["O que acontece se o CNPJ", "estourar a sub-cota?"]),
            ("agent", ["Depende da política. Hoje é", "overage: o CNPJ continua rodando", "e o excedente entra na fatura.", "Com hard_stop, novos runs", "bloqueariam na hora."]),
            ("user", ["E os tokens?"]),
            ("agent", ["São só estimativa de referência.", "Quem governa o teto e o bloqueio", "é o valor em R$.", "O consumo vem do reporte da", "célula, nunca digitado."]),
        ],
        ["Alternar para hard_stop", "Ver allowlist do tenant", "Projeção por CNPJ"],
        "Unidade da cota é R$ · tokens só referência · sem valor fiscal",
    )
    return b


def screen_ia_consumo():
    b = rect(0, 0, W, H, BG)
    b += sidebar_saas("ia-consumo", ia=True)
    b += topbar("Consumo de tokens", "Control plane · Tenants · IA · Consumo",
                status=SAAS_STATUS, unit="Control plane")

    # filtros
    fy = 80
    b += rect(CX, fy, CW, 56, PANEL, BORDER, rx=10)
    b += text(CX + 16, 102, "Filtros", 10, FAINT, 700, ls="0.7")
    for i, label in enumerate(["Período: dia ▾", "Módulo: todos ▾", "Agente: todos ▾"]):
        fx0 = CX + 16 + i * 158
        b += rect(fx0, 108, 150, 26, PANEL, BORDER, rx=6)
        b += text(fx0 + 10, 125, label, 10.5, INK)
    b += text(CX + CW - 16, 125, "14 dias · 1.284 execuções", 10.5, FAINT, 400, "end")

    # KPIs
    metrics = [
        ("Consumo no período", "R$ 3.100,00", "62% do teto global", BLUE),
        ("Crédito restante", "R$ 1.900,00", "saldo do tenant", OK),
        ("Projeção", "12 dias", "neste ritmo, a cota estoura", WARN),
        ("Execuções", "1.284", "agente + ferramentas", NAVY),
    ]
    x = CX
    for label, value, detail, accent in metrics:
        b += kpi(x, 144, label, value, detail, accent=accent)
        x += 201 + 16

    # grafico
    gx, gy, gw, gh = CX, 248, CW, 280
    b += rect(gx, gy, gw, gh, PANEL, BORDER, rx=12)
    b += text(gx + 16, gy + 28, "Consumo em R$ por dia", 14, INK, 700)
    b += text(gx + 16, gy + 46, "custo real · tokens só como referência", 10.5, FAINT)
    b += seg(gx + gw - 190, gy + 16, 174, 26, [("dia", "d"), ("mês", "m")], 0, size=10.5)

    values = [182, 196, 210, 188, 224, 240, 205, 198, 232, 255, 248, 262, 240, 236]
    days = list(range(8, 22))
    base = gy + 214
    top = gy + 74
    max_h = base - top
    bw, gap = 47, 8
    total = len(values) * bw + (len(values) - 1) * gap
    sx = gx + (gw - total) / 2
    # grade
    for i in range(5):
        gy0 = top + i * max_h / 4
        b += line(gx + 16, gy0, gx + gw - 16, gy0, BORDER, opacity=0.5)
    b += line(gx + 16, base, gx + gw - 16, base, "#b9c6d4", sw=1.4)
    peak = max(values)
    for i, v in enumerate(values):
        bh = v / peak * max_h
        bx = sx + i * (bw + gap)
        last = i == len(values) - 1
        b += rect(bx, base - bh, bw, bh, BLUE if last else "#bcd4ec", rx=4)
        if v == peak:
            b += text(bx + bw / 2, base - bh - 8, "R$ " + str(v), 10, NAVY, 700, "middle")
        if i % 2 == 0:
            b += text(bx + bw / 2, base + 18, f"{days[i]:02d}", 9.5, FAINT, 500, "middle")
    b += text(gx + 16, gy + gh - 16, "14 dias · 08 a 21/11 · custo por dia, em R$ · pico R$ 262 · média R$ 223/dia", 10, FAINT)

    # trilha
    tx, ty, tw, th = CX, 544, CW, 284
    b += rect(tx, ty, tw, th, PANEL, BORDER, rx=12)
    b += text(tx + 16, ty + 28, "Trilha de execuções", 14, INK, 700)
    b += text(tx + 16, ty + 46, "AI_RUN · AI_TOOL_CALL · custo em R$ é o número principal", 10.5, FAINT)

    cols = [tx + 16, tx + 150, tx + 300, tx + 520, tx + 700, tx + tw - 16]
    heads = ["Quando", "Quem", "O quê", "Modelo", "Tokens (ref.)", "Custo"]
    for i, head in enumerate(heads):
        b += text(cols[i], ty + 74, head, 9.5, FAINT, 700, "end" if i == 5 else "start", ls="0.6")

    runs = [
        ("21/11 14:02", "maria.souza", "Assistente de atendimento", "gpt-4o", "1.240 / 380", "R$ 18,40"),
        ("21/11 13:47", "ronaldo", "Resumo de contrato", "gpt-4o", "820 / 210", "R$ 9,85"),
        ("21/11 11:20", "admin.saas", "Embedding de documento", "text-embedding-3-large", "42.000 / 0", "R$ 3,10"),
        ("20/11 18:44", "maria.souza", "Triagem de mensagem", "claude-sonnet-4", "2.100 / 640", "R$ 22,60"),
    ]
    y = ty + 88
    for quando, quem, oque, modelo, tokens, custo in runs:
        b += text(cols[0], y + 20, quando, 11, INK, 400)
        b += text(cols[1], y + 20, quem, 11, INK, 600)
        b += text(cols[2], y + 20, oque, 11, MUTED)
        b += text(cols[3], y + 20, modelo, 11, MUTED)
        b += text(cols[4], y + 20, tokens, 10.5, FAINT)
        b += text(cols[5], y + 20, custo, 12, INK, 700, "end")
        b += line(tx + 16, y + 32, tx + tw - 16, y + 32, BORDER, opacity=0.6)
        y += 40

    b += line(tx + 16, ty + th - 40, tx + tw - 16, ty + th - 40)
    b += text(tx + 16, ty + th - 18, "Tokens são estimativa de referência (preço de tabela por 1M). Quem governa o teto é o custo em R$.", 10.5, MUTED)

    b += rail(
        "Control plane · Consumo de IA",
        "Admin SaaS",
        "SaaS online",
        [
            ("user", ["Onde o consumo mais cresceu", "esta semana?"]),
            ("agent", ["Em embeddings de documento:", "42 mil tokens de entrada num run,", "R$ 3,10. E em triagem de", "mensagem, R$ 22,60.", "Tokens são referência; o custo", "em R$ é o número que importa."]),
            ("user", ["E se a cota estourar?"]),
            ("agent", ["Com overage, o excedente entra", "na fatura. Com hard_stop, os", "runs bloqueiam na hora. Hoje", "este tenant está em overage."]),
        ],
        ["Filtrar por módulo", "Ver projeção por CNPJ", "Exportar trilha"],
        "Consumo vem do reporte da célula · nunca digitado · sem PII",
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
        ("08-painel-saas-ia-provedores.svg", "Painel SaaS · Provedores de IA", "Admin SaaS", screen_ia_provedores()),
        ("09-painel-saas-ia-modelos-cota.svg", "Painel SaaS · Modelos e cota de tokens", "Admin SaaS", screen_ia_modelos()),
        ("10-painel-saas-ia-consumo.svg", "Painel SaaS · Consumo de tokens", "Admin SaaS", screen_ia_consumo()),
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
<header><img src="../../../assets/brand/amparius-icon.svg" alt=""/><div><h1>Mockups · Painel SaaS</h1><p>Control plane · entitlement por licença + IA · v0.1</p></div></header>
<div class="wrap">
<div class="note">Seis telas do admin SaaS: cotas do tenant, licença, auditoria e as três de IA (provedores, modelos/cota e consumo), conforme o ADR 0008. Nenhum valor fiscal; placeholders fictícios; chave sempre mascarada.</div>
<div class="grid">{cards}</div>
</div></body></html>'''
    with open(os.path.join(OUT, "index.html"), "w", encoding="utf-8", newline="\n") as fh:
        fh.write(index)

    print("galeria em", os.path.abspath(os.path.join(OUT, "index.html")))


if __name__ == "__main__":
    build()
