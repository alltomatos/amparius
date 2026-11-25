# XSD NFS-e Nacional — pacote de Produção Restrita v1.01-20261020 (NT 009)

- Fonte oficial: https://www.gov.br/nfse/pt-br/biblioteca/documentacao-tecnica/producao-restrita/esquemas_prod_restrita_20261020.zip
- Página de referência: https://www.gov.br/nfse/pt-br/biblioteca/documentacao-tecnica/producao-restrita
- Rótulo oficial: `NFSe-ESQUEMAS_XSD-PRODREST-v1.01-20261020`
- Nota Técnica: SE/CGNFS-e nº 009 (v1.0.1)
- Namespace: http://www.sped.fazenda.gov.br/nfse
- Baixado em: 2026-11-25
- Arquivo do pacote no reposit?rio: `nfse-esquemas-prodrest-20261020.zip`
- SHA-256 do zip: 25FDCECC651A67A216496AB10D7566631E6ED10F721E37D62C7845DADB0EA267

## Vigência

- Entrada em produção do leiaute NT 009: **2026-12-01** (comunicado CGNFS-e de
  20/10/2026), decidida pela data de emissão (`dhEmi`) da DPS.
- Até 2026-11-30 a produção usa o leiaute anterior. O pacote histórico
  `docs/fiscal/xsd-nfse-v1.01` (20260209) fica preservado para competências
  antigas e não é usado para emissão nova.

## Arquivos (todos em `Schemas/1.01` no pacote; extraídos na raiz)

| Arquivo | SHA-256 |
|---|---|
| CNC_v1.00.xsd | 7032188BB6F137D52B16512583B739A361E6B1434C42CB539A29B8EFA32F8321 |
| DPS_v1.01.xsd | FE45E5250A48E519ABA89FC6A472863B8E602ED957778FB64692804933A00D0C |
| evento_v1.01.xsd | 986D0A1C4D27454F712169849AA7C2380AAA4560BD9C920E0FF03BA6599AE28B |
| NFSe_v1.01.xsd | AF0BD2D8C50ACBA3D9C3F3F515426ECA083B3F66E3211CE8DD48A7CF101818B8 |
| pedRegEvento_v1.01.xsd | E90B6816D29CCA0BD5ED8F86AD98D3B7B0D8BBFC11A7583E6FF8F98170FD4009 |
| tiposCnc_v1.00.xsd | AF606B7317824FA8FA7AD8E44BAC2AD8530CD5D162EBCDEC647205473C41D525 |
| tiposComplexos_v1.01.xsd | 4F66F7C15BD3B05B3A16CBE5FA671B90C10E4C9C74276038730BE1C8C794B721 |
| tiposEventos_v1.01.xsd | 1B32BEA21089DC232D78B62D805E9C07382F440D4E0EA2CB3FB6B82EBDE68C11 |
| tiposSimples_v1.01.xsd | 562E326724F85CD7C41176D35E67C542376784D4C950EC1719704D480A8AF226 |
| xmldsig-core-schema.xsd | 8C9792A056AEDCADFC95BF16F2D7448A35DA40DF71533E20AEA37D0329980C48 |

## Diferenças relevantes em relação ao pacote de 20260209

- `TSSerieDPS`: `^0{0,4}\d{1,5}$` → `[0-9]{1,4}|[0-8][0-9]{4}` (o padrão anterior
  era literal e rejeitava toda série válida).
- `TSCNPJ`: `[0-9]{14}` → `[0-9A-Z]{14}` (CNPJ alfanumérico em produção desde
  2026-08-10).
- `TSIdDPS`: passa a `DPS[0-9]{7}(1[0-9]{14}|2[0-9A-Z]{14})[0-9]{20}`.
- `TCInfDPS` ganha `finNFSe` obrigatório antes de `cLocEmi` e aceita
  `IBSCBS` opcional.

Validação: XSDs extraídos do pacote oficial; nenhum código, alíquota ou segredo
adicionado.
