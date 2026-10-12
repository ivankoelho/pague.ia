package ai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func refText(t *testing.T) string {
	b, err := os.ReadFile("testdata/fatura_vivo_demo.txt")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func want(t *testing.T, ex Extraction) {
	t.Helper()
	if ex.TaxID != "11222333000181" || ex.Amount != 1497.80 || ex.DueOn != "2026-11-15" ||
		ex.Competence != "2026-10" || len(ex.DigitableLine) != 47 {
		t.Fatalf("extração: %+v", ex)
	}
}

func TestRulesOnReferenceDocument(t *testing.T) {
	ex := Rules(refText(t))
	want(t, ex)
	if ex.DocNumber != "DEMO-202610-001" {
		t.Fatalf("doc number %q", ex.DocNumber)
	}
}

// fakeGroq responde no formato da API OpenAI-compatível.
func fakeGroq(t *testing.T, content string, status int) *OpenAICompat {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if r.Header.Get("Authorization") != "Bearer k" || !strings.Contains(string(b), `"json_object"`) {
			t.Errorf("requisição inesperada: %s", b)
		}
		w.WriteHeader(status)
		w.Write([]byte(`{"choices":[{"message":{"content":` + quote(content) + `}}]}`))
	}))
	t.Cleanup(srv.Close)
	g := NewGroq("k", "")
	g.BaseURL = srv.URL
	return g
}

func quote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	return `"` + r.Replace(s) + `"`
}

func TestExtractWithAI(t *testing.T) {
	// IA erra o valor e o vencimento: a linha digitável válida prevalece.
	g := fakeGroq(t, `{"supplier_name":"Telefônica","tax_id":"11.222.333/0001-81","competence":"2026-10","due_on":"2026-11-16","amount":1500,"digitable_line":"00190000090281234567289012345677116310000149780","service_hint":"SIP","confidence":0.9}`, 200)
	ex, run := Extract(context.Background(), g, refText(t))
	if run == nil || !run.OK || run.PromptVersion != PromptVersion {
		t.Fatalf("run: %+v", run)
	}
	want(t, ex)
	if ex.SupplierName != "Telefônica" || ex.ServiceHint != "SIP" {
		t.Fatalf("%+v", ex)
	}
}

func TestExtractFallsBackWhenAIFails(t *testing.T) {
	g := fakeGroq(t, "", 500)
	ex, run := Extract(context.Background(), g, refText(t))
	if run == nil || run.OK || run.Error == "" {
		t.Fatalf("falha não registrada: %+v", run)
	}
	want(t, ex)
}
