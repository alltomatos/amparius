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

Em suporte, use o `X-Request-Id` e o `X-Trace-Id` para correlacionar logs sem
solicitar dados pessoais. Métricas devem usar somente labels de baixa
cardinalidade, como tenant, módulo, estado e resultado.
