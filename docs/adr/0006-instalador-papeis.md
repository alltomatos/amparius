# ADR 0006 — Um instalador, dois papéis: estação e servidor da loja

Status: aceito · 21/11/2026 · refina o ADR 0002

## Contexto

O Amparius precisa rodar no computador do cliente (Windows e Linux primeiro, macOS depois) e continuar funcionando quando a internet falha. Em muitas unidades há mais de um terminal no mesmo balcão. O relay não deve ser uma caixa separada e opaca: é o mesmo produto, com outro papel.

## Decisão

- **Um instalador, um aplicativo, dois papéis escolhidos na configuração:**
  - **Estação**: cliente desktop do operador; fala com o SaaS ou com o servidor da loja.
  - **Servidor da loja (relay)**: mantém banco local, serve os terminais da rede local e sincroniza com o SaaS.
- O papel é uma configuração persistida e reversível; a mesma licença cobre os dois.
- No papel de servidor da loja, o aplicativo:
  1. roda PostgreSQL local e um servidor web local (HTTPS) para os terminais da LAN;
  2. mantém a **base temporária**: cadastros, catálogo e preços, tabelas fiscais vigentes, contratos/OS abertos, caixa do dia, agenda e últimos 90 dias de histórico (janela configurável, com busca sob demanda);
  3. autentica operadores com credencial emitida pelo SaaS e validada localmente (cache com validade);
  4. enfileira efeitos remotos e sincroniza ao reconectar, com idempotência e reconciliação.
- **Acesso web**:
  - online: o portal web do SaaS, sempre disponível;
  - offline: o próprio servidor da loja serve a mesma interface na rede local;
  - opcional: túnel de saída para acesso remoto, desligado por padrão.
- **Segurança**: matrícula do dispositivo no control plane, chave por instalação, TLS local com certificado emitido no provisionamento, servidor da loja escutando apenas na LAN por padrão.
- **Backup local** diário com exportação, além do backup do SaaS.

## Consequências

- Instalação e atualização viram um só fluxo, com verificação de papel no primeiro uso.
- A base local é deliberadamente **parcial**: não é réplica completa do SaaS.
- A interface precisa ser a mesma nos três acessos (web SaaS, web local, desktop), evitando três produtos.
- Testes de paridade entre modos passam a ser obrigatórios, incluindo operação em LAN sem internet.
- Suporte ganha um diagnóstico local (estado do banco, fila, versão, conectividade).

## Alternativas descartadas

- Relay como appliance separado: mais um produto para instalar, atualizar e suportar.
- Somente web: não atende queda de internet nem balcão com vários terminais.
- Banco local completo: custo e complexidade sem ganho para o caso de uso.
