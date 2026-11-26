# Cliente web da loja (staging)

Prévia web da loja do Amparius. Tem dois modos:

- **Demonstração (padrão):** estado sintético em memória (`client-demo.js`),
  sem rede. É o que a prévia pública em homologação serve.
- **Operacional (privado):** quando `window.AMPARIUS_CONFIG` está presente,
  `client-api.js` conversa com a API Go e `client-live.js` substitui os fluxos
  de Cadastros e Atendimento por chamadas reais, com estados de vazio, erro,
  `401`, `403` e offline.

## Configurar o modo operacional

```html
<script>
  // Rotas autenticadas /v1 (Authorization: Bearer):
  window.AMPARIUS_CONFIG = { apiBase: "http://localhost:8080", accessToken: "token-em-memoria" };

  // Ou, ambiente privado de desenvolvimento (rotas /_dev, sem OIDC real):
  // window.AMPARIUS_CONFIG = {
  //   apiBase: "http://localhost:8080",
  //   devTenantId: "<uuid>",
  //   devCompanyId: "<uuid>",
  // };
</script>
```

O token não é persistido em `localStorage`, cookie criado pelo frontend ou
URL. Em produção, o bootstrap recebe a sessão pelo mecanismo OIDC/Bearer do
ADR 0024. As rotas `/_dev` só existem quando `APP_ENV=development` e
`ENABLE_DEV_ONBOARDING=true`, e permanecem fora do edge público.

## Contratos consumidos

- PF: `POST /v1/parties`, `GET /v1/parties` (e `/_dev/parties`) —
  `docs/contracts/party-pf-v1.md`.
- Falecido e vínculo: `POST /v1/deceased`,
  `POST /v1/deceased/{id}/family-links` (e `/_dev/deceased`,
  `/_dev/family-links`) — `docs/contracts/party-deceased-family-link-v1.md`.
- Catálogo: `GET /v1/catalog/items`, `GET /v1/catalog/price-lists`
  (e `/_dev/catalog/...`) — `docs/contracts/catalog-operational-v1.md`.

## Testes

```bash
node --test web/staging/client-api.test.mjs
```

O teste de contrato cobre os dois modos, as formas de sucesso/erro de
produção e de desenvolvimento, e os estados de vazio, erro, `401`, `403` e
offline usando um `fetch` simulado, sem rede real.

## Limites

A emissão fiscal, a assinatura de contrato e a sincronização real com o relay
não fazem parte desta superfície. O vínculo e o falecido são persistidos via
API; a fila do atendimento permanece local até a sincronização ponta a ponta
da jornada offline (issue #77 / #59).
