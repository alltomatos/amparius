# FIS-02A — investigação de fonte oficial para `cTribMun` de Fortaleza

Data: 2026-11-26
Issue: #25 (FIS-02A). Dependência formal #4 (MVP-FIS-01) já fechada; a issue
segue travada em substância, não em dependência.
Resultado: **bloqueio externo confirmado, não resolvido**. Nenhum código,
alíquota ou `cTribMun` foi criado, estimado ou aproximado (AGENTS.md #1).

## Pergunta investigada

Existe fonte oficial pública (Painel Administrativo Municipal, Emissor
Nacional, planilha ou API do gov.br/nfse) que publique o `cTribMun` (código de
3 dígitos, "Código de Tributação Municipal") especializado por Fortaleza/CE
(IBGE 2304400) para os itens de serviço relevantes aos golden files de
#26/#27 (serviços funerários, itens 25.1–25.5 do Anexo I do Código Tributário
municipal)?

## Fontes consultadas nesta fatia

### 1. Guia para preenchimento do Painel Municipal da NFS-e v1.2 (out/2025)

- URL correta e vigente (a referenciada em `fontes-oficiais.md` como "Guia do
  PAM v1.2" está **404**; o link certo, obtido na página índice
  `documentacao-tecnica/documentacao-atual`, é
  `https://www.gov.br/nfse/pt-br/biblioteca/documentacao-tecnica/documentacao-atual/guia-painel-administrativo-municipal-snnfse-v1.2-20251001.pdf`,
  172 páginas, texto extraído localmente com `pdftotext`).
- Seção 3.5.2.1 "Configuração do Código Original do Município" (pg. 51–53):
  > "A administração municipal pode optar por criar mais especializações para
  > cada um dos desdobramentos nacionais existentes. Estas especializações
  > formam o 'Código de Tributação Municipal' e são específicas de cada
  > Município."
  > "O primeiro campo a ser preenchido diz respeito à Identificação do Serviço
  > no Sistema Nacional. Nesse espaço deverá ser digitado um código de três
  > dígitos que corresponderá ao Código Tributário Municipal."
  O fluxo descrito é executado **dentro do PAM**, por quem acessa com
  certificado/login da própria Administração Tributária Municipal (ATM) —
  não há tela de consulta pública para terceiros nesta seção.
- Seção 3.5.4 "Download da lista de serviços" (pg. 71–72): a "Lista Completa"
  (que traria as especializações já configuradas) é descrita como função da
  "ATM"/"gestor municipal" para seu próprio uso, não como download público
  para contribuintes ou desenvolvedores.
- Conclusão: o próprio manual oficial do sistema nacional não descreve nenhum
  canal de consulta pública de `cTribMun` por município. A criação e a leitura
  da especialização são internas ao município.

### 2. Anexo V — leiaute de manutenção de parametrização do PAM (XLSX, 16/12/2025)

- URL: `.../anexo-v-painel-adm-municipal-snnfse-v1.00-20251216.xlsx`.
- Conteúdo: especificação de campos do XML `PARAM` (schema/layout de como o
  município **envia** parametrização via API — `cMun`, `CNPJ`, `idPar`,
  `tpAmb`, `dtVig`, `idLeg` etc.), não dados de nenhum município. Não é fonte
  de valor de `cTribMun` para Fortaleza.

### 3. Planilha nacional "Alíquotas de ISSQN" (biblioteca gov.br/nfse)

- URL: `https://www.gov.br/nfse/pt-br/biblioteca/aliquotas` →
  `aliquotas-municipios-20260903-extr1.zip` (18,7 MB; conferido hoje,
  26/11/2026 — a página mostra "atualizado em 16/09/2026", mas o conteúdo do
  ZIP segue com data de geração 2026-09-03, igual à consulta anterior de
  23/11/2026 registrada em `docs/fiscal/2026-11-23-nfse-slice-01-fortaleza.md`).
- Colunas de `CE.csv`: `codigo_ibge;uf;nome_municipio;codigo_servico;incidencia;aliquota;dt_ini;dt_fim`.
  **Não existe coluna `cTribMun`** nesta planilha — ela traz só o código de
  serviço nacional (LS_Nacional) e a alíquota, nunca a especialização
  municipal de 3 dígitos.
- `grep -c "2304400" CE.csv` = **0**. Fortaleza continua ausente da planilha,
  confirmando (sem novidade) o achado de 23/11/2026.

### 4. "NFS-e Via" — nova API de consulta de alíquotas (anúncio 10/07/2026)

- URL: `https://www.gov.br/nfse/pt-br/noticias/nfs-e-via-publicada-nova-api-para-consulta-de-aliquotas-do-iss`.
- Trata de alíquotas de ISS sobre **trechos de vias concedidas** (rodovias),
  não de código de serviço nem `cTribMun`. Fora de escopo para este caso.

### 5. "API de integração" (produtos disponíveis para municípios)

- URL: `https://www.gov.br/nfse/pt-br/municipios/produtos-disponiveis/api-de-integracao`.
- Texto genérico ("download de parâmetros municipais, entre outros"), sem
  listar campos nem confirmar acesso público. Não é evidência suficiente nem
  a favor nem contra.

## Conclusão

Não foi localizada nenhuma fonte oficial pública — documento, planilha ou API
sem exigência de credencial do próprio município ou de contribuinte
certificado em Fortaleza — que publique o `cTribMun` especializado para
Fortaleza/CE. O mecanismo oficial existente (PAM) é de administração interna
da prefeitura; o único outro caminho possível (ver a especialização no
dropdown do Emissor Público Nacional Web ao montar uma DPS como contribuinte
domiciliado em Fortaleza) exige certificado ICP-Brasil e CNPJ de teste
domiciliado no município — gap já registrado e fora do repositório
(`docs/fiscal/2026-11-23-nfse-slice-01-fortaleza.md`, item 12 da seção 8; e
`2026-11-24-fis-02a-motor-tributos-dps.md`, pendência 7).

Isto é um **bloqueio externo**, não uma falha de implementação: a regra
operacional já registrada em
`skills/nfse-documentacao-tecnica/references/fontes-fortaleza.md` (omitir o
campo sem confirmação) permanece correta e é mantida. Nenhum valor de
`cTribMun` foi atribuído a Fortaleza nesta investigação, sintético ou não.

## Decisão sobre a issue #25

A issue #25 **permanece aberta e explicitamente bloqueada** por esta lacuna
externa (confirmação oficial do `cTribMun` de Fortaleza), não fechada. Os
três critérios de aceite da fatia (regra ausente bloqueia; município é
parâmetro; DPS segue XSD oficial v1.01) continuam sem golden file oficial de
cálculo — apenas golden sintético, como já documentado em
`2026-11-24-fis-02a-motor-tributos-dps.md`. Fechar a issue exigiria uma das
duas: (a) confirmação oficial documentada (PAM do próprio município, ou
captura autenticada do Emissor Nacional com certificado/CNPJ de teste
domiciliado em Fortaleza), ou (b) decisão de produto de usar outro município
piloto com `cTribMun` já público. Nenhuma das duas está disponível nesta
fatia.

## Fora de escopo nesta fatia

- Issues #59/#60 (bloqueadas por #5): não tocadas.
- DNS/`hml.amparius.com.br`: não tocado.
- Migrations já aplicadas: não editadas.
- Arquivos não versionados em `skills/nfse-documentacao-tecnica/` (`SKILL.md`,
  `fontes-oficiais.md`, `fontes-fortaleza.md`,
  `anexo-b-codigos-tributacao-nacional-v1.01.json`): preservados, não
  sobrescritos nem descartados — permanecem como estavam antes desta fatia.
