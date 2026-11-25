# ADR 0028 - Reconciliação explícita do relay

Status: aceito em 25/11/2026 - RELAY-02B

## Contexto

O protocolo Sync v1 garante ordem, replay e idempotência, mas não decide o que
fazer quando uma operação local diverge do estado do servidor. Escolher a
última escrita esconderia perda de dados em mestres e seria inaceitável para
dinheiro ou fiscal.

## Decisão

1. Toda operação é classificada em um domínio fechado: `master`, `money` ou
   `fiscal`.
2. O servidor é autoritativo para mestres. O relay só marca um mestre como
   sincronizado após uma confirmação do transporte; uma divergência não aplica
   a versão local nem a versão remota automaticamente.
3. Divergência em qualquer domínio marca a operação como `conflict` e cria uma
   `ReviewTask` aberta, tenant-scoped, deduplicada por operação. A tarefa guarda
   somente o identificador opaco e código sanitizado, nunca payload ou PII.
4. Dinheiro e fiscal não têm last-write-wins. Rejeição fiscal continua como
   `failed` com `FiscalFailure`; conflito fiscal usa `FiscalConflict` e também
   exige revisão explícita.
5. Replays do mesmo conflito devolvem a tarefa existente e não criam outra
   linha nem alteram a operação já bloqueada.

## Consequências

- A fila fica visivelmente bloqueada até uma etapa de resolução futura.
- O store JSON preserva a trilha entre reinícios, com a mesma proteção de tenant
  usada pela fila.
- Ainda não existe endpoint de resolução, aplicação de snapshot autoritativo de
  mestre ou adaptador HTTP/SaaS nesta fatia; esses passos exigem contrato
  próprio e revisão de segurança.

## Verificação

```powershell
cargo fmt --manifest-path relay/Cargo.toml -- --check
cargo test --manifest-path relay/Cargo.toml
cargo clippy --manifest-path relay/Cargo.toml --all-targets --all-features -- -D warnings
git diff --check
```

Contrato: `docs/contracts/relay-sync-v1.md`.
