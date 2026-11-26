# Operação administrativa de licenças

O detalhe de entitlement do control plane é usado por `owner` e `admin`. O
papel `auditor` pode consultar, mas não concede, revoga ou altera quota.

As ações administrativas trabalham sobre uma empresa/CNPJ já vinculada ao
usuário autenticado. O tenant não é escolhido por cabeçalho nem por formulário.
O CNPJ aparece mascarado nas listagens.

Conceder usa `PUT /v1/companies/{companyId}/entitlements/{module}` com
`Idempotency-Key`; revogar, desativar, reativar validade ou ajustar quota usa
`PATCH` com `version` obrigatório. Se outra pessoa alterou o registro, o
servidor responde `STALE_VERSION`; a tela deve recarregar o detalhe antes de
tentar novamente.

Repetir uma requisição com a mesma chave e o mesmo corpo é seguro: o servidor
reapresenta o resultado persistido e não cria uma segunda auditoria. Usar a
mesma chave para outro corpo é erro `IDEMPOTENCY_CONFLICT`; a correção é gerar
uma chave nova.

O histórico pode ser aberto pelo detalhe ou pelo ID da licença. Ele mostra
ator, ação, origem e data, sem PII ou payload livre. Concessão, revogação,
desativação, reativação e alteração de quota não removem dados operacionais.

Se a quota não existir, a concessão falha fechado. Se não houver licença
disponível, a API retorna `LICENSE_POOL_EXHAUSTED`; o painel deve manter o
módulo bloqueado e explicar o motivo.
