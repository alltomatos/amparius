---
name: nfse-documentacao-tecnica
description: Localiza, organiza e resume a documentação técnica oficial da NFS-e (Nota Fiscal de Serviço eletrônica) no portal gov.br — versões, manuais, XSDs/schemas, APIs, autenticação, emissão, consulta, cancelamento, contingência e notas técnicas. Use quando for preciso pesquisar regras, endereços de API ou artefatos (schemas) da NFS-e nacional, distinguindo o que é oficial, o que está datado e o que depende de validação municipal.
---

# NFS-e — Documentação Técnica Oficial

Como achar, entender e referenciar a documentação técnica oficial da NFS-e
padrão nacional (ambiente do governo federal — ADN/CNC/Emissor Público) no portal
gov.br. Prioriza fonte oficial, registra data de consulta e separa o que é vigente,
o que está datado e o que exige validação municipal.

## 1. Ponto de partida

- Biblioteca > Documentação técnica:
  https://www.gov.br/nfse/pt-br/biblioteca/documentacao-tecnica

Toda a documentação relevante parte dessa seção. Confira a data de
publicação/atualização indicada no topo de cada página antes de usar o conteúdo.

## 2. Seções principais do portal

| Seção | URL (prefixo `https://www.gov.br/nfse/pt-br/biblioteca/documentacao-tecnica/`) | Para que serve |
|---|---|---|
| Documentação Atual (Produção) | `/documentacao-atual` | Manuais, guias, XSDs e anexos vigentes em **produção** |
| Documentação Técnica (Prod. Restrita) | `/producao-restrita` | Ambiente de **homologação/testes** (produção restrita) |
| APIs — Prod. Restrita e Produção | `/apis-prod-restrita-e-producao` | Detalhes das APIs por ambiente (endereços, autenticação) |
| Atualizações e Implantações | `/atualizacoes-e-implantacoes` | Cronograma e mudanças por ambiente |
| RTC — Rotina de Tributação Calculada | `/rtc` | Documentação da RTC (padrão nacional de tributos) |
| Leiaute e esquemas antigos | `/leiaute-e-esquemas-antigos` | Layouts datados (jul/2022–28/09/2025) — conteúdo **histórico** |

## 3. Documentos-chave (ambiente de Produção)

- Guia do Emissor Público Nacional Web
- Guia do Painel Administrativo Municipal
- API — Manual de Contribuintes — Emissor Público
- API — Manual de Municípios — ADN (Compartilhamento)
- API — Manual de Municípios — CNC (Cadastro Nacional de Contribuintes)
- API — Manual de Municípios — Emissor Público
- API — Manual de Contribuintes — Guia de Utilização das APIs do ADN
- API — Manual de Contribuintes — Emissor Público Nacional — Emissão por Decisão Administrativa ou Judicial

(Os títulos podem mudar com novas versões; sempre confirme o nome e a versão na
página “Documentação Atual”.)

## 4. Esquemas XSD

- Produção: pacote `NFSe-ESQUEMAS_XSD-v1.01-…` (ver sufixo de data na página).
- Produção restrita (testes): pacote `NFSe-ESQUEMAS_XSD-PRODREST-v1.01-…`.

Os XSDs definem a estrutura XML dos eventos (emissão, consulta, cancelamento,
substituição etc.). Baixe **sempre da seção correspondente ao ambiente** em que
vai integrar e confira a versão contra a página de atualizações.

## 5. Notas técnicas

- Nota Técnica SE/CGNFS-e nº 009 (com anexos I–V e XSD de PRODREST).

Notas técnicas trazem mudanças de regra e de schema; leia junto com o pacote XSD
vigente e com a página “Atualizações e Implantações”.

## 6. Autenticação, emissão, consulta e cancelamento (resumo funcional)

- **Autenticação:** certificado digital (padrão ICP-Brasil / e-CNPJ) no
  ambiente do contribuinte, conforme guias de API de contribuintes. Detalhes
  exatos de mTLS/certificado estão nos manuais de API — validar na fonte.
- **Emissão:** via Emissor Público Nacional Web ou por API (Emissor Público
  Nacional e endpoints de municípios). O XML segue os XSDs vigentes.
- **Consulta:** endpoints por número de NFS-e / chave de acesso, conforme
  manual de API do ambiente.
- **Cancelamento / substituição:** por evento, com prazos e regras definidos
  pelo município — **validar municipalmente**.
- **Contingência:** regras de emissão em contingência e reenvio constam nos
  guias; comportamento varia por município.

> Este resumo é orientativo. Os nomes de endpoint, parâmetros e regras de prazo
> devem ser confirmados nos manuais de API e nos XSDs oficiais antes de codificar.

## 7. Como usar esta skill (passos para o agente)

1. Abra a página de “Documentação técnica” e identifique a seção correta
   (produção vs. produção restrita).
2. Anote a **versão** e a **data** de publicação do documento/XSD consultado.
3. Baixe o XSD correspondente do mesmo ambiente e versão.
4. Distinga o que é regra nacional (ADN/CNC/RTC) do que é decisão municipal.
5. Registre sempre: URL, versão, data de consulta e nível de confiança.
6. Para conteúdo antigo, use “Leiaute e esquemas antigos” e marque como **datado**.

## 8. Limitações e alertas

- A documentação do portal é a **fonte oficial**, mas muitas regras operacionais
  (prazos, alíquotas, cancelamento) são **municipais** e não constam ali —
  exigem validação junto ao município.
- Nomes de arquivos, versões e URLs mudam; sempre reconfirme na página antes de
  citar em documentação de produção.
- Este resumo não substitui a leitura dos manuais e XSDs oficiais.

## 9. Fonte e data de consulta

- Portal gov.br — NFS-e — Biblioteca > Documentação técnica
  https://www.gov.br/nfse/pt-br/biblioteca/documentacao-tecnica
- **Data de consulta: 24/11/2026**
- Lista detalhada de fontes: ver `references/fontes-oficiais.md`
- Licença do portal: Creative Commons Atribuição-SemDerivações 3.0 Não Adaptada
