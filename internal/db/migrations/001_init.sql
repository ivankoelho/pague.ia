-- Esquema inicial do Pague.IA (Gestão de Serviços)

CREATE TABLE units (
  id         bigserial PRIMARY KEY,
  name       text NOT NULL,
  kind       text NOT NULL DEFAULT 'unidade' CHECK (kind IN ('empresa','unidade','departamento')),
  parent_id  bigint REFERENCES units(id),
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE users (
  id            bigserial PRIMARY KEY,
  email         text NOT NULL UNIQUE,
  name          text NOT NULL,
  password_hash text NOT NULL,
  role          text NOT NULL CHECK (role IN ('admin','gestor','auditor','ti','financeiro','consulta')),
  active        boolean NOT NULL DEFAULT true,
  created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE sessions (
  token_hash text PRIMARY KEY,
  user_id    bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at timestamptz NOT NULL
);

CREATE TABLE suppliers (
  id          bigserial PRIMARY KEY,
  name        text NOT NULL,
  legal_name  text,
  trade_name  text,
  tax_id      text UNIQUE,
  contacts    text,
  email       text,
  phone       text,
  address     text,
  status      text NOT NULL DEFAULT 'ativo' CHECK (status IN ('ativo','inativo')),
  notes       text,
  is_demo     boolean NOT NULL DEFAULT false,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE documents (
  id           bigserial PRIMARY KEY,
  invoice_id   bigint,
  contract_id  bigint,
  filename     text NOT NULL,
  mime         text NOT NULL,
  size         bigint NOT NULL,
  sha256       text NOT NULL,
  source       text NOT NULL DEFAULT 'upload',
  storage_key  text NOT NULL,
  text_content text,
  created_by   text NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX documents_sha256 ON documents(sha256);

CREATE TABLE contracts (
  id               bigserial PRIMARY KEY,
  number           text NOT NULL,
  supplier_id      bigint NOT NULL REFERENCES suppliers(id),
  starts_on        date NOT NULL,
  ends_on          date,
  auto_renew       boolean NOT NULL DEFAULT false,
  periodicity      text NOT NULL DEFAULT 'mensal' CHECK (periodicity IN ('mensal','bimestral','trimestral','semestral','anual')),
  amount           numeric(14,2),
  adjustment_index text,
  adjustment_pct   numeric(7,3),
  owner            text,
  document_id      bigint REFERENCES documents(id),
  status           text NOT NULL DEFAULT 'vigente' CHECK (status IN ('vigente','em_negociacao','encerrado','cancelado')),
  notes            text,
  is_demo          boolean NOT NULL DEFAULT false,
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE services (
  id              bigserial PRIMARY KEY,
  supplier_id     bigint NOT NULL REFERENCES suppliers(id),
  contract_id     bigint REFERENCES contracts(id),
  unit_id         bigint REFERENCES units(id),
  name            text NOT NULL,
  description     text,
  category        text,
  owner           text,
  cost_center     text,
  periodicity     text NOT NULL DEFAULT 'mensal' CHECK (periodicity IN ('mensal','bimestral','trimestral','semestral','anual')),
  expected_amount numeric(14,2),
  tolerance_pct   numeric(6,2) NOT NULL DEFAULT 5,
  due_day         int CHECK (due_day BETWEEN 1 AND 31),
  status          text NOT NULL DEFAULT 'ativo' CHECK (status IN ('ativo','suspenso','cancelado','encerrado','em_implantacao')),
  criticality     text NOT NULL DEFAULT 'media' CHECK (criticality IN ('baixa','media','alta','critica')),
  starts_on       date,
  ends_on         date,
  renewal_on      date,
  notes           text,
  is_demo         boolean NOT NULL DEFAULT false,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE invoices (
  id                   bigserial PRIMARY KEY,
  supplier_id          bigint REFERENCES suppliers(id),
  service_id           bigint REFERENCES services(id),
  contract_id          bigint REFERENCES contracts(id),
  unit_id              bigint REFERENCES units(id),
  competence           date CHECK (competence IS NULL OR extract(day FROM competence) = 1),
  issued_on            date,
  due_on               date,
  amount               numeric(14,2),
  doc_number           text,
  tax_id               text,
  kind                 text NOT NULL DEFAULT 'fatura' CHECK (kind IN ('fatura','boleto','nf','nfse','recibo','outro')),
  status               text NOT NULL DEFAULT 'RECEIVED' CHECK (status IN (
                         'RECEIVED','PROCESSING','EXTRACTED','IDENTIFIED','AUDITING',
                         'REVIEW','APPROVED','BLOCKED','REJECTED','FOR_PAYMENT','PAID')),
  source               text NOT NULL DEFAULT 'manual' CHECK (source IN ('manual','email','api','demo')),
  received_at          timestamptz NOT NULL DEFAULT now(),
  digitable_line       text,
  barcode              text,
  pix                  text,
  audit_result         text,
  payment_scheduled_on date,
  paid_on              date,
  paid_amount          numeric(14,2),
  notes                text,
  is_demo              boolean NOT NULL DEFAULT false,
  created_at           timestamptz NOT NULL DEFAULT now(),
  updated_at           timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX invoices_service_competence ON invoices(service_id, competence);
CREATE INDEX invoices_due_on ON invoices(due_on);
CREATE INDEX invoices_status ON invoices(status);

ALTER TABLE documents ADD FOREIGN KEY (invoice_id) REFERENCES invoices(id) ON DELETE SET NULL;
ALTER TABLE documents ADD FOREIGN KEY (contract_id) REFERENCES contracts(id) ON DELETE SET NULL;

-- Expectativa de cobrança: o que o sistema espera receber por serviço/competência.
CREATE TABLE expectations (
  id              bigserial PRIMARY KEY,
  service_id      bigint NOT NULL REFERENCES services(id) ON DELETE CASCADE,
  competence      date NOT NULL CHECK (extract(day FROM competence) = 1),
  expected_amount numeric(14,2),
  expected_due    date,
  invoice_id      bigint REFERENCES invoices(id) ON DELETE SET NULL,
  status          text NOT NULL DEFAULT 'pendente' CHECK (status IN ('pendente','recebida','faltante','dispensada')),
  notes           text,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE (service_id, competence)
);

CREATE TABLE invoice_events (
  id          bigserial PRIMARY KEY,
  invoice_id  bigint NOT NULL REFERENCES invoices(id) ON DELETE CASCADE,
  from_status text,
  to_status   text NOT NULL,
  actor       text NOT NULL,
  note        text,
  at          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX invoice_events_invoice ON invoice_events(invoice_id);

CREATE TABLE audits (
  id         bigserial PRIMARY KEY,
  invoice_id bigint NOT NULL REFERENCES invoices(id) ON DELETE CASCADE,
  result     text NOT NULL,
  checks     jsonb NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audits_invoice ON audits(invoice_id);

CREATE TABLE ai_runs (
  id             bigserial PRIMARY KEY,
  invoice_id     bigint REFERENCES invoices(id) ON DELETE CASCADE,
  provider       text NOT NULL,
  model          text NOT NULL,
  prompt_version text NOT NULL,
  input          text,
  output         text,
  ok             boolean NOT NULL,
  error          text,
  duration_ms    int,
  created_at     timestamptz NOT NULL DEFAULT now()
);

-- Fila de jobs + agendador nativos (sem Redis, sem cron externo).
CREATE TABLE jobs (
  id           bigserial PRIMARY KEY,
  kind         text NOT NULL,
  payload      jsonb NOT NULL DEFAULT '{}',
  status       text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','running','done','failed')),
  run_at       timestamptz NOT NULL DEFAULT now(),
  attempts     int NOT NULL DEFAULT 0,
  max_attempts int NOT NULL DEFAULT 5,
  last_error   text,
  duration_ms  int,
  created_at   timestamptz NOT NULL DEFAULT now(),
  started_at   timestamptz,
  finished_at  timestamptz
);
CREATE INDEX jobs_pending ON jobs(run_at) WHERE status = 'pending';

CREATE TABLE schedules (
  kind        text PRIMARY KEY,
  every       interval NOT NULL,
  next_run_at timestamptz NOT NULL DEFAULT now(),
  enabled     boolean NOT NULL DEFAULT true
);

CREATE TABLE automation_rules (
  id         bigserial PRIMARY KEY,
  name       text NOT NULL,
  event      text NOT NULL,
  conditions jsonb NOT NULL DEFAULT '[]',
  action     text NOT NULL CHECK (action IN ('notify','transition')),
  params     jsonb NOT NULL DEFAULT '{}',
  priority   int NOT NULL DEFAULT 100,
  enabled    boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE automation_runs (
  id      bigserial PRIMARY KEY,
  rule_id bigint REFERENCES automation_rules(id) ON DELETE SET NULL,
  event   text NOT NULL,
  facts   jsonb,
  ok      boolean NOT NULL,
  error   text,
  at      timestamptz NOT NULL DEFAULT now()
);

-- Notificações funcionam como outbox: falha de integração vira retry, nunca perda.
CREATE TABLE notifications (
  id         bigserial PRIMARY KEY,
  channel    text NOT NULL CHECK (channel IN ('in_app','email','webhook','whatc')),
  target     text NOT NULL DEFAULT '',
  subject    text NOT NULL,
  body       text NOT NULL,
  status     text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','sent','failed','mock')),
  attempts   int NOT NULL DEFAULT 0,
  last_error text,
  ref_entity text,
  ref_id     bigint,
  dedupe_key text UNIQUE,
  created_at timestamptz NOT NULL DEFAULT now(),
  sent_at    timestamptz,
  read_at    timestamptz
);

CREATE TABLE audit_log (
  id        bigserial PRIMARY KEY,
  user_id   bigint,
  user_name text NOT NULL,
  action    text NOT NULL,
  entity    text NOT NULL,
  entity_id bigint,
  before    jsonb,
  after     jsonb,
  reason    text,
  source    text NOT NULL DEFAULT 'api',
  ip        text,
  at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audit_log_entity ON audit_log(entity, entity_id);

-- Views de leitura (nomes resolvidos para a UI).
CREATE VIEW services_v AS
  SELECT s.*, sp.name AS supplier_name, c.number AS contract_number, u.name AS unit_name
  FROM services s
  JOIN suppliers sp ON sp.id = s.supplier_id
  LEFT JOIN contracts c ON c.id = s.contract_id
  LEFT JOIN units u ON u.id = s.unit_id;

CREATE VIEW contracts_v AS
  SELECT c.*, sp.name AS supplier_name,
         (c.ends_on - current_date) AS days_to_end
  FROM contracts c JOIN suppliers sp ON sp.id = c.supplier_id;

CREATE VIEW invoices_v AS
  SELECT i.*, sp.name AS supplier_name, s.name AS service_name, s.category,
         s.owner, s.cost_center, u.name AS unit_name, c.number AS contract_number,
         (i.due_on - current_date) AS days_to_due
  FROM invoices i
  LEFT JOIN suppliers sp ON sp.id = i.supplier_id
  LEFT JOIN services s ON s.id = i.service_id
  LEFT JOIN units u ON u.id = i.unit_id
  LEFT JOIN contracts c ON c.id = i.contract_id;

CREATE VIEW expectations_v AS
  SELECT e.*, s.name AS service_name, sp.name AS supplier_name, s.supplier_id
  FROM expectations e
  JOIN services s ON s.id = e.service_id
  JOIN suppliers sp ON sp.id = s.supplier_id;

INSERT INTO schedules(kind, every) VALUES
  ('daily_scan', '1 hour'),
  ('send_notifications', '1 minute'),
  ('imap_poll', '5 minutes');

INSERT INTO automation_rules(name, event, conditions, action, params, priority) VALUES
 ('Comunicar financeiro (Whatc) quando aprovada', 'invoice.status_changed',
  '[{"field":"to","op":"eq","value":"APPROVED"}]', 'notify',
  '{"channel":"whatc","target":"financeiro","subject":"Fatura validada para pagamento",
    "template":"Fatura validada para pagamento\nServiço: {{.service_name}}\nCompetência: {{.competence_fmt}}\nVencimento: {{.due_on_fmt}}\nValor: R$ {{.amount_fmt}}\nAuditoria: APROVADA\nProtocolo: PG-{{.id}}"}', 10),
 ('Encaminhar ao Contas a Pagar quando aprovada', 'invoice.status_changed',
  '[{"field":"to","op":"eq","value":"APPROVED"}]', 'transition', '{"to":"FOR_PAYMENT"}', 20),
 ('Alertar vencimento próximo', 'invoice.due_soon',
  '[{"field":"days_left","op":"in","value":[7,3,1,0]}]', 'notify',
  '{"channel":"in_app","subject":"Vencimento próximo","template":"{{.supplier_name}} / {{.service_name}}: vence em {{.days_left}} dia(s) ({{.due_on_fmt}}), status {{.status}}."}', 100),
 ('Alertar fatura bloqueada', 'invoice.status_changed',
  '[{"field":"to","op":"in","value":["BLOCKED","REVIEW"]}]', 'notify',
  '{"channel":"in_app","subject":"Fatura requer atenção","template":"Fatura #{{.id}} ({{.supplier_name}}) foi para {{.to}}."}', 100),
 ('Alertar contrato vencendo', 'contract.expiring',
  '[{"field":"days_left","op":"in","value":[90,60,30,15,0]}]', 'notify',
  '{"channel":"in_app","subject":"Contrato próximo do vencimento","template":"Contrato {{.number}} ({{.supplier_name}}) termina em {{.days_left}} dia(s) ({{.ends_on_fmt}})."}', 100),
 ('Alertar fatura faltante', 'expectation.missing',
  '[]', 'notify',
  '{"channel":"in_app","subject":"Fatura não recebida","template":"{{.supplier_name}} / {{.service_name}}: fatura da competência {{.competence_fmt}} não chegou (vencimento esperado {{.expected_due_fmt}})."}', 100);
