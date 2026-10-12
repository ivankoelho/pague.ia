package app

import (
	"context"
	"time"

	"github.com/ivankoelho/pague.ia/internal/br"
	"github.com/ivankoelho/pague.ia/internal/workflow"
)

// SeedDemo cria dados FICTÍCIOS (marcados com [DEMO] e is_demo=true) para
// demonstrar os cenários: aprovada, divergente, bloqueada, pendente, vencida.
// Só roda com SEED_DEMO=true e banco sem fornecedores.
func (a *App) SeedDemo(ctx context.Context) error {
	var n int
	a.DB.QueryRowContext(ctx, `SELECT count(*) FROM suppliers`).Scan(&n)
	if n > 0 {
		return nil
	}
	today := a.today()
	cur := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC)
	ymd := func(t time.Time) string { return t.Format("2006-01-02") }
	var err error
	id := func(q string, args ...any) int64 {
		var v int64
		if err == nil {
			err = a.DB.QueryRowContext(ctx, q+" RETURNING id", args...).Scan(&v)
		}
		return v
	}

	matriz := id(`INSERT INTO units(name,kind) VALUES('[DEMO] CORPAT','empresa')`)
	sede := id(`INSERT INTO units(name,kind,parent_id) VALUES('[DEMO] Sede','unidade',$1)`, matriz)
	loja := id(`INSERT INTO units(name,kind,parent_id) VALUES('[DEMO] Loja 01','unidade',$1)`, matriz)

	sup := func(name, legal, base string) int64 {
		return id(`INSERT INTO suppliers(name,legal_name,tax_id,email,is_demo,notes) VALUES($1,$2,$3,'faturas@demo.local',true,'Dados de demonstração')`,
			"[DEMO] "+name, legal+" (DEMO)", br.WithCNPJDV(base))
	}
	vivo := sup("Vivo", "Telefônica Brasil S.A.", "112223330001")
	ms := sup("Microsoft", "Microsoft do Brasil Ltda.", "234567890001")
	aws := sup("AWS", "Amazon AWS Serviços Brasil Ltda.", "345678900001")
	gen := sup("Fornecedor Genérico", "Manutenção Genérica Ltda.", "456789010001")

	ctr := func(num string, s int64, start, end time.Time, amount float64) int64 {
		return id(`INSERT INTO contracts(number,supplier_id,starts_on,ends_on,amount,adjustment_index,adjustment_pct,owner,is_demo)
			VALUES($1,$2,$3,$4,$5,'IPCA',4.5,'Ivan',true)`, num, s, ymd(start), ymd(end), amount)
	}
	cVivo := ctr("DEMO-VIVO-2025", vivo, cur.AddDate(-1, 0, 0), cur.AddDate(1, 3, 0), 1500)
	cMS := ctr("DEMO-MS-2024", ms, cur.AddDate(-2, 0, 0), today.AddDate(0, 0, 60), 2890)
	cGen := ctr("DEMO-GEN-2023", gen, cur.AddDate(-3, 0, 0), cur.AddDate(0, -3, 0), 800)

	svc := func(s, c, unit int64, name, cat string, amount, tol float64, due int) int64 {
		var cv any
		if c != 0 {
			cv = c
		}
		return id(`INSERT INTO services(supplier_id,contract_id,unit_id,name,category,owner,cost_center,expected_amount,tolerance_pct,due_day,starts_on,criticality,is_demo)
			VALUES($1,$2,$3,$4,$5,'Ivan','TI',$6,$7,$8,$9,'alta',true)`, s, cv, unit, "[DEMO] "+name, cat, amount, tol, due, ymd(cur.AddDate(-12, 0, 0)))
	}
	sSIP := svc(vivo, cVivo, sede, "Tronco SIP", "Telefonia", 1500, 5, 15)
	sNet := svc(vivo, cVivo, loja, "Internet Loja 01", "Internet", 399.90, 5, 10)
	sM365 := svc(ms, cMS, sede, "Microsoft 365", "Software/SaaS", 2890, 5, 5)
	sAWS := svc(aws, 0, sede, "Cloud AWS", "Cloud", 1200, 15, 2)
	sGen := svc(gen, cGen, loja, "Manutenção predial", "Manutenção", 800, 5, 20)
	if err != nil {
		return err
	}
	if err := a.dailyScan(ctx); err != nil {
		return err
	}

	// fatura auditada "como se" hoje fosse at: os eventos ficam coerentes com o workflow
	invoice := func(at time.Time, s, sp int64, comp time.Time, due time.Time, amount float64, doc string, pix bool) (int64, error) {
		a.DB.ExecContext(ctx, `INSERT INTO expectations(service_id,competence,expected_amount,expected_due)
			SELECT id, $2, expected_amount, $3 FROM services WHERE id=$1 ON CONFLICT DO NOTHING`, s, ymd(comp), ymd(due))
		var tax string
		a.DB.QueryRowContext(ctx, `SELECT tax_id FROM suppliers WHERE id=$1`, sp).Scan(&tax)
		var p any
		if pix {
			p = "00020126580014BR.GOV.BCB.PIX0136demo-pix-ficticio-nao-pagar5204000053039865802BR"
		}
		var iid int64
		if err := a.DB.QueryRowContext(ctx, `INSERT INTO invoices(supplier_id,service_id,competence,due_on,amount,doc_number,tax_id,pix,source,is_demo,received_at,notes)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,'demo',true,$9,'[DEMO] fatura fictícia') RETURNING id`,
			sp, s, ymd(comp), ymd(due), amount, doc, tax, p, at).Scan(&iid); err != nil {
			return 0, err
		}
		a.DB.ExecContext(ctx, `INSERT INTO invoice_events(invoice_id,to_status,actor,note,at) VALUES($1,'RECEIVED','seed','dados de demonstração',$2)`, iid, at)
		now := a.Now
		a.Now = func() time.Time { return at }
		defer func() { a.Now = now }()
		return iid, a.auditInvoice(ctx, iid, "sistema")
	}
	steps := []func() error{
		// histórico pago do SIP (alimenta gráfico e média)
		func() error {
			for m := 4; m >= 2; m-- {
				comp := cur.AddDate(0, -m, 0)
				due := comp.AddDate(0, 1, 14)
				iid, err := invoice(due.AddDate(0, 0, -10), sSIP, vivo, comp, due, 1480+float64(4-m)*10, "DEMO-SIP-"+comp.Format("200601"), true)
				if err != nil {
					return err
				}
				if err := a.transition(ctx, iid, workflow.Paid, "seed", "pagamento fictício"); err != nil {
					return err
				}
			}
			return nil
		},
		// aprovada → encaminhada ao Contas a Pagar pela automação
		func() error {
			_, err := invoice(today, sSIP, vivo, cur.AddDate(0, -1, 0), today.AddDate(0, 0, 9), 1497.80, "DEMO-SIP-"+cur.AddDate(0, -1, 0).Format("200601"), true)
			return err
		},
		// divergente: aumento de ~19% bloqueia
		func() error {
			_, err := invoice(today, sM365, ms, cur.AddDate(0, -1, 0), today.AddDate(0, 0, 5), 3450, "DEMO-M365-01", true)
			return err
		},
		// bloqueada: contrato expirado
		func() error {
			_, err := invoice(today, sGen, gen, cur.AddDate(0, -1, 0), today.AddDate(0, 0, 12), 800, "DEMO-GEN-01", true)
			return err
		},
		// revisão: sem dados de pagamento
		func() error {
			_, err := invoice(today, sAWS, aws, cur.AddDate(0, -1, 0), today.AddDate(0, 0, 3), 1310.55, "DEMO-AWS-01", false)
			return err
		},
		// vencida: aprovada há 10 dias, não paga
		func() error {
			_, err := invoice(today.AddDate(0, 0, -10), sNet, vivo, cur.AddDate(0, -1, 0), today.AddDate(0, 0, -3), 399.90, "DEMO-NET-01", true)
			return err
		},
		// pendente: recebida, ainda não processada
		func() error {
			_, err := a.DB.ExecContext(ctx, `INSERT INTO invoices(supplier_id,amount,source,is_demo,notes) VALUES($1,215.00,'demo',true,'[DEMO] aguardando processamento')`, aws)
			return err
		},
	}
	for _, s := range steps {
		if err := s(); err != nil {
			return err
		}
	}
	a.Log.Info("dados de demonstração criados")
	return a.dailyScan(ctx)
}
