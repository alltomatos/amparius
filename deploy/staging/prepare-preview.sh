#!/bin/sh
set -eu

destination=${1:?Informe um diretório absoluto vazio para os arquivos públicos}
case "$destination" in
  /*) ;;
  *) echo "O diretório deve ser absoluto." >&2; exit 1 ;;
esac

cd "$(dirname "$0")/../.."
mkdir "$destination"
mkdir -p "$destination/assets/brand" "$destination/manager/assets/brand"
cp assets/brand/amparius-logo.svg "$destination/assets/brand/"
cp web/staging/index.html web/staging/client-demo.js web/staging/staging.css "$destination/"
cp web/control-plane/styles.css "$destination/"
cp web/staging/panel-demo.js web/staging/staging.css web/control-plane/styles.css \
  web/control-plane/helpers.js web/control-plane/app.js "$destination/manager/"
cp assets/brand/amparius-logo.svg "$destination/manager/assets/brand/"

render_panel() {
  sed '
    /<link rel="stylesheet" href=".\/styles.css">/a\
      <link rel="stylesheet" href="/staging.css">
    /<\/header>/r web/staging/panel-overview.html
    /Tenant é derivado do token/c\
          <p>Contexto fixo de demonstração.</p>
    /<script src=".\/app.js"/i\
      <script src="./panel-demo.js" defer></script>
  ' web/control-plane/index.html
}

render_panel \
  | sed 's#\.\./\.\./assets/brand/amparius-logo\.svg#./assets/brand/amparius-logo.svg#g' \
  > "$destination/manager/index.html"
