// Package audit é o motor determinístico que valida uma fatura contra o
// cadastro (fornecedor, serviço, contrato, expectativa, histórico).
// A IA nunca decide aqui: ela só preenche campos; este pacote aprova ou bloqueia.
package audit

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/ivankoelho/pague.ia/internal/br"
)

const (
	Approved = "APPROVED"
	Review   = "REVIEW"
	Blocked  = "BLOCKED"
)

type Outcome string

const (
	OK   Outcome = "OK"
	Warn Outcome = "WARN" // informativo, não muda o resultado
	Fail Outcome = "FAIL"
)

type Check struct {
	Code     string         `json:"code"`
	Label    string         `json:"label"`
	Outcome  Outcome        `json:"outcome"`
	Severity string         `json:"severity,omitempty"` // REVIEW ou BLOCKED quando Fail
	Detail   string         `json:"detail"`
	Evidence map[string]any `json:"evidence,omitempty"`
}

type Invoice struct {
	ID            int64
	TaxID         string
	Amount        float64
	Due           *time.Time
	Competence    *time.Time
	DocNumber     string
	DigitableLine string
	Pix           string
}

type Supplier struct {
	Name, TaxID, Status string
}

type Service struct {
	Name, Status   string
	ExpectedAmount float64
	TolerancePct   float64
}

type Contract struct {
	Number, Status string
	StartsOn       time.Time
	EndsOn         *time.Time
	AutoRenew      bool
}

type Expectation struct {
	ExpectedAmount float64
	ExpectedDue    *time.Time
}

// Facts reúne tudo que o motor precisa; a camada de aplicação carrega do banco.
type Facts struct {
	Invoice        Invoice
	Supplier       *Supplier
	Service        *Service
	Contract       *Contract
	Expectation    *Expectation
	History        []float64 // valores das últimas faturas do serviço
	Duplicates     []int64   // outras faturas mesmo serviço+competência ou mesmo nº de documento
	SameFileIDs    []int64   // faturas com o mesmo hash de arquivo
	Today          time.Time
	AutoApproveMax float64 // 0 = sem limite
}

type Result struct {
	Status string  `json:"status"`
	Checks []Check `json:"checks"`
}

func Run(f Facts) Result {
	var cs []Check
	add := func(c Check) { cs = append(cs, c) }
	ok := func(code, label, detail string) { add(Check{Code: code, Label: label, Outcome: OK, Detail: detail}) }
	fail := func(code, label, sev, detail string, ev map[string]any) {
		add(Check{Code: code, Label: label, Outcome: Fail, Severity: sev, Detail: detail, Evidence: ev})
	}
	warn := func(code, label, detail string) { add(Check{Code: code, Label: label, Outcome: Warn, Detail: detail}) }
	inv := f.Invoice

	// Fornecedor e CNPJ
	switch {
	case f.Supplier == nil:
		fail("supplier", "Fornecedor", Blocked, "fornecedor não identificado no cadastro", map[string]any{"tax_id": inv.TaxID})
	case f.Supplier.Status != "ativo":
		fail("supplier", "Fornecedor", Blocked, "fornecedor "+f.Supplier.Status, nil)
	default:
		ok("supplier", "Fornecedor", f.Supplier.Name)
	}
	switch {
	case f.Supplier == nil:
	case inv.TaxID == "":
		fail("tax_id", "CNPJ", Review, "CNPJ não encontrado no documento", nil)
	case br.Digits(inv.TaxID) != br.Digits(f.Supplier.TaxID):
		fail("tax_id", "CNPJ", Blocked, "CNPJ do documento difere do cadastro",
			map[string]any{"documento": inv.TaxID, "cadastro": f.Supplier.TaxID})
	default:
		ok("tax_id", "CNPJ", "confere com o cadastro")
	}

	// Serviço
	switch {
	case f.Service == nil:
		fail("service", "Serviço", Review, "fatura sem serviço associado", nil)
	case f.Service.Status != "ativo":
		fail("service", "Serviço", Blocked, fmt.Sprintf("serviço %q está %s", f.Service.Name, f.Service.Status), nil)
	default:
		ok("service", "Serviço", f.Service.Name+" ativo")
	}

	// Contrato
	switch c := f.Contract; {
	case c == nil:
		warn("contract", "Contrato", "serviço sem contrato vinculado")
	case c.Status != "vigente":
		fail("contract", "Contrato", Blocked, "contrato "+c.Number+" está "+c.Status, nil)
	case c.EndsOn != nil && !c.AutoRenew && ref(inv, f.Today).After(*c.EndsOn):
		fail("contract", "Contrato", Blocked, "contrato "+c.Number+" expirou em "+c.EndsOn.Format("02/01/2006"), nil)
	default:
		ok("contract", "Contrato", c.Number+" vigente")
	}

	// Competência
	switch {
	case inv.Competence == nil:
		fail("competence", "Competência", Review, "competência não identificada", nil)
	case f.Expectation == nil:
		fail("competence", "Competência", Review, "competência "+inv.Competence.Format("01/2006")+" não era esperada para o serviço", nil)
	default:
		ok("competence", "Competência", inv.Competence.Format("01/2006")+" esperada")
	}

	// Duplicidade
	switch {
	case len(f.SameFileIDs) > 0:
		fail("duplicate", "Duplicidade", Blocked, "mesmo arquivo já recebido", map[string]any{"faturas": f.SameFileIDs})
	case len(f.Duplicates) > 0:
		fail("duplicate", "Duplicidade", Blocked, "já existe fatura para o mesmo serviço/competência ou nº de documento", map[string]any{"faturas": f.Duplicates})
	default:
		ok("duplicate", "Duplicidade", "nenhuma duplicidade")
	}

	// Valor: contra o esperado (bloqueia) e contra o histórico (informa)
	expected := 0.0
	if f.Expectation != nil && f.Expectation.ExpectedAmount > 0 {
		expected = f.Expectation.ExpectedAmount
	} else if f.Service != nil {
		expected = f.Service.ExpectedAmount
	}
	tol := 5.0
	if f.Service != nil && f.Service.TolerancePct > 0 {
		tol = f.Service.TolerancePct
	}
	switch {
	case inv.Amount <= 0:
		fail("amount", "Valor", Review, "valor não identificado", nil)
	case expected <= 0:
		warn("amount", "Valor", "serviço sem valor esperado cadastrado")
	default:
		v := Variance(inv.Amount, expected)
		ev := map[string]any{"valor": inv.Amount, "esperado": expected, "variacao_pct": round2(v), "tolerancia_pct": tol}
		if math.Abs(v) > tol {
			fail("amount", "Valor", Blocked, fmt.Sprintf("variação de %.2f%% acima da tolerância de %.2f%%", v, tol), ev)
		} else {
			add(Check{Code: "amount", Label: "Valor", Outcome: OK, Detail: fmt.Sprintf("dentro da tolerância (%.2f%%)", v), Evidence: ev})
		}
	}
	if avg := mean(f.History); avg > 0 && inv.Amount > 0 {
		if v := Variance(inv.Amount, avg); math.Abs(v) > tol {
			add(Check{Code: "history", Label: "Histórico", Outcome: Warn,
				Detail:   fmt.Sprintf("%.2f%% em relação à média das últimas %d faturas", v, len(f.History)),
				Evidence: map[string]any{"media": round2(avg), "historico": f.History}})
		}
	}

	// Vencimento
	switch {
	case inv.Due == nil:
		fail("due", "Vencimento", Review, "vencimento não identificado", nil)
	case inv.Due.Before(day(f.Today)):
		fail("due", "Vencimento", Review, "fatura vencida em "+inv.Due.Format("02/01/2006"), nil)
	case inv.Due.Sub(day(f.Today)) <= 48*time.Hour:
		warn("due", "Vencimento", "vence em até 2 dias")
	default:
		ok("due", "Vencimento", inv.Due.Format("02/01/2006"))
	}

	// Dados de pagamento: a linha digitável confere valor e vencimento independentemente da IA
	switch {
	case inv.DigitableLine != "":
		b, err := br.ParseLine(inv.DigitableLine)
		switch {
		case err != nil:
			fail("payment", "Dados de pagamento", Blocked, "linha digitável com dígito verificador inválido", nil)
		case b.Amount > 0 && math.Abs(b.Amount-inv.Amount) > 0.009:
			fail("payment", "Dados de pagamento", Blocked, fmt.Sprintf("valor do boleto (%.2f) difere da fatura (%.2f)", b.Amount, inv.Amount), nil)
		case b.Due != nil && inv.Due != nil && !b.Due.Equal(day(*inv.Due)):
			fail("payment", "Dados de pagamento", Review, "vencimento do boleto ("+b.Due.Format("02/01/2006")+") difere da fatura", nil)
		default:
			ok("payment", "Dados de pagamento", "boleto "+b.Kind+" válido")
		}
	case inv.Pix != "":
		ok("payment", "Dados de pagamento", "Pix informado")
	default:
		fail("payment", "Dados de pagamento", Review, "sem linha digitável nem Pix", nil)
	}

	if f.AutoApproveMax > 0 && inv.Amount > f.AutoApproveMax {
		fail("approval_limit", "Alçada", Review, fmt.Sprintf("valor acima do limite de aprovação automática (%.2f)", f.AutoApproveMax), nil)
	}

	status := Approved
	for _, c := range cs {
		if c.Outcome != Fail {
			continue
		}
		if c.Severity == Blocked {
			status = Blocked
			break
		}
		status = Review
	}
	return Result{Status: status, Checks: cs}
}

// Variance em % de got sobre want.
func Variance(got, want float64) float64 { return (got - want) / want * 100 }

func ref(inv Invoice, today time.Time) time.Time {
	if inv.Competence != nil {
		return *inv.Competence
	}
	return today
}

func day(t time.Time) time.Time { return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC) }

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := 0.0
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

func round2(f float64) float64 { return math.Round(f*100) / 100 }

// Summary gera um texto curto com as falhas, usado em notificações e na UI.
func Summary(r Result) string {
	var parts []string
	for _, c := range r.Checks {
		if c.Outcome == Fail {
			parts = append(parts, c.Label+": "+c.Detail)
		}
	}
	if len(parts) == 0 {
		return "todas as verificações OK"
	}
	return strings.Join(parts, "; ")
}
