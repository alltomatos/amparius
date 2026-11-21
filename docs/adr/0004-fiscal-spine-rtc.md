# ADR 0004 — Espinha dorsal fiscal RTC-first

Status: aceito · 21/11/2026

## Contexto

O Brasil está na transição da Reforma Tributária do Consumo (LC 214/2025): CBS, IBS e Imposto Seletivo passam a conviver com ICMS/ISS/PIS/COFINS até 2033. Um ERP novo que nasça só com o modelo antigo vai precisar de retrofit caro. A Receita Federal publica calculadoras oficiais da RTC e um gerador de DANFSe.

## Decisão

- O módulo `fiscal` é a espinha dorsal: todo fato gerador relevante aponta para um documento fiscal.
- O motor calcula por **item**, com tabelas **datadas** (`valid_from`, `valid_to`) e resultado reproduzível (entrada + regra + hash).
- Duas trilhas de regime: **geral** e **Simples Nacional**.
- RTC é cidadão de primeira classe: CBS, IBS e IS com cClassTrib; legado (ICMS/ISS/PIS/COFINS) convive por data de competência.
- XML é validado contra XSD oficial antes de transmitir; retorno é persistido cru.
- DANFSe/DANFE é gerado a partir do XML autorizado, com serviço próprio e possibilidade de sidecar.
- Calculadoras da RFB são usadas como **oráculo de teste** e referência de contrato, não copiadas.

## Consequências

- Nenhuma alíquota no código: tudo vem de tabela versionada com origem e data.
- Testes golden por regime, UF/município e data de competência.
- Toda emissão tem trilha completa (XML, protocolo, tentativa, hash) para auditoria e defesa.
- A reforma vira vantagem competitiva: o produto acompanha a transição sem reescrita.
