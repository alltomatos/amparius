# ADR 0010 — NFS-e Nacional: Padrão Nacional com Fortaleza como primeiro município

Status: proposto · 23/11/2026 · referencia o brief `docs/fiscal/2026-11-23-nfse-slice-01-fortaleza.md`, os ADRs 0002, 0003 e 0004, o DER v0.3 e a Fase 1 do roadmap

## Contexto

A Fase 1 do roadmap exige "NFS-e (1 município), XML, XSD, DANFSe". O ADR 0004 fixou a
espinha dorsal fiscal RTC-first — tabelas datadas, XML validado por XSD, retorno cru
persistido — mas não respondeu **como** se emite NFS-e: qual protocolo, qual ambiente e
quem é o primeiro município. O módulo `fiscal` (ARCHITECTURE.md §4) prevê "NFS-e por
município/provedor" como adaptador, ainda sem decisão.

O **Comunicado SEFIN nº 06/2026** (republicado em 28/10/2026) muda o jogo: migração
obrigatória ao Emissor Nacional — ME/EPP do Simples em 01/11/2026, demais contribuintes
em 01/12/2026 — deixando o emissor municipal apenas para competências anteriores,
cancelamento/substituição de notas municipais, EFS-e e DAM. Emitir primeiro pelo padrão
municipal (Ginfes) seria nascer em um caminho que a própria prefeitura está descontinuando.

Dois fatos adicionais condicionam o escopo:

1. **Fortaleza não consta** no arquivo oficial "Alíquotas de ISSQN" (03/09/2026) — zero
   linhas para o IBGE 2304400, embora 155 dos 184 municípios do CE estejam presentes. A
   fonte municipal existe (LC 159/2013, Código Tributário, art. 245, com alterações até a
   LC 452/2025), mas **não está parametrizada na base nacional**. Logo, a API de parâmetros
   municipais do Emissor Nacional não é fonte suficiente para o ISS de Fortaleza hoje.
2. **NFS-e nacional é síncrona**: não há protocolo de contingência offline que autorize
   emissão sem resposta `201`. Isso colide com a premissa genérica de "fiscal em
   contingência" do ADR 0002 e exige um refinamento explícito.

## Decisão

### 1. Padrão Nacional (DPS → Sefin Nacional), não emissor municipal

O adaptador de emissão NFS-e inicial é o **Padrão Nacional da NFS-e**: o sistema monta a
**DPS** (Declaração de Prestação de Serviços), assina em XMLDSig e a transmite à **Sefin
Nacional**. Ginfes (emissor municipal) não é implementado nesta fatia.

Justificativa: o Padrão Nacional é documentado e versionado (XSD v1.01 de 09/02/2026,
Anexo I DPS/NFS-e v1.01, Anexo B NBS2 v1.01, Anexo C IndOp IBS/CBS v1.01, manual de API
do Emissor Público v1.2) e é o destino da migração obrigatória do Comunicado 06/2026. O
mesmo adaptador atende qualquer município aderente ao Emissor Nacional.

### 2. Fortaleza como parâmetro municipal, não código específico

Fortaleza/CE (IBGE **2304400**) entra como **primeiro parâmetro municipal**, não como
código de adaptador próprio. O motor de tributos resolve alíquota/retenções por `tax_rule`
(competência + município + código de serviço), nunca por constante; o adaptador recebe
valores já calculados. O ISS de Fortaleza exige fonte legal municipal (LC 159/2013 art.
245 e alterações) e **não** vem da planilha nacional de alíquotas — onde o município
está ausente.

### 3. Fatia 01 sem transmissão real

A fatia 01 entrega **port + adapter fake determinístico + montagem de DPS + XSD + golden
files + persistência crua**, com **zero transmissão real**. O primeiro ambiente de rede é
**produção restrita** (`sefin.producaorestrita.nfse.gov.br`), e só na fatia 02, após os
gates da decisão 6. Produção (`sefin.nfse.gov.br`) fica condicionada à fatia 02 concluída.

### 4. Contrato do adapter (port), sem cálculo de tributo

O adapter **não calcula tributo**: recebe a DPS já montada e assinada, com valores
persistidos em `tax_calc`, e devolve transporte + resposta. Operações do contrato mínimo:

| Operação | Papel |
| --- | --- |
| `EmitDPS` | envia a DPS; `201` = autorizada (`chaveAcesso`); `4xx` = rejeição com lista de erros |
| `GetNFSByKey` | consulta por `chaveAcesso`; idempotente |
| `GetDPSStatus` | verifica se o `idDps` já gerou NFS-e; **obrigatório antes de reenviar** |
| `RegisterEvent` | cancelamento/substituição; contrato reservado, fora da fatia 01 |
| `MunicipalParameters` | parâmetros oficiais por IBGE/serviço/competência; só conferência, não substitui `tax_rule` |
| `ValidateDPS` | validação XSD local, sem rede |

Regras transversais: `context.Context` primeiro; timeout e retry limitado com jitter
**apenas em leituras idempotentes**; emissão repetida só após `GetDPSStatus` confirmar
ausência de NFS-e; erros tipados (`ErrAdapterUnavailable`, `ErrDocumentRejected` com
código/descrição oficiais, `ErrIdempotencyConflict`); idempotência por
`(tenant_id, idempotency_key)` e por `idDps`.

### 5. Contingência é pendência, nunca autorização offline

NFS-e nacional é **síncrona** e não existe emissão offline autorizada (diferente da NF-e).
Isso **refina o ADR 0002** para NFS-e: o relay **nunca** marca documento como emitido sem
resposta `201`.

- Sem rede: o relay grava a intenção e o documento fica em fila (`draft`/pendente), com
  comprovante local **não fiscal**; a UI diz "não emitida".
- `fiscal_document.contingency_mode = 'offline'` significa **fila pendente**, nunca
  autorização.
- Ao reconectar: consultar `GetDPSStatus` pelo mesmo `idDps`; se já existe NFS-e,
  reconciliar com a chave retornada; se não, reenviar com a mesma chave de idempotência.
- Prazo de cancelamento/substituição é regra municipal: só após fonte oficial datada,
  sem hardcode.

A emissão em si só ocorre no caminho online (worker do SaaS); o relay enfileira a
intenção e sincroniza — nunca transmite.

### 6. Gates para a fatia 02 (fontes oficiais e certificado)

Nenhuma `tax_rule` é inserida e nenhuma transmissão ocorre antes de:

1. **Fontes oficiais fechadas e datadas** (12 do brief §8): LC 159/2013 art. 245 + LC
   241/2017, 367/2023, 430/2025, 452/2025; exportação "CNAE × Lista de Serviços" da SEFIN;
   Anexo I (Lista de Serviços com alíquotas); planilha nacional como evidência de ausência;
   Anexo B NBS2; Anexo C IndOp IBS/CBS; LC 214/2025 + NT SE/CGNFS-e nº 007/2026; XSD
   v1.01; Anexo I DPS/NFS-e; manual de API + Swagger de produção restrita; Comunicado
   06/2026. Cada linha de `tax_rule` exige `source_kind`, `source_name`, `source_url`,
   `source_version`, `source_published_at`, `source_hash` e `import_batch`.
2. **Certificado ICP-Brasil A1/A3** e, para SaaS multiempresa, **procuração eletrônica**
   confirmada na FAQ oficial. Certificado e CNPJ de teste **nunca** entram no repositório
   (AGENTS.md §6); o fingerprint do certificado é persistido como metadado, nunca a chave.
3. **Importação de teste** provando uma alíquota única por código de serviço com
   `source_hash` e vigência; código sem regra bloqueia a emissão (fail-closed,
   `ErrTaxRuleNotFound`).

### 7. Ginfes apenas para legado

Ginfes v3 (`iss.fortaleza.ce.gov.br/grpfor-iss/ServiceGinfesImplService`) permanece
necessário **só** para competências antigas e cancelamento/substituição de notas emitidas
no emissor municipal. É contrato e teste próprios, em fatia separada — nunca um caminho
alternativo de emissão corrente.

## Não-escopo

Fora desta fatia, de forma explícita:

- **Transmissão real** a qualquer ambiente, certificado e procuração eletrônica.
- **Cancelamento/substituição** (contrato `RegisterEvent` reservado, implementação depois).
- **Alíquotas**: nenhuma alíquota é criada, importada em massa ou fixada no código.
- **DAM, EFS-e, Ginfes v3, NF-e/NFC-e** — obrigações municipais/estaduais fora do adapter
  de emissão NFS-e.
- **Importação massiva de alíquotas** e parametrização de Fortaleza (é gate da fatia 02).

## Consequências

- **Um adaptador, N municípios.** O Padrão Nacional é um único adapter parametrizado por
  município; adicionar um município é configurar `tax_rule` + `fiscal_config`, não escrever
  adaptador novo. Isso muda a leitura de ARCHITECTURE.md §7.1 "NFS-e por município/provedor":
  município vira **parâmetro**, não código.
- **Sem cálculo no adapter.** A separação motor de tributos (`tax_calc`) ↔ transporte
  (adapter) é reforçada; o adapter é agnóstico de alíquota e de RTC.
- **Contingência NFS-e não autoriza offline.** O ADR 0002 §7.3 ("documento emitido
  localmente em contingência") não se aplica a NFS-e: não há autorização offline. O relay
  mantém a regra "a operação não para" (venda/atendimento/caixa funcionam), mas o documento
  fiscal fica pendente e a UI declara "não emitida".
- **Gaps de persistência crua.** O DER v0.3 `emission_attempt` cobre `response_body`,
  `response_hash`, `response_content_type`, `protocol`, `idempotency_key`, mas **faltam**
  `http_status`, `ambiente`, `chave_acesso`, `dps_id`, `request_hash`. Enquanto não
  existirem, usa-se `protocol` + `response_body` com critério documentado; a adição é
  migração forward-only futura.
- **Trilha de auditoria completa por tentativa**: bytes crus, `content_type`, status HTTP,
  ambiente, timestamp UTC, SHA-256 do corpo e do XML enviado, `idDps`, `chaveAcesso`,
  fingerprint do certificado (metadado) e referência do XML no object storage.
- **Determinismo e golden.** Mesma entrada + mesmas regras + mesmo número/série produzem
  XML canônico e hash idênticos; 8 golden files (por regime, serviço e competência) validam
  XSD antes de qualquer chamada.

## Alternativas rejeitadas

- **Ginfes (emissor municipal) como adaptador inicial**: nasceria em um caminho que o
  Comunicado 06/2026 descontinua; exigiria adaptador municipal próprio por cidade e
  retrofits caros. Rejeitado em favor do Padrão Nacional.
- **Código específico por município (adaptador "Fortaleza")**: acopla código a município
  quando o Emissor Nacional já uniformiza o protocolo; município vira parâmetro de
  configuração (decisão 2).
- **Assumir alíquota de Fortaleza a partir da base nacional**: Fortaleza está ausente do
  arquivo oficial; assumir violaria o ADR 0004 (fiscal não se estima). A fonte é a lei
  municipal (LC 159/2013 art. 245), fechada como gate da fatia 02.
- **Contingência que marca documento como emitido sem `201`**: inventaria autorização
  offline inexistente na NFS-e nacional e feriria a auditabilidade. Rejeitado (decisão 5).
- **`RegisterEvent`/cancelamento nesta fatia**: sem fonte oficial de prazo municipal, seria
  hardcode de regra legal. Contrato reservado, implementação posterior.

## Critérios de aceite

1. **XSD**: toda DPS gerada valida contra `DPS_v1.01.xsd` (e `NFSe_v1.01.xsd` /
   `xmldsig-core-schema.xsd` quando aplicável) com zero erros; assinatura XMLDSig
   verificável com certificado de teste.
2. **Determinismo**: mesma entrada + mesmas regras + mesmo número/série produzem XML
   canônico e hash idênticos.
3. **Regra datada**: consulta por competência com precedência tenant > global; sem regra,
   falha fechada; teste com tabela fake, sem alíquota literal no código.
4. **Adapter fake**: cobre `201`, `400` com lista de erros, timeout + `GetDPSStatus`, e
   replay com a mesma chave sem duplicar.
5. **Persistência crua**: teste confere bytes, `content_type`, status HTTP, `idDps`,
   `chaveAcesso` e SHA-256; alteração de 1 byte muda o hash.
6. **Contingência**: sem adapter, documento **não** vira `authorized`; após reconexão,
   `GetDPSStatus` decide reconciliar ou reenviar.
7. **Golden**: os 8 casos do brief §5 passam XSD, com valores RTC iguais ao oráculo da RFB
   e diff revisado.
8. **LGPD/logs**: nenhum XML, PII ou segredo em log.
9. **Orçamento**: montagem + validação da DPS p95 < 100 ms por item em ambiente de teste
   (medição revisada no PR).
10. **Nenhum commit de código ou alíquota**: esta decisão é documental; código e migrações
    ficam para as faturas de implementação.

## Mudanças decorrentes

### DER (v0.3 → v0.4, com o Engenheiro de Dados)

- Adicionar a `emission_attempt` as colunas `http_status`, `ambiente`, `chave_acesso`,
  `dps_id`, `request_hash` por **migração forward-only** (a migração existente não é
  editada). Enquanto não existirem, persistência usa `protocol` + `response_body` com
  critério documentado.

### ARCHITECTURE.md

- §7.1: registrar que o adaptador NFS-e é o **Padrão Nacional** e que município é
  **parâmetro** (`fiscal_config` + `tax_rule`), não adaptador por município.
- §7.3: registrar que NFS-e nacional é **síncrona** e não há emissão offline autorizada;
  relay enfileira a intenção e sincroniza, nunca transmite.

### Research (`docs/research/receita-federal.md`)

- Fechar o item de backlog "XSD e manuais de NFS-e por município alvo": Fortaleza usa o
  Padrão Nacional; o emissor municipal (Ginfes) é legado.

### Roadmap

- Fase 1 "NFS-e (1 município)" fica coerente com a fatia 01: montagem de DPS + XSD +
  golden + persistência, sem transmissão real; a emissão real e a parametrização de
  Fortaleza são gates da fatia 02.
