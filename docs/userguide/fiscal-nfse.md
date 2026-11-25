# Fiscal — NFS-e no padrão nacional

## Como o Amparius emite

A NFS-e do Amparius segue o **Padrão Nacional** (DPS → Sefin Nacional). O
município é um parâmetro da configuração fiscal; não existe adaptador por
cidade para a emissão nova.

O leiaute do XML é **datado**:

- competências até 2026-11-30 usam o leiaute anterior;
- a partir de 2026-12-01 vale o leiaute da Nota Técnica 009.

O sistema escolhe o leiaute pela data de emissão/competência. Nenhuma versão é
trocada manualmente.

## Regra fiscal ausente bloqueia

Se faltar tabela datada para o serviço, município ou regime — alíquota, CST,
cClassTrib, NCM, CFOP, código de serviço ou vigência — a operação **não** é
calculada por aproximação. Ela falha e informa o que falta. Isso é intencional:
imposto não se estima.

## O que o usuário vê

- Cálculo com memória: base, alíquota efetiva, valor e a regra usada (com data
  de vigência e hash).
- Bloqueio explícito quando a regra não existe, com o campo faltante.
- XML enviado e retorno da Sefin guardados como vieram, com hash, para
  auditoria e reprocessamento.

## Estado atual

O motor de tributos e a geração da DPS no leiaute vigente estão implementados e
testados. A **emissão em produção ainda não está liberada**: faltam a
persistência/idempotência (FIS-02B), o adapter de envio e fechamento (FIS-02C),
a importação das tabelas oficiais e os testes contra o ambiente do ADN.

Enquanto isso, nenhuma nota é transmitida e nenhum imposto é calculado com dado
inventado.
