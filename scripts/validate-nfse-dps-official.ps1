# Valida estritamente as fixtures de DPS oficial contra o XSD v1.01.
# Diferente de validate-nfse-xsd-parity.ps1, este script NAO tolera o gap do
# facet TSSerieDPS: ele sai com codigo 1 enquanto uma serie valida for rejeitada.
#
# Uso: .\scripts\validate-nfse-dps-official.ps1
# Requer Python 3 com lxml.

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

@'
from pathlib import Path
from lxml import etree

schema = etree.XMLSchema(etree.parse("docs/fiscal/xsd-nfse-v1.01/Schemas/1.01/DPS_v1.01.xsd"))
fixtures = sorted(Path("internal/modules/fiscal/nfse/testdata/official").glob("*.xml"))
if not fixtures:
    raise SystemExit("ERRO: nenhuma fixture XML oficial encontrada")

failed = 0
for fixture in fixtures:
    document = etree.parse(str(fixture))
    if schema.validate(document):
        print(f"OK         {fixture}")
        continue
    failed += 1
    print(f"REJEITADA  {fixture}")
    for entry in schema.error_log:
        print(f"  linha {entry.line}: {entry.message}")

print(f"lxml={'.'.join(map(str, etree.LXML_VERSION))} libxml2={'.'.join(map(str, etree.LIBXML_VERSION))}")
print(f"rejeitadas={failed}/{len(fixtures)}")
raise SystemExit(1 if failed else 0)
'@ | python -

exit $LASTEXITCODE
