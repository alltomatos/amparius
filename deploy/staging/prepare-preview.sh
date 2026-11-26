#!/bin/sh
set -eu

destination=${1:?Informe um diretório absoluto vazio para os arquivos públicos}
case "$destination" in
  /*) ;;
  *) echo "O diretório deve ser absoluto." >&2; exit 1 ;;
esac

cd "$(dirname "$0")/../.."
mkdir "$destination"
mkdir -p "$destination/painel" "$destination/assets/brand"
cp assets/brand/amparius-logo.svg "$destination/assets/brand/"
cp web/staging/index.html web/staging/client-demo.js web/staging/staging.css "$destination/"
cp web/staging/panel-demo.js web/control-plane/styles.css web/control-plane/helpers.js \
  web/control-plane/app.js "$destination/painel/"
sed '
  /<link rel="stylesheet" href=".\/styles.css">/a\
    <link rel="stylesheet" href="/staging.css">
  /data-name="control-plane-shell"/a\
      <nav class="demo-nav" aria-label="Demonstração"><a href="/">Voltar ao cliente web</a></nav>\
      <p class="state-panel">Dados sintéticos. Alterações e auditoria ficam nesta aba até recarregar.</p>
  /<\/header>/r web/staging/panel-overview.html
  /Tenant é derivado do token/c\
        <p>Contexto fixo de demonstração.</p>
  /<script src=".\/app.js"/i\
    <script src="./panel-demo.js" defer></script>
' web/control-plane/index.html > "$destination/painel/index.html"
