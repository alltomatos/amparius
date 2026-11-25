# Contrato v1 — Persistência da emissão NFS-e

Este contrato descreve a trilha persistente da emissão no Padrão Nacional. Ele
não autoriza transmissão real nem substitui as regras oficiais datadas do
motor fiscal.

## Chave e replay

Cada tentativa exige uma `Idempotency-Key`, persistida como
`fiscal.emission_attempt.idempotency_key` e única por tenant. Repetir a mesma
chave devolve a tentativa original. Reutilizar a chave para outro documento,
competência, `dps_id`, modo ou `request_hash` retorna
`ErrIdempotencyConflict`/`409 IDEMPOTENCY_CONFLICT` na borda.

## Bytes e hashes

| Registro | Conteúdo | Integridade |
| --- | --- | --- |
| `fiscal.fiscal_document` | XML bruto enviado | `xml_hash` = SHA-256 do XML |
| `fiscal.emission_attempt` | `request_xml` e `response_raw` | `request_hash`/`response_hash` |

Os bytes são preservados no replay. O XML do documento e a resposta da tentativa
são imutáveis depois de persistidos; a aplicação não atualiza nem remove linhas
de tentativa.

## Status

Uma resposta HTTP `201` persiste a tentativa como `authorized` e fecha o
documento como `authorized` na mesma transação. Timeout, indisponibilidade ou
ausência de resposta deixam a tentativa `pending` e o documento não autorizado.
`authorized` sem XML bruto e hash é rejeitado pelo banco.

## Isolamento

Todas as tabelas são tenant-scoped e protegidas por RLS com `app.tenant_id`.
Testes de integração usam `amparius_app` sem `SUPERUSER`/`BYPASSRLS`; o papel
administrativo é usado somente para fixtures e cleanup verificável.
