# Painel SaaS: tenants e licenças

Esta área é do domínio `manager.amparius.com` e só aceita a pessoa com o papel
`platform_admin`, com MFA. Clientes (`app.amparius.com`) não acessam essas
rotas e usuários de tenant, mesmo `owner`, recebem `403`.

O painel lê todos os tenants de uma vez por views de leitura, sem abrir uma
requisição por empresa. Cada resposta mostra `scope: platform` para deixar claro
que o dado é da plataforma, não de um tenant.

O que já aparece:

- **Tenants:** total de módulos com cota, soma de cotas, licenças ativas,
  assentos de usuário e data do último evento.
- **Licenças do tenant:** cota e uso por módulo e as licenças por empresa
  (identificadas pelo id, ainda sem nome ou CNPJ).
- **Histórico:** quem concedeu, revogou ou alterou, quando e por qual origem.

Ações disponíveis (todas com o tenant escolhido na rota e `Idempotency-Key`):

- conceder licença a uma empresa;
- revogar licença, informando a `version` lida na tela (se outra pessoa mudou
  antes, o servidor responde `STALE_VERSION`: recarregue e tente de novo);
- ajustar a cota de um módulo.

Se a cota do módulo acabou, a concessão é recusada com `LICENSE_POOL_EXHAUSTED`.
Se o relatório de plataforma estiver indisponível, a tela mostra o erro
`PLATFORM_REPORT_UNAVAILABLE` em vez de qualquer número estimado.

Ainda não disponível: nome e CNPJ mascarado das empresas, saúde por módulo,
vencimento de licenças e IA. Em produção, a entrada por OIDC/MFA de plataforma
ainda precisa ser ligada; até lá as rotas respondem `503`.
