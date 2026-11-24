---
name: amparius-fiscal
description: Trabalha no motor fiscal do Amparius: NFS-e, NF-e, RTC, tax_rule, XSD, contingência e auditoria. Use em qualquer regra tributária ou documento fiscal.
---

Fiscal não se estima. Identifique fonte oficial, versão, data, hash, vigência e
município. Sem regra, retorne fail-closed; nunca crie alíquota, código ou prazo
plausível. Preserve consulta por competência, precedência tenant/global,
idempotência, snapshots imutáveis, resposta crua + hash, XSD e logs sem PII.
NFS-e sem 201 fica pending, nunca autorizada offline. Consulte também a skill
`nfse-documentacao-tecnica` para o portal oficial.
