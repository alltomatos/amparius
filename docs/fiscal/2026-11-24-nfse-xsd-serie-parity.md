# NFS-e v1.01 — paridade do facet `serie`

Status: bloqueio confirmado em 24/11/2026. O pacote oficial versionado nao foi alterado.

## Evidencia

`docs/fiscal/xsd-nfse-v1.01/Schemas/1.01/tiposSimples_v1.01.xsd:161` declara:

```xml
<xs:pattern value="^0{0,4}\d{1,5}$"/>
```

No regex de XML Schema, `^` e `$` sao literais; eles nao sao ancoras. O
libxml2 rejeita `serie=1` e `serie=00001`, reportando o facet `pattern`. O
mesmo motor aceita `serie=^1$`, que tem comprimento permitido, mas nao e uma
serie semanticamente valida para o dominio da DPS.

O validador Go tratava `^` e `$` como ancoras de `regexp`, criando falso
positivo. Ele agora escapa esses caracteres antes de compilar o pattern e
reproduz a rejeicao do motor real. O teste `TestXSDOficialParidadeDoFacetSerie`
tambem prova que `^1$` e aceito apenas como literal do schema.

## Decisao

As fixtures continuam com `serie=1`, pois esse e o valor sintetico
semanticamente correto para o caso de teste. Nao foi inventado `^1$`, nao foi
editado o XSD oficial e nenhuma regra fiscal foi inferida.

O caminho oficial bloqueia com `ErrInvalidDPS` antes do adapter/transmissao
ate que a fonte oficial publique uma correcao ou esclarecimento do facet. Os
ramos `toma`, `interm`, `idDocTec`, `docRef`, `xPed` e `xInfComp` continuam
cobertos no XML opcional, mas nao podem mascarar o bloqueio de `serie`.

## Reproducao

Na raiz do repositorio, com Python 3 e `lxml` instalados:

```powershell
./scripts/validate-nfse-xsd-parity.ps1
```

O script informa as versoes efetivas de lxml/libxml2 e exige rejeicao por
`serie/pattern` nas duas fixtures oficiais.
