# FIS-02C — adapter fake, status e fechamento da NFS-e

Data: 2026-11-25
Issue: #27 (parent #5 MVP-FIS-02)
Estado: **entregue no working tree; sem commit/push**

## Resultado

| Item | Status | Evidência |
| --- | --- | --- |
| Adapter fake determinístico (201/400/timeout) | **PASS** | `internal/modules/fiscal/nfse/fake.go`, `fake_test.go` |
| Timeout não duplica emissão | **PASS** | `TestServicoTimeoutReconciliaSemDuplicarEmissao` |
| Status persistido e consultável | **PASS** | `Service.Status` + `TestServicoRejeicaoPersisteStatusConsultavel` |
| Autorização fecha documento | **PASS** | `TestServicoTimeoutReconciliaSemDuplicarEmissao` |
| Rejeição persistida e consultável | **PASS** | `TestServicoRejeicaoPersisteStatusConsultavel` |
| Contrato DANFSe/PDF sem transmissão | **PASS** | `danfse.go`, `TestFakeDANFSe*`, `TestServicoDANFSeFakeDeterministico` |
| Gaps XSD antigos classificados | **PASS** | `ErrXSDIncompatible`; NT 009 em `official.go` |
| Transmissão real | **NÃO LIBERADA** | fora do escopo desta fatia |
| `tax_rule` oficial | **AUSENTE** | nenhuma alíquota/código importado |

## O que foi entregue

### 1. Fechamento idempotente sem colisão de chave

A `Idempotency-Key` passou a identificar a intenção, e cada transporte usa uma
chave append-only própria:

- primeira emissão: `<chave>`;
- reenvio após ausência confirmada: `<chave>:attempt-N`;
- reconciliação após timeout: `<chave>:reconcile-N`.

Antes de emitir, `Emit` lê o histórico do documento/competência. Autorização ou
rejeição já persistida encerra o pedido sem novo transporte. Isso elimina o
`ErrIdempotencyConflict` que ocorria quando a linha `pending` do timeout era
regravada como `authorized` com modo/resultado diferentes.

### 2. Rejeição tipada e persistida

`EmitDPS` devolvendo `*ErrDocumentRejected` grava uma tentativa `rejected` com
`http_status=400`, `response_body` `{"codigo","descricao"}` e `response_raw`
cru. O documento vira `rejected`. Replay devolve o mesmo código/descrição sem
chamar o adapter. Antes, qualquer erro caía em `pending`.

### 3. Status consultável

`Service.Status(ctx, StatusInput{DocumentID, Competence})` lê documento e
tentativas append-only e devolve `DocumentStatus`, `AttemptResult`, `Attempts`,
`AccessKey`, `IDDps`, `HTTPStatus`, `Environment`, `Rejection` e
`LastAttemptAt`. É leitura pura, exige tenant e não transmite.

### 4. Contrato DANFSe/PDF

`DANFSeRenderer` + `FakeDANFSeRenderer` produzem PDF 1.4 mínimo,
determinístico, com hash SHA-256 e texto explícito “DANFSe simulada — sem
validade fiscal”. `Service.RenderDANFSe` só aceita documento `authorized`;
caso contrário `ErrDANFSeNotAuthorized`. Sem assinatura, object storage ou
transmissão.

### 5. Gaps mantidos e classificados

O builder estrutural da fatia 01 continua incompatível com o XSD oficial,
classificado por `ErrXSDIncompatible`/`ClassifyXSDIncompatibility`. O leiaute
vigente NT 009 (pacote 20261020) segue como caminho oficial em
`BuildDPSOficial`; o pacote 20260209 permanece histórico, sem workaround.

## Evidência reproduzível

```powershell
$env:AMPA_TEST_POSTGRES_DSN = 'postgres://amparius_app:...@127.0.0.1:5432/amparius?sslmode=disable'
$env:AMPA_TEST_POSTGRES_ADMIN_DSN = 'postgres://amparius:...@127.0.0.1:5432/amparius?sslmode=disable'
go test ./internal/modules/fiscal/... -count=1
# ok internal/modules/fiscal
# ok internal/modules/fiscal/nfse
```

Saída observada em 2026-11-25:

```
ok  github.com/alltomatos/amparius/internal/modules/fiscal       4.608s
ok  github.com/alltomatos/amparius/internal/modules/fiscal/nfse  2.962s
```

Casos novos, com `-v`:

```
PASS TestFakeDANFSeDeterministico
PASS TestFakeDANFSeRejeitaEntradaIncompleta
PASS TestFakeDANFSeContextoCancelado
PASS TestServicoTimeoutReconciliaSemDuplicarEmissao
PASS TestServicoRejeicaoPersisteStatusConsultavel
PASS TestServicoDANFSeFakeDeterministico
```

O teste de timeout prova `fake.EmitCount == 1` após a reconciliação e após novo
replay, com histórico `pending → authorized` e documento `authorized`.

Outras verificações executadas:

```powershell
gofmt -l internal/modules/fiscal/nfse   # vazio
go vet ./internal/modules/fiscal/...    # sem saída
go build ./...                          # sem saída
goose ... up                            # "no migrations to run. current version: 35"
git diff --check                        # sem erro de whitespace (apenas aviso CRLF)
```

O `goose` instalado é v3.24.3 e não expõe o subcomando `validate`; a versão
aplicada foi conferida por `goose status` (todas as migrations até a 0035) e por
`goose up` sem migração pendente.

## Limites explícitos

- Nenhuma transmissão real, certificado ou procuração.
- Nenhuma autorização offline: timeout continua `pending`.
- Nenhum `tax_rule` oficial importado; goldens do motor seguem sintéticos.
- DANFSe é contrato + renderer fake, sem validade fiscal.
- Duas execuções concorrentes do mesmo documento podem colidir na unicidade
  `(tenant_id, document_id, competence, attempt)` e falhar fechado; não há
  duplicação silenciosa.
- Cancelamento/substituição (`RegisterEvent`), importação oficial de tributos e
  emissão em produção permanecem em fatias posteriores.

## Arquivos

- `internal/modules/fiscal/nfse/service.go`
- `internal/modules/fiscal/nfse/danfse.go`
- `internal/modules/fiscal/nfse/danfse_test.go`
- `internal/modules/fiscal/nfse/service_status_test.go`
- `docs/adr/0033-adapter-fake-status-danfse.md`
- `docs/contracts/fiscal-nfse-status-danfse-v1.md`
- `docs/contracts/fiscal-nfse-persistence-v1.md`
- `docs/ENGINEERING.md`
- `docs/userguide/fiscal-nfse.md`
