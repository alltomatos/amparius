$ErrorActionPreference = "Stop"

Write-Host "== Validador Go: TSSerieDPS do leiaute vigente =="
go test -count=1 -v ./internal/modules/fiscal/nfse -run '^TestXSDOficialAceitaSerieDoLeiauteVigente$'

Write-Host "== Motor XSD real via lxml/libxml2 =="
@'
from pathlib import Path
from lxml import etree

schema_path = Path("docs/fiscal/xsd-nfse-prodrest-20261020/DPS_v1.01.xsd")
fixture_dir = Path("internal/modules/fiscal/nfse/testdata/official")
schema = etree.XMLSchema(etree.parse(str(schema_path)))
print(f"lxml={'.'.join(map(str, etree.LXML_VERSION))} libxml2={'.'.join(map(str, etree.LIBXML_VERSION))}")

for fixture in sorted(fixture_dir.glob("*.xml")):
    document = etree.parse(str(fixture))
    if not schema.validate(document):
        messages = [entry.message for entry in schema.error_log]
        raise SystemExit(f"ERRO: {fixture} deveria ser aceita pelo leiaute vigente: {messages}")
    print(f"{fixture}: aceita")

probe = etree.fromstring((fixture_dir / "dps-minima.xml").read_bytes().replace(b"<serie>1</serie>", b"<serie>99999</serie>"))
if schema.validate(probe):
    raise SystemExit("ERRO: serie=99999 deveria ser rejeitada pelo TSSerieDPS")
messages = [entry.message for entry in schema.error_log]
if not any("serie" in message and "pattern" in message for message in messages):
    raise SystemExit(f"ERRO: serie=99999 rejeitada por motivo inesperado: {messages}")
outras = [m for m in messages if not ("serie" in m and "pattern" in m)]
if outras:
    raise SystemExit(f"ERRO: probe acumulou erros alem do facet serie: {outras}")
print("probe serie=99999: rejeitada por serie/pattern, como esperado")
'@ | py -3 -
