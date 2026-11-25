# Control plane frontend

Shell inicial das telas `CP-02` e `CP-05`. A aplicação é clean-room e usa somente os contratos do Amparius.

## Executar localmente

Sirva esta pasta com um servidor HTTP estático e configure o runtime antes de carregar `app.js`:

```html
<script>
  window.AMPARIUS_CONFIG = { apiBase: "http://localhost:8080", accessToken: "token-em-memoria" };
</script>
```

O exemplo não persiste o token em `localStorage`, cookies criados pelo frontend ou URL. Em produção, o bootstrap deve receber a sessão pelo mecanismo OIDC/Bearer aprovado no ADR 0024.

## Cobertura

- contexto autenticado e empresa atual;
- listagem filtrável de empresas com CNPJ mascarado;
- entitlements com estado, validade e uso de quota;
- paginação por cursor;
- estados loading, empty, error e success;
- layout responsivo para 360 px e foco visível.

Ainda não há troca de empresa: o backend fixa `company_id` no token, conforme `docs/contracts/control-plane-context-v1.md`.
