# Cliente web da loja (staging)

Prévia web da loja do Amparius. Tem dois modos:

- **Demonstração (padrão):** estado sintético em memória (`client-demo.js`),
  sem rede. É o que a prévia pública em homologação serve.
- **Operacional (privado):** quando `window.AMPARIUS_CONFIG` está presente,
  `client-api.js` conversa com a API Go e `client-live.js` substitui os fluxos
  de Cadastros e Atendimento por chamadas à API. Com Bearer, habilita o
  Painel do dia e os títulos financeiros. Sem Bearer, essas duas leituras
  ficam indisponíveis, inclusive no modo dev privado.

## Configurar o modo operacional

```html
<script>
  // Rotas autenticadas /v1 (Authorization: Bearer):
  window.AMPARIUS_CONFIG = {
    apiBase: "http://localhost:8080",
    accessToken: "token-em-memoria",
    agendaDate: "2026-11-26",
    agendaTimeZone: "America/Fortaleza",
  };

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
Forneça a data civil da operação (`agendaDate`, no formato AAAA-MM-DD) e o
fuso IANA da loja (`agendaTimeZone`) no bootstrap privado, antes dos
scripts. O Painel do dia consulta a janela configurada, usa o fuso informado
para exibir horários e mostra um aviso se a configuração estiver ausente
ou inválida. Atualize a data no bootstrap para trocar o dia.

## Contratos consumidos

- PF: `POST /v1/parties`, `GET /v1/parties` (e `/_dev/parties`) —
  `docs/contracts/party-pf-v1.md`.
- Falecido e vínculo: `POST /v1/deceased`,
  `POST /v1/deceased/{id}/family-links` (e `/_dev/deceased`,
  `/_dev/family-links`) — `docs/contracts/party-deceased-family-link-v1.md`.
- Catálogo: `GET /v1/catalog/items`, `GET /v1/catalog/price-lists`
  (e `/_dev/catalog/...`) — `docs/contracts/catalog-operational-v1.md`.
- Painel do dia: `GET /v1/funeral/agenda` — agendamentos e confirmações
  pendentes da empresa autenticada, com licença Funerária vigente.
  [Contrato da agenda](../../docs/contracts/funeral-schedule-http-v1.md#agenda-da-empresa).
- Financeiro: `GET /v1/finance/titles` — títulos do grupo econômico,
  com filtros de situação, direção e vencimento inclusivo. **Próxima
  página** usa o cursor retornado pela API com os mesmos filtros.
  [Contrato financeiro](../../docs/contracts/finance-titles-payments-v1.md).

## Testes

```bash
node --test --test-concurrency=1 web/staging/client-api.test.mjs web/control-plane/tests/contract.test.mjs web/control-plane/tests/staging-demo.test.mjs
```

O harness Node usa transporte e DOM simulados. Exercita a configuração de
data e fuso, os estados de carregamento, vazio, offline, erro, `401`, `403`,
`503` e licença bloqueada, bem como respostas fora de ordem, filtros,
cursor, precisão dos centavos e a prévia sem rede.

## Limites

A emissão fiscal, a assinatura de contrato e a sincronização real com o relay
não fazem parte desta superfície. O vínculo e o falecido são persistidos via
API; a fila do atendimento permanece local até a sincronização ponta a ponta
da jornada offline (issue #77 / #59). A fila de demonstração simula a
sincronização local em memória. A leitura de títulos mostra o valor
nominal, a direção e a situação persistida. Parcelas, recebimentos, saldo,
caixa e conciliação ficam em fatias posteriores. A
[evidência da #79](../../docs/design/mvp-store-dashboard-read-evidence.md#fatia-web-após-a-92)
registra a verificação local e o que falta validar com a sessão real.

O navegador deve suportar `BigInt`. Para valores acima do limite inteiro
seguro de `Number`, deve também disponibilizar o texto do número ao
reviver de `JSON.parse`. Nessa faixa, a tela apresenta erro se o navegador
perder a precisão durante a leitura.
