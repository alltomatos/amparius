# ADR 0013 — Nome social e nome de exibição no cadastro de pessoa

Status: aceito · 24/11/2026 · referencia os ADRs 0002, 0003, 0004 e 0006, o
PRD §4.1/RF-003/RF-004, o DER §1 e §6 e `docs/architecture/ARCHITECTURE.md`
§4, §6 e §9

## Contexto

O cadastro único (`party`) representa pessoas físicas, pessoas jurídicas,
falecidos e familiares. Hoje `party.party.name` é o único nome do cadastro e
`party.person` guarda os atributos específicos da pessoa. O produto precisa
permitir que uma pessoa informe um nome social opcional sem apagar,
substituir ou confundir o nome civil armazenado.

O nome social deve ser o nome de uso cotidiano no atendimento e nas telas,
mas há contextos que exigem o nome civil: obrigação legal ou fiscal, contrato
formal e auditoria. A decisão precisa funcionar nos três acessos do produto,
no relay offline e nas APIs existentes, sem criar uma segunda identidade,
duplicar o nome em `deceased`/`family_link` ou introduzir uma regra fiscal.

## Decisão

### 1. Modelo e semântica

- `party.party.name` continua sendo o **nome civil** da pessoa e permanece
  obrigatório. Ele não é apagado nem sobrescrito quando um nome social é
  informado.
- `party.person.social_name` será um campo textual **opcional e anulável**,
  aplicável somente a `party.kind = 'person'`. Ausência e valor vazio não
  representam um nome social; a implementação deve persistir a ausência como
  `NULL` e não copiar automaticamente `name` para esse campo.
- `display_name(party)` é a função semântica de apresentação:

  ```text
  pessoa com social_name não vazio → social_name
  pessoa sem social_name             → party.name
  pessoa jurídica                   → party.name
  ```

  A função não altera dados, não cria valor derivado persistido e não pode ser
  usada para identificação fiscal, chave de deduplicação ou autorização.
- Quando o contexto exigir o nome civil, o caso de uso deve pedir
  explicitamente `civil_name`; não pode depender de um fallback implícito de
  `display_name`.

### 2. Superfícies de uso

Com `social_name` preenchido, `display_name` é o padrão em telas, buscas,
listas, relatórios operacionais, respostas de API voltadas à operação e
fluxos do sistema. Isso inclui a visualização de uma pessoa como falecida ou
como familiar.

O nome civil é usado quando o contexto for obrigação legal/fiscal, contrato
formal ou auditoria. Documentos formais que precisarem preservar o estado no
momento da emissão devem guardar o snapshot exigido pelo próprio contrato do
documento; uma alteração posterior do cadastro não reescreve documento já
emitido ou evidência de auditoria.

`deceased` continua referenciando `person` e `family_link` continua
referenciando pessoas. Nenhuma dessas tabelas recebe uma cópia de nome. A
alteração do nome social de uma pessoa não altera vínculos, grau de parentesco,
identificadores ou dados de óbito; as leituras operacionais resolvem
`display_name` na origem.

### 3. APIs, eventos e compatibilidade

- O contrato público deve expor `display_name` como campo de apresentação e,
  quando autorizado e necessário pelo contexto, `civil_name` como campo
  distinto. Os campos não são sinônimos.
- A forma dos endpoints existentes não deve ser quebrada: o campo histórico
  `name` não é removido nem renomeado nesta decisão. Na versão existente, seu
  significado permanece documentado como nome civil; consumidores que exibem
  pessoas devem migrar para `display_name`.
- Novos endpoints e versões de contrato não devem exigir que o cliente
  deduza o nome social a partir de `name`. Se uma versão futura redefinir
  `name` como nome de exibição, isso exige contrato/versionamento explícito e
  nota de compatibilidade.
- Eventos e payloads de sincronização recebem os campos novos de forma
  aditiva e opcional quando isso for compatível com o contrato versionado.
  Remover ou mudar o significado de campo existente exige nova versão do
  evento. Consumidor antigo, ao receber somente `name`, continua funcional e
  exibe o nome civil.
- O nome social não é usado para localizar a pessoa por CPF/CNPJ, gerar XML,
  calcular tributo, definir idempotência ou substituir qualquer identificador
  opaco.

### 4. Tenant, RLS e offline

- O campo pertence ao schema `party`, em `party.person`, e herda o mesmo
  `tenant_id`, vínculo `(tenant_id, party_id)`, política RLS e contexto de
  tenant do cadastro existente. Nenhum endpoint aceita `tenant_id` no corpo.
- Leitura e escrita passam pelo serviço público de `party`; nenhum módulo
  acessa a tabela diretamente ou faz `JOIN` entre schemas para obter o nome.
- O relay pode exibir o `display_name` disponível no cache local. A alteração
  offline é uma operação de dado mestre: segue o log de operações,
  `seq`, `idempotency_key` e `version` do ADR 0002. O servidor é autoritativo;
  conflito de versão não é resolvido por *last write wins* silencioso e deve
  gerar reconciliação auditável.
- O snapshot local não transforma nome social em autorização nem permite que
  uma resposta offline seja tratada como documento legal ou fiscal.

### 5. LGPD e proteção operacional

Nome social é dado pessoal e recebe as mesmas salvaguardas de minimização,
controle de acesso, finalidade, retenção e auditoria aplicadas ao cadastro de
pessoa. Esta ADR não inventa base legal, consentimento ou prazo de retenção;
esses itens permanecem definidos pela política de privacidade e pelo contexto
do tratamento.

O sistema não deve inferir, corrigir, questionar ou substituir o nome social
com base em outro atributo. Logs técnicos, métricas, traces e prompts de IA
não devem carregar nomes; quando um caso de uso de IA precisar trabalhar com
uma pessoa, usa identificador opaco e recebe o mínimo de dados necessário,
com mascaramento conforme o contrato de IA. Relatórios ou auditorias que
precisem do nome civil exigem finalidade e permissão compatíveis.

## Consequências

- O cadastro passa a distinguir claramente identidade persistida (nome civil)
  e apresentação operacional (`display_name`), sem criar uma entidade nova.
- O DER e a migration de implementação precisarão adicionar uma coluna
  nullable em `party.person`; `0002_party_catalog.sql` não será editada. A
  alteração será uma migration nova, forward-only, com valores existentes
  inicialmente nulos e sem backfill especulativo.
- Repositórios, handlers, buscas, relatórios, contratos de evento e clientes
  deverão carregar a relação `party`/`person` dentro do módulo `party` e
  aplicar a semântica central de `display_name`. A implementação deve tratar
  incrementos de `version` e `updated_at` como qualquer mudança de dado mestre.
- Telas operacionais ganham consistência entre SaaS, servidor da loja,
  estação e relay; documentos formais conservam a trilha civil necessária.
- A busca por nome social pode exigir índice ou estratégia de consulta
  adicional, mas o desenho físico não autoriza duplicação entre módulos nem
  índice que exponha PII fora do escopo do tenant.
- Não há mudança de regra, alíquota, código fiscal, XML ou identificação
  perante o fisco. A escolha do nome enviado a um documento fiscal depende do
  contrato legal/fiscal aplicável e de um caso de uso explícito; esta ADR não
  presume que o nome social seja aceito como substituto em qualquer documento.

## Não-escopo

- alterar código Go/Rust, APIs, eventos, relay ou cliente nesta etapa;
- criar ou editar migration aplicada;
- definir base legal, consentimento, retenção ou política de atendimento;
- decidir requisito de nome para qualquer documento fiscal ou obrigação
  municipal/federal;
- renomear `party.party.name`, alterar o documento da pessoa ou criar cópias
  em `deceased`/`family_link`;
- definir indexação, normalização linguística ou algoritmo de busca além da
  semântica funcional desta decisão.

## Critérios de aceite para a implementação

1. Cadastro de pessoa sem nome social mantém `display_name = party.name`;
   cadastro com nome social retorna `display_name = social_name`, sem alterar
   o nome civil.
2. Pessoa jurídica continua usando `party.name`; não existe `social_name`
   aplicável a `party_company` nesta decisão.
3. Telas, buscas, listas, relatórios operacionais e respostas de API
   operacionais exibem `display_name`; contratos formais, fiscal e auditoria
   usam uma seleção explícita de `civil_name` e registram/snapshotam o que
   for exigido pelo contrato.
4. `deceased` e `family_link` não duplicam nome e exibem a semântica correta
   tanto para a pessoa falecida quanto para o familiar.
5. Testes de tenant com papel sem `SUPERUSER`/`BYPASSRLS` comprovam que leitura
   e escrita do nome social não atravessam tenants; nenhum endpoint aceita
   `tenant_id` do cliente.
6. Migration nova, forward-only, adiciona o campo nullable sem editar
   migration aplicada, preserva todos os nomes civis e deixa valores antigos
   como `NULL`.
7. Replay offline com a mesma `idempotency_key` não duplica a alteração;
   conflito de `version` fica pendente para reconciliação e não sobrescreve
   silenciosamente o dado mestre.
8. Cliente antigo continua desserializando o contrato existente; novos
   consumidores conseguem usar `display_name`, e nenhum evento versionado
   remove ou muda silenciosamente o significado de campo existente.
9. Logs, traces, métricas e prompts não contêm o nome social ou civil; uma
   consulta autorizada de auditoria consegue recuperar o registro por
   identificador opaco e finalidade registrada.
10. Testes cobrem entrada ausente, nula, vazia e preenchida, além de alteração
    posterior do nome social sem alteração de documentos formais já
    persistidos.

## Mudanças decorrentes

- **DER:** adicionar `social_name` opcional a `PERSON` e documentar
  `display_name`; não alterar `DECEASED` nem `FAMILY_LINK`.
- **Migration:** criar migration forward-only para `party.person`, com RLS e
  grants equivalentes ao restante do schema `party`; não editar `0002`.
- **API/eventos:** introduzir `display_name` de forma aditiva e documentar a
  compatibilidade de `name`/`civil_name` antes de alterar qualquer endpoint.
- **Relay/sync:** incluir o campo no contrato versionado de dado mestre,
  respeitando `version`, cursor, idempotência e reconciliação do ADR 0002.
- **Documentação de produto e LGPD:** atualizar PRD, DER, contratos públicos e
  política de privacidade somente na fatia de implementação, com revisão de
  segurança e dos contextos formal, fiscal e de auditoria.
