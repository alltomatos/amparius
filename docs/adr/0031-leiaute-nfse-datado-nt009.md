# ADR 0031 — Leiaute NFS-e datado: NT 009 em produção a partir de 2026-12-01

Status: aceito e implementado em 25/11/2026 — FIS-02A (#25)

## Contexto

A issue #25 estava parcialmente bloqueada porque o pacote XSD versionado em
`docs/fiscal/xsd-nfse-v1.01` (20260209) declara `TSSerieDPS` com
`^0{0,4}\d{1,5}$`. Em XML Schema, `^` e `$` são literais; com isso nenhuma
série real (`1`, `00001`) passava no facet. O bloqueio era do pacote, não da
DPS gerada.

A Sefin corrigiu o pattern no pacote de Produção Restrita
`esquemas-nfse-rtc-v1-01-20260727` (leiaute em produção desde 2026-08-10) e
publicou o pacote `esquemas_prod_restrita_20261020` (NT 009), que entra em
produção em 2026-12-01. A página “Documentação Atual” ainda aponta o zip de
20260209 por defasagem.

## Decisão

1. O Amparius adota o pacote oficial **NT 009 (20261020)** como leiaute de
   emissão a partir de 2026-12-01, versionado em
   `docs/fiscal/xsd-nfse-prodrest-20261020/`, com URL oficial, data e SHA-256
   do zip e de cada arquivo no `MANIFEST.md`. Nenhum XSD é editado.
2. O pacote 20260209 permanece no repositório apenas como referência
   histórica de competências anteriores. Ele não é usado para emissão nova.
3. O leiaute é **datado**: a seleção por `dhEmi`/competência (histórico até
   2026-11-30, NT 009 a partir de 2026-12-01) é requisito de FIS-02B/02C. Até
   lá o builder emite o leiaute NT 009 e os testes usam `dhEmi` ≥ 2026-12-01.
4. A DPS passa a exigir `finNFSe` (`0`), Id no `TSIdDPS` de 45 posições e aceita
   CNPJ alfanumérico conforme `TSCNPJ`. `BuildDPSID` implementa a regra
   publicada; dígito verificador continua no módulo `party`.
5. O validador Go preserva `^` como negação dentro de classe de caracteres
   (`[^\s]`) e como literal fora dela. A distinção é testada.

## Consequências

- A DPS oficial valida 7/7 contra o XSD vigente no lxml/libxml2 e no validador
  Go; o bloqueio de `TSSerieDPS` deixa de existir.
- Emissão continua **não liberada**: faltam FIS-02B/02C, `tax_rule` oficial,
  gate por data, assinatura XML-DSig e teste contra o ADN.
- Trocar o leiaute sem nova ADR exige revisar `MANIFEST.md`, o gate por data e
  os goldens; o hash do pacote é a evidência de integridade.
- Nenhuma alíquota, código fiscal ou vigência foi inferida nesta decisão.
