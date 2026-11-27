# Cadastro operacional de PJ — contrato HTTP v1

A [ADR 0044](../adr/0044-cadastro-operacional-pj.md) registra as decisões da #78. As rotas v1 são registradas nos dois ramos do bootstrap. Sem autenticador OIDC, retornam `503 AUTHENTICATION_UNAVAILABLE`. A sessão autenticada valida OIDC/MFA e vínculo com a empresa selecionada. O repositório usa o tenant da sessão e aplica RLS. O `companyId` de uma resposta de representante identifica a PJ representada.

| Método e caminho | Entrada | Sucesso |
| --- | --- | --- |
| `POST /v1/legal-entities` | `legalName`, `cnpj`, opcionais `tradeName`, `stateRegistration`, `municipalRegistration` | 201, resumo da PJ |
| `GET /v1/legal-entities` | `q` obrigatório, `limit` opcional | 200, `{"items":[]}` com resumos |
| `GET /v1/legal-entities/{id}` | UUID da PJ | 200, resumo com `contacts`, `addresses` e `representatives` |
| `POST /v1/legal-entities/{id}/representatives` | `personId` (UUID de PF existente), `role` | 201, representante |
| `POST /v1/parties/{partyId}/contacts` | `kind`, `value`, `primary` opcional | 201, contato |
| `GET /v1/parties/{partyId}/contacts` | UUID da parte | 200, `{"items":[]}` com contatos |
| `POST /v1/parties/{partyId}/addresses` | `street`, `number`, `city`, `state`, `zip`, `primary` opcional | 201, endereço |
| `GET /v1/parties/{partyId}/addresses` | UUID da parte | 200, `{"items":[]}` com endereços |

Os GETs exigem `party:read` (owner, admin, manager, operator e auditor). Os POSTs exigem `party:write` (owner, admin, manager e operator). Os caminhos de contato e endereço aceitam PF e PJ do tenant.

- PJ. O resumo inclui `id`, `kind`, `status` e os campos da criação. Textos têm até 200 caracteres. O CNPJ numérico ou alfanumérico é normalizado em 14 posições com validação dos dígitos verificadores. A busca usa razão social, nome fantasia ou CNPJ normalizado, com `q` de até 200 caracteres e `limit` entre 1 e 100 (padrão 25). As listas retornam `items` sem cursor ou offset.
- Contato. Tipos `email`, `phone`, `whatsapp` e `other`. Valor de até 254 caracteres. E-mail exige formato válido. Telefone e WhatsApp são reduzidos a 10–13 dígitos.
- Endereço. Logradouro, número e cidade têm até 120 caracteres cada. UF é normalizada para duas letras maiúsculas e CEP para oito dígitos.
- Contato e endereço retornam os campos gravados mais `id` e `partyId`. `primary` tem padrão `false`. Um novo principal substitui os anteriores da mesma parte (por tipo, no caso de contatos) na mesma transação.
- Representante. Papéis `legal_representative`, `administrator`, `attorney` e `contact`. Retorna `id`, `companyId`, `personId`, `role`, `civilName`, `socialName` e `displayName`.

## Validação e privacidade

| HTTP | Condição |
| --- | --- |
| 400 `VALIDATION_ERROR` | Corpo, filtro, UUID ou campo inválido |
| 401 / 403 | Sessão inválida ou acesso bloqueado por vínculo, MFA ou permissão |
| 404 `NOT_FOUND` | Recurso ausente ou fora do tenant |
| 409 `LEGAL_ENTITY_ALREADY_EXISTS` | CNPJ já cadastrado no tenant |
| 409 `REPRESENTATIVE_ALREADY_EXISTS` | Mesmo representante e papel para a PJ |
| 409 `PARTY_KIND_INVALID` | Parte indicada como representante é uma PJ |
| 503 `PRIVACY_POLICY_UNAVAILABLE` | Falta política válida para a finalidade |
| 503 `PARTY_UNAVAILABLE` | Serviço indisponível ou falha na persistência da operação |

Contato, endereço e representante exigem política por tenant aprovada pelo DPO para `party.contact.add`, `party.address.add` e `party.representative.link`, respectivamente. A política fornece finalidade, base legal e retenção. A auditoria grava códigos e IDs opacos na mesma transação. Falha na auditoria reverte a gravação. O cadastro básico de PJ tem um caminho próprio, sem esse gate. Consulte o [contrato de auditoria](audit-lgpd-v1.md).

PJ legada mantém o ID e o documento armazenado na resposta. CNPJ válido com pontuação continua buscável pela forma canônica. Uma PJ com documento mascarado pode ser localizada pelo nome. Para criar representação, o CNPJ legado precisa ser válido. Quando faltar a extensão de empresa, a transação do vínculo a cria com o ID existente.

Exemplo de corpo com CNPJ sintético da fixture de teste.

```json
{"legalName":"Funerária Exemplo","cnpj":"12.ABC.345/01DE-35","tradeName":"Exemplo"}
```

O espelho `/_dev` é opt-in em desenvolvimento privado e usa dados sintéticos. A busca dev usa o mesmo repositório e envelope da v1. As garantias de sessão, política e auditoria acima descrevem o contrato v1.
