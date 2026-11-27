# ADR 0044 — Cadastro operacional de pessoa jurídica

Status: implementado no worktree da issue #78, com verificação local.

O cadastro usa o escopo de tenant de party e a empresa autorizada da sessão.
O CNPJ é normalizado em 14 posições e validado pelo cálculo do dígito
verificador documentado pela Receita Federal. Cadastros anteriores mantêm os
IDs, inclusive quando o documento tem pontuação ou não existe uma extensão
em party.party_company. A criação compara a forma normalizada para impedir
novos cadastros duplicados. A leitura conserva o documento armazenado e usa
a forma canônica para a busca por CNPJ. Documentos com dígitos ocultados
continuam consultáveis pelo nome, sem servir de identidade para o vínculo.

O vínculo liga a pessoa jurídica a uma pessoa física do mesmo tenant. A nova
tabela usa chaves compostas, RLS e grants mínimos. Ao vincular a uma PJ
legada com CNPJ válido, a transação cria a extensão vazia quando necessário
e reaproveita o ID existente. Contato, endereço e representante exigem
política aprovada no fluxo autenticado. A auditoria é gravada na mesma
transação que cada alteração e guarda somente códigos operacionais e IDs
opacos.

A migração 0038 preserva os dados legados. A API valida o formato de novos
endereços e o banco checa o CEP e a UF em inserções e alterações desses
campos. Ao definir um novo principal, o repositório serializa os escritores
e rebaixa os principais anteriores na mesma transação. Registros legados
continuam disponíveis com seus IDs e valores originais.

O cliente privado consome as rotas autenticadas. As rotas de desenvolvimento
permanecem limitadas ao ambiente privado com dados sintéticos.
A busca privada (GET /_dev/legal-entities) aceita q e limit e usa o mesmo
repositório e formato de resposta de GET /v1/legal-entities. O edge público
serve a demonstração e mantém sua política de conexão. A operação autenticada
depende de OIDC/MFA, e as gravações de dados pessoais dependem da política
aprovada por tenant.

[Especificação de CNPJ da Receita Federal](https://www.gov.br/receitafederal/pt-br/centrais-de-conteudo/publicacoes/documentos-tecnicos/cnpj/manual-dv-cnpj.pdf/view)

Consulte o [contrato HTTP v1](../contracts/party-legal-entity-v1.md) e o
[guia de operação privada](../userguide/operacao-staging.md#cadastro-pj-web-privado).
As rotas v1 são montadas nos dois ramos do bootstrap e retornam
`AUTHENTICATION_UNAVAILABLE` (503) sem OIDC. O modo dev é opt-in.
O DPO aprova finalidade, base legal e retenção por tenant para contato,
endereço e representante. A política resolvida fornece a retenção da
auditoria. Uma falha ao registrar a auditoria reverte a gravação.

## Verificação anterior à revisão da documentação

- O upgrade de 0037 a 0038 passou no PostgreSQL 16.16 descartável.
  A fixture tinha uma PJ sem extensão com CNPJ alfanumérico pontuado, dois
  contatos principais do mesmo tipo e dois endereços principais com valores
  históricos de CEP e UF. A comparação de todas as colunas de party,
  address e contact antes e depois produziu o mesmo hash. Sob o papel
  restrito, o banco permitiu rebaixar os dois endereços e rejeitou
  um novo endereço com formato inválido.
- `go test -p 1 -count=1 -v ./internal/modules/party -run '^TestProduction(LegalEntity|DeceasedFamilyLink|Parties)Integration'`
  passou em 23 casos com os dois DSNs apontados para o banco descartável
  migrado a 0038. O papel `amparius_app` tinha `rolsuper=false` e
  `rolbypassrls=false`. Os casos da #78 cobrem busca e detalhe legados, novo
  vínculo, duplicidade, isolamento, concorrência na troca de principal e
  falha fechada ou rollback de auditoria. A prova HTTP usa os handlers Go,
  PostgreSQL real e duplos OIDC, MFA e de política. A limpeza deixou zero
  linhas das fixtures e o container foi removido.
- `go build -p 1 ./...`, `go vet -p 1 ./...`,
  `go test -p 1 -count=1 ./...` e `goose -dir migrations validate` passaram.
  A suíte Go ampla rodou sem os DSNs de integração.
- `node --test web/staging/client-api.test.mjs` passou em 30 casos e
  `node --check` passou em client-api.js e client-live.js. O fetch simulado
  verifica a URL, o escopo e a desserialização da busca de PJ a partir de
  uma nova instância dev. Os eventos simulados da interface cobrem respostas
  fora de ordem, estados de erro e vazio, cliques repetidos e seleção de
  representante PF. A navegação real no navegador, OIDC/MFA real e a
  política DPO real ficaram fora desta verificação.

## Verificação independente do Líder Amparius

- Goose validate e up até 0038 no PostgreSQL 16.16 descartável. As 23
  integrações da #78 e de regressão das #72/#73 passaram com `amparius_app`
  (`rolsuper=f`, `rolbypassrls=f`). O comando dirigido também incluiu
  unitários de CNPJ e rotas dev. O pacote party concluiu em 11.154s.
  As sete contagens de fixtures zeraram. O ciclo down/up da 0038 passou.
  Container e volume foram removidos.
- `go test ./...` sem DSNs, vet e build passaram. O teste de `cmd/api`
  foi repetido após o registro das rotas da #78, incluindo o retorno sem OIDC.
- Passaram 51 testes Node do cliente e do painel, incluindo a seleção
  do representante legal no atendimento. Os testes da UI usam fetch
  e DOM simulados. A prova HTTP usa PostgreSQL real e duplos de autenticação
  e política. Navegação real da #78, integração OIDC/MFA real, política DPO
  real e implantação permanecem sem evidência nesta fatia.
