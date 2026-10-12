package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ivankoelho/pague.ia/internal/ai"
	"github.com/ivankoelho/pague.ia/internal/audit"
	"github.com/ivankoelho/pague.ia/internal/automation"
	"github.com/ivankoelho/pague.ia/internal/br"
	"github.com/ivankoelho/pague.ia/internal/db"
	"github.com/ivankoelho/pague.ia/internal/workflow"
)

var ErrTransition = errors.New("transição não permitida")

type depthKey struct{}

// transition muda o status respeitando a máquina de estados, grava o evento
// e dispara as automações de invoice.status_changed.
func (a *App) transition(ctx context.Context, id int64, to, actor, note string) error {
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var from string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM invoices WHERE id=$1 FOR UPDATE`, id).Scan(&from); err != nil {
		return err
	}
	if from == to {
		return nil
	}
	if !workflow.Allowed(from, to) {
		return fmt.Errorf("%w: %s → %s", ErrTransition, from, to)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE invoices SET status=$2, updated_at=now() WHERE id=$1`, id, to); err != nil {
		return err
	}
	if to == workflow.Paid {
		tx.ExecContext(ctx, `UPDATE invoices SET paid_on=COALESCE(paid_on,$2), paid_amount=COALESCE(paid_amount,amount) WHERE id=$1`, id, a.today())
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO invoice_events(invoice_id,from_status,to_status,actor,note) VALUES($1,$2,$3,$4,NULLIF($5,''))`,
		id, from, to, actor, note); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	depth, _ := ctx.Value(depthKey{}).(int)
	if depth >= 3 { // evita loops entre regras de transição
		return nil
	}
	facts, err := a.invoiceFacts(ctx, id)
	if err != nil {
		return err
	}
	facts["from"], facts["to"] = from, to
	return a.fire(context.WithValue(ctx, depthKey{}, depth+1), "invoice.status_changed", facts, "")
}

// invoiceFacts são os campos da fatura disponíveis para regras e templates.
func (a *App) invoiceFacts(ctx context.Context, id int64) (map[string]any, error) {
	m, err := db.One(ctx, a.DB, `SELECT * FROM invoices_v WHERE id=$1`, id)
	if err != nil || m == nil {
		return nil, fmt.Errorf("fatura %d: %v", id, err)
	}
	return withFormats(m), nil
}

// withFormats acrescenta versões formatadas (pt-BR) para os templates: due_on_fmt, amount_fmt...
func withFormats(m map[string]any) map[string]any {
	for k, v := range m {
		switch x := v.(type) {
		case string:
			if t, err := time.Parse("2006-01-02", x); err == nil {
				f := t.Format("02/01/2006")
				if k == "competence" {
					f = t.Format("01/2006")
				}
				m[k+"_fmt"] = f
			}
		case float64:
			if strings.Contains(k, "amount") {
				m[k+"_fmt"] = money(x)
			}
		}
	}
	return m
}

func money(f float64) string {
	s := strconv.FormatFloat(f, 'f', 2, 64)
	intPart, dec, _ := strings.Cut(s, ".")
	neg := strings.HasPrefix(intPart, "-")
	intPart = strings.TrimPrefix(intPart, "-")
	var b strings.Builder
	for i, r := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(r)
	}
	out := b.String() + "," + dec
	if neg {
		out = "-" + out
	}
	return out
}

// fire executa as regras do evento. dedupe != "" evita repetir a mesma notificação.
func (a *App) fire(ctx context.Context, event string, facts map[string]any, dedupe string) error {
	rules, err := db.Maps(ctx, a.DB, `SELECT * FROM automation_rules WHERE enabled AND event=$1 ORDER BY priority, id`, event)
	if err != nil {
		return err
	}
	var firstErr error
	for _, rm := range rules {
		var r automation.Rule
		b, _ := json.Marshal(rm)
		json.Unmarshal(b, &r)
		ok, err := automation.Match(r.Conditions, facts)
		if err == nil && !ok {
			continue
		}
		did := false
		if err == nil {
			did, err = a.runAction(ctx, r, facts, dedupe)
		}
		if err != nil || did {
			fj, _ := json.Marshal(facts)
			a.DB.ExecContext(ctx, `INSERT INTO automation_runs(rule_id,event,facts,ok,error) VALUES($1,$2,$3,$4,NULLIF($5,''))`,
				r.ID, event, string(fj), err == nil, errStr(err))
		}
		if err != nil {
			a.Log.Error("automação", "regra", r.Name, "err", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func errStr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (a *App) runAction(ctx context.Context, r automation.Rule, facts map[string]any, dedupe string) (bool, error) {
	var p struct {
		Channel, Target, Subject, Template, To string
	}
	json.Unmarshal(r.Params, &p)
	switch r.Action {
	case "notify":
		body, err := automation.Render(p.Template, facts)
		if err != nil {
			return false, err
		}
		subj, _ := automation.Render(p.Subject, facts)
		entity, _ := facts["_entity"].(string)
		if entity == "" {
			entity = "invoice"
		}
		var key any
		if dedupe != "" {
			key = fmt.Sprintf("%d:%s", r.ID, dedupe)
		}
		res, err := a.DB.ExecContext(ctx, `INSERT INTO notifications(channel,target,subject,body,ref_entity,ref_id,dedupe_key)
			VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT (dedupe_key) DO NOTHING`, p.Channel, p.Target, subj, body, entity, toInt(facts["id"]), key)
		if err != nil {
			return false, err
		}
		n, _ := res.RowsAffected()
		return n > 0, nil
	case "transition":
		id := toInt(facts["id"])
		if id == 0 || p.To == "" {
			return false, fmt.Errorf("regra de transição sem fatura/destino")
		}
		return true, a.transition(ctx, id, p.To, "automação: "+r.Name, "")
	}
	return false, fmt.Errorf("ação desconhecida: %s", r.Action)
}

func toInt(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case float64:
		return int64(x)
	case string:
		n, _ := strconv.ParseInt(x, 10, 64)
		return n
	}
	return 0
}

// --- pipeline de processamento ---

// processInvoice: documento → texto (OCR) → extração (IA/regras) → identificação → auditoria.
func (a *App) processInvoice(ctx context.Context, id int64) error {
	var status string
	if err := a.DB.QueryRowContext(ctx, `SELECT status FROM invoices WHERE id=$1`, id).Scan(&status); err != nil {
		return err
	}
	if status != workflow.Processing {
		if err := a.transition(ctx, id, workflow.Processing, "sistema", ""); err != nil {
			return err
		}
	}
	doc, err := db.One(ctx, a.DB, `SELECT id, storage_key, mime, text_content FROM documents WHERE invoice_id=$1 ORDER BY id DESC LIMIT 1`, id)
	if err != nil {
		return err
	}
	note := "sem documento anexado; dados manuais"
	if doc != nil {
		text, _ := doc["text_content"].(string)
		if text == "" {
			path, _ := a.Store.Path(doc["storage_key"].(string))
			t, err := a.OCR.Text(ctx, path, doc["mime"].(string))
			if err != nil {
				a.Log.Warn("ocr", "invoice", id, "err", err)
				note = "falha na leitura do documento: " + err.Error()
			}
			text = t
			a.DB.ExecContext(ctx, `UPDATE documents SET text_content=$2 WHERE id=$1`, doc["id"], text)
		}
		ex, run := ai.Extract(ctx, a.AI, text)
		if run != nil {
			a.DB.ExecContext(ctx, `INSERT INTO ai_runs(invoice_id,provider,model,prompt_version,input,output,ok,error,duration_ms)
				VALUES($1,$2,$3,$4,$5,$6,$7,NULLIF($8,''),$9)`, id, run.Provider, run.Model, run.PromptVersion,
				run.Input, run.Output, run.OK, run.Error, run.Duration.Milliseconds())
		}
		if err := a.applyExtraction(ctx, id, ex); err != nil {
			return err
		}
		note = "extração: " + map[bool]string{true: "IA + regras", false: "regras"}[run != nil && run.OK]
	}
	if err := a.transition(ctx, id, workflow.Extracted, "sistema", note); err != nil {
		return err
	}
	if err := a.identify(ctx, id); err != nil {
		return err
	}
	if err := a.transition(ctx, id, workflow.Identified, "sistema", ""); err != nil {
		return err
	}
	return a.auditInvoice(ctx, id, "sistema")
}

// applyExtraction preenche apenas o que está vazio: dado digitado por pessoa prevalece.
func (a *App) applyExtraction(ctx context.Context, id int64, ex ai.Extraction) error {
	nz := func(s string) any {
		if s == "" {
			return nil
		}
		return s
	}
	var amount, comp any
	if ex.Amount > 0 {
		amount = ex.Amount
	}
	if ex.Competence != "" {
		comp = ex.Competence + "-01"
	}
	kind := ex.DocType
	switch kind {
	case "fatura", "boleto", "nf", "nfse", "recibo", "outro":
	default:
		kind = ""
	}
	notes := ""
	if ex.SupplierName != "" || ex.ServiceHint != "" {
		notes = strings.TrimSpace("IA: " + ex.SupplierName + " — " + ex.ServiceHint)
	}
	_, err := a.DB.ExecContext(ctx, `UPDATE invoices SET
		tax_id = COALESCE(tax_id, $2), amount = COALESCE(amount, $3::numeric), due_on = COALESCE(due_on, $4::date),
		competence = COALESCE(competence, $5::date), issued_on = COALESCE(issued_on, $6::date),
		doc_number = COALESCE(doc_number, $7), digitable_line = COALESCE(digitable_line, $8), pix = COALESCE(pix, $9),
		kind = CASE WHEN $10 = '' THEN kind ELSE $10 END,
		notes = COALESCE(notes, NULLIF($11,'')), updated_at = now()
		WHERE id=$1`,
		id, nz(ex.TaxID), amount, nz(ex.DueOn), comp, nz(validDate(ex.IssuedOn)), nz(ex.DocNumber), nz(ex.DigitableLine), nz(ex.Pix), kind, notes)
	if err == nil && ex.DigitableLine != "" {
		if b, perr := br.ParseLine(ex.DigitableLine); perr == nil {
			a.DB.ExecContext(ctx, `UPDATE invoices SET barcode=COALESCE(barcode,$2) WHERE id=$1`, id, b.Barcode)
		}
	}
	return err
}

func validDate(s string) string {
	if _, err := time.Parse("2006-01-02", s); err != nil {
		return ""
	}
	return s
}

// identify liga a fatura a fornecedor, serviço, contrato e unidade quando ainda não ligados.
func (a *App) identify(ctx context.Context, id int64) error {
	inv, err := db.One(ctx, a.DB, `SELECT * FROM invoices WHERE id=$1`, id)
	if err != nil {
		return err
	}
	supplierID := toInt(inv["supplier_id"])
	if supplierID == 0 {
		if tax, _ := inv["tax_id"].(string); tax != "" {
			a.DB.QueryRowContext(ctx, `SELECT id FROM suppliers WHERE tax_id=$1`, br.Digits(tax)).Scan(&supplierID)
		}
	}
	if supplierID == 0 {
		// nome sugerido pela IA nas notas: só aceita casamento único
		if notes, _ := inv["notes"].(string); strings.HasPrefix(notes, "IA: ") {
			name := strings.TrimSpace(strings.Split(strings.TrimPrefix(notes, "IA: "), " — ")[0])
			if len(name) >= 3 {
				ids := ids(ctx, a.DB, `SELECT id FROM suppliers WHERE status='ativo' AND (name ILIKE $1 OR legal_name ILIKE $1 OR trade_name ILIKE $1)`, "%"+name+"%")
				if len(ids) == 1 {
					supplierID = ids[0]
				}
			}
		}
	}
	serviceID := toInt(inv["service_id"])
	if serviceID == 0 && supplierID != 0 {
		cands := ids(ctx, a.DB, `SELECT id FROM services WHERE supplier_id=$1 AND status <> 'encerrado'`, supplierID)
		if len(cands) == 1 {
			serviceID = cands[0]
		} else if len(cands) > 1 {
			// vários serviços do mesmo fornecedor: escolhe o de valor esperado mais próximo, se dentro da tolerância
			var best int64
			a.DB.QueryRowContext(ctx, `SELECT id FROM services WHERE supplier_id=$1 AND status <> 'encerrado' AND expected_amount > 0
				AND abs($2::numeric - expected_amount) / expected_amount * 100 <= tolerance_pct
				ORDER BY abs($2::numeric - expected_amount) LIMIT 1`, supplierID, inv["amount"]).Scan(&best)
			serviceID = best
		}
	}
	_, err = a.DB.ExecContext(ctx, `UPDATE invoices i SET
		supplier_id = COALESCE(i.supplier_id, NULLIF($2::bigint, 0)),
		service_id  = COALESCE(i.service_id, NULLIF($3::bigint, 0)),
		contract_id = COALESCE(i.contract_id, (SELECT contract_id FROM services WHERE id = COALESCE(i.service_id, NULLIF($3::bigint, 0)))),
		unit_id     = COALESCE(i.unit_id, (SELECT unit_id FROM services WHERE id = COALESCE(i.service_id, NULLIF($3::bigint, 0)))),
		-- sem competência: assume o mês anterior ao vencimento (cobrança pós-paga)
		competence  = COALESCE(i.competence, (date_trunc('month', i.due_on) - interval '1 month')::date),
		updated_at  = now()
		WHERE i.id = $1`, id, supplierID, serviceID)
	return err
}

func ids(ctx context.Context, q db.Queryer, query string, args ...any) []int64 {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		out = append(out, id)
	}
	return out
}

// auditInvoice executa o motor determinístico e move a fatura conforme o resultado.
func (a *App) auditInvoice(ctx context.Context, id int64, actor string) error {
	if err := a.transition(ctx, id, workflow.Auditing, actor, ""); err != nil {
		return err
	}
	f, err := a.auditFacts(ctx, id)
	if err != nil {
		return err
	}
	res := audit.Run(f)
	checks, _ := json.Marshal(res.Checks)
	if _, err := a.DB.ExecContext(ctx, `INSERT INTO audits(invoice_id,result,checks) VALUES($1,$2,$3)`, id, res.Status, string(checks)); err != nil {
		return err
	}
	a.DB.ExecContext(ctx, `UPDATE invoices SET audit_result=$2 WHERE id=$1`, id, res.Status)
	if res.Status != audit.Blocked {
		a.DB.ExecContext(ctx, `UPDATE expectations e SET invoice_id=i.id, status='recebida', updated_at=now()
			FROM invoices i WHERE i.id=$1 AND e.service_id=i.service_id AND e.competence=i.competence
			AND e.invoice_id IS NULL AND e.status IN ('pendente','faltante')`, id)
	}
	return a.transition(ctx, id, res.Status, actor, audit.Summary(res))
}

func (a *App) auditFacts(ctx context.Context, id int64) (audit.Facts, error) {
	f := audit.Facts{Today: a.today(), AutoApproveMax: a.Cfg.AutoApproveMax}
	inv, err := db.One(ctx, a.DB, `SELECT * FROM invoices WHERE id=$1`, id)
	if err != nil || inv == nil {
		return f, fmt.Errorf("fatura %d: %v", id, err)
	}
	str := func(m map[string]any, k string) string { s, _ := m[k].(string); return s }
	num := func(m map[string]any, k string) float64 { n, _ := m[k].(float64); return n }
	date := func(m map[string]any, k string) *time.Time {
		t, err := time.Parse("2006-01-02", str(m, k))
		if err != nil {
			return nil
		}
		return &t
	}
	f.Invoice = audit.Invoice{ID: id, TaxID: str(inv, "tax_id"), Amount: num(inv, "amount"), Due: date(inv, "due_on"),
		Competence: date(inv, "competence"), DocNumber: str(inv, "doc_number"), DigitableLine: str(inv, "digitable_line"), Pix: str(inv, "pix")}

	if s, _ := db.One(ctx, a.DB, `SELECT * FROM suppliers WHERE id=$1`, inv["supplier_id"]); s != nil {
		f.Supplier = &audit.Supplier{Name: str(s, "name"), TaxID: str(s, "tax_id"), Status: str(s, "status")}
	}
	svc, _ := db.One(ctx, a.DB, `SELECT * FROM services WHERE id=$1`, inv["service_id"])
	if svc != nil {
		f.Service = &audit.Service{Name: str(svc, "name"), Status: str(svc, "status"),
			ExpectedAmount: num(svc, "expected_amount"), TolerancePct: num(svc, "tolerance_pct")}
	}
	contractID := inv["contract_id"]
	if contractID == nil && svc != nil {
		contractID = svc["contract_id"]
	}
	if ct, _ := db.One(ctx, a.DB, `SELECT * FROM contracts WHERE id=$1`, contractID); ct != nil {
		f.Contract = &audit.Contract{Number: str(ct, "number"), Status: str(ct, "status"), EndsOn: date(ct, "ends_on"),
			AutoRenew: ct["auto_renew"] == true}
		if s := date(ct, "starts_on"); s != nil {
			f.Contract.StartsOn = *s
		}
	}
	if e, _ := db.One(ctx, a.DB, `SELECT * FROM expectations WHERE service_id=$1 AND competence=$2 AND status<>'dispensada'`,
		inv["service_id"], inv["competence"]); e != nil {
		f.Expectation = &audit.Expectation{ExpectedAmount: num(e, "expected_amount"), ExpectedDue: date(e, "expected_due")}
	}
	rows, err := a.DB.QueryContext(ctx, `SELECT amount FROM invoices WHERE service_id=$1 AND id<>$2 AND amount IS NOT NULL
		AND status NOT IN ('REJECTED','BLOCKED') ORDER BY competence DESC NULLS LAST, id DESC LIMIT 6`, inv["service_id"], id)
	if err == nil {
		for rows.Next() {
			var v float64
			rows.Scan(&v)
			f.History = append(f.History, v)
		}
		rows.Close()
	}
	f.Duplicates = ids(ctx, a.DB, `SELECT id FROM invoices WHERE id<>$1 AND status <> 'REJECTED' AND (
		(service_id=$2 AND competence=$3) OR (supplier_id=$4 AND doc_number IS NOT NULL AND doc_number=$5) OR
		(digitable_line IS NOT NULL AND digitable_line=$6))`,
		id, inv["service_id"], inv["competence"], inv["supplier_id"], inv["doc_number"], inv["digitable_line"])
	f.SameFileIDs = ids(ctx, a.DB, `SELECT DISTINCT o.invoice_id FROM documents d JOIN documents o ON o.sha256=d.sha256 AND o.id<>d.id
		JOIN invoices i ON i.id=o.invoice_id WHERE d.invoice_id=$1 AND o.invoice_id<>$1 AND i.status<>'REJECTED'`, id)
	return f, nil
}
