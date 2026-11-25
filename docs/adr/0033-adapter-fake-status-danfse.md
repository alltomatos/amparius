# ADR 0033 � Fechamento da NFS-e: adapter fake, status, rejeição e DANFSe

Status: aceito em 25/11/2026

## Contexto

A FIS-02B (#26) fechou a persistência idempotente: documento, tentativas, XML
bruto, hash e status sem sobrescrever replay. Faltava o fechamento de
comportamento da #27: adapter fake determinístico, consulta de status,
autorização/rejeição e contrato de DANFSe/PDF, ainda sem transmissão real.

Ao revisar o caminho de timeout apareceu um defeito de contrato: a tentativa
era gravada com `(tenant_id, idempotency_key)` e o reprocessamento tentava
regravar a mesma chave com outro modo/resultado. Como a tabela é append-only e
`ON CONFLICT DO NOTHING`, a reconciliação falhava com `ErrIdempotencyConflict`
e o documento permanecia pendente mesmo quando a NFS-e já existia no fisco.

## Decisão

### 1. A chave de negócio não é a chave da tentativa

`Idempotency-Key` identifica a **intenção** do pedido. Cada transporte é uma
linha append-only própria:

| Momento | `idempotency_key` da tentativa | `attempt` |
| --- | --- | --- |
| primeira emissão | a chave original | 1 |
| reenvio após ausência confirmada | `<chave>:attempt-N` | N |
| reconciliação após timeout (`GetDPSStatus` existe) | `<chave>:reconcile-N` | N |

O serviço calcula `N` a partir do histórico do documento. Assim o replay nunca
sobrescreve uma linha e nunca reenvia quando já há `authorized` ou `rejected`.

### 2. Fechamento idempotente por estado persistido

Antes de qualquer transporte, `Emit` lê o histórico do documento/competência:

- existe tentativa `authorized` → devolve a chave de acesso persistida;
- existe tentativa `rejected` → devolve o mesmo erro tipado e a mesma
  descrição oficiais;
- só então consulta `GetDPSStatus`; se a DPS já gerou NFS-e, reconcilia com a
  chave retornada; se não, emite.

### 3. Rejeição é estado terminal persistido

`400` com `ErrDocumentRejected` grava tentativa `result='rejected'`,
`http_status=400`, `response_body` com `{"codigo","descricao"}` e o corpo cru
em `response_raw`. O documento vira `rejected`. Replay devolve o mesmo erro sem
chamar o adapter.

### 4. Status é leitura pura

`Service.Status` lê `fiscal_document.status` e as tentativas append-only e
devolve `DocumentStatus`, `AttemptResult`, `Attempts`, `AccessKey`, `IDDps`,
`HTTPStatus`, `Environment`, rejeição e instante da última tentativa. Não
transmite, não reenvia e exige tenant no contexto (RLS).

### 5. DANFSe/PDF é contrato, não emissão

`DANFSeRenderer` recebe somente dados de uma NFS-e autorizada e persistida.
O `FakeDANFSeRenderer` produz um PDF 1.4 mínimo, determinístico e declarado
como **sem validade fiscal**, com hash SHA-256. Não há assinatura, transmissão,
object storage nem DANFSe oficial nesta fatia; isso depende da NFS-e oficial
retornada pelo ambiente real.

### 6. Gaps de XSD antigos permanecem classificados

O builder estrutural da fatia 01 continua incompatível com o XSD oficial e
segue classificado por `ErrXSDIncompatible`/`ClassifyXSDIncompatibility`. O
leiaute vigente NT 009 (pacote 20261020) é o usado por `BuildDPSOficial`; o
pacote 20260209 permanece apenas como histórico. Nenhum workaround de XSD foi
introduzido.

### 7. Sem migration nesta fatia

Nenhuma coluna ou tabela nova. As chaves derivadas são valores; o fechamento
usa as colunas já existentes (`result`, `http_status`, `response_body`,
`response_raw`, `chave_acesso`, `dps_id`, `request_hash`).

## Consequências

- Timeout + replay não duplicam emissão e fecham o documento quando o fisco já
  tem a NFS-e.
- O histórico cresce uma linha por transporte, o que é correto para auditoria
  fiscal; replay de pedido já fechado não cria linha.
- A numeração de tentativa é calculada no serviço. Duas execuções concorrentes
  do mesmo documento podem colidir na unicidade
  `(tenant_id, document_id, competence, attempt)` e falhar fechado; não há
  autorização offline nem duplicação silenciosa.
- Continua proibido transmitir de verdade, cancelar/substituir, importar
  `tax_rule` oficial ou gerar DANFSe oficial sem os gates da FIS-02D+.

## Alternativas rejeitadas

- **Atualizar a linha `pending` para `authorized`**: viola append-only e a
  migration 0034, que congela o histórico de tentativa.
- **Reutilizar a chave original no reenvio**: colide com
  `UNIQUE (tenant_id, idempotency_key)` e transforma reconciliação em conflito.
- **Marcar `authorized` quando o adapter dá timeout**: criaria autorização
  offline inexistente na NFS-e nacional (ADR 0010, decisão 5).
- **Gerar DANFSe real a partir do JSON do fake**: inventaria documento fiscal
  sem XML oficial; rejeitado em favor do contrato + renderer declaradamente
  não fiscal.

## Verificação

`go test ./internal/modules/fiscal/...` com PostgreSQL real e `amparius_app`
(sem `SUPERUSER`/`BYPASSRLS`). Casos novos:

- `TestServicoTimeoutReconciliaSemDuplicarEmissao`
- `TestServicoRejeicaoPersisteStatusConsultavel`
- `TestServicoDANFSeFakeDeterministico`
- `TestFakeDANFSeDeterministico` / `TestFakeDANFSeRejeitaEntradaIncompleta`

Contrato: `docs/contracts/fiscal-nfse-status-danfse-v1.md`.
