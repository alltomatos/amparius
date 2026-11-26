# Control plane frontend

Shell inicial das telas `CP-02` e `CP-05`. A aplicacao e clean-room e usa somente os contratos do Amparius.

## Executar localmente

Sirva a raiz do repositório com um servidor HTTP estático local, abra
`/web/control-plane/` e configure o runtime antes de carregar `app.js`. O
servidor deve expor `assets/brand/amparius-logo.svg` para o cabeçalho:

```html
<script>
  window.AMPARIUS_CONFIG = { apiBase: "http://localhost:8080", accessToken: "token-em-memoria" };
</script>
```

O exemplo nao persiste o token em `localStorage`, cookies criados pelo frontend ou URL. Em producao, o bootstrap deve receber a sessao pelo mecanismo OIDC/Bearer aprovado no ADR 0024.

## Cobertura

- contexto autenticado e empresa atual;
- listagem filtravel de empresas com CNPJ mascarado;
- entitlements com estado, validade e uso de quota;
- paginacao por cursor;
- estados loading, empty, error e success;
- layout responsivo para 360 px e foco visivel.

Ainda nao ha troca de empresa: o backend fixa `company_id` no token, conforme `docs/contracts/control-plane-context-v1.md`.

## Testes

```bash
node --test web/control-plane/tests/contract.test.mjs
```
## Prévia de homologação

A [prévia sintética](https://hml.amparius.com/painel/) usa esta interface.
Ela carrega o cenário de demonstração antes de `app.js` e mantém as alterações
e a auditoria somente em memória. Consulte o
[guia de staging](../../docs/userguide/operacao-staging.md) para preparar
o pacote estático e verificar as rotas publicadas.
