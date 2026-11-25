# Runbook — migração de DNS para Cloudflare

## Escopo

Migrar a zona DNS para Cloudflare preservando a separação:

| Hostname | Papel |
| --- | --- |
| `manager.amparius.com` | control plane e superadmin |
| `app.amparius.com` | SaaS dos clientes |
| `hml.amparius.com.br` | homologação |

A migração de DNS não autoriza trocar origem, banco, TLS ou regras de roteamento sem evidência separada.

## Antes da troca

- Exportar a zona atual do Registro.br e guardar cópia versionada fora do repositório.
- Inventariar `A`, `AAAA`, `CNAME`, `TXT`, `MX`, `CAA`, SPF, DKIM, DMARC e verificações de domínio.
- Confirmar os IPs/origens de cada ambiente com o responsável pela infraestrutura.
- Reduzir o TTL dos registros que serão alterados, respeitando o TTL mínimo aceito pelo provedor atual.
- Não ativar proxy laranja em endpoints que ainda não suportam o modelo de TLS/origem; começar com DNS-only.
- Conferir que nenhum segredo, token ou conteúdo de produção entra no inventário ou no commit.

## Sequência segura

1. Criar a zona no Cloudflare e importar/revisar os registros; não apagar registros automaticamente.
2. Comparar a zona importada com a exportação do Registro.br, registro por registro.
3. Configurar SSL/TLS compatível com o certificado/origem e manter DNS-only durante o primeiro corte.
4. Trocar os nameservers no Registro.br pelos dois nameservers fornecidos pelo Cloudflare.
5. Verificar delegação, resolução autoritativa, HTTPS, redirecionamentos e os três hostnames.
6. Só depois avaliar proxy, WAF e regras de cache por hostname; `manager` e API não devem ser cacheados.

## Verificação pós-corte

Registrar data/hora UTC, resolvedores usados e resultado de:

```powershell
Resolve-DnsName manager.amparius.com -Type A
Resolve-DnsName app.amparius.com -Type A
Resolve-DnsName hml.amparius.com.br -Type A
Resolve-DnsName amparius.com -Type NS
curl.exe -I https://manager.amparius.com
curl.exe -I https://app.amparius.com
curl.exe -I https://hml.amparius.com.br
```

Critério de sucesso: os três hosts resolvem para as origens esperadas, HTTPS apresenta certificado válido para o hostname, health/readiness responde conforme o ambiente e nenhum endpoint administrativo fica publicamente cacheado.

## Rollback

Manter os nameservers anteriores documentados e não removê-los até a janela de estabilidade terminar. Em falha de resolução, TLS ou roteamento, restaurar os nameservers no Registro.br e registrar o motivo; não alterar aplicação ou banco como tentativa de correção de DNS.

## Bloqueio atual

A troca efetiva exige acesso autorizado às contas Registro.br e Cloudflare e os destinos/origens confirmados. O repositório não contém esses dados e nenhum nameserver foi alterado por este documento.
