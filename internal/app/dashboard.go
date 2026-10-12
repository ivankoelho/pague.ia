package app

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ivankoelho/pague.ia/internal/db"
)

// Dashboards nativos: consultas SQL agregadas, sem BI externo.

type filter struct {
	where []string
	args  []any
}

func (f *filter) arg(v any) string {
	f.args = append(f.args, v)
	return "$" + strconv.Itoa(len(f.args))
}

func (f *filter) sql() string {
	if len(f.where) == 0 {
		return "true"
	}
	return strings.Join(f.where, " AND ")
}

// period devolve [from,to] (padrão: mês corrente).
func (a *App) period(q url.Values) (string, string) {
	t := a.today()
	from := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 1, -1)
	if v, err := time.Parse("2006-01-02", q.Get("from")); err == nil {
		from = v
	}
	if v, err := time.Parse("2006-01-02", q.Get("to")); err == nil {
		to = v
	}
	return from.Format("2006-01-02"), to.Format("2006-01-02")
}

// dims aplica os filtros de dimensão (fornecedor, serviço, unidade, categoria, responsável).
func dims(q url.Values, f *filter, alias string) {
	for _, k := range []string{"supplier_id", "service_id", "unit_id", "category", "owner"} {
		if v := q.Get(k); v != "" {
			f.where = append(f.where, alias+k+"::text = "+f.arg(v))
		}
	}
}

var asService = strings.NewReplacer("supplier_id", "s.supplier_id", "service_id", "s.id", "unit_id", "s.unit_id", "category", "s.category", "owner", "s.owner")

func (a *App) dashExecutive(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, to := a.period(q)
	ctx := r.Context()

	inv := &filter{}
	dims(q, inv, "")
	if v := q.Get("status"); v != "" {
		inv.where = append(inv.where, "status = "+inv.arg(v))
	}
	pf, pt := inv.arg(from), inv.arg(to)
	k, err := db.One(ctx, a.DB, `SELECT
		count(*) FILTER (WHERE due_on BETWEEN `+pf+` AND `+pt+`) AS invoices,
		coalesce(sum(amount) FILTER (WHERE due_on BETWEEN `+pf+` AND `+pt+` AND status<>'REJECTED'),0) AS received,
		coalesce(sum(amount) FILTER (WHERE due_on BETWEEN `+pf+` AND `+pt+` AND status IN ('APPROVED','FOR_PAYMENT','PAID')),0) AS approved,
		coalesce(sum(coalesce(paid_amount,amount)) FILTER (WHERE due_on BETWEEN `+pf+` AND `+pt+` AND status='PAID'),0) AS paid,
		count(*) FILTER (WHERE status NOT IN ('APPROVED','FOR_PAYMENT','PAID','REJECTED')) AS pending,
		count(*) FILTER (WHERE status IN ('REVIEW','BLOCKED')) AS divergences,
		count(*) FILTER (WHERE status NOT IN ('PAID','REJECTED') AND due_on < current_date) AS overdue
		FROM invoices_v WHERE `+inv.sql(), inv.args...)
	if err != nil {
		serverErr(w, err)
		return
	}
	svc := &filter{}
	dims(q, svc, "")
	s, err := db.One(ctx, a.DB, `SELECT count(*) AS active_services, coalesce(sum(expected_amount),0) AS monthly_recurring
		FROM services_v WHERE status='ativo' AND `+strings.ReplaceAll(svc.sql(), "service_id", "id"), svc.args...)
	if err != nil {
		serverErr(w, err)
		return
	}
	ex := &filter{}
	dims(q, ex, "s.")
	ef, et := ex.arg(from), ex.arg(to)
	e, err := db.One(ctx, a.DB, `SELECT coalesce(sum(e.expected_amount) FILTER (WHERE e.expected_due BETWEEN `+ef+` AND `+et+`),0) AS expected,
		count(*) FILTER (WHERE e.status='faltante') AS missing
		FROM expectations e JOIN services_v s ON s.id=e.service_id WHERE e.status<>'dispensada' AND `+strings.ReplaceAll(ex.sql(), "s.service_id", "s.id"), ex.args...)
	if err != nil {
		serverErr(w, err)
		return
	}
	ct, err := db.One(ctx, a.DB, `SELECT count(*) FILTER (WHERE status='vigente') AS contracts_active,
		count(*) FILTER (WHERE status='vigente' AND ends_on BETWEEN current_date AND current_date + 90) AS contracts_expiring
		FROM contracts_v WHERE ($1='' OR supplier_id::text=$1)`, q.Get("supplier_id"))
	if err != nil {
		serverErr(w, err)
		return
	}
	for _, m := range []map[string]any{s, e, ct} {
		for kk, v := range m {
			k[kk] = v
		}
	}

	// séries: últimos 12 meses por competência (realizado x esperado)
	m := &filter{}
	dims(q, m, "")
	monthly, err := db.Maps(ctx, a.DB, `WITH months AS (
			SELECT generate_series(date_trunc('month', `+m.arg(to)+`::date) - interval '11 months', date_trunc('month', `+m.arg(to)+`::date), interval '1 month')::date AS competence)
		SELECT to_char(mo.competence,'MM/YYYY') AS label,
			coalesce((SELECT sum(amount) FROM invoices_v WHERE competence=mo.competence AND status<>'REJECTED' AND `+m.sql()+`),0) AS actual,
			coalesce((SELECT sum(e.expected_amount) FROM expectations e JOIN services_v s ON s.id=e.service_id
				WHERE e.competence=mo.competence AND e.status<>'dispensada' AND `+asService.Replace(m.sql())+`),0) AS expected
		FROM months mo ORDER BY mo.competence`, m.args...)
	if err != nil {
		serverErr(w, err)
		return
	}
	g := &filter{}
	dims(q, g, "")
	gf, gt := g.arg(from), g.arg(to)
	bySupplier, _ := db.Maps(ctx, a.DB, `SELECT coalesce(supplier_name,'(não identificado)') AS label, sum(amount) AS value, count(*) AS n
		FROM invoices_v WHERE due_on BETWEEN `+gf+` AND `+gt+` AND status<>'REJECTED' AND `+g.sql()+` GROUP BY 1 ORDER BY 2 DESC NULLS LAST LIMIT 8`, g.args...)
	byCategory, _ := db.Maps(ctx, a.DB, `SELECT coalesce(category,'(sem categoria)') AS label, sum(amount) AS value, count(*) AS n
		FROM invoices_v WHERE due_on BETWEEN `+gf+` AND `+gt+` AND status<>'REJECTED' AND `+g.sql()+` GROUP BY 1 ORDER BY 2 DESC NULLS LAST`, g.args...)
	byStatus, _ := db.Maps(ctx, a.DB, `SELECT status AS label, count(*) AS value FROM invoices_v WHERE due_on BETWEEN `+gf+` AND `+gt+` AND `+g.sql()+` GROUP BY 1 ORDER BY 2 DESC`, g.args...)
	expiring, _ := db.Maps(ctx, a.DB, `SELECT id, number, supplier_name, ends_on, days_to_end FROM contracts_v
		WHERE status='vigente' AND ends_on BETWEEN current_date - 1 AND current_date + 90 ORDER BY ends_on LIMIT 10`)

	writeJSON(w, 200, map[string]any{"from": from, "to": to, "kpis": k, "monthly": monthly,
		"by_supplier": bySupplier, "by_category": byCategory, "by_status": byStatus, "contracts_expiring": expiring})
}

func (a *App) dashDue(w http.ResponseWriter, r *http.Request) {
	f := &filter{}
	dims(r.URL.Query(), f, "")
	buckets, err := db.One(r.Context(), a.DB, `SELECT
		count(*) FILTER (WHERE days_to_due < 0) AS overdue, coalesce(sum(amount) FILTER (WHERE days_to_due < 0),0) AS overdue_amount,
		count(*) FILTER (WHERE days_to_due = 0) AS today, coalesce(sum(amount) FILTER (WHERE days_to_due = 0),0) AS today_amount,
		count(*) FILTER (WHERE days_to_due BETWEEN 0 AND 3) AS d3, coalesce(sum(amount) FILTER (WHERE days_to_due BETWEEN 0 AND 3),0) AS d3_amount,
		count(*) FILTER (WHERE days_to_due BETWEEN 0 AND 7) AS d7, coalesce(sum(amount) FILTER (WHERE days_to_due BETWEEN 0 AND 7),0) AS d7_amount,
		count(*) FILTER (WHERE days_to_due BETWEEN 0 AND 15) AS d15, coalesce(sum(amount) FILTER (WHERE days_to_due BETWEEN 0 AND 15),0) AS d15_amount,
		count(*) FILTER (WHERE days_to_due BETWEEN 0 AND 30) AS d30, coalesce(sum(amount) FILTER (WHERE days_to_due BETWEEN 0 AND 30),0) AS d30_amount
		FROM invoices_v WHERE status NOT IN ('PAID','REJECTED') AND `+f.sql(), f.args...)
	if err != nil {
		serverErr(w, err)
		return
	}
	items, err := db.Maps(r.Context(), a.DB, `SELECT id, supplier_name, service_name, due_on, days_to_due, amount, status, audit_result
		FROM invoices_v WHERE status NOT IN ('PAID','REJECTED') AND due_on <= current_date + 30 AND `+f.sql()+` ORDER BY due_on, id LIMIT 300`, f.args...)
	if err != nil {
		serverErr(w, err)
		return
	}
	// expectativas ainda sem fatura também aparecem no calendário
	expected, _ := db.Maps(r.Context(), a.DB, `SELECT e.id, e.supplier_name, e.service_name, e.expected_due AS due_on, e.expected_amount AS amount, e.status
		FROM expectations_v e WHERE e.invoice_id IS NULL AND e.status IN ('pendente','faltante') AND e.expected_due <= current_date + 30
		ORDER BY e.expected_due LIMIT 300`)
	writeJSON(w, 200, map[string]any{"buckets": buckets, "items": items, "expected": expected})
}

func (a *App) dashAudit(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, to := a.period(q)
	ctx := r.Context()
	k, err := db.One(ctx, a.DB, `WITH inv AS (SELECT * FROM invoices WHERE received_at::date BETWEEN $1 AND $2),
		last AS (SELECT DISTINCT ON (invoice_id) invoice_id, checks FROM audits WHERE invoice_id IN (SELECT id FROM inv) ORDER BY invoice_id, id DESC)
		SELECT
		 (SELECT count(*) FROM inv) AS received,
		 (SELECT count(*) FROM inv i WHERE EXISTS (SELECT 1 FROM invoice_events e WHERE e.invoice_id=i.id AND e.to_status='APPROVED' AND e.actor='sistema')) AS auto_approved,
		 (SELECT count(*) FROM inv i WHERE EXISTS (SELECT 1 FROM invoice_events e WHERE e.invoice_id=i.id AND e.to_status='APPROVED' AND e.actor<>'sistema')) AS manual_approved,
		 (SELECT count(*) FROM inv WHERE status='REVIEW') AS review,
		 (SELECT count(*) FROM inv WHERE status='BLOCKED') AS blocked,
		 (SELECT count(*) FROM inv WHERE status='REJECTED') AS rejected,
		 (SELECT count(*) FROM last WHERE checks @> '[{"code":"duplicate","outcome":"FAIL"}]') AS duplicates,
		 (SELECT count(*) FROM last WHERE checks @> '[{"code":"amount","outcome":"FAIL"}]') AS amount_variance,
		 (SELECT count(*) FROM last WHERE checks @> '[{"code":"supplier","outcome":"FAIL"}]') AS unknown_supplier,
		 (SELECT count(*) FROM inv WHERE service_id IS NULL) AS without_service,
		 (SELECT count(*) FROM inv WHERE service_id IS NOT NULL AND contract_id IS NULL) AS without_contract,
		 (SELECT count(*) FROM expectations WHERE status='faltante') AS missing`, from, to)
	if err != nil {
		serverErr(w, err)
		return
	}
	attention, _ := db.Maps(ctx, a.DB, `SELECT i.id, i.supplier_name, i.service_name, i.amount, i.due_on, i.status,
		(SELECT checks FROM audits WHERE invoice_id=i.id ORDER BY id DESC LIMIT 1) AS checks
		FROM invoices_v i WHERE i.status IN ('REVIEW','BLOCKED') ORDER BY i.due_on NULLS LAST LIMIT 50`)
	missing, _ := db.Maps(ctx, a.DB, `SELECT * FROM expectations_v WHERE status='faltante' ORDER BY expected_due LIMIT 50`)
	writeJSON(w, 200, map[string]any{"from": from, "to": to, "kpis": k, "attention": attention, "missing": missing})
}
