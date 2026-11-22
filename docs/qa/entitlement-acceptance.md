# QA — Aceite e contratos de teste: entitlement por licença

Versão 1.0 · 21/11/2026 · QA de Contrato · repo em fase de documentação (casos descritivos, não código)

Referências: ADR 0007 (`docs/adr/0007-entitlement-licenca.md`), DER v0.2 (`docs/data/DER.md` §5 e regras 11–14), ADR 0003 (`docs/adr/0003-multitenancy-rls.md`), ADR 0002 (`docs/adr/0002-relay-offline.md`), ADR 0005 (`docs/adr/0005-celulas-vps.md`), `SCALING.md` §7.

## 1. Escopo e convenções

Este documento fixa os **critérios de aceite observáveis** e os **contratos de teste** do modelo de entitlement por licença aprovado no ADR 0007. Cada caso é Given/When/Then com critério objetivo (status HTTP, contagem, ausência de registro, versão, TTL). Onde o contrato ainda não está implementado, o caso já define o **golden esperado** (payload, shape de evento, código de erro) para o qual a implementação deve convergir.

**Fora de escopo:** cálculo de tributo. Nenhuma alíquota, CST, cClassTrib, NCM, CFOP ou código de serviço aparece neste documento; esses valores vêm de tabela oficial datada consultada pela data de competência (ADR 0004). Os casos fiscais abaixo verificam apenas a **regra estrutural**: módulo ativo + licença vigente + config/certificado do CNPJ da empresa.

**Nomenclatura usada nos casos:**

| Termo | Significado |
| --- | --- |
| **CNPJ / empresa** | `COMPANY`, entidade legal operadora; é onde o entitlement amarra e onde vivem `FISCAL_CONFIG`/`CERTIFICATE`. |
| **Filial** | `ESTABLISHMENT`; herda o entitlement da empresa, não recebe licença própria. |
| **Tenant** | grupo econômico; pode conter várias empresas (CNPJs). |
| **Célula** | VPS com a pilha completa que atende tenants (ADR 0005). |
| **Control plane** | fonte da verdade: cotas, licenças atribuídas, auditoria (schema `entitlement`). |
| **Snapshot** | pacote assinado e versionado de enforcement mantido na célula e no relay; não é tabela de negócio. |
| **Módulo opcional** | `funeral`, `cemetery`, `cremation`, `floriculture`, `inventory`, `documents`, `ai` (na cota). |
| **Módulo núcleo** | `identity`, `party`, `catalog`, `finance`, `fiscal`, `sync`, `reporting` (fora da cota). |

**Contratos propostos neste documento** (a confirmar pela implementação — são o alvo do teste, não fato do código):

| Código | HTTP | Quando |
| --- | --- | --- |
| `ENTITLEMENT_MODULE_NOT_LICENSED` | 403 | Módulo sem licença ativa no CNPJ da empresa. |
| `ENTITLEMENT_LICENSE_NOT_ACTIVE` | 403 | Licença existe, mas fora da vigência (`valid_from`/`valid_to`) ou `status != active`. |
| `ENTITLEMENT_CACHE_EXPIRED` | 403 | Relay bloqueou por expiração do snapshot (fail-closed). |
| `LICENSE_POOL_EXHAUSTED` | 409 | Self-service/admin tenta atribuir licença acima da cota do tenant. |
| `LICENSE_NOT_AVAILABLE` | 409 | Self-service tenta ativar módulo sem licença vigente disponível. |
| `FISCAL_CONFIG_MISSING` | 422 | Emissão sem `FISCAL_CONFIG` do `company_id` do fato gerador. |
| `FISCAL_CERTIFICATE_INVALID` | 422 | Emissão sem `CERTIFICATE` válido do `company_id` do fato gerador (inclui expirado). |

O corpo de erro segue um único envelope: `{"error":{"code":"<código>","module_code":"<módulo>","company_id":"<uuid>","message":"<texto não sensível>"}}`. Nunca inclui dado de outro tenant.

---

## 2. Casos

### Ponto 1 — Bloqueio nas três camadas

**QA-E-01 · UI esconde menu e ação.**
Given o CNPJ da empresa atual não possui licença ativa para o módulo `cemetery`.
When o usuário autenticado abre o portal.
Then o item de menu `Cemitério` não renderiza, os botões de ação (novo, editar, excluir) não renderizam, e o endpoint de catálogo de módulos (`GET /api/v1/me/modules`) não lista `cemetery` para aquele CNPJ.
Critério: ausência no DOM + ausência na resposta do catálogo. Um menu presente por cache de cliente não é falha deste caso; a falha é o servidor listar o módulo.

**QA-E-02 · API nega endpoint de módulo.**
Given o CNPJ da empresa atual não possui licença ativa para `cemetery`.
When `POST /api/v1/cemeteries` com payload válido.
Then HTTP 403 `ENTITLEMENT_MODULE_NOT_LICENSED`.
And nenhum registro é criado (contagem de `cemetery` antes = depois).
And nenhum evento é publicado no outbox (`OUTBOX_EVENT` inalterado).
And a negação ocorre **antes** de qualquer leitura/escrita do recurso (sem query de insert; o teste instrumenta o repositório e não observa INSERT).

**QA-E-03 · API nega publicação de evento do módulo.**
Given módulo `cemetery` sem licença ativa no CNPJ.
When um produtor publica `cemetery.plot.created`.
When o consumidor da fila tenta processar a mensagem.
Then a rejeição ocorre com `ENTITLEMENT_MODULE_NOT_LICENSED` antes do handler de domínio.
And nenhum efeito colateral do handler acontece (nenhuma linha nova, nenhum documento, nenhum lançamento).

**QA-E-04 · API nega chamada de IA.**
Given módulo `ai` sem licença ativa no CNPJ.
When `POST /api/v1/ai/runs`.
Then HTTP 403 `ENTITLEMENT_MODULE_NOT_LICENSED`.
And nenhuma linha em `AI_RUN` é criada.
And nenhuma linha em `AI_TOOL_CALL` é criada.
And o provedor de modelo não é acionado (instrumentação de saída: zero requisição à API do provedor).

**QA-E-05 · Relay com cache válido bloqueia o uso local.**
Given o relay está com um snapshot de enforcement válido (dentro do TTL) que **não** contém licença para `funeral`.
When o atendente tenta criar um contrato local.
Then o relay bloqueia a operação localmente, antes de gravá-la, com erro estruturado que a UI traduz.
And nenhuma `SYNC_OPERATION` de escrita para `funeral.*` é enfileirada.
And a UI sinaliza "módulo bloqueado por licença" (não mostra erro genérico de sync).

---

### Ponto 2 — Desativação preserva dado

**QA-D-01 · Desligar não apaga nem anonimiza.**
Given a empresa possui 5 contratos `funeral` ativos.
When o admin SaaS revoga a licença de `funeral` da empresa.
Then a contagem de contratos continua 5.
And nenhum `deleted_at` foi preenchido em `CONTRACT`.
And nenhum dado foi movido para arquivo morto ou anonimizado.
And o histórico permanece legível por consulta administrativa autorizada.

**QA-D-02 · Reativar restaura o uso.**
Given `funeral` desligado conforme QA-D-01.
When uma nova licença vigorosa é atribuída à empresa.
Then `POST /api/v1/contracts` volta a responder 201 com o contrato criado.
And os 5 contratos anteriores permanecem íntegros e com o mesmo estado.

**QA-D-03 · Leitura congelada de `fiscal`.**
Given módulo `fiscal` desligado.
When `GET /api/v1/fiscal/documents` lista documentos já emitidos.
Then HTTP 200 com o histórico completo.
And `POST /api/v1/fiscal/documents` (emissão nova) responde 403 `ENTITLEMENT_LICENSE_NOT_ACTIVE`.
Critério: a desativação bloqueia escrita, nunca a leitura fiscal. Este caso falha se a leitura for negada ou filtrada pelo entitlement.

**QA-D-04 · Leitura congelada de `reporting`.**
Given módulo `reporting` desligado.
When o contador roda o relatório mensal do período.
Then o relatório é gerado com os dados completos do período.
And nenhuma exceção de entitlement interrompe a geração.

**QA-D-05 · Dado de módulo opcional desligado permanece no banco.**
Given módulo `floriculture` desligado.
When o dono do tenant consulta o banco por `ORDER` do período.
Then as ordens continuam presentes com valores originais.
And nenhuma rotina de limpeza/exporação é executada como efeito da desativação.

---

### Ponto 3 — Self-service vs licença

**QA-S-01 · Self-service sem licença ativa falha.**
Given o `LICENSE_POOL` do tenant tem `quota = 0` para `cemetery` (ou não há `LICENSE_ASSIGNMENT` vigoroso para a empresa).
When o dono do tenant tenta ativar `cemetery` em self-service para a sua empresa.
Then HTTP 409 `LICENSE_NOT_AVAILABLE`.
And o estado do módulo permanece desativado (catálogo de módulos continua sem `cemetery`).
And nenhuma `LICENSE_ASSIGNMENT` nova é persistida.
And nenhum evento de ativação bem-sucedida é emitido.

**QA-S-02 · Admin SaaS sobrepõe o estado.**
Given `funeral` ativado via self-service pelo dono do tenant.
When o admin SaaS revoga a licença no control plane.
Then o estado no control plane fica revogado.
And o self-service subsequente do dono não consegue reativar sem nova licença (409 `LICENSE_NOT_AVAILABLE`).
And a revogação não depende de ato do dono.

**QA-S-03 · Toda mudança gera auditoria completa.**
Given qualquer mudança de estado (ativar, desativar, revogar, reativar) feita por self-service ou por admin SaaS.
When a mudança é aplicada.
Then uma linha `ENTITLEMENT_EVENT` é gravada, append-only, com `tenant_id`, `module_code`, `company_id`, `actor_id`, `action`, `origin` (`self-service` | `admin`) e `at`.
Critérios: campos não nulos; o registro anterior não é alterado nem apagado; dois eventos de origens diferentes no mesmo instante são distinguíveis por `origin`.

**QA-S-04 · Cota por módulo é respeitada.**
Given `LICENSE_POOL.quota = 2` para `cemetery` e duas `LICENSE_ASSIGNMENT` vigorosas para duas empresas do tenant.
When se tenta atribuir uma terceira licença de `cemetery`.
Then HTTP 409 `LICENSE_POOL_EXHAUSTED`.
And a ocupação é contada por assignments vigentes, não por coluna digitada.
And ao vencer/encerrar uma das duas assignments, a terceira passa a ser aceita (a cota é derivada do estado vigente).

---

### Ponto 4 — Propagação e TTL (caos/offline)

**QA-P-01 · Revogação propaga por invalidação com versão monotônica.**
Given uma licença ativa e um snapshot de enforcement com `version = N`.
When o admin revoga a licença no control plane.
Then um novo snapshot é distribuído com `version = N+1` (estritamente maior).
And a célula substitui o snapshot ao receber a versão maior.
And o relay substitui o snapshot ao receber a versão maior.
And um snapshot com `version <= N` nunca sobrescreve um mais novo (caso fora de ordem: o remetido com versão menor é ignorado).

**QA-P-02 · `entitlement.revoked` é sync prioritária.**
Given a fila de sincronização com operações pendentes de módulos quaisquer.
When o evento `entitlement.revoked` chega ao relay.
Then ele é processado antes das operações ordinárias pendentes.
And o snapshot local correspondente é invalidado imediatamente após o processamento.
And a invalidação é visível em teste com relógio controlado: o bloqueio vale no ciclo seguinte ao evento.

**QA-P-03 · Offline com TTL expirado bloqueia (fail-closed).**
Given o relay está offline com um snapshot cujo `valid_until` já passou.
When o atendente tenta usar um módulo opcional.
Then o uso é bloqueado localmente com `ENTITLEMENT_CACHE_EXPIRED`.
And a UI sinaliza que o bloqueio é por expiração de cache (não por "sem internet").
And o módulo permanece bloqueado até a reconexão trazer snapshot novo — não existe caminho que o desbloqueie localmente.

**QA-P-04 · Sem emissão fiscal pós-revogação.**
Given o relay recebeu e aplicou a revogação de `fiscal`, ou está com o snapshot expirado.
When uma emissão é tentada.
Then a tentativa é barrada **antes** do transporte: nenhum XML é escrito em object storage e nenhuma requisição sai para a SEFAZ/prefeitura.
And nenhuma linha `EMISSION_ATTEMPT` com resultado de sucesso é gravada.
And o bloqueio não depende de a SEFAZ estar acessível.
Critério de caos: o relógio do relay é avançado para além do `valid_until` no meio do fluxo de emissão; a emissão falha com `ENTITLEMENT_LICENSE_NOT_ACTIVE` ou `ENTITLEMENT_CACHE_EXPIRED`, nunca é transmitida.

**QA-P-05 · Reconciliação após reconexão.**
Given o relay ficou offline durante a revogação de um módulo.
When a rede volta e o relay aplica o snapshot novo.
Then as operações locais pendentes daquele módulo são rejeitadas ou encaminhadas para revisão auditada.
And nenhuma delas é aplicada silenciosamente como se o módulo estivesse licenciado.
And as operações de módulos não revogados seguem sendo aplicadas normalmente.

---

### Ponto 5 — Emissão fiscal

**QA-F-01 · Emissão só com os três requisitos cumpridos.**
Given módulo `fiscal` ativo, `LICENSE_ASSIGNMENT` vigoroso para `module_code = 'fiscal'` no `company_id` do fato gerador, e `FISCAL_CONFIG` + `CERTIFICATE` válidos para esse `company_id`.
When o documento é emitido.
Then a emissão é aceita e transmitida.
And o documento é associado ao `company_id`/ao `establishment_id` do fato gerador.
Critério: a transmissão só ocorre quando os três requisitos são simultaneamente verdadeiros. Nenhuma regra de valor é avaliada aqui.

**QA-F-02 · Negação sem licença vigente no CNPJ do fato.**
Given módulo `fiscal` ativo, mas sem `LICENSE_ASSIGNMENT` vigoroso para o `company_id` do fato gerador.
When a emissão é submetida.
Then HTTP 403 `ENTITLEMENT_LICENSE_NOT_ACTIVE`.
And nenhum XML é gerado ou transmitido.
And não há `EMISSION_ATTEMPT` de sucesso.

**QA-F-03 · Negação sem configuração fiscal do CNPJ.**
Given licença vigorosa, mas sem `FISCAL_CONFIG` para o `company_id` do fato gerador.
When a emissão é submetida.
Then HTTP 422 `FISCAL_CONFIG_MISSING`.
And nada é transmitido.

**QA-F-04 · Negação com certificado ausente ou expirado.**
Given licença vigorosa e configuração presente, mas `CERTIFICATE` ausente ou com `valid_until` anterior à data de emissão.
When a emissão é submetida.
Then HTTP 422 `FISCAL_CERTIFICATE_INVALID`.
And nada é transmitido.

**QA-F-05 · A filial resolve a configuração pela empresa.**
Given uma filial (`ESTABLISHMENT`) cuja empresa (`COMPANY`) possui configuração e certificado válidos.
When a emissão ocorre nessa filial.
Then `FISCAL_CONFIG` e `CERTIFICATE` são resolvidos pelo `company_id` do fato gerador.
And a configuração de outra empresa do mesmo tenant jamais é usada.
And uma filial sem empresa com configuração válida é negada (QA-F-03/QA-F-04), mesmo que o tenant tenha outro CNPJ configurado.

**QA-F-06 · Negação não grava rastro de transmissão.**
Given qualquer negação de QA-F-02 a QA-F-05.
When se inspeciona o object storage e as tabelas de emissão.
Then não existe XML escrito para o documento.
And não existe `EMISSION_ATTEMPT` com resultado de sucesso.

---

### Ponto 6 — DER: empresa vs estabelecimento

**QA-M-01 · Integridade de modelo do entitlement.**
Given o schema `entitlement` e o catálogo `module`.
When se inspeciona o modelo.
Then `LICENSE_ASSIGNMENT.company_id` é FK para `COMPANY.id` e pertence ao mesmo `tenant_id` da assignment.
And não existe coluna de licença em `ESTABLISHMENT` — filial não recebe licença própria.
And não existe segunda entidade chamada `COMPANY`: o subtipo de cadastro de PJ é `PARTY_COMPANY` (sem colisão de nomes).

**QA-M-02 · Filial herda o entitlement da empresa.**
Given uma filial de uma empresa com licença ativa para `cemetery`.
When se usa `cemetery` a partir da filial.
Then o enforcement resolve o `company_id` da filial e encontra a licença da empresa.
And se a empresa não tem licença, a filial é bloqueada na mesma condição (herança, não exceção).

**QA-M-03 · Configuração e certificado por CNPJ.**
Given duas empresas do mesmo tenant, com configurações fiscais distintas.
When cada uma emite.
Then a emissão de cada uma usa exclusivamente a sua `FISCAL_CONFIG` e o seu `CERTIFICATE`.
And a unicidade é por `company_id` (`FISCAL_CONFIG.company_id` é PK); `tenant_id` não é mais a chave.
And não existe `certificate.tenant_id` como identificador do certificado.

**QA-M-04 · Migração forward-only não quebra o vínculo.**
Given a migração que move `FISCAL_CONFIG` de `tenant_id PK` para `company_id PK` e `CERTIFICATE` de `tenant_id` para `company_id`.
When aplicada.
Then ela é uma migração nova; a anterior não é editada.
And o `NOT NULL` de `company_id` só é introduzido depois de a carga inicial criar `COMPANY` por tenant.
Critério: a migração é reversível por nova migração e o banco resultante satisfaz QA-M-03.

---

### Ponto 7 — RLS e isolamento

**QA-R-01 · Corpo não define escopo.**
Given uma sessão autenticada do tenant A.
When uma requisição envia `{"tenant_id":"<uuid do tenant B>"}` e `{"company_id":"<uuid de empresa de B>"}` no corpo.
Then esses campos são descartados.
And o escopo efetivo é o da sessão (tenant A, empresa da sessão).
And a resposta não contém dado de B.
Critério: não existe bind de `tenant_id`/`company_id` a partir do corpo em nenhum endpoint de entitlement, módulo ou fiscal.

**QA-R-02 · Leitura cross-tenant falha.**
Given um token do tenant A.
When `GET /api/v1/companies/{id_de_empresa_de_B}` ou equivalente de lista com filtro por id de B.
Then HTTP 404 (não revela existência) ou 403.
And nenhum campo de B (nome, documento, configuração) aparece na resposta.

**QA-R-03 · Query direta sem contexto falha.**
Given uma conexão com `app.tenant_id` definido para A.
When `SELECT` sobre `LICENSE_ASSIGNMENT`, `FISCAL_CONFIG`, `CERTIFICATE` ou tabelas de módulo filtrando por id de B.
Then a RLS retorna zero linhas.
Critério: qualquer query de teste que tente cruzar tenants sem `BYPASSRLS` retorna vazio ou erro.

**QA-R-04 · Referência entre tenants é impossível.**
Given um tenant A e uma `COMPANY` do tenant B.
When se tenta persistir uma `LICENSE_ASSIGNMENT` de A apontando para essa `company_id`.
Then a persistência é recusada por violação de integridade (FK/constraint de tenant).
And não há caminho de escrita que crie a referência.

**QA-R-05 · Respostas não vazam identificadores vizinhos.**
Given uma sessão do tenant A.
When listagens e autocomplete de empresas, licenças, certificados e módulos respondem.
Then as respostas contêm apenas entidades de A.
And ids/nomes de B não aparecem em sugestões, em erros nem em metadados.

---

## 3. Rastreabilidade

| Critério de aceite ADR 0007 | Casos |
| --- | --- |
| 2 — UI esconde, API nega endpoint/evento/IA, relay bloqueia | QA-E-01, QA-E-02, QA-E-03, QA-E-04, QA-E-05 |
| 3 — Desligar não apaga; reativar restaura; fiscal/reporting leem | QA-D-01, QA-D-02, QA-D-03, QA-D-04, QA-D-05 |
| 4 — Self-service falha sem licença; admin sobrepõe; auditoria | QA-S-01, QA-S-02, QA-S-03, QA-S-04 |
| 5 — Propagação por invalidação; fail-closed por TTL; sem emissão pós-revogação | QA-P-01, QA-P-02, QA-P-03, QA-P-04, QA-P-05 |
| 6 — Emissão fiscal com fiscal ativo + licença + config/certificado | QA-F-01 … QA-F-06 |
| 7 — DER distingue empresa de estabelecimento; config por CNPJ | QA-M-01, QA-M-02, QA-M-03, QA-M-04 |
| ADR 0003 — RLS e recusa de escopo pelo corpo | QA-R-01 … QA-R-05 |

---

## Anexo A — Golden: pacote de entitlement assinado (snapshot de enforcement)

Shape esperado do pacote distribuído pelo control plane para células e relays (mesmo mecanismo das tabelas fiscais em `SCALING.md` §7: pacote assinado e versionado).

```json
{
  "version": 41,
  "tenant_id": "<uuid>",
  "valid_until": "2026-11-21T17:00:00Z",
  "modules": [
    { "module_code": "cemetery", "company_id": "<uuid>", "licensed": true },
    { "module_code": "funeral", "company_id": "<uuid>", "licensed": false }
  ],
  "signature": "<assinatura>"
}
```

Critérios de contrato: `version` inteiro estritamente monotônico por tenant; `valid_until` sempre futuro na emissão; cada entrada liga `module_code` a um `company_id` (nunca a `establishment_id`); `licensed` deriva de `LICENSE_ASSIGNMENT` vigente; a assinatura é verificável contra a chave do control plane; pacote com `version` menor que o local corrente é descartado.

## Anexo B — Golden: evento `entitlement.revoked`

```json
{
  "topic": "entitlement.revoked",
  "payload": {
    "tenant_id": "<uuid>",
    "module_code": "fiscal",
    "company_id": "<uuid>",
    "version": 42,
    "actor_id": "<uuid>",
    "origin": "admin",
    "at": "2026-11-21T14:00:00Z"
  }
}
```

Critérios de contrato: `version` é sempre maior que o snapshot prévio; `origin` é `self-service` ou `admin`; o payload carrega o `company_id` (a revogação é por CNPJ, não por tenant); o evento é tratado pelo relay como **operação prioritária** (QA-P-02).

## Anexo C — Golden: auditoria `ENTITLEMENT_EVENT`

Colunas não nulas: `tenant_id`, `module_code`, `company_id`, `actor_id`, `action`, `origin`, `at`. Append-only: inserção permitida; UPDATE/DELETE recusado por policy/gatilho. `action` cobre ativar/desativar/revogar/reativar. Dois eventos podem ter o mesmo `at`; diferenciam-se por `id` (uuidv7) e `origin`.

## Anexo D — Notas de execução

- Casos de caos (QA-P-03, QA-P-04) usam relógio controlado para avançar `valid_until`; a afirmação é sobre o estado após a expiração, não sobre esperar tempo real.
- Casos de negação fiscal (QA-F-02 a QA-F-05) instrumentam a fronteira de transmissão (object storage + client SEFAZ) e exigem zero chamadas; um teste que passa por mock alterado do transporte não conta como evidência.
- Todos os casos que envolvem tenant avaliam também o teste espelho: o mesmo pedido feito com o escopo de outro tenant deve falhar (QA-R-02, QA-R-03).
