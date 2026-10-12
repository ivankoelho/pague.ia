# Arquitetura

## Princípio

Monólito modular: **um binário** e **um banco**. Não há servidor separado para IA, OCR, automação, workflow, painel ou integrações.

```
┌──────────────────────────── app (container único) ─────────────────────────────┐
│  HTTP (API REST + SPA React embutida)                                           │
│  ├── auth / RBAC / trilha de auditoria                                          │
│  ├── cadastros declarativos (fornecedores, serviços, contratos, expectativas)  │
│  ├── faturas: upload, detalhe, transições                                       │
│  └── painéis (SQL agregado)                                                     │
│                                                                                 │
│  Workers (goroutines) ◄── fila `jobs` no Postgres (FOR UPDATE SKIP LOCKED)      │
│  Agendador            ──► tabela `schedules` (varredura, envio, IMAP)           │
│                                                                                 │
│  Pipeline: ocr ─► ai.Extract ─► identify ─► audit.Run ─► workflow ─► automation │
│  Motores puros: audit · workflow · automation · br · ai · ocr                   │
│  Saídas: notify (in_app, email, webhook, whatc) via outbox `notifications`      │
└─────────────────────────────────────┬───────────────────────────────────────────┘
                                      │
                                 PostgreSQL 16
```

## Decisões

| Decisão | Motivo | Quando revisar |
|---|---|---|
| Fila e agendador no Postgres | elimina Redis/cron; funciona com N réplicas (`SKIP LOCKED`, advisory lock no envio) | volume de jobs que pressione o banco |
| OCR por `pdftotext` + `tesseract` na mesma imagem | sem servidor de OCR; PDF com texto nem passa pelo Tesseract | necessidade de OCR de layout complexo (entra pela interface `ocr.Extractor`) |
| IA opcional, atrás de `ai.Provider` | o sistema funciona sem IA (regras + linha digitável); troca de provedor sem tocar no pipeline | — |
| Auditoria determinística separada da IA | decisão financeira rastreável e testável | — |
| Cadastros declarativos (`internal/app/resources.go`) | uma definição por entidade gera list/get/create/update, validação e audit log | entidade com regra de escrita própria (fica em handler dedicado, como faturas) |
| `lib/pq` como única dependência Go | sem árvore de dependências transitivas | — |
| Frontend embutido via `go:embed` | um artefato só para deploy | — |
| Router por hash no frontend, sem libs de UI | bundle pequeno, nada a manter | navegação mais rica |

## Como os módulos se separam no futuro

Os motores (`audit`, `automation`, `workflow`, `ai`, `ocr`, `br`) não conhecem banco nem HTTP. Se um dia o OCR precisar escalar sozinho, basta implementar `ocr.Extractor` chamando um serviço externo; o mesmo vale para `storage.Store` (S3/MinIO) e `notify.Provider`.

## Observabilidade

- Logs estruturados em JSON (`log/slog`) no stdout.
- `GET /healthz` (vivo), `GET /readyz` (banco acessível), `GET /metrics` (formato Prometheus: jobs, faturas e notificações por status, idade do job pendente mais antigo). O Zabbix lê o mesmo endpoint pelo item *HTTP agent*.
- Toda execução de job guarda tentativas, duração e erro (`/api/logs/jobs`).
