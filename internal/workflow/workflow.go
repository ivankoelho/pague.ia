// Package workflow define a máquina de estados da fatura. Qualquer mudança
// de status passa por Allowed; a aplicação grava cada transição como evento.
package workflow

const (
	Received   = "RECEIVED"
	Processing = "PROCESSING"
	Extracted  = "EXTRACTED"
	Identified = "IDENTIFIED"
	Auditing   = "AUDITING"
	Review     = "REVIEW"
	Approved   = "APPROVED"
	Blocked    = "BLOCKED"
	Rejected   = "REJECTED"
	ForPayment = "FOR_PAYMENT"
	Paid       = "PAID"
)

var next = map[string][]string{
	Received:   {Processing, Auditing},
	Processing: {Extracted, Review},
	Extracted:  {Identified, Review},
	Identified: {Auditing},
	Auditing:   {Approved, Review, Blocked, Rejected},
	Review:     {Approved, Rejected, Blocked, Auditing, Processing},
	Blocked:    {Review, Rejected, Auditing, Processing},
	Approved:   {ForPayment, Review},
	ForPayment: {Paid, Review},
	Rejected:   {Review},
	Paid:       {},
}

func Allowed(from, to string) bool {
	for _, s := range next[from] {
		if s == to {
			return true
		}
	}
	return false
}

// Manual indica transições que um usuário pode disparar pela API, e a permissão exigida.
var Manual = map[string]string{
	Approved:   "audits.execute",
	Rejected:   "audits.execute",
	Review:     "audits.execute",
	Auditing:   "audits.execute",
	Processing: "invoices.write",
	ForPayment: "payments.write",
	Paid:       "payments.write",
}

func Final(s string) bool { return s == Paid || s == Rejected }
