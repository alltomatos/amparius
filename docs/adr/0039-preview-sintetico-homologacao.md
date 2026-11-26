# ADR 0039 — Prévia web sintética de homologação

- **Status:** aceito para desenvolvimento.
- **Data:** 2026-11-26.

O cliente web de homologação fica em `hml.amparius.com` e a demonstração do
painel SaaS em `/painel/`. A superfície web é um caminho de validação visual
do painel administrativo e do fluxo de fila. A estação Rust continua sendo
o cliente com persistência offline e transporte previstos nos ADRs 0026 e 0034.

O edge serve somente arquivos estáticos selecionados para a demonstração.
As alterações acontecem em memória na aba com dados sintéticos e contexto fixo.
A política de conteúdo bloqueia conexões de rede iniciadas pela aplicação.
Somente os probes de saúde e prontidão são encaminhados à API. Operações de
desenvolvimento com banco continuam via túnel SSH e usam RLS.

A prévia independe da escolha de OIDC/MFA. O acesso administrativo real
continua sujeito ao ADR 0024. A API mantém suas regras de licenças e quotas,
sem alteração de schema, tabela ou contrato HTTP. Emissão fiscal oficial e
sincronização ponta a ponta continuam com seus critérios de aceite próprios.
