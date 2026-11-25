# ADR 0032 — Persistência idempotente da NFS-e

Status: aceito · 25/11/2026

## Contexto

O fluxo nacional da NFS-e precisa sobreviver a timeout, replay e falha de
transporte sem perder o documento enviado nem substituir a resposta recebida.
As migrations fiscais anteriores já guardavam metadados, mas não persistiam os
bytes do XML e não distinguiam uma repetição legítima de uma mesma
`Idempotency-Key` reutilizada para outro XML.

## Decisão

- `fiscal.fiscal_document.xml_raw` guarda os bytes do XML enviado; `xml_hash`
  guarda o SHA-256 hexadecimal minúsculo correspondente.
- `fiscal.emission_attempt.request_xml` e `response_raw` preservam os bytes de
  cada tentativa. As linhas de tentativa são append-only e nunca são
  atualizadas nem apagadas pelo papel da aplicação.
- O replay usa `(tenant_id, idempotency_key)`. Se a chave já existir, a linha
  original é devolvida sem sobrescrever resposta, XML ou metadados. Documento,
  competência, modo, `dps_id` e `request_hash` incompatíveis retornam
  `ErrIdempotencyConflict`.
- O registro autorizado atualiza, na mesma transação, o status do documento e
  o XML bruto. Um documento `authorized` exige XML e hash; depois de preenchido,
  o XML/hash não podem ser substituídos.
- O status de uma emissão sem resposta permanece pendente/draft; não há
  autorização offline e nenhuma chamada real ao fisco é feita nesta fatia.

As migrations `0034_fiscal_emission_persistence.sql` e
`0035_fiscal_authorized_xml_guard.sql` são forward-only. A aplicação usa
`amparius_app` sem `SUPERUSER`/`BYPASSRLS`; limpeza de testes é administrativa e
não participa do caminho de produção.

## Consequências

O banco passa a ser a fonte auditável dos bytes enviados e recebidos. Object
storage continua sendo a fronteira para retenção de arquivos maiores; a coluna
bruta desta fatia mantém a evidência necessária para replay e validação. O
adapter permanece fora do escopo: não há transmissão real nem regra tributária
inventada.

## Verificação

`TestDocumentsRLSIdempotencyAndAppendOnly` prova RLS entre tenants, replay,
conflito de chave, bytes brutos e bloqueio de UPDATE. O teste de serviço prova
que uma autorização fecha o documento e que timeout não o autoriza.
