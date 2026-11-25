# Contrato v1 — auditoria e privacidade LGPD

Este contrato descreve a API interna `internal/platform/audit`. Ele não é uma
autorização para registrar PII: a trilha guarda referências opacas e códigos.

## Auditoria

`audit.Repository.Append(ctx, Entry)` exige `tenant_id` no contexto. O usuário
é derivado do principal OIDC (`UserID`) quando presente; `actor_user_id`
informado não pode divergir dele. `company_id`, correlação e trace também são
derivados do contexto quando disponíveis.

Campos obrigatórios: `action`, `resource_type`, `outcome` e
`retention_until`. `resource_id` é UUID opaco opcional. `outcome` é
`success`, `denied` ou `failure`. `detail` é objeto JSON pequeno, apenas com
códigos, contagens e referências opacas.

Cada linha é tenant-scoped, RLS e append-only. Uma alteração de estado de
auditoria é uma nova linha, nunca `UPDATE` ou `DELETE`.

## Consentimento

`RecordConsent` grava `subject_type` + `subject_id` opaco, `purpose_code`,
`legal_basis_code`, `status`, evidência opcional/obrigatória para estados de
concessão ou retirada e `retention_until`. Os status são `granted`,
`withdrawn`, `denied` e `not_required`.

`purpose_code`, `legal_basis_code` e prazos são política configurada pelo
tenant e devem ser aprovados pelo responsável de privacidade/DPO. O contrato
não presume uma base legal nem um número de dias. Fixtures de teste não são
política de produção.

O registro cria, na mesma transação, `privacy.consent_record` e uma linha
`audit.log` com `privacy.consent.recorded`.

## Não permitido

Não registrar nome, e-mail, CPF/CNPJ, telefone, endereço, texto livre,
segredos, token, payload HTTP ou XML fiscal em `detail`, métricas, traces ou
logs. A validação Go e a trigger PostgreSQL devem permanecer ativas.
