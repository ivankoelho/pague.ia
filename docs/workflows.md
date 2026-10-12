# Workflows

## Estados da fatura

```
RECEIVED ─► PROCESSING ─► EXTRACTED ─► IDENTIFIED ─► AUDITING ─┬─► APPROVED ─► FOR_PAYMENT ─► PAID
                │              │                               ├─► REVIEW ──► APPROVED | REJECTED | BLOCKED | AUDITING | PROCESSING
                └──► REVIEW ◄──┘                               ├─► BLOCKED ─► REVIEW | REJECTED | AUDITING | PROCESSING
                                                               └─► REJECTED ─► REVIEW
```

As transições permitidas estão em `internal/workflow/workflow.go`. Qualquer mudança passa por `App.transition`, que:

1. trava a linha (`FOR UPDATE`) e confere se a transição é permitida;
2. grava o evento (`invoice_events`) com ator e nota;
3. dispara o evento de automação `invoice.status_changed`.

Um bloqueio **nunca** vira aprovação direta: `BLOCKED → APPROVED` não existe. É preciso mandar para revisão, corrigir dados ou cadastro, e reauditar ou aprovar com justificativa.

## Pipeline automático

```
upload / e-mail ─► documento salvo (hash) ─► job process_invoice
   PROCESSING: texto do documento (pdftotext; se vazio, Tesseract)
   extração: IA (se configurada) + regras; linha digitável válida prevalece sobre a IA
   EXTRACTED: preenche só campos vazios (o que uma pessoa digitou não é sobrescrito)
   IDENTIFIED: fornecedor por CNPJ (ou nome único sugerido pela IA); serviço único do fornecedor
               ou o de valor esperado mais próximo dentro da tolerância; contrato e unidade do serviço
   AUDITING: motor de auditoria ─► APPROVED | REVIEW | BLOCKED
```

Se o job falhar 5 vezes (com backoff exponencial), a fatura vai para `REVIEW` com o erro na nota.

## Aprovação

- **Automática:** auditoria `APPROVED`. A regra padrão comunica o financeiro pelo Whatc e move para `FOR_PAYMENT`.
- **Alçada:** com `AUTO_APPROVE_MAX > 0`, valores acima vão para `REVIEW` mesmo com tudo OK.
- **Manual:** quem tem `audits.execute` aprova ou rejeita da revisão, sempre com justificativa.
- **Pagamento:** quem tem `payments.write` registra `PAID`; `paid_on` e `paid_amount` são preenchidos se vierem vazios.

## Permissão por destino

| Destino | Permissão |
|---|---|
| APPROVED, REJECTED, REVIEW, AUDITING | `audits.execute` |
| PROCESSING | `invoices.write` |
| FOR_PAYMENT, PAID | `payments.write` |
