# Contrato v1 — Status e DANFSe da NFS-e

Complementa `fiscal-nfse-persistence-v1.md` com o fechamento da FIS-02C
(adapter fake, consulta de status e contrato de impressão). Não autoriza
transmissão real, não cria regra tributária e não gera documento fiscal.

## Chaves de tentativa

A `Idempotency-Key` do pedido identifica a intenção, não o transporte. Cada
tentativa append-only usa:

| Situação | Chave persistida |
| --- | --- |
| primeira emissão | `<chave>` |
| reenvio após ausência confirmada | `<chave>:attempt-N` |
| reconciliação após timeout | `<chave>:reconcile-N` |

`N` é o próximo número livre do documento/competência. O serviço nunca
sobrescreve linha de tentativa; replay de pedido já `authorized`/`rejected` não
fecha nova tentativa.

## `Emit` — desfechos

| Desfecho | `ServiceResult.Status` | Erro |
| --- | --- | --- |
| autorizado (`201`) | `authorized` | `nil` |
| timeout/indisponível | `pending` | `ErrAdapterUnavailable` |
| rejeitado (`400`) | `rejected` | `*ErrDocumentRejected` |
| DPS inválida | — | `ErrInvalidDPS` |

Em `rejected`, `ServiceResult` carrega `RejectionCode` e
`RejectionDescription` oficiais. Em `pending`, nenhum documento vira
`authorized`; a contingência é fila, nunca autorização.

## `Status` — leitura pura

Entrada: `document_id` (UUID opaco) + `competência` (data). Exige tenant no
contexto; RLS continua valendo. Resposta:

| Campo | Origem |
| --- | --- |
| `DocumentStatus` | `fiscal.fiscal_document.status` |
| `AttemptResult` | última tentativa por `attempt` |
| `Attempts` | quantidade de linhas append-only |
| `AccessKey` | `chave_acesso` autorizada |
| `IDDps` | `dps_id` |
| `HTTPStatus` | `http_status` |
| `Environment` | `ambiente` |
| `Rejection` | `response_body` da rejeição (`codigo`, `descricao`) |
| `LastAttemptAt` | `attempted_at` |

A consulta não chama o adapter e não altera estado. Documento inexistente ou de
outro tenant falha fechado (`ErrDocumentNotFound`/RLS).

## DANFSe/PDF

Entrada (`DANFSeInput`): documento + competência. O serviço exige documento com
status `authorized` e tentativa autorizada; caso contrário retorna
`ErrDANFSeNotAuthorized`. Saída (`DANFSeDocument`):

| Campo | Conteúdo |
| --- | --- |
| `ContentType` | `application/pdf` |
| `Bytes` | PDF determinístico |
| `Hash` | SHA-256 dos bytes |
| `AccessKey` / `IDDps` / `Competence` / `IssuedAt` | metadados persistidos |

O `FakeDANFSeRenderer` gera um PDF 1.4 mínimo com o texto “DANFSe simulada —
sem validade fiscal”. O contrato `DANFSeRenderer` permite injetar um renderer
oficial numa fatia futura; nenhum renderer desta fatia transmite, assina ou
publica em object storage.

Erros estáveis: `ErrDANFSeNotAuthorized`, `ErrInvalidDANFSeRequest`,
`ErrStatusUnavailable`.

## Gaps explicitamente mantidos

- Sem transmissão real, certificado ou procuração.
- Sem cancelamento/substituição (`RegisterEvent` continua reservado).
- Sem `tax_rule` oficial importada.
- Builder estrutural da fatia 01 continua incompatível com o XSD oficial e é
  classificado por `ErrXSDIncompatible`; o leiaute vigente NT 009 é o usado por
  `BuildDPSOficial`.
- DANFSe oficial depende da NFS-e oficial e fica fora desta fatia.
