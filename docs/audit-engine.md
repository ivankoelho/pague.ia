# Motor de auditoria

`internal/audit` é puro: recebe `Facts` (carregados do banco por `App.auditFacts`) e devolve `Result{Status, Checks}`. A IA não participa da decisão.

## Verificações

| Código | Verifica | Falha leva a |
|---|---|---|
| `supplier` | fornecedor identificado e ativo | BLOCKED |
| `tax_id` | CNPJ do documento = cadastro (ausente: REVIEW) | BLOCKED |
| `service` | serviço associado (ausente: REVIEW) e ativo | BLOCKED |
| `contract` | contrato vigente e não expirado na competência (sem contrato: alerta) | BLOCKED |
| `competence` | competência identificada e esperada para o serviço | REVIEW |
| `duplicate` | mesmo arquivo (SHA-256), mesmo serviço+competência, mesmo nº de documento ou mesma linha digitável | BLOCKED |
| `amount` | variação sobre o valor esperado (expectativa ou serviço) dentro da tolerância | BLOCKED |
| `history` | variação sobre a média das últimas 6 faturas | só alerta |
| `due` | vencimento identificado e não passado (≤ 2 dias: alerta) | REVIEW |
| `payment` | linha digitável com DV válido, valor e vencimento iguais aos da fatura; ou Pix | BLOCKED (DV/valor), REVIEW (vencimento, sem dados) |
| `approval_limit` | valor acima de `AUTO_APPROVE_MAX` | REVIEW |

Resultado: qualquer falha de severidade BLOCKED → `BLOCKED`; senão, qualquer falha → `REVIEW`; senão `APPROVED`. Alertas (`WARN`) não mudam o resultado.

## Evidências

Cada verificação guarda `detail` e, quando útil, `evidence` (ex.: valor, esperado, variação, tolerância; ids das faturas duplicadas). Tudo fica em `audits.checks` (JSONB) e aparece na tela da fatura e no painel de auditoria.

## Linha digitável

`internal/br` valida boleto bancário (47 dígitos: DV dos três campos em módulo 10 e DV geral em módulo 11) e de arrecadação/concessionárias (48 dígitos, DV por bloco em módulo 10 ou 11 conforme o identificador de valor). Do boleto bancário extrai valor e vencimento (com o reinício do fator de vencimento em 22/02/2025). É a âncora que confere a leitura da IA: boleto adulterado ou valor trocado é bloqueado.

## Ajustes

Tolerância por serviço (`tolerance_pct`), alçada global (`AUTO_APPROVE_MAX`). Regras novas entram como mais uma verificação em `audit.Run` com teste em `audit_test.go`.
