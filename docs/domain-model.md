# Modelo de domínio

```
Unidade (empresa ▸ unidade ▸ departamento)
Fornecedor 1─N Contrato
Fornecedor 1─N Serviço N─1 Contrato
Serviço    1─N Expectativa (uma por competência)
Serviço    1─N Fatura 1─N Documento
Fatura     1─N Evento de workflow · Auditoria · Execução de IA · Notificação
```

| Entidade | Papel | Pontos importantes |
|---|---|---|
| **Fornecedor** | quem cobra | CNPJ/CPF validado e único; usado para identificar a fatura |
| **Serviço** | o que é pago | valor esperado, tolerância (%), dia de vencimento, periodicidade, unidade, centro de custo, responsável, criticidade, status (`ativo`, `suspenso`, `cancelado`, `encerrado`, `em_implantacao`) |
| **Contrato** | base legal | vigência, renovação automática, índice e percentual de reajuste; alertas configuráveis (90/60/30/15/0 dias) |
| **Expectativa** | o que o sistema espera receber | gerada pela varredura para a competência anterior, atual e seguinte de cada serviço ativo, respeitando a periodicidade; `pendente → recebida`, ou `faltante` quando não chega até 5 dias antes do vencimento esperado |
| **Fatura** | a cobrança recebida | competência (mês), vencimento, valor, documento, CNPJ, linha digitável/código de barras/Pix, origem (`manual`, `email`, `api`, `demo`), dados de pagamento |
| **Documento** | arquivo original | nome, tipo (detectado pelo conteúdo), tamanho, SHA-256 (duplicidade), origem, texto extraído |
| **Auditoria** | resultado do motor | `APPROVED`, `REVIEW`, `BLOCKED`; cada verificação com evidência |
| **Execução de IA** | rastreabilidade | provedor, modelo, versão do prompt, entrada, resposta, duração, erro |
| **Notificação** | outbox | canal, destino, mensagem, tentativas, status (`pending`, `sent`, `mock`, `failed`) |
| **Trilha de auditoria** | quem mudou o quê | usuário, ação, entidade, antes/depois (apenas campos alterados), motivo, IP |

## Competência e vencimento

Competência é o mês do serviço prestado, gravado como o 1º dia do mês. A expectativa de vencimento é o `due_day` do serviço no **mês seguinte** à competência (cobrança pós-paga): competência 10/2026, vencimento esperado 15/11/2026. Quando a fatura não informa competência, o sistema assume o mês anterior ao vencimento, e a auditoria confere se essa competência era esperada.

## Histórico de valores

O histórico do serviço é a sequência de faturas não rejeitadas. A auditoria compara o valor com a média das últimas 6 e registra a variação como alerta; o bloqueio é sempre contra o valor esperado e a tolerância do serviço.

## Dados de demonstração

Tudo criado pelo seed tem `is_demo = true` e nome iniciado por `[DEMO]`.
