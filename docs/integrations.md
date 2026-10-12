# Integrações

Todas são opcionais. Com `INTEGRATIONS_DRY_RUN=true` (padrão) **nada sai do sistema**: as notificações de e-mail, webhook e Whatc são gravadas com status `mock` ("simulada" na interface).

## E-mail de entrada (IMAP)

`IMAP_ADDR` (host:993, TLS), `IMAP_USER`, `IMAP_PASSWORD`, `IMAP_MAILBOX`. A cada 5 min o app busca mensagens não lidas, salva os anexos PDF/XML/imagem (tipo detectado pelo conteúdo), cria uma fatura por anexo com origem `email` e nota com assunto e remetente, e marca a mensagem como lida. O cliente IMAP é mínimo e usa só a stdlib (`internal/ingest`). Microsoft Graph e Gmail API entram como outra fonte que chama `App.ingestFile`.

Recomendação: uma caixa dedicada (ex.: `faturas@`) e senha de aplicativo.

## Whatc (WhatsApp e ocorrências)

O Whatc é externo; o adaptador fica em `internal/notify` (`notify.Whatc`).

| Variável | Uso |
|---|---|
| `WHATC_URL`, `WHATC_TOKEN` | base da API e token Bearer |
| `WHATC_MESSAGE_PATH` | `POST {to, text}` para enviar a mensagem (padrão `/api/messages`) |
| `WHATC_TICKET_PATH` | se definido, `POST {title, description, group, external_ref}` abre a ocorrência antes; o protocolo devolvido (`protocol` ou `id`) é anexado à mensagem |
| `WHATC_GROUPS` | mapeia destinos lógicos para ids reais: `financeiro=ID,ti=ID` |

**Pendente:** alinhar os caminhos e payloads com a API real do Whatc. Enquanto isso, o contrato acima é a interface; consulta de status e envio do PDF entram quando os endpoints forem definidos. Indisponibilidade do Whatc não trava nada: a notificação fica `pending` e é reenviada até 5 vezes; depois, `failed`, e pode ser reenviada pela tela de Notificações.

Mensagem padrão (editável na regra):

```
Fatura validada para pagamento
Serviço: Vivo SIP
Competência: 09/2026
Vencimento: 15/10/2026
Valor: R$ 1.497,80
Auditoria: APROVADA
Protocolo: PG-123
```

## E-mail de saída (SMTP) e webhook

`SMTP_HOST/PORT/USER/PASSWORD/FROM` (STARTTLS pelo `net/smtp`). `WEBHOOK_URL` recebe o JSON da notificação; uma regra pode usar uma URL própria no `target`. Serve para n8n, ERP ou qualquer automação externa.

## API para terceiros

Contrato em `api/openapi.yaml` (servido em `/api/openapi.yaml`). Hoje a autenticação é por sessão; tokens de API para n8n/ERP estão no roadmap.

## Groq

Ver [ai-engine.md](ai-engine.md).

## Zabbix / Prometheus / Metabase

O Zabbix monitora `/healthz`, `/readyz` e `/metrics` (HTTP agent). O Metabase pode ler o mesmo Postgres (views `*_v`) para BI avançado; nenhum dos dois é dependência.
