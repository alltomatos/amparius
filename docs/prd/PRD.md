# PRD — Amparius

Versão 0.1 · 21/11/2026 · status: rascunho para revisão

## 1. Visão

Um ERP modular, multi-tenant e agêntico para o **setor de serviços funerários, cemitérios e floriculturas**, com financeiro e fiscal como espinha dorsal. A operação continua mesmo sem internet, através de um **relay local**; a IA é parte do produto (atendimento, cobrança, fiscal, análise), não um plug-in.

Em uma frase: *o sistema que uma funerária usa para vender, atender a família, emitir nota e fechar o mês — sem parar quando a conexão cai.*

## 2. Problema e oportunidade

O setor é fragmentado: planilhas para contratos, sistema de cemitério separado do financeiro, emissão de NFS-e em portal municipal, controle de flores em caderno. Consequências:

- retrabalho fiscal e risco de multa;
- caixa sem visão consolidada (contratos, planos, serviços avulsos);
- atendimento lento no momento mais sensível (velório/sepultamento);
- perda de venda de floricultura por falta de estoque/entrega;
- nenhuma leitura analítica do negócio.

Ao mesmo tempo, 2026–2033 é a transição da **reforma tributária do consumo (IBS/CBS/IS)**. Um ERP novo tem a chance de nascer com o motor RTC correto, em vez de remendar um legado.

## 3. Personas e trabalhos

| Persona | Contexto | Trabalhos principais |
|---|---|---|
| **Dono da funerária** | 1–3 lojas, 10–60 funcionários | ver caixa, margem por plano, inadimplência, emitir nota sem susto |
| **Atendente funerário** | plantão, pressão emocional | abrir contrato rápido, reservar jazigo, agendar velório, receber pagamento |
| **Administrador de cemitério** | jazigos, sepultamentos, manutenção | mapa de quadras, concessões, taxas, exumação, histórico |
| **Floricultor / loja** | pedidos, entregas, perecíveis | catálogo, estoque por lote, rota de entrega, pedido no balcão |
| **Financeiro** | contas, conciliação, cobrança | títulos, baixa, conciliação bancária, régua de cobrança |
| **Contador externo** | escrituração | exportar XML/SPED, conferir tributação, fechamento |
| **Administrador SaaS** | multiempresa | onboarding, planos, limites, suporte, auditoria |
| **Agente de IA** | copiloto em cada módulo | responder, sugerir, executar com permissão |

## 4. Escopo

### 4.1 Espinha dorsal (MVP obrigatório)

- **Identidade e tenant**: empresa, filial, usuários, papéis, permissões, MFA, trilha de acesso.
- **Cadastro único**: pessoas físicas/jurídicas, endereços, contatos, representantes, falecidos e familiares.
- **Catálogo**: produtos, serviços, planos, tabelas de preço, NCM, código de serviço (LC 116), CNAE.
- **Financeiro**: plano de contas, centros de custo, contas bancárias, títulos a pagar/receber, parcelas, baixas, caixa, conciliação, ledger de partidas dobradas.
- **Fiscal**: configuração por empresa/CNPJ (regime, certificado, inscrições) — não por tenant (ADR 0007 §10); motor de tributos (incluindo RTC), emissão de **NFS-e** e **NF-e/NFC-e**, contingência, cancelamento, armazenamento de XML, DANFSe/PDF.
- **Relay local**: operação offline, fila de emissão, sincronização, reconciliação.

### 4.2 Pacotes de setor (ativáveis)

- **Funerário**: contrato, plano funerário, serviço avulso, ordem de serviço, velório, traslado, memorial, cobrança recorrente.
- **Cemitério**: cemitério, quadra, jazigo, concessão, sepultamento, exumação, manutenção, mapa e ocupação.
- **Cremação**: crematório, cremação, urna, autorização (módulo próprio — ADR 0007; independente de funerário/cemitério).
- **Floricultura**: catálogo sazonal, lote/validade, arranjo, pedido, entrega, rota, fornecedor, compras.

### 4.3 Transversal

- Estoque e almoxarifado; documentos e assinatura; notificações (e-mail/WhatsApp); relatórios e dashboards; IA/MCP; auditoria; importação/exportação; API pública e webhooks.

### 4.4 Modos de uso e acesso

| Modo | Onde roda | Para quem |
|---|---|---|
| **Web** | navegador, portal SaaS | gestão, financeiro, contador, acesso remoto |
| **Desktop — estação** | PC do operador (Windows/Linux; macOS depois) | atendimento no balcão |
| **Desktop — servidor da loja** | um PC da unidade, marcado na configuração | serve os terminais da LAN e assume quando a internet cai |

O servidor da loja guarda uma **base temporária** (cadastros, preços, tabelas fiscais, contratos/OS abertos, caixa, agenda e histórico recente) e sincroniza ao reconectar. A interface é a mesma nos três modos.

### 4.5 Fora de escopo (v1)

Folha de pagamento, eSocial completo, contabilidade completa (diário/razão oficial), produção industrial, frota, BI avançado, marketplace.

## 5. Jornadas críticas

### J1 — Atendimento e contrato (funerária, com internet)

1. Atendente busca família/pessoa; se não existe, cadastra em < 60 s.
2. Escolhe plano ou monta serviço; sistema mostra preço, impostos estimados e disponibilidade.
3. Gera contrato, coleta assinatura, registra pagamento parcial.
4. Agenda velório/sepultamento; reserva jazigo.
5. Emite NFS-e; XML e PDF ficam anexados ao contrato.

### J2 — Mesma jornada sem internet

1. Relay local autentica com credencial em cache (validade configurável).
2. Contrato e recebimento acontecem localmente; NFS-e entra em **fila de contingência**.
3. Ao voltar a conexão: sync envia operações; emissão é transmitida na ordem; falha vira tarefa com alerta.
4. Divergência de cadastro é reconciliada com regra explícita (servidor autoritativo para mestres).

### J3 — Fechamento mensal

1. Financeiro fecha caixa e concilia banco.
2. Sistema consolida receitas por contrato/plano, impostos por documento e pendências.
3. Contador exporta XMLs, relatório de apuração e arquivo para contabilidade.
4. Dono vê margem por serviço e inadimplência.

### J4 — Floricultura no balcão

1. Atendente monta arranjo; sistema baixa insumos por lote e calcula preço.
2. Agenda entrega; rota do dia é impressa/enviada.
3. Pedido gera título e, se aplicável, nota.

### J5 — Cemitério

1. Administrador consulta mapa de ocupação.
2. Registra sepultamento em jazigo com concessão vigente.
3. Emite taxa, agenda manutenção e mantém histórico do jazigo.

## 6. Requisitos funcionais (recorte)

| ID | Módulo | Requisito |
|---|---|---|
| RF-001 | Identidade | Criar tenant com filial, regime tributário e certificado |
| RF-002 | Identidade | Papéis owner/admin/operador/financeiro/contador com permissões por módulo |
| RF-003 | Cadastro | Unificar PF/PJ, endereço, contato, representante e vínculos |
| RF-004 | Cadastro | Cadastro de falecido e familiares com consentimento e retenção definida |
| RF-010 | Catálogo | Produto/serviço com NCM, CST, cClassTrib, código LC 116 e vigência |
| RF-020 | Financeiro | Título a receber/pagar com parcelas, juros, desconto e baixa parcial |
| RF-021 | Financeiro | Caixa por usuário com abertura, sangria, suprimento e fechamento |
| RF-022 | Financeiro | Conciliação bancária por arquivo OFX/CNAB |
| RF-023 | Financeiro | Ledger de partidas dobradas com fechamento por competência |
| RF-030 | Fiscal | Motor de tributos por data de competência, com tabelas versionadas |
| RF-031 | Fiscal | Emitir NFS-e com validação de XSD e retorno persistido |
| RF-032 | Fiscal | Emitir NF-e/NFC-e com contingência e reconciliação |
| RF-033 | Fiscal | Cancelar/substituir dentro do prazo legal, bloqueando fora dele |
| RF-034 | Fiscal | Gerar DANFSe/DANFE em PDF a partir do XML autorizado |
| RF-040 | Funerário | Contrato com plano/serviço, parcelamento e ordem de serviço |
| RF-041 | Funerário | Agenda de velório/sepultamento com conflito de recurso |
| RF-050 | Cemitério | Mapa de quadras/jazigos com ocupação e concessão |
| RF-051 | Cemitério | Registrar sepultamento, exumação e manutenção com histórico |
| RF-060 | Floricultura | Estoque por lote/validade e baixa por composição de arranjo |
| RF-061 | Floricultura | Pedido com entrega, janela e rota |
| RF-070 | Relay | Operar offline com fila idempotente e status visível |
| RF-071 | Relay | Sincronizar e reconciliar conflitos com trilha |
| RF-080 | IA | Copiloto por módulo com tools permitidas e auditoria |
| RF-081 | IA | Sugerir cobrança, classificação fiscal e resposta a cliente |
| RF-090 | Plataforma | API pública com chave por tenant e webhooks assinados |
| RF-100 | Entitlement | Catálogo de módulos: núcleo (7) fora da cota e opcionais (7) na cota |
| RF-101 | Entitlement | Cota de licenças por módulo e cota de acessos/usuários por tenant (admin SaaS) |
| RF-102 | Entitlement | Distribuir licenças por CNPJ: 1 licença = 1 módulo em 1 empresa; filial herda |
| RF-103 | Entitlement | Ativar/desativar módulo em self-service, com bloqueio nas 3 camadas (UI/backend/relay) |
| RF-104 | Entitlement | Sem licença ativa o módulo não funciona; admin SaaS sobrepõe o estado do tenant |
| RF-105 | Entitlement | Auditoria de ativação/desativação (quem, quando, origem) + leitura congelada de fiscal/reporting |

## 7. Requisitos não funcionais

| Área | Meta |
|---|---|
| Latência (SaaS) | leitura p95 < 200 ms; escrita p95 < 600 ms (fora SEFAZ) |
| Emissão fiscal | < 2 s até transmissão; tempo de SEFAZ fora do nosso controle |
| Disponibilidade | 99,9% no SaaS; relay garante operação local |
| Offline | mínimo 24 h de operação; sync em até 5 min após reconexão |
| RPO/RTO | RPO 5 min; RTO 1 h |
| Isolamento | RLS por tenant; teste automatizado de vazamento |
| Segurança | OIDC + MFA; RBAC/ABAC; segredos em cofre; criptografia em repouso |
| LGPD | base legal por finalidade, retenção, anonimização, trilha de consentimento |
| Observabilidade | trace por requisição, log estruturado, métricas por tenant |
| Acessibilidade | WCAG 2.2 AA nas telas críticas |
| Portabilidade | dados exportáveis por tenant (JSON/CSV + XML fiscal) |

## 8. Métricas de sucesso

- Tempo para emitir a primeira NFS-e em um tenant novo: **< 1 dia**.
- Contrato completo (da busca ao pagamento): **< 6 min**.
- Operações offline sincronizadas sem intervenção: **> 99%**.
- Divergências fiscais em fechamento: **< 0,5%** dos documentos.
- Retenção de tenant após 90 dias: **> 90%**.
- Uso do copiloto: **> 40%** dos usuários ativos semanais.

## 9. Riscos

| Risco | Mitigação |
|---|---|
| Regras municipais de NFS-e heterogêneas | adaptadores por município + cadastro de provedor; começar por 2–3 cidades |
| Reforma tributária em transição | motor RTC com tabelas oficiais datadas; testes com calculadoras da RFB |
| Complexidade do offline | escopo explícito do que funciona offline; fila idempotente e reconciliação auditável |
| Certificado digital do cliente | suporte A1 no servidor e no relay; alerta de expiração |
| Dados de falecidos/familiares | minimização, retenção, acesso restrito e auditoria |
| Time-to-market | monólito modular primeiro; extrair serviço só com evidência de necessidade |

## 10. Decisões abertas

1. **NFS-e primeiro ou NF-e?** Proposta: NFS-e (serviço é o core do setor) + NFC-e opcional para floricultura.
2. **Municípios iniciais** para NFS-e (Fortaleza, São Paulo, Rio?) — define adaptadores.
3. **Contador**: exportação de arquivos ou integração com sistemas contábeis específicos?
4. **Relay**: PostgreSQL local (consistente) ou embarcado (leve)? Proposta: Postgres local no relay, com modo leve depois.
5. **App de campo**: PWA primeiro ou Rust desktop desde o início?
6. **Cobrança do SaaS**: assinatura por empresa, por usuário ou por volume fiscal? (ADR 0007: cobrança fica para depois, mas o modelo já grava licença por módulo com cota e vigência datada — gancho pronto.)
7. **Hospedagem**: região única no Brasil ou duas (ativa/ativa leve)?

## 11. Glossário

- **Tenant**: grupo econômico cliente do SaaS, com uma ou mais empresas (CNPJ); cada empresa pode ter filiais (estabelecimentos).
- **Relay**: servidor local no cliente que mantém operação e sincroniza.
- **Espinha dorsal**: financeiro + fiscal, dos quais os módulos dependem.
- **Pacote de setor**: conjunto de módulos e vocabulário ativado por vertical.
- **RTC**: Reforma Tributária do Consumo (IBS/CBS/IS), LC 214/2025.
- **Contingência**: modo de emissão quando a transmissão não é possível.
