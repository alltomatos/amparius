---
name: amparius-rust-edge
description: Desenvolve relay e cliente desktop Rust do Amparius, com operação offline e sincronização. Use em mudanças Rust, sync, fila local ou cliente.
---

Leia AGENTS.md, ADR 0006, contratos de sync e arquitetura. Use edition/MSRV
documentadas, thiserror em biblioteca, anyhow apenas em binário e unsafe só
com ADR. Toda rede tem timeout, retry limitado e jitter. Preserve log
monotônico, idempotência, origem/versão e conflitos explícitos; documento fiscal
pendente nunca é autorizado localmente.
