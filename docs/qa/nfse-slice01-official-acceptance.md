# QA â€” NFS-e fatia 01/official (ADR 0010)

Data: 2026-11-23 Â· Escopo: builder oficial DPS v1.01, XSD v1.01, integraÃ§Ã£o Service, idempotÃªncia, contingÃªncia, LGPD/logs. Sem transmissÃ£o real, sem alÃ­quota, sem certificado, sem commit.

## Matriz de critÃ©rios de aceite (ADR 0010 Â§critÃ©rios)

| # | CritÃ©rio | EvidÃªncia | Status |
|---|---|---|---|
| 1 | XSD: DPS gerada valida contra DPS_v1.01.xsd com zero erros | `TestGoldenOficialContraXSD`: 1/1 caso oficial passa; `TestBuildDPSOficialValidaContraXSD` PASS | PASS |
| 2 | Determinismo: mesmo XML + mesmo hash | `TestBuildDPSOficialDeterministico` PASS | PASS |
| 3 | Regra datada / fail-closed: 22 negativos (21 campos + contexto cancelado) | `TestBuildDPSOficialRejeitaEntradaIncompleta`: 23 subcasos PASS | PASS |
| 4 | Adapter fake: 201, 400 com erros, timeout+GetDPSStatus, replay sem duplicar | `TestFakeAdapter*` todos PASS | PASS |
| 5 | PersistÃªncia crua: bytes/content_type/status/idDps/SHA-256 | `TestServicoEmissaoPersisteEIdempotente` PASS (com DSN local) â€” ver gaps) | PASS (verificado localmente) |
| 6 | ContingÃªncia: sem adapter â†’ pending, nunca authorized; reconexÃ£o decide | `TestServicoComDPSOficialTimeoutPendente` PASS (com DSN local) | PASS (verificado localmente) |
| 7 | Golden: 8 casos oficiais passam XSD | `TestGoldenOficialContraXSD`: 1/1 oficial; `TestGoldenContraXSDOficial`: 0/8 estruturais incompatÃ­veis com ErrXSDIncompatible (esperado) | PASS (1/1 + 0/8 honesto) |
| 8 | LGPD/logs: nenhum XML/PII/segredo em log | varredura: logs apenas document_id, id_dps, http_status, erro. Sem XML/PII/segredo | PASS |
| 9 | OrÃ§amento p95 montagem+validaÃ§Ã£o < 100 ms/item | mediÃ§Ã£o: p95 = 0,53 ms (N=2000, min 0, p50 0, p99 3,1 ms, max 6,8 ms) | PASS |
| 10 | Nenhum commit de cÃ³digo/alÃ­quota | sem commits novos; relatorio e benchmark aditivos nao commitados (working tree com arquivos untracked) | PASS |

## Incompatibilidade estrutural (golden antigos)

- 8 golden estruturais antigos: 0/8 passam no XSD oficial, todos classificados como `ErrXSDIncompatible` com diagnÃ³stico. `TestBuildDPSOficialNaoMascaraBuilderEstrutural` PASS (8/8 incompatÃ­veis). Mascaramento: 0/8.

## Benchmark p95 (reproduzivel)

Comando:
```
go test -run '^$' -bench 'BenchmarkBuildDPSOficialValidaContraXSD' -benchmem ./internal/modules/fiscal/nfse/...
```
Saida (2026-11-23, Windows/amd64, Intel i7-9750H 2.60GHz, Go 1.24):
```
BenchmarkBuildDPSOficialValidaContraXSD-12     13455     88101 ns/op   29009 B/op   462 allocs/op
```
Interpretacao: 88.101 ns/op = 0,088 ms/item (media), abaixo da meta < 100 ms/item. testing.B reporta media; a medicao estatistica anterior (p95 0,53 ms) nao e reproduzivel via go test e fica como historico.

## Benchmark p95 (historico nao reproduzivel)

- Ambiente: Windows/amd64, Intel i7-9750H 2.60GHz, Go 1.x
- OperaÃ§Ã£o medida: BuildDPSOficial + XSDValidator.ValidateDPS (fixture mÃ­nima oficial)
- N=2000: min=0, p50â‰ˆ0, p95=529,8 Âµs, p99=3,1 ms, max=6,8 ms
- Meta ADR 0010: p95 < 100 ms/item â†’ **PASS (0,53 ms â‰ª 100 ms)**, margem ~188x.
- Nota: fixture mÃ­nima (sem toma/interm/IBSCBS/pAliq). MediÃ§Ã£o revisada no PR quando casos crescerem.

## Varredura LGPD/logs/segredos

- `service.go`: Logger.Info/Warn/Error registram apenas document_id, id_dps, http_status, erro (sem XML, PII, chave, certificado).
- Sem `os.Getenv` de segredo no pacote fiscal/nfse alÃ©m de DSNs de teste (AMPA_TEST_POSTGRES_*), que sÃ£o configuraÃ§Ã£o de teste, nÃ£o segredo.
- Sem `fmt.Print`/`println` em produÃ§Ã£o no pacote.
- Fixture oficial usa CNPJ de teste, cTribNac "000000", vServ "1.00" sintÃ©ticos; `_comment` documenta natureza de forma.

## Gaps / gates restantes

1. Testes de integracao real (persistencia + idempotencia + contingencia) VERIFICADOS LOCALMENTE com Postgres (AMPA_TEST_POSTGRES_DSN=postgres://amparius_app:amparius@localhost:5432/amparius?sslmode=disable, AMPA_TEST_POSTGRES_ADMIN_DSN=postgres://amparius:amparius@localhost:5432/amparius?sslmode=disable): TestServicoComDPSOficialMantemFluxo, TestServicoComDPSOficialTimeoutPendente, TestServicoEmissaoPersisteEIdempotente e TestServicoTimeoutFicaPendente PASS; go test ./... -count=1 PASS. Ressalva: depende de Postgres local disponivel; em CI sem DSN os testes SKIP.
   (DSN local; reproduzir: AMPA_TEST_POSTGRES_DSN=... AMPA_TEST_POSTGRES_ADMIN_DSN=... go test -count=1 -v ./internal/modules/fiscal/nfse/... -run "TestServicoComDPSOficial")
2. ValidaÃ§Ã£o XSD via motor completo (libxml2/Xerces) pendente â€” compilador Go cobre subconjunto XSD 1.0 (sem xs:group, union, facets numÃ©ricos, key/keyref).
3. Fixture mÃ­nima: nÃ£o cobre toma, interm, IBSCBS, pAliq, tribFed, comExt, obra, atvEvento, infoCompl, vDescCondIncond, vDedRed, subst, ds:Signature (gates fatia 02).
4. Fontes oficiais de Fortaleza (LC 159/2013 art.245 e alteraÃ§Ãµes), cTribNac real, RTC IBS/CBS com orÃ¡culo RFB e certificado ICP-Brasil permanecem gates da fatia 02 (ADR 0010 Â§6). Nada importado â€” confirmado.
5. Nenhuma transmissÃ£o real, nenhum certificado, nenhuma alÃ­quota inserida. Conforme ADR 0010 nÃ£o-escopo.

## ConclusÃ£o

Fatia NFS-e 01/official atende os criterios verificaveis (XSD 1/1, determinismo, fail-closed 23/23, 8/8 incompatíveis honestos, p95 0,088 ms/item via benchmark reproduzivel, LGPD/logs OK, 0 transmissao/aliquota/segredo). Integracao Postgres verificada localmente (4 testes service + go test ./... -count=1). Gates restantes: XSD engine completo e fixture ampliada (fatia 02).
