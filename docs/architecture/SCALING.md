# Escala — células em VPS

Versão 0.1 · 21/11/2026 · complementa `ARCHITECTURE.md` §11

## 1. Estratégia em uma frase

O Amparius cresce por **células**: cada VPS roda uma pilha Docker completa para um conjunto de tenants; um **control plane** pequeno decide onde cada tenant mora. Escala vertical (aumentar a VPS) até um teto; a partir dele, **adiciona-se outra VPS** e novos tenants vão para lá.

```
                    ┌─────────────────────────────┐
   clientes ───────▶│  Edge / DNS  *.amparius     │
                    └──────────┬──────────────────┘
                               │ roteia por tenant
        ┌──────────────────────┼──────────────────────┐
        ▼                      ▼                      ▼
┌───────────────┐      ┌───────────────┐      ┌───────────────┐
│ Célula A (VPS)│      │ Célula B (VPS)│      │ Célula C (VPS)│
│ traefik       │      │ traefik       │      │ traefik       │
│ api x2        │      │ api x2        │      │ api x2        │
│ worker x2     │      │ worker x2     │      │ worker x2     │
│ postgres + rep│      │ postgres + rep│      │ postgres + rep│
│ redis/rabbitmq│      │ redis/rabbitmq│      │ redis/rabbitmq│
│ S3 object     │      │ S3 object     │      │ S3 object     │
└───────────────┘      └───────────────┘      └───────────────┘
        ▲                      ▲                      ▲
        └──────────┬───────────┴──────────────────────┘
                   │ provisiona, registra, mede
        ┌──────────▼──────────────────────────────────┐
        │ Control plane (2 VPS pequenas + Postgres)   │
        │ diretório de tenants · billing · status     │
        └─────────────────────────────────────────────┘
```

## 2. O que é uma célula

Uma VPS com Docker Compose (ou Swarm leve, se necessário) contendo:

| Componente | Réplicas | Observação |
|---|---|---|
| Traefik | 1 (host network) | TLS, roteamento por hostname, rate limit |
| `api` | 2 | Go, stateless, atrás do Traefik |
| `worker` | 2 | filas, emissão fiscal, relatórios |
| PostgreSQL | 1 primário + 1 réplica | streaming replication, PITR para object storage |
| PgBouncer | 1 | pool por tenant, limite de conexões |
| Redis | 1 | cache, locks, idempotência curta |
| RabbitMQ | 1 | quorum queues, DLQ |
| Object storage S3-compatible | externo ou por célula | XML, PDF, anexos |
| Backup (pgBackRest/wal-g) | sidecar | WAL contínuo + base diária |
| Agentes | node_exporter, otel collector | métricas e logs para o control plane |

Isolamento: rede Docker por célula, sem porta de banco exposta, mTLS entre edge/control plane e a célula.

O relay **não** é uma célula: é um papel do instalador no cliente (ADR 0006), com banco local e base temporária. Ele não entra na conta de capacidade das VPS; entra apenas como carga de sincronização e emissão fiscal.

## 3. Control plane

Pequeno e independente das células:

- **Diretório de tenants**: `tenant → célula`, plano, status, região, pesos de capacidade.
- **Provisionamento**: Terraform (VPS, DNS, firewall) + Ansible/cloud-init (stack Docker, segredos, TLS).
- **Billing e limites**: plano, usuários, documentos fiscais, armazenamento.
- **Autenticação global**: OIDC, chaves de assinatura distribuídas às células (validação offline com cache).
- **Observabilidade agregada**: métricas por célula e por tenant; capacidade e alertas.
- **Status page** e incidentes.

O control plane **não** guarda dado de negócio. Se ele cair, as células continuam operando (chaves e mapeamentos em cache; provisionamento pausa).

## 4. Roteamento

1. Cliente acessa `cliente.amparius.com.br`.
2. Edge resolve o tenant (subdomínio; fallback por header assinado).
3. Edge consulta o mapeamento em cache (origem: control plane) e encaminha para a célula.
4. Relay recebe o endpoint da célula no provisionamento e atualiza via control plane.
5. Falha de resolução → página de status, nunca “banco não encontrado”.

## 5. Dimensionamento

### 5.1 Perfil de referência (a validar com carga)

**Unidade de Tenant (TU)**: 8 usuários, 300 documentos fiscais/mês, 20 mil requisições/dia, 5 GB/ano.

| Tamanho | vCPU | RAM | Disco | Tenants estimados |
|---|---|---|---|---|
| S | 4 | 8 GB | 160 GB NVMe | ~40 |
| M | 8 | 32 GB | 320 GB NVMe | ~120 |
| L | 16 | 64 GB | 640 GB NVMe | ~250 |

Números são **hipótese de planejamento** até o teste de carga da fase 1. O perfil pesado (cemitério com mapa e muitos documentos) consome 3–5× um perfil simples; o balanceador usa peso, não contagem.

### 5.2 Quando crescer

Adicionar célula quando **qualquer** condição persistir por 30 min:

- CPU da célula > 65% (p95);
- p95 de leitura > 300 ms ou escrita > 900 ms;
- conexões do Postgres > 70% do máximo do PgBouncer;
- disco > 70% ou IOPS > 70%;
- lag da fila > 2 min;
- falha de emissão fiscal por timeout > 1%.

Antes de adicionar célula: tentar **escala vertical** (subir um tier) se a célula estiver abaixo de L e o gargalo for CPU/RAM. Acima de L, só sharding.

## 6. Operações de escala

### 6.1 Adicionar célula

1. Terraform cria a VPS e o DNS interno.
2. Ansible instala Docker, stack, segredos e TLS.
3. Célula se registra no control plane com capacidade e região.
4. Health check e teste sintético (criar tenant, emitir documento de teste).
5. Célula entra como “elegível”; novos tenants passam a cair nela.

Tempo alvo: **< 30 min** do comando ao estado elegível.

### 6.2 Distribuir tenants

- Bin-packing por TU e peso de perfil, com afinidade (grupo econômico, revendedor).
- Nunca ultrapassar 70% da capacidade planejada.
- Tenant novo sempre vai para a célula elegível com menor custo de colocação.

### 6.3 Migrar tenant entre células

1. Exportação lógica (schemas do tenant) + objetos no S3-compatible.
2. Restauração na célula destino e verificação de contagem/hash.
3. Congelamento curto de escrita (manutenção programada).
4. Troca do mapeamento no control plane; relay recebe novo endpoint.
5. Descongelamento e verificação fiscal (últimos documentos, saldo).

Alvo: **< 30 min** para tenant pequeno; janela comunicada com antecedência.

### 6.4 Desativar célula

Drenar tenants (migração), congelar provisionamento, exportar backups finais, destruir após 30 dias de retenção.

## 7. Dados e backup

- **PostgreSQL por célula**; nenhuma consulta cruza células.
- Tabelas de referência fiscal (NCM, CFOP, cClassTrib, alíquotas) chegam às células como **pacote assinado e versionado**, atualizado pelo control plane.
- Backup: WAL contínuo para object storage + base diária; retenção 30 dias; teste de restauração mensal.
- Exportação por tenant é funcionalidade de produto (portabilidade e LGPD), não só operação.

## 8. Falhas

| Falha | Impacto | Resposta |
|---|---|---|
| Célula fora | tenants daquela célula indisponíveis; relay segue local | restaurar/recriar; RTO 1 h, RPO 5 min |
| Postgres da célula corrompido | idem | PITR + verificação de integridade fiscal |
| Control plane fora | provisionamento pausa; células operam | chaves e mapeamentos em cache; corrigir |
| Edge fora | acesso web indisponível | edge secundário + DNS failover |
| VPS sem disco | escrita falha | alerta em 70%; expansão a quente |
| Célula comprometida | blast radius limitado | isolar, rotacionar segredos, reconstruir |

## 9. Segurança operacional

- Uma VPS = uma célula; sem containers de tenants diferentes no mesmo host fora da célula.
- Firewall: só 80/443 no edge da célula; SSH por chave e bastion; banco apenas na rede interna.
- Segredos por célula (Vault/agenix/SOPS), nunca compartilhados entre células.
- Patch mensal do host e das imagens; CIS baseline.
- Backup criptografado; teste de restauração semestral com evidência.

## 10. Custos e decisão

Regra prática: adicionar célula quando o custo incremental da VPS for menor que o risco de manter uma célula acima de 70% (latência, indisponibilidade, janela de deploy). Escala vertical é mais simples, mas concentra risco: uma VPS maior continua sendo um ponto único de falha e um blast radius maior.

| Cenário | Ação |
|---|---|
| Célula S em 70% e fila crescendo | subir para M |
| Célula M em 70% com tenant pesado | subir para L |
| Célula L em 70% | adicionar célula e distribuir novos tenants |
| Tenant grande (> 20% de uma célula) | célula dedicada |
| Grupo econômico com 10+ lojas | avaliar célula dedicada por afinidade |

## 11. Roteiro de implantação da escala

- **Fase 1**: uma célula (M) + control plane mínimo (diretório + status).
- **Fase 2**: provisionamento automatizado e segunda célula; migração de tenant ensaiada.
- **Fase 3**: packing automático, painel de capacidade, teste de caos de célula.
- **Fase 4**: HA do Postgres por célula (Patroni ou gerenciado), multi-região opcional.
