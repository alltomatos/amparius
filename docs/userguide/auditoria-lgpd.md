# Auditoria e privacidade

O Amparius mantém uma trilha por tenant para ações relevantes. A trilha mostra
ator (usuário autenticado ou serviço), ação, recurso, resultado, empresa,
correlação e trace. Ela não mostra nome, e-mail, documento, telefone, endereço,
token ou payload.

Registros de consentimento usam uma referência opaca ao sujeito, finalidade,
base legal, estado, evidência e `retention_until`. Conceder, retirar ou negar
consentimento gera uma nova ocorrência histórica; o registro anterior não é
editado.

`retention_until` é obrigatório. O prazo e o código da base legal devem vir da
política aprovada pelo responsável de privacidade/DPO; o sistema não escolhe
prazo por aproximação. Expurgo, anonimização e atendimento ao titular exigem
fluxo operacional próprio e não são executados automaticamente por esta fatia.

## Falecido e vínculo familiar

Nas duas criações da API autenticada, a política do tenant fornece
finalidade, base legal e retenção. O cadastro de falecido e sua trilha
são gravados na mesma transação. Para o vínculo familiar, a API exige
o UUID da evidência externa e grava vínculo, consentimento `granted`
e auditoria em uma única transação. Se uma gravação falhar, toda a
transação é desfeita. A verificação do artefato externo e a retirada do
consentimento ficam fora da #73.

A operação depende do provedor real de autenticação e da política
aprovada pelo DPO. Veja o [ADR 0042](../adr/0042-falecido-vinculo-familiar-consentimento-v1.md).

Em suporte, use o `X-Request-Id` e o `X-Trace-Id` para correlacionar logs sem
solicitar dados pessoais. Métricas devem usar somente labels de baixa
cardinalidade, como tenant, módulo, estado e resultado.
