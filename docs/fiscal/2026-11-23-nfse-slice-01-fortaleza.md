# Fatia NFS-e 01 — Fortaleza (brief para Engenharia Go/Dados)

Status: recomendação técnica · 23/11/2026 · sem código, sem commit, sem alíquota criada.
Escopo: primeira fatia emissão NFS-e. Município inicial: **Fortaleza/CE (IBGE 2304400)**, default do projeto.

## 1. Decisão recomendada

1. O adaptador inicial é do **Padrão Nacional da NFS-e** (DPS → Sefin Nacional), não do emissor municipal Ginfes.
2. O mesmo adaptador serve qualquer município no Emissor Nacional; Fortaleza entra como **primeiro parâmetro municipal**, não como código específico.
3. O primeiro ambiente é **produção restrita** (`sefin.producaorestrita.nfse.gov.br`); produção só depois dos gates da seção 8.
4. A fatia 01 entrega **port + fake adapter + DPS + XSD + golden + persistência crua**, com **zero transmissão real**.
5. Ginfes v3 (`iss.fortaleza.ce.gov.br/grpfor-iss/ServiceGinfesImplService`) fica fora desta fatia: só serve competências antigas e cancelamento/substituição de notas já emitidas no emissor municipal.
6. Fortaleza **não fecha o caminho de emissão real com segurança documental hoje**: a fonte legal existe, mas as alíquotas de ISS do município não estão parametrizadas na base nacional (seção 3). O menor contrato seguro é o item 4.

## 2. Fatos que sustentam a decisão

- **Comunicado SEFIN nº 06/2026** (republicado em 28/10/2026): migração obrigatória ao Emissor Nacional — ME/EPP do Simples em **01/11/2026**; demais contribuintes em **01/12/2026**. O emissor municipal permanece só para competências anteriores, cancelamento/substituição de notas municipais, EFS-e e DAM.
- **Padrão Nacional** está documentado e versionado: XSD v1.01 (09/02/2026), Anexo I DPS/NFS-e v1.01, Anexo B NBS2 v1.01 (22/01/2026), Anexo C IndOp IBS/CBS v1.01, manual de API do Emissor Público v1.2 (out/2025).
- **Fortaleza não consta** no arquivo oficial "Alíquotas de ISSQN" de 03/09/2026: 0 linhas para o código 2304400 (155 dos 184 municípios do CE estão no arquivo). Logo, a API de parâmetros municipais não é fonte suficiente para o ISS de Fortaleza.
- A fonte legal municipal existe: **LC 159/2013 (Código Tributário), art. 245**, alterado por LC 241/2017, LC 367/2023, LC 430/2025 e LC 452/2025; a SEFIN publica a lista CNAE × Lista de Serviços e o Anexo I com alíquotas.
- RTC: LC 214/2025 e Nota Técnica SE/CGNFS-e nº 007/2026; Simples Nacional está dispensado de destaque/recolhimento de IBS/CBS em 2026 (art. 348, III, "c", LC 214/2025), conforme o próprio comunicado da SEFIN.

## 3. Fronteira do slice

**Dentro:**
- montagem da DPS a partir do documento/itens já modelados;
- validação local contra XSD oficial + assinatura XMLDSig (interface, sem certificado real);
- adapter fake determinístico para testes;
- persistência do XML, retorno cru, hash e trilha de tentativa;
- idempotência por `(tenant_id, idempotency_key)` e por `idDps`;
- golden files por regime, serviço e competência.

**Fora:**
- transmissão real, certificado, procuração eletrônica;
- cancelamento/substituição (contrato previsto, implementação depois);
- DAM, EFS-e, Ginfes v3, NF-e/NFC-e;
- importação massiva de alíquotas.

## 4. Contrato mínimo do adapter (port)

O adapter **não calcula tributo**. Recebe valores já calculados e persistidos em `tax_calc`; devolve transporte e resposta.

| Operação | Entrada | Saída | Observações |
|---|---|---|---|
| `EmitDPS` | tenant/company/establishment, competência, `idDps`, DPS XML assinado, chave de idempotência | status HTTP, `chaveAcesso`, XML da NFS-e, corpo cru, hash, `idDps`, ambiente | síncrono; 201 = autorizada; 4xx = rejeição com lista de erros |
| `GetNFSByKey` | `chaveAcesso` | XML/corpo cru + hash | consulta; idempotente |
| `GetDPSStatus` | `idDps` | existe/não existe + `chaveAcesso` quando houver | recuperação após timeout; **antes** de reenviar |
| `RegisterEvent` | `chaveAcesso`, evento assinado | protocolo/corpo cru + hash | fora da fatia 01; contrato reservado |
| `MunicipalParameters` | código IBGE, código de serviço, competência | alíquota/retenções/benefícios oficiais | usado só para conferência; não substitui `tax_rule` |
| `ValidateDPS` | XML | resultado XSD | local, sem rede |

Regras transversais:
- `context.Context` primeiro; timeout e retry limitado com jitter **apenas** em leituras idempotentes.
- Emissão só é repetida após `GetDPSStatus` confirmar que a DPS não gerou NFS-e.
- Erros distintos: `ErrAdapterUnavailable`, `ErrDocumentRejected` (com código/descrição oficiais), `ErrIdempotencyConflict`.
- Nenhum segredo, certificado ou PII em log; XML e retorno vão para object storage/colunas, não para log.

## 5. XSD e golden

- Validar localmente com `DPS_v1.01.xsd`, `NFSe_v1.01.xsd`, `evento_v1.01.xsd`, `pedRegEvento_v1.01.xsd` e `xmldsig-core-schema.xsd` (pacote oficial v1.01 de 09/02/2026; namespace `http://www.sped.fazenda.gov.br/nfse`).
- A DPS deve ser validada **antes** de qualquer chamada; falha de XSD é erro de construção, não rejeição do fisco.
- Golden mínimo (entrada + DPS canônica + resultado XSD + resposta simulada):
  1. Serviço funerário 25.01, regime geral, tomador PF, ISS próprio;
  2. Serviço 25.02 (cremação), regime geral;
  3. Serviço 25.03 (plano funerário), Simples Nacional;
  4. Serviço 25.04 (jazigo/cemitério), Simples Nacional;
  5. Tomador PJ com retenção de ISS;
  6. Competência 2026 com regras RTC de transição;
  7. Rejeição 400 com lista de erros;
  8. Timeout seguido de `GetDPSStatus` com nota já gerada.
- Valores de RTC conferidos contra as calculadoras oficiais da RFB como oráculo; nunca copiadas para o código.
- Exemplo de âncora oficial: no Anexo I da SEFIN, os itens 25.01–25.04 constam a 5% (PDF capturado em 23/11/2026, SHA-256 `1a394719ff8f87309acb3146ed82cb30d8d3a4f2dcda3ce945132ef6dff53e03`); usar apenas após conferência com a LC 159/2013 art. 245 e alterações até a LC 452/2025.

## 6. Contingência

- NFS-e nacional é **síncrona**: não existe emissão offline autorizada como na NF-e. Não criar "contingência" que marque nota como emitida sem 201.
- Sem rede: o relay grava a intenção e o documento fica em fila (`draft`/pendente), com comprovante local **não fiscal**; a UI diz "não emitida".
- Ao reconectar: consultar `GetDPSStatus` pelo mesmo `idDps`; se já existe NFS-e, reconciliar com a chave retornada; se não, reenviar com a mesma chave de idempotência.
- `fiscal_document.contingency_mode = 'offline'` deve significar **fila pendente**, nunca autorização.
- Prazo de cancelamento/substituição é regra municipal: só implementar depois de fonte oficial datada; não hardcode.

## 7. Resposta crua e hash

Persistir por tentativa:
- corpo cru exatamente como recebido (bytes), `content_type`, status HTTP, ambiente, timestamp UTC;
- SHA-256 do corpo; SHA-256 do XML enviado; `idDps`; `chaveAcesso`; protocolo/`idDps` de correlação;
- fingerprint do certificado usado (metadado, nunca a chave);
- referência do XML no object storage + hash no documento.

Mapeamento atual: `emission_attempt` já cobre `response_body`, `response_hash`, `response_content_type`, `protocol`, `idempotency_key`. **Gaps de schema** (migração forward-only futura): `http_status`, `ambiente`, `chave_acesso`, `dps_id`, `request_hash`. Enquanto não existirem, usar `protocol` e `response_body` com critério documentado — sem inventar coluna em código.

## 8. Fontes obrigatórias antes de inserir `tax_rule` ou transmitir

| # | Fonte oficial | Versão/data | Uso | Status |
|---|---|---|---|---|
| 1 | LC 159/2013 (CTM Fortaleza), art. 245 + LC 241/2017, 367/2023, 430/2025, 452/2025 | texto vigente até nov/2025 | alíquota de ISS por item/subitem | obrigatória |
| 2 | SEFIN Fortaleza — consulta "CNAE × Lista de Serviços" (exportação XLSX) | data do export | vínculo CNAE ↔ serviço ↔ alíquota ↔ incidência | obrigatória |
| 3 | SEFIN Fortaleza — Anexo I (Lista de Serviços com alíquotas) | PDF oficial | conferência da alíquota | obrigatória |
| 4 | Planilha nacional "Alíquotas de ISSQN" (CSV/TXT) | 03/09/2026 | controle: confirma ausência de Fortaleza; não é fonte de alíquota | obrigatória como evidência |
| 5 | Anexo B — NBS2 / Lista de Serviço Nacional | v1.01 22/01/2026 | `codigoServico` nacional | obrigatória |
| 6 | Anexo C — IndOp IBS/CBS | v1.01 | CST/cClassTrib/indicador de operação RTC | obrigatória |
| 7 | LC 214/2025 + NT SE/CGNFS-e nº 007/2026 | vigente | RTC, base de CBS/IBS, PIS/COFINS | obrigatória |
| 8 | XSD oficiais NFS-e | v1.01 09/02/2026 | validação local | obrigatória |
| 9 | Anexo I — DPS/NFS-e | v1.01 09/02/2026 | leiaute | obrigatória |
| 10 | Manual API Emissor Público + Swagger produção restrita | v1.2 out/2025; swagger atual | contrato HTTP, envelope, erros | obrigatória |
| 11 | Comunicado SEFIN nº 06/2026 | 28/10/2026 | cronograma e limites do emissor municipal | obrigatória |
| 12 | Certificado ICP-Brasil A1/A3 e procuração eletrônica (se multiempresa) | — | autenticação mTLS/assinatura | **fora do repo; gate humano** |

Links oficiais:
- Comunicado SEFIN 06/2026 — https://www.sefin.fortaleza.ce.gov.br/Canal/16/Generico/3815/Ler
- ISS/alíquotas Fortaleza — https://www.sefin.fortaleza.ce.gov.br/Canal/16/Generico/18/Ler
- Código Tributário (PDFs) — https://www.sefin.fortaleza.ce.gov.br/Canal/16/Generico/24/Ler
- CNAE × Lista de Serviços (exportação) — https://issadmin.sefin.fortaleza.ce.gov.br/grpfor/pagesPublic/atividadeCPBS/consultarAtividadeCPBS.seam
- Anexo I (lista com alíquotas) — https://www.sefin.fortaleza.ce.gov.br/anexoCT/27/f3igvsra5.c0h247/pdf/Regulamento%20ISSQN%20-%20Anexo%20I%20(%20Lista%20de%20Servi%C3%A7os)
- Documentação NFS-e Nacional — https://www.gov.br/nfse/pt-br/biblioteca/documentacao-tecnica/documentacao-atual
- APIs (produção e restrita) — https://www.gov.br/nfse/pt-br/biblioteca/documentacao-tecnica/apis-prod-restrita-e-producao
- Alíquotas de ISSQN (planilha nacional) — https://www.gov.br/nfse/pt-br/biblioteca/aliquotas
- RTC — https://www.gov.br/nfse/pt-br/biblioteca/documentacao-tecnica/rtc

Regra de importação: cada linha de `tax_rule` exige `source_kind`, `source_name`, `source_url`, `source_version`, `source_published_at`, `source_hash` e `import_batch`; uma única regra vigente por chave lógica; ausência ou ambiguidade falha fechado (`ErrTaxRuleNotFound`).

## 9. Critérios de aceite testáveis

1. **XSD**: toda DPS gerada valida contra `DPS_v1.01.xsd` com 0 erros; assinatura XMLDSig verificável com certificado de teste.
2. **Determinismo**: mesma entrada + mesmas regras + mesmo número/série produzem XML canônico e hash idênticos.
3. **Regra datada**: consulta por competência com precedência tenant > global; sem regra → falha fechada; teste com tabela fake, sem alíquota literal no código.
4. **Fortaleza ISS**: importação de teste prova 1 alíquota única por código de serviço com `source_hash` e vigência; código sem regra bloqueia a emissão. Itens 25.01–25.04 = 5% conforme Anexo I, após conferência legal.
5. **Adapter fake**: cobre 201, 400 com lista de erros, timeout + `GetDPSStatus`, e replay com mesma chave sem duplicar.
6. **Persistência crua**: teste confere bytes, `content_type`, status, `idDps`, `chaveAcesso` e SHA-256; alteração de 1 byte muda o hash.
7. **Contingência**: sem adapter, documento não vira `authorized`; após reconexão, `GetDPSStatus` decide reconciliar ou reenviar.
8. **Golden**: 8 casos da seção 5 passam XSD, com valores RTC iguais ao oráculo da RFB e diff revisado.
9. **LGPD/logs**: nenhum XML, PII ou segredo em log; teste de redação.
10. **Orçamento**: montagem + validação da DPS p95 < 100 ms por item em ambiente de teste (ajustar no PR com medição).

## 10. Gaps explícitos

- Fortaleza ausente da base nacional de alíquotas (0 linhas no arquivo de 03/09/2026); a API de parâmetros municipais pode vir vazia — não assumir.
- Não há texto consolidado oficial único do CTM com todas as alterações de 2025; fixar os PDFs das leis com data/hash e detectar divergência.
- Não há certificado nem CNPJ de teste no repositório; transmissão real bloqueada.
- Autorização por procuração eletrônica para SaaS multiempresa precisa de confirmação na FAQ oficial antes do desenho final.
- `emission_attempt` não tem `http_status`, `ambiente`, `chave_acesso`, `dps_id`, `request_hash`.
- Prazo e regras de cancelamento/substituição de Fortaleza ainda não foram levantados de fonte oficial.
- Simples 2026: dispensa de IBS/CBS precisa virar regra datada (art. 348, III, "c"), não `if` no código.
- EFS-e e DAM continuam obrigações municipais fora do adapter de emissão.
- Ginfes v3 permanece necessário só para legado; contrato e testes ficam para fatia própria.

## 11. Sequência

- **Fatia 01 (agora):** port + fake + DPS + XSD + golden + persistência + importador de teste. Sem rede.
- **Fatia 02:** importação real das fontes 1–3 + validação em produção restrita com certificado/CNPJ de teste + reconciliação. Só então avaliar produção.

## 12. Conformidade

- **ADR 0004:** tabelas datadas, XML validado por XSD, retorno cru persistido, RTC de primeira classe — atendido; a decisão pelo Padrão Nacional deve virar **ADR 0010** antes do código de produção.
- **DER:** `fiscal_config`, `tax_rule`, `fiscal_document`, `fiscal_item`, `tax_calc`, `emission_attempt` são suficientes para a fatia 01; gaps da seção 7 pedem migração forward-only.
- **Research:** o item "XSD e manuais de NFS-e por município alvo" passa a ter resposta para Fortaleza via padrão nacional; a pesquisa deve registrar que o emissor municipal é legado.
- **AGENTS.md:** sem alíquota no código, sem segredo no repositório, falha fechada, migração forward-only.
