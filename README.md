# Pague.IA — Gestão de Serviços

Controle de serviços recorrentes, contratos, assinaturas e faturas, com auditoria automática antes de encaminhar ao Contas a Pagar.

```
E-mail ─► Fatura ─► Leitura (OCR) ─► IA/regras ─► Identificação ─► Auditoria ─► Workflow
       ─► Aprovação ─► Whatc (ocorrência + WhatsApp) ─► Contas a Pagar ─► Pagamento ─► Painel/Histórico
```

Uma única aplicação (Go + React embutido) e um PostgreSQL. Workflow, automação, agendador, fila de jobs, OCR, IA, notificações e painéis são nativos; Groq, Whatc, e-mail e webhooks são integrações opcionais. Sem elas, o sistema continua funcionando.

## Subir localmente

```bash
cp .env.example .env        # defina ADMIN_PASSWORD
docker compose up --build
```

Acesse http://localhost:8080 com `ADMIN_EMAIL` / `ADMIN_PASSWORD`.
Com `SEED_DEMO=true` o banco recebe dados **fictícios**, todos marcados com `[DEMO]`: uma fatura aprovada, uma divergente, uma bloqueada, uma em revisão, uma vencida, uma pendente e o histórico pago.

Com `INTEGRATIONS_DRY_RUN=true` (padrão), nenhuma mensagem sai do sistema: Whatc, e-mail e webhook ficam registrados como *simulados*.

## Desenvolvimento

```bash
# backend (precisa de PostgreSQL, poppler-utils e tesseract-ocr-por no PATH)
DATABASE_URL=postgres://... ADMIN_PASSWORD=... go run ./cmd/server

# frontend com hot reload (proxy /api → :8080)
cd web && npm install && npm run dev
```

### Testes

```bash
go test ./...                                     # unitários
TEST_DATABASE_URL=postgres://.../pague_test go test -p 1 ./...   # + integração (recria o schema)
cd web && npm test
```

Os testes de integração cobrem fatura normal, duplicada (mesmo serviço/competência e mesmo arquivo), valor divergente, fornecedor desconhecido, contrato expirado, fatura faltante, vencimento próximo, o upload de PDF real até `FOR_PAYMENT`, RBAC, CSRF e rate limit.

## Estrutura

```
cmd/server         ponto de entrada (migrations, admin inicial, workers, HTTP)
internal/app       módulos da aplicação: auth/RBAC, cadastros, faturas, pipeline, jobs, automação, painéis, seed
internal/audit     motor de auditoria determinístico (puro)
internal/workflow  máquina de estados da fatura
internal/automation avaliação de regras WHEN/THEN
internal/ai        abstração de provider + Groq + extração por regras
internal/br        CNPJ/CPF, boleto bancário e de arrecadação
internal/ocr       pdftotext/Tesseract
internal/ingest    cliente IMAP e leitura de anexos
internal/notify    canais: aviso interno, e-mail, webhook, Whatc
internal/storage   armazenamento de arquivos (local; S3/MinIO pela mesma interface)
internal/db        conexão, migrations embutidas
web/               React + TypeScript (Vite)
api/openapi.yaml   contrato da API (também em /api/openapi.yaml)
docs/              arquitetura, domínio, motores, integrações, segurança, deploy, roadmap
```

## Documentação

[Arquitetura](docs/architecture.md) · [Domínio](docs/domain-model.md) · [Workflows](docs/workflows.md) · [Auditoria](docs/audit-engine.md) · [Automação](docs/automation-engine.md) · [IA](docs/ai-engine.md) · [Integrações](docs/integrations.md) · [Segurança](docs/security.md) · [Deploy](docs/deployment.md) · [Roadmap](docs/roadmap.md)
