# Motor de automação e agendador

## Regras (WHEN / THEN)

Tabela `automation_rules`: `event`, `conditions` (lista de `{field, op, value}`, todas precisam valer), `action` e `params`.

| Evento | Quando | Fatos disponíveis |
|---|---|---|
| `invoice.status_changed` | toda transição | campos de `invoices_v` + `from`, `to` |
| `invoice.due_soon` | varredura, faturas não pagas até 30 dias | `days_left` + campos da fatura |
| `contract.expiring` | varredura, contratos vigentes sem renovação automática | `days_left` + campos do contrato |
| `expectation.missing` | expectativa marcada como faltante | campos de `expectations_v` |

Operadores: `eq`, `ne`, `gt`, `gte`, `lt`, `lte`, `in`.

Ações:
- `notify`: `{channel, target, subject, template}`. O template usa `text/template` com os fatos; campos de data ganham `*_fmt` (dd/mm/aaaa; competência em mm/aaaa) e valores ganham `*_fmt` em reais.
- `transition`: `{to}` move a fatura (respeitando o workflow).

Regras padrão (migration `001`):
1. Aprovada → mensagem ao financeiro pelo Whatc.
2. Aprovada → mover para `FOR_PAYMENT`.
3. Vencimento em 7, 3, 1 e 0 dias → aviso interno.
4. Fatura em revisão ou bloqueada → aviso interno.
5. Contrato a 90, 60, 30, 15 ou 0 dias do fim → aviso interno.
6. Fatura faltante → aviso interno.

Cada execução que age ou falha fica em `automation_runs`. Notificações periódicas usam `dedupe_key` para não repetir (ex.: um aviso por fatura por marco de dias).

## Agendador

Tabela `schedules` (`kind`, `every`, `next_run_at`). A cada 15 s o app reserva as entradas vencidas e enfileira um job.

| Tarefa | Intervalo | Faz |
|---|---|---|
| `daily_scan` | 1 h | gera expectativas, marca faltantes, avisa vencimentos e contratos |
| `send_notifications` | 1 min | entrega o outbox (retry até 5 tentativas) |
| `imap_poll` | 5 min | lê a caixa de faturas (se configurada) |

## Fila de jobs

Tabela `jobs` com `FOR UPDATE SKIP LOCKED`: vários workers e várias réplicas sem disputa. Retentativas com backoff exponencial (1 min, 2 min, 4 min…), limite de 5, timeout de 5 min por execução, recuperação de jobs órfãos no restart. Histórico em **Registros → Tarefas em segundo plano**.
