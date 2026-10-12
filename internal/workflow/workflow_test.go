package workflow

import "testing"

func TestAllowed(t *testing.T) {
	ok := [][2]string{{Received, Processing}, {Auditing, Approved}, {Approved, ForPayment}, {ForPayment, Paid}, {Review, Approved}, {Blocked, Review}}
	no := [][2]string{{Blocked, Approved}, {Paid, Review}, {Received, Approved}, {Rejected, Paid}}
	for _, p := range ok {
		if !Allowed(p[0], p[1]) {
			t.Errorf("%s→%s deveria ser permitido", p[0], p[1])
		}
	}
	for _, p := range no {
		if Allowed(p[0], p[1]) {
			t.Errorf("%s→%s deveria ser negado", p[0], p[1])
		}
	}
}
