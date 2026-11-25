# Operação offline do relay: contrato e caixa

Este guia é para quem atende no balcão e precisa continuar trabalhando quando a internet cai.
Ele descreve o que já funciona nesta versão, o que ainda não funciona e como ler o aviso de
fila.

## O que continua funcionando sem internet

- Abrir contrato e registrar seus itens e valores.
- Abrir caixa, registrar entrada e saída de dinheiro e fechar caixa.
- Consultar o catálogo e os preços que já estão no cache local da loja.
- Reiniciar o computador ou o relay sem perder contrato ou caixa já registrados.

Cada contrato ou movimento de caixa é gravado primeiro no computador da loja, com número de
ordem próprio do dispositivo. A ordem nunca volta atrás nem se repete.

## O que ainda não funciona sem internet

- Emitir NFS-e ou falar com SEFAZ/prefeitura em tempo real.
- Concluir a transmissão com o SaaS: o relay já preserva e reapresenta a fila no engine de
  reconexão, com envelope versionado por dispositivo, mas o adaptador de rede autenticado
  ainda não está nesta versão.
- Relatórios consolidados de várias lojas.
- Contratar novo tenant ou alterar plano do SaaS.

Se uma tela depender de algo que não funciona offline, ela deve dizer isso de forma clara em
vez de fingir que salvou no servidor.

## Como ver o estado

O relay expõe o estado da fila para a tela do cliente. A tela deve mostrar um dos estados:

| Estado | Significado |
|---|---|
| **Online** | não há operação pendente; o sistema está conectado |
| **Offline com fila** | há operações guardadas no computador da loja aguardando envio |
| **Degradado** | há falha terminal ou conflito que exige revisão do suporte |
| **Contingência fiscal** | aparece quando a emissão fiscal em contingência existir; ainda não nesta versão |

Ao abrir o estado da fila, a tela mostra:

- quantas operações estão **pendentes**, **sincronizadas**, com **falha** e em **conflito**;
- quantas operações estão em **retry** e, separadamente, o resumo fiscal com pendentes,
  sincronizadas, falhas, conflitos e último erro;
- a **sequência** da última operação do dispositivo;
- **quantos minutos ou horas** tem a operação pendente mais antiga;
- o **último erro** de envio e o número de tarefas de revisão abertas, sem dados pessoais.

O relay já entrega esses números pela rota de status; a ligação visual é da tela do cliente.

Contagem e idade são o sinal de risco. Uma fila que cresce por horas é esperada numa queda de
rede; uma fila que cresce sem queda de rede pede suporte.

O relay tenta uma submissão no máximo três vezes por operação. Timeout ou
indisponibilidade antes disso aparece como pendente em retry. Depois da terceira
falha, a operação fica como falha terminal e uma tarefa de revisão é aberta; o
relay não cria outro número nem autoriza a emissão.

## Queda de rede: o que fazer

1. Continue o atendimento normalmente. Contrato e caixa são gravados localmente e aparecem
   como pendentes.
2. Não desligue o computador da loja no meio de uma gravação. Se precisar reiniciar, espere a
   confirmação na tela.
3. Ao voltar a internet, não apague a pasta local do relay nem reinstale o programa: a fila
   fica nela. A sincronização automática entra na próxima etapa.
4. Se a tela mostrar falha ou conflito, anote o horário e chame o suporte. Não repita a mesma
   operação com outro número nem edite arquivos do relay.

A ordem de envio é a ordem gravada no dispositivo. Se a conexão cair no meio, o relay
reapresenta a mesma operação, sem criar um número novo; o servidor reconhece a repetição e
não aplica duas vezes. Um intervalo na ordem não é completado por conta própria: a operação
não é dada como sincronizada e o suporte é acionado.

Se uma transmissão fiscal for rejeitada, a operação fica falha e o relay cria uma tarefa de
revisão. Isso não autoriza a emissão nem substitui a conferência do suporte.

O suporte pode consultar as tarefas tenant-scoped em
`GET /relay/v1/review-tasks`. A resposta mostra somente referência opaca e código
sanitizado; não contém payload, resposta fiscal crua ou dado pessoal.

## Conflito e revisão

Quando a reconexão encontra divergência, o relay não escolhe automaticamente a
última alteração. A operação fica em **conflito** e uma tarefa de revisão é
criada para o suporte. Isso vale para mestres, dinheiro e fiscal.

- **Mestres:** o servidor é a autoridade; a alteração local só fica sincronizada
  após aceite explícito.
- **Dinheiro:** nenhum contrato ou caixa é sobrescrito para resolver a
  divergência.
- **Fiscal:** conflito exige revisão; rejeição fiscal continua bloqueada e não
  autoriza nova emissão por conta própria.

A tarefa mostra apenas uma referência opaca e um código seguro. Não contém
payload, resposta fiscal crua ou dado pessoal. Reapresentar a mesma operação
não abre tarefas duplicadas.

## Operação por 24 horas

A loja pode ficar sem internet por pelo menos 24 horas registrando contrato e caixa. A fila
não expira: as operações continuam guardadas até serem sincronizadas. O que expira é a
credencial de acesso do operador; quando ela vencer, o relay pede nova autenticação e não
aceita operação nova até a credencial ser renovada.

## Segurança e privacidade

- O relay guarda apenas o necessário para a operação e a sincronização. O log de fila carrega
  referências e valores, não o cadastro completo de clientes ou falecidos.
- A senha/token do operador não é gravada no disco; fica somente o resumo criptográfico.
- Os arquivos locais do relay não devem ser copiados para pen drive, e-mail ou nuvem sem
  orientação do suporte.
- O acesso de outros computadores da loja deve passar pela rede local protegida; não exponha
  o relay diretamente na internet.

## Limites desta versão

- A fila é local ao computador da loja. Se o disco falhar antes da sincronização, a operação
  pode ser perdida; faça backup conforme a orientação do suporte.
- O relay ainda não substitui o banco definitivo do SaaS. O adaptador de rede, a migração para
  PostgreSQL local e a autoridade remota continuam fora desta versão; os testes de sync usam
  transporte roteirizado em memória.
- Documento fiscal em contingência ainda não é emitido por esta versão; quando entrar, seguirá
  as tabelas oficiais e nunca será calculado por aproximação.

## Para o suporte

Os contratos técnicos estão em `docs/contracts/relay-operations-v1.md` e
`docs/contracts/relay-sync-v1.md`. As decisões de arquitetura estão em
`docs/adr/0023-relay-fila-offline-contrato-caixa.md` e
`docs/adr/0026-relay-sync-reconexao-revisao-fiscal.md` e
`docs/adr/0027-relay-protocolo-sincronizacao-versionado.md` e
`docs/adr/0029-relay-status-retry-observabilidade-fiscal.md`. Os comandos de verificação do
relay estão no `relay/README.md`.
