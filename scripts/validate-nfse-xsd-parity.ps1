$ErrorActionPreference = "Stop"

Write-Host "== Validador Go: paridade do facet serie =="
go test -count=1 -v ./internal/modules/fiscal/nfse -run '^TestXSDOficialParidadeDoFacetSerie$'

Write-Host "== Motor XSD real via lxml/libxml2 =="
@'
from pathlib import Path
from lxml import etree

schema_path = Path("docs/fiscal/xsd-nfse-v1.01/Schemas/1.01/DPS_v1.01.xsd")
fixture_dir = Path("internal/modules/fiscal/nfse/testdata/official")
schema = etree.XMLSchema(etree.parse(str(schema_path)))
print(f"lxml={'.'.join(map(str, etree.LXML_VERSION))} libxml2={'.'.join(map(str, etree.LIBXML_VERSION))}")

for fixture in (fixture_dir / "dps-minima.xml", fixture_dir / "dps-opcionais.xml"):
    document = etree.parse(str(fixture))
    if schema.validate(document):
        raise SystemExit(f"ERRO: {fixture} foi aceita; esperada rejeicao do facet serie")
    messages = [entry.message for entry in schema.error_log]
    if not any("serie" in message and "pattern" in message for message in messages):
        raise SystemExit(f"ERRO: {fixture} rejeitada por motivo inesperado: {messages}")
    print(f"{fixture}: rejeitada por serie/pattern")

probe = etree.fromstring((fixture_dir / "dps-minima.xml").read_bytes().replace(b"<serie>1</serie>", b"<serie>^1$</serie>"))
if not schema.validate(probe):
    raise SystemExit(f"ERRO: o motor nao reproduziu a aceitacao literal de ^1$: {[entry.message for entry in schema.error_log]}")
print("probe serie=^1$: aceita como literal pelo schema")
'@ | py -3 -
