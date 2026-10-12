# Roadmap

| Fase | Tema | Situação |
|---|---|---|
| 1 | Foundation: Go, Postgres, migrations, Docker, auth, RBAC, trilha de auditoria | ✅ entregue |
| 2 | Serviços, fornecedores, contratos, unidades, expectativas | ✅ entregue |
| 3 | Faturas e documentos: upload, hash, OCR, armazenamento | ✅ entregue |
| 4 | Motor de auditoria (11 verificações, boleto/arrecadação, CNPJ) | ✅ entregue |
| 5 | Workflow, automação, agendador, fila de jobs, outbox | ✅ entregue |
| 6 | IA: abstração de provider, Groq, extração rastreável | ✅ entregue (extração) |
| 7 | Painéis: executivo, vencimentos (lista e calendário), auditoria | ✅ entregue |
| 8 | Integrações: IMAP, Whatc, SMTP, webhook | ✅ base entregue; Whatc a alinhar com a API real |
| 9 | Analytics avançado | pendente |
| 10 | ERP / financeiro | pendente |

## Próximas entregas sugeridas

**Curto prazo**
- Alinhar o adaptador do Whatc com a API real (ocorrência, mensagem em grupo, anexo do PDF, consulta de status).
- Tokens de API por integração (n8n, ERP) com escopo de permissões.
- Editor de regras de automação na interface (hoje: ativar/desativar; criação via SQL/migration).
- Fluxo de aprovação em múltiplos níveis por valor/categoria/unidade (hoje: alçada única `AUTO_APPROVE_MAX`).
- IA explicando divergências e resumindo a auditoria.
- Parser de XML de NF-e/NFS-e (campos estruturados sem IA).

**Médio prazo**
- Widgets configuráveis no painel; exportação CSV/PDF dos relatórios.
- Comprovante de pagamento anexado e conciliação.
- Microsoft Graph / Gmail API como fontes de e-mail.
- S3/MinIO para documentos; segredos cifrados no banco.
- LDAP/SSO, MFA.

**Fora do escopo atual** (por decisão do projeto): pagamento bancário, integração fiscal complexa, Kubernetes, microsserviços, WhatsApp próprio.
