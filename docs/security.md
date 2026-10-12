# Segurança

## Autenticação e sessão

- Senhas com PBKDF2-SHA256 (600 mil iterações, salt aleatório), comparação em tempo constante. Mínimo de 10 caracteres.
- Sessão por token aleatório de 256 bits; no banco fica só o SHA-256 do token. Validade de 12 h.
- Cookie `sid`: `HttpOnly`, `SameSite=Strict`, `Secure` com `COOKIE_SECURE=true`.
- Trocar senha ou desativar o usuário encerra todas as sessões dele.
- Login limitado a 10 tentativas por minuto por IP; falhas vão para a trilha de auditoria.

## CSRF e cabeçalhos

- Cookie `SameSite=Strict` e, para qualquer escrita na API, recusa de `Origin` de outro host.
- `Content-Security-Policy` restrita a `'self'`, `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: same-origin`.

## Autorização (RBAC)

| Papel | Pode |
|---|---|
| Administrador | tudo, inclusive usuários e configurações |
| Gestor | cadastros, faturas, auditoria (executar), leitura de pagamentos e registros |
| Auditor | leitura geral e executar auditoria/decisões |
| TI | cadastros, faturas, auditoria |
| Financeiro | leitura, pagamentos (encaminhar e registrar pagamento) |
| Consulta | somente leitura |

Permissões granulares (`services.read/write`, `invoices.*`, `audits.read/execute`, `payments.*`, `settings.*`, `logs.read`, `users.*`) em `internal/app/auth.go`. O backend é a autoridade; o frontend só esconde o que não pode ser usado.

## Documentos

- Tipo detectado pelo **conteúdo** (`http.DetectContentType`), não pela extensão. Aceitos: PDF, XML, PNG, JPEG.
- Limite de tamanho (`MAX_UPLOAD_MB`, padrão 20).
- Arquivos guardados fora da pasta pública, com nome = SHA-256; download só autenticado, com `Content-Security-Policy: sandbox`.
- Ferramentas de OCR rodam com o usuário sem privilégios do container.

## Dados e segredos

- Consultas sempre parametrizadas; nomes de colunas vêm de listas fixas.
- Segredos (Groq, IMAP, SMTP, Whatc) só por variáveis de ambiente, nunca no banco nem nos logs.
- Container roda como usuário `pague` (uid 10001).

## Trilha de auditoria

Toda escrita relevante grava usuário, data/hora, ação, entidade, id, antes/depois (só campos alterados), motivo, origem e IP (`X-Forwarded-For` só com `TRUST_PROXY=true`).

## Pendências conhecidas

- Segredos cifrados no banco (hoje ficam no ambiente): necessário quando a configuração de integrações for pela interface.
- Rate limit em memória por instância: mover para o Postgres se houver várias réplicas.
- MFA e SSO/LDAP: roadmap.
