package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/ivankoelho/pague.ia/internal/db"
	"github.com/ivankoelho/pague.ia/internal/ingest"
	"github.com/ivankoelho/pague.ia/internal/notify"
)

// Fila e agendador vivem no Postgres (FOR UPDATE SKIP LOCKED): sem Redis, sem cron,
// e funcionam com mais de uma instância do app.

func (a *App) enqueue(ctx context.Context, q db.Queryer, kind string, payload any) error {
	b, _ := json.Marshal(payload)
	_, err := q.ExecContext(ctx, `INSERT INTO jobs(kind,payload) VALUES($1,$2)`, kind, string(b))
	return err
}

func (a *App) handlers() map[string]func(context.Context, json.RawMessage) error {
	return map[string]func(context.Context, json.RawMessage) error{
		"process_invoice": func(ctx context.Context, p json.RawMessage) error {
			var in struct{ ID int64 }
			json.Unmarshal(p, &in)
			return a.processInvoice(ctx, in.ID)
		},
		"daily_scan":         func(ctx context.Context, _ json.RawMessage) error { return a.dailyScan(ctx) },
		"send_notifications": func(ctx context.Context, _ json.RawMessage) error { return a.sendNotifications(ctx) },
		"imap_poll":          func(ctx context.Context, _ json.RawMessage) error { return a.pollIMAP(ctx) },
	}
}

// Run inicia workers e agendador até ctx terminar.
func (a *App) Run(ctx context.Context) {
	a.DB.ExecContext(ctx, `UPDATE jobs SET status='pending' WHERE status='running'`) // jobs órfãos de um restart
	for i := 0; i < max(a.Cfg.Workers, 1); i++ {
		go a.worker(ctx)
	}
	go func() {
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for {
			a.schedule(ctx)
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
}

func (a *App) schedule(ctx context.Context) {
	for {
		var kind string
		err := a.DB.QueryRowContext(ctx, `UPDATE schedules SET next_run_at = now() + every
			WHERE kind = (SELECT kind FROM schedules WHERE enabled AND next_run_at <= now() FOR UPDATE SKIP LOCKED LIMIT 1)
			RETURNING kind`).Scan(&kind)
		if err != nil {
			return
		}
		a.enqueue(ctx, a.DB, kind, map[string]any{})
	}
}

func (a *App) worker(ctx context.Context) {
	for ctx.Err() == nil {
		if !a.RunOne(ctx) {
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
			}
		}
	}
}

// RunOne executa o próximo job pronto. Devolve false se a fila estava vazia.
func (a *App) RunOne(ctx context.Context) bool {
	var id int64
	var kind string
	var payload []byte
	var attempts, maxAttempts int
	err := a.DB.QueryRowContext(ctx, `UPDATE jobs SET status='running', started_at=now(), attempts=attempts+1
		WHERE id = (SELECT id FROM jobs WHERE status='pending' AND run_at <= now() ORDER BY run_at, id FOR UPDATE SKIP LOCKED LIMIT 1)
		RETURNING id, kind, payload, attempts, max_attempts`).Scan(&id, &kind, &payload, &attempts, &maxAttempts)
	if err != nil {
		return false
	}
	start := time.Now()
	h := a.handlers()[kind]
	if h == nil {
		err = fmt.Errorf("tipo de job desconhecido: %s", kind)
	} else {
		jctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		err = func() (err error) {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("panic: %v", r)
				}
			}()
			return h(jctx, payload)
		}()
		cancel()
	}
	ms := time.Since(start).Milliseconds()
	if err == nil {
		a.DB.ExecContext(ctx, `UPDATE jobs SET status='done', finished_at=now(), duration_ms=$2, last_error=NULL WHERE id=$1`, id, ms)
		return true
	}
	a.Log.Error("job", "id", id, "kind", kind, "attempt", attempts, "err", err)
	if attempts >= maxAttempts {
		a.DB.ExecContext(ctx, `UPDATE jobs SET status='failed', finished_at=now(), duration_ms=$2, last_error=$3 WHERE id=$1`, id, ms, err.Error())
		if kind == "process_invoice" { // não deixa a fatura presa em processamento
			var in struct{ ID int64 }
			json.Unmarshal(payload, &in)
			a.transition(ctx, in.ID, "REVIEW", "sistema", "processamento falhou: "+err.Error())
		}
		return true
	}
	backoff := time.Duration(math.Pow(2, float64(attempts))) * 30 * time.Second
	a.DB.ExecContext(ctx, `UPDATE jobs SET status='pending', run_at=now()+$2::interval, duration_ms=$3, last_error=$4 WHERE id=$1`,
		id, fmt.Sprintf("%d seconds", int(backoff.Seconds())), ms, err.Error())
	return true
}

// --- varredura diária: expectativas, faltantes, vencimentos, contratos ---

var periodMonths = map[string]int{"mensal": 1, "bimestral": 2, "trimestral": 3, "semestral": 6, "anual": 12}

func (a *App) dailyScan(ctx context.Context) error {
	today := a.today()
	cur := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC)
	svcs, err := db.Maps(ctx, a.DB, `SELECT id, periodicity, expected_amount, due_day, starts_on, created_at FROM services WHERE status='ativo'`)
	if err != nil {
		return err
	}
	// 1. expectativas da competência anterior, atual e seguinte
	for _, s := range svcs {
		start := cur.AddDate(0, -12, 0)
		if v, ok := s["starts_on"].(string); ok {
			if t, err := time.Parse("2006-01-02", v); err == nil {
				start = time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
			}
		}
		step := max(periodMonths[fmt.Sprint(s["periodicity"])], 1)
		for _, comp := range []time.Time{cur.AddDate(0, -1, 0), cur, cur.AddDate(0, 1, 0)} {
			months := (comp.Year()-start.Year())*12 + int(comp.Month()-start.Month())
			if months < 0 || months%step != 0 {
				continue
			}
			var due any
			if d := toInt(s["due_day"]); d > 0 {
				next := comp.AddDate(0, 1, 0)
				last := next.AddDate(0, 1, -1).Day()
				due = time.Date(next.Year(), next.Month(), min(int(d), last), 0, 0, 0, 0, time.UTC).Format("2006-01-02")
			}
			if _, err := a.DB.ExecContext(ctx, `INSERT INTO expectations(service_id,competence,expected_amount,expected_due)
				VALUES($1,$2,$3,$4) ON CONFLICT (service_id,competence) DO NOTHING`, s["id"], comp.Format("2006-01-02"), s["expected_amount"], due); err != nil {
				return err
			}
		}
	}
	// 2. faltantes: expectativa sem fatura a 5 dias (ou menos) do vencimento esperado
	missing, err := db.Maps(ctx, a.DB, `UPDATE expectations SET status='faltante', updated_at=now()
		WHERE status='pendente' AND invoice_id IS NULL AND expected_due IS NOT NULL AND expected_due <= $1::date + 5
		RETURNING id`, today)
	if err != nil {
		return err
	}
	for _, m := range missing {
		e, _ := db.One(ctx, a.DB, `SELECT * FROM expectations_v WHERE id=$1`, m["id"])
		if e != nil {
			e = withFormats(e)
			e["_entity"] = "expectation"
			a.fire(ctx, "expectation.missing", e, fmt.Sprintf("exp:%v", e["id"]))
		}
	}
	// 3. vencimentos próximos (a regra decide em quais dias avisar)
	due, err := db.Maps(ctx, a.DB, `SELECT * FROM invoices_v WHERE status NOT IN ('PAID','REJECTED')
		AND due_on BETWEEN $1::date AND $1::date + 30`, today)
	if err != nil {
		return err
	}
	for _, inv := range due {
		inv = withFormats(inv)
		inv["days_left"] = inv["days_to_due"]
		a.fire(ctx, "invoice.due_soon", inv, fmt.Sprintf("due:%v:%v", inv["id"], inv["days_left"]))
	}
	// 4. contratos vencendo (intervalos 90/60/30/15/0 configurados na regra)
	cts, err := db.Maps(ctx, a.DB, `SELECT * FROM contracts_v WHERE status='vigente' AND NOT auto_renew
		AND ends_on BETWEEN $1::date - 1 AND $1::date + 120`, today)
	if err != nil {
		return err
	}
	for _, ct := range cts {
		ct = withFormats(ct)
		ct["days_left"], ct["_entity"] = ct["days_to_end"], "contract"
		a.fire(ctx, "contract.expiring", ct, fmt.Sprintf("contract:%v:%v", ct["id"], ct["days_left"]))
	}
	return nil
}

// --- envio de notificações (outbox com retry) ---

func (a *App) sendNotifications(ctx context.Context) error {
	var locked bool
	conn, err := a.DB.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(7427)`).Scan(&locked)
	if !locked {
		return nil // outro worker já está enviando
	}
	defer conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(7427)`)
	pend, err := db.Maps(ctx, conn, `SELECT * FROM notifications WHERE status='pending' ORDER BY id LIMIT 100`)
	if err != nil {
		return err
	}
	for _, n := range pend {
		m := notify.Message{ID: toInt(n["id"]), Channel: fmt.Sprint(n["channel"]), Target: fmt.Sprint(n["target"]),
			Subject: fmt.Sprint(n["subject"]), Body: fmt.Sprint(n["body"]), RefEntity: fmt.Sprint(n["ref_entity"]), RefID: toInt(n["ref_id"])}
		p := a.Notifiers[m.Channel]
		if p == nil {
			err = fmt.Errorf("canal sem provider: %s", m.Channel)
		} else {
			err = p.Send(ctx, m)
		}
		switch {
		case err == nil:
			conn.ExecContext(ctx, `UPDATE notifications SET status='sent', sent_at=now(), attempts=attempts+1 WHERE id=$1`, m.ID)
		case errors.Is(err, notify.ErrMock):
			conn.ExecContext(ctx, `UPDATE notifications SET status='mock', sent_at=now(), attempts=attempts+1, last_error=$2 WHERE id=$1`, m.ID, err.Error())
		default:
			a.Log.Warn("notificação", "id", m.ID, "canal", m.Channel, "err", err)
			conn.ExecContext(ctx, `UPDATE notifications SET attempts=attempts+1, last_error=$2,
				status = CASE WHEN attempts+1 >= 5 THEN 'failed' ELSE 'pending' END WHERE id=$1`, m.ID, err.Error())
		}
	}
	return nil
}

// --- ingestão de e-mail ---

func (a *App) pollIMAP(ctx context.Context) error {
	if a.Cfg.IMAPAddr == "" {
		return nil
	}
	c, err := ingest.DialTLS(a.Cfg.IMAPAddr)
	if err != nil {
		return err
	}
	defer c.Logout()
	if err := c.Login(a.Cfg.IMAPUser, a.Cfg.IMAPPass); err != nil {
		return err
	}
	if err := c.Select(a.Cfg.IMAPMailbox); err != nil {
		return err
	}
	uids, err := c.SearchUnseen()
	if err != nil {
		return err
	}
	for _, uid := range uids {
		raw, err := c.FetchRaw(uid)
		if err != nil {
			return err
		}
		m, err := ingest.Parse(raw)
		if err != nil {
			a.Log.Warn("imap: mensagem ilegível", "uid", uid, "err", err)
			c.MarkSeen(uid)
			continue
		}
		note := strings.TrimSpace("E-mail: " + m.Subject + " (" + m.From + ")")
		for _, att := range m.Attachments {
			if _, err := a.ingestFile(ctx, att.Filename, att.Data, "email", note, 0, User{Name: "e-mail"}); err != nil {
				a.Log.Warn("imap: anexo recusado", "arquivo", att.Filename, "err", err)
			}
		}
		if err := c.MarkSeen(uid); err != nil {
			return err
		}
	}
	return nil
}
