# Deploy

## Local / homologação

```bash
cp .env.example .env    # ADMIN_PASSWORD obrigatório no primeiro start
docker compose up --build -d
docker compose logs -f app
```

Sobe `app` (Go + React + poppler + tesseract-por) e `db` (Postgres 16). Volumes: `pgdata` (banco) e `files` (documentos).

## Produção (quando for a hora)

Fora do escopo desta entrega; checklist para quando o deploy for autorizado:

- [ ] `SEED_DEMO=false` e banco novo (ou apagar os registros `is_demo`).
- [ ] HTTPS no proxy reverso + `COOKIE_SECURE=true` + `TRUST_PROXY=true`.
- [ ] Postgres gerenciado/com backup (dump diário) e backup do volume `files`.
- [ ] Caixa IMAP dedicada; `GROQ_API_KEY`; `WHATC_*` alinhado com a API real.
- [ ] Validar com faturas reais em modo `INTEGRATIONS_DRY_RUN=true` antes de desligá-lo.
- [ ] Zabbix monitorando `/readyz` e `/metrics` (`pague_jobs{status="failed"}`, `pague_jobs_oldest_pending_seconds`).
- [ ] Trocar a senha do administrador inicial e criar usuários por papel.

## Variáveis

Ver `.env.example`. Principais: `DATABASE_URL`, `ADMIN_EMAIL`, `ADMIN_PASSWORD`, `DATA_DIR`, `INTEGRATIONS_DRY_RUN`, `AUTO_APPROVE_MAX`, `WORKERS`, `TZ`.

## Migrations

Embutidas no binário (`internal/db/migrations`), aplicadas no start em ordem, uma vez, sob advisory lock (seguro com várias réplicas). Mudanças de schema = novo arquivo `00N_descricao.sql`; nunca editar um já aplicado.

## Escala

O app é stateless (sessões e fila no banco, arquivos no volume). Para mais de uma réplica: volume compartilhado ou `storage.Store` em S3/MinIO, e rate limit no Postgres.
