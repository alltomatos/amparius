# Receita Federal — o que existe e como usar

Levantado em 21/11/2026 via API pública do GitHub (`orgs/ReceitaFederal`). A organização tem **3 repositórios públicos**, todos ligados à Reforma Tributária do Consumo (RTC, LC 214/2025) e à NFS-e.

## 1. Repositórios

| Repositório | Linguagem | O que é |
|---|---|---|
| `calculadora-rtc-regime-regular` | Java 25 | API REST que calcula **CBS, IBS e Imposto Seletivo** no regime geral; base de cálculo unificada de NF-e/NFC-e e NFS-e; geração e validação de XML por XSD; consulta de CST/cClassTrib |
| `calculadora-rtc-simples-nacional` | Java 25 | API equivalente para o **Simples Nacional** (CBS/IBS), com atividades vigentes por data, anexo e cClassTrib |
| `calculadora-rtc-gerador-danfse` | Go 1.27+ | Serviço HTTP que gera o **DANFSe em PDF** a partir do XML da NFS-e, com limite de tamanho, logs JSON e telemetria OTel |

Observações da leitura dos READMEs:

- As calculadoras expõem Swagger e endpoints de observabilidade (erro por item, sem interromper os demais).
- A do Simples tem perfil **offline** e endpoint de status de versão comparando base local e online — conceito diretamente útil para o relay.
- O gerador de DANFSe é um serviço pequeno, com Dockerfile e validação de XML — candidato natural a sidecar.

## 2. Como o Amparius usa isso

**Não copiar código.** Consumir como referência de contrato e, em desenvolvimento/teste, como oráculo:

1. **Oráculo de testes**: rodar as calculadoras em container e comparar o resultado do nosso motor com o oficial para uma matriz de casos (NCM × CST × cClassTrib × regime × UF/município × data).
2. **Validação de XML**: usar os endpoints de validação XSD como segunda opinião nos testes; em produção, validar localmente com os XSD oficiais embarcados.
3. **DANFSe**: integrar por HTTP (sidecar) em vez de reimplementar o layout; manter fallback próprio se o serviço não estiver disponível.
4. **Tabelas**: modelar `tax_rule` com vigência e origem (`source`, `source_version`), alimentada por importação versionada — nunca por constante no código.

> Licença: verificar o `LICENSE` de cada repositório antes de qualquer reutilização de código. A arquitetura aqui assume **integração por HTTP e uso como oráculo**, o que independe de licença para código.

## 3. Implicações para o produto

- O motor fiscal precisa nascer **RTC-first**: CBS/IBS/IS com transição até 2033, além de ICMS/ISS/PIS/COFINS legados.
- “Base de cálculo unificada” é um conceito que devemos espelhar no domínio: um cálculo por item, com memória reproduzível (entradas + tabela + resultado + hash).
- Simples Nacional não é um detalhe: boa parte do setor funerário/floricultura é Simples. Duas trilhas de cálculo desde o começo.
- O perfil offline da calculadora do Simples confirma a necessidade de **tabelas locais versionadas** no relay.

## 4. O que ainda falta pesquisar (backlog)

- XSD e manuais de NFS-e por município alvo (Fortaleza, São Paulo, Rio).
- Regras de contingência de NFS-e e NF-e vigentes.
- Layouts SPED (contribuições, fiscal) e obrigações acessórias do setor.
- Tabelas oficiais de NCM/CFOP/cClassTrib e seus calendários de atualização.
- Requisitos de certificado (A1/A3) e provedores de assinatura.
