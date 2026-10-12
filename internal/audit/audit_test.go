package audit

import (
	"testing"
	"time"
)

func d(s string) *time.Time {
	t, _ := time.Parse("2006-01-02", s)
	return &t
}

func base() Facts {
	return Facts{
		Invoice:     Invoice{ID: 1, TaxID: "11.222.333/0001-81", Amount: 1497.80, Due: d("2026-11-15"), Competence: d("2026-10-01"), Pix: "chave"},
		Supplier:    &Supplier{Name: "Vivo", TaxID: "11222333000181", Status: "ativo"},
		Service:     &Service{Name: "SIP", Status: "ativo", ExpectedAmount: 1500, TolerancePct: 5},
		Contract:    &Contract{Number: "C-1", Status: "vigente", StartsOn: *d("2025-01-01"), EndsOn: d("2027-01-01")},
		Expectation: &Expectation{ExpectedAmount: 1500, ExpectedDue: d("2026-11-15")},
		Today:       *d("2026-10-11"),
	}
}

func check(t *testing.T, r Result, code string) Check {
	t.Helper()
	for _, c := range r.Checks {
		if c.Code == code {
			return c
		}
	}
	t.Fatalf("check %s ausente", code)
	return Check{}
}

func TestAudit(t *testing.T) {
	cases := []struct {
		name   string
		mut    func(*Facts)
		status string
		code   string
	}{
		{"normal", func(*Facts) {}, Approved, ""},
		{"duplicada", func(f *Facts) { f.Duplicates = []int64{9} }, Blocked, "duplicate"},
		{"mesmo arquivo", func(f *Facts) { f.SameFileIDs = []int64{9} }, Blocked, "duplicate"},
		{"valor divergente", func(f *Facts) { f.Invoice.Amount = 1800 }, Blocked, "amount"},
		{"fornecedor desconhecido", func(f *Facts) { f.Supplier = nil }, Blocked, "supplier"},
		{"cnpj diferente", func(f *Facts) { f.Invoice.TaxID = "00.000.000/0001-91" }, Blocked, "tax_id"},
		{"contrato expirado", func(f *Facts) { f.Contract.EndsOn = d("2026-06-30") }, Blocked, "contract"},
		{"serviço cancelado", func(f *Facts) { f.Service.Status = "cancelado" }, Blocked, "service"},
		{"competência inesperada", func(f *Facts) { f.Expectation = nil }, Review, "competence"},
		{"vencida", func(f *Facts) { f.Invoice.Due = d("2026-10-01") }, Review, "due"},
		{"sem pagamento", func(f *Facts) { f.Invoice.Pix = "" }, Review, "payment"},
		{"linha inválida", func(f *Facts) { f.Invoice.DigitableLine = "123" }, Blocked, "payment"},
		{"acima da alçada", func(f *Facts) { f.AutoApproveMax = 1000 }, Review, "approval_limit"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := base()
			tc.mut(&f)
			r := Run(f)
			if r.Status != tc.status {
				t.Fatalf("status %s, esperado %s: %s", r.Status, tc.status, Summary(r))
			}
			if tc.code != "" && check(t, r, tc.code).Outcome != Fail {
				t.Fatalf("check %s deveria falhar", tc.code)
			}
		})
	}
}

func TestHistoryWarnOnly(t *testing.T) {
	f := base()
	f.History = []float64{1000, 1000, 1000}
	r := Run(f)
	if r.Status != Approved || check(t, r, "history").Outcome != Warn {
		t.Fatalf("histórico deve só alertar: %+v", r)
	}
}
