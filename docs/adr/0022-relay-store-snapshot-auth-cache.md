# ADR 0022 — Store temporário, snapshot assinado e autenticação cacheada no relay

Status: aceito · 25/11/2026 · RELAY-01A

## Contexto

O relay precisa continuar atendendo a loja durante uma indisponibilidade do SaaS, mas não
pode transformar uma cópia local em fonte de verdade permanente nem manter autorização
indefinidamente. A primeira fatia implementável precisa cobrir a base temporária, o cache de
credencial do operador e o snapshot de enforcement vindo do control plane.

## Decisão

1. O relay mantém um store local JSON versionado, configurável por
   `AMPARIUS_RELAY_STORE_PATH` e com default em `relay-data/store.json`. Cada registro contém
   `tenant_id`, chave, payload e `expires_at`; leitura exige o mesmo tenant e registros
   expirados são removidos antes de retornar.
2. O cache de autenticação persiste `tenant_id`, `subject_id`, janela de validade e somente
   `SHA-256(token)`. O token bruto nunca é serializado. A comparação do hash é em tempo
   constante e expiração ou credencial ausente nega o acesso.
3. O snapshot é um envelope JSON versionado assinado com Ed25519. A assinatura cobre schema,
   `key_id`, `tenant_id`, versão, emissão, validade e payload. O relay valida a chave pública,
   tenant, janela de validade e assinatura antes de aceitar; snapshot expirado falha fechado.
4. A aplicação aceita somente versão monotônica maior que a última armazenada. Rollback de
   snapshot vira erro explícito e não sobrescreve o estado local.
5. O store é por instalação e temporário. PostgreSQL local, log de operações, fila de saída,
   sync por cursor, criptografia de disco e rotação/distribuição da chave pública permanecem
   nas fatias seguintes. O payload do snapshot é enforcement derivado, nunca segredo ou dado
   de negócio cru.

## Segurança e LGPD

- O `tenant_id` é obrigatório em registros, autenticação e snapshots; não existe API de leitura
  sem tenant.
- Expiração é `valid_until <= now`, inclusive no limite, para evitar autorização após TTL.
- O store pode conter dados locais temporários da operação; o chamador deve enviar somente o
  mínimo necessário e aplicar a janela de retenção do produto.
- O caminho do store é dado local ignorado pelo Git. Não há chave privada, token real,
  certificado ou segredo nos fontes, fixtures ou documentação.

## Consequências

- A operação offline tem uma primitiva verificável para cache e enforcement, sem depender de
  relógio de atualização de arquivo ou de last-write-wins.
- A troca de chave pública exige distribuição segura de `key_id` e rotação planejada; esta
  ADR não implementa provisionamento.
- A substituição usa arquivo temporário e backup recuperável (`.bak`); a abertura recupera o
  backup se uma interrupção ocorrer entre as duas renomeações. A camada é adequada para base
  temporária, mas não substitui WAL/atomicidade do PostgreSQL para dinheiro ou fiscal.

## Verificação

```powershell
cargo fmt --manifest-path relay/Cargo.toml -- --check
cargo test --manifest-path relay/Cargo.toml
cargo clippy --manifest-path relay/Cargo.toml --all-targets -- -D warnings
```

O contrato executável está em `docs/contracts/relay-cache-v1.md`.
