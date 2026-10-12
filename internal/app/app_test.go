package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/ivankoelho/pague.ia/internal/db"
)

// Testes de integração: exigem TEST_DATABASE_URL (o banco é recriado a cada teste).
// Ex.: TEST_DATABASE_URL=postgres://postgres@localhost:55432/pague_test?sslmode=disable

var fixedNow = time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)

func newApp(t *testing.T) *App {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL não definido")
	}
	ctx := context.Background()
	d, err := sql.Open("postgres", url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := d.Exec(`DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, d); err != nil {
		t.Fatal(err)
	}
	a := New(d, Config{DataDir: t.TempDir(), DryRun: true, MaxUploadMB: 5, AdminEmail: "admin@test", AdminPassword: "senha-de-teste-123"})
	a.Now = func() time.Time { return fixedNow }
	if err := a.EnsureAdmin(ctx); err != nil {
		t.Fatal(err)
	}
	return a
}

func exec(t *testing.T, a *App, q string, args ...any) int64 {
	t.Helper()
	var id int64
	if err := a.DB.QueryRow(q+" RETURNING id", args...).Scan(&id); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return id
}

type world struct {
	supplier, service, contract int64
}

// cadastro base: Vivo SIP R$1500, vence dia 15, contrato vigente, expectativa de 09/2026.
func setup(t *testing.T, a *App) world {
	var w world
	w.supplier = exec(t, a, `INSERT INTO suppliers(name,tax_id) VALUES('Vivo','11222333000181')`)
	w.contract = exec(t, a, `INSERT INTO contracts(number,supplier_id,starts_on,ends_on) VALUES('C-1',$1,'2025-01-01','2027-12-31')`, w.supplier)
	w.service = exec(t, a, `INSERT INTO services(supplier_id,contract_id,name,expected_amount,tolerance_pct,due_day) VALUES($1,$2,'SIP',1500,5,15)`, w.supplier, w.contract)
	exec(t, a, `INSERT INTO expectations(service_id,competence,expected_amount,expected_due) VALUES($1,'2026-09-01',1500,'2026-10-15')`, w.service)
	return w
}

func newInvoice(t *testing.T, a *App, w world, amount float64, due, comp, doc string) int64 {
	return exec(t, a, `INSERT INTO invoices(supplier_id,service_id,amount,due_on,competence,doc_number,tax_id,pix)
		VALUES($1,$2,$3,$4,$5,$6,'11222333000181','pix-demo')`, w.supplier, w.service, amount, due, comp, doc)
}

func status(t *testing.T, a *App, id int64) string {
	var s string
	a.DB.QueryRow(`SELECT status FROM invoices WHERE id=$1`, id).Scan(&s)
	return s
}

func count(t *testing.T, a *App, q string, args ...any) int {
	var n int
	if err := a.DB.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestScenarios(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name string
		mut  func(t *testing.T, a *App, w world) int64
		want string
	}{
		{"normal: aprovada e encaminhada ao financeiro", func(t *testing.T, a *App, w world) int64 {
			return newInvoice(t, a, w, 1497.80, "2026-10-15", "2026-09-01", "F-1")
		}, "FOR_PAYMENT"},
		{"duplicada", func(t *testing.T, a *App, w world) int64 {
			first := newInvoice(t, a, w, 1497.80, "2026-10-15", "2026-09-01", "F-1")
			if err := a.auditInvoice(ctx, first, "t"); err != nil {
				t.Fatal(err)
			}
			return newInvoice(t, a, w, 1497.80, "2026-10-15", "2026-09-01", "F-1-bis")
		}, "BLOCKED"},
		{"valor divergente", func(t *testing.T, a *App, w world) int64 {
			return newInvoice(t, a, w, 1800, "2026-10-15", "2026-09-01", "F-2")
		}, "BLOCKED"},
		{"fornecedor desconhecido", func(t *testing.T, a *App, w world) int64 {
			return exec(t, a, `INSERT INTO invoices(amount,due_on,competence,tax_id,pix) VALUES(100,'2026-10-20','2026-09-01','00000000000191','x')`)
		}, "BLOCKED"},
		{"contrato expirado", func(t *testing.T, a *App, w world) int64 {
			a.DB.Exec(`UPDATE contracts SET ends_on='2026-06-30' WHERE id=$1`, w.contract)
			return newInvoice(t, a, w, 1500, "2026-10-15", "2026-09-01", "F-3")
		}, "BLOCKED"},
		{"vencimento passado vai para revisão", func(t *testing.T, a *App, w world) int64 {
			return newInvoice(t, a, w, 1500, "2026-10-01", "2026-09-01", "F-4")
		}, "REVIEW"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newApp(t)
			w := setup(t, a)
			id := tc.mut(t, a, w)
			if err := a.auditInvoice(ctx, id, "teste"); err != nil {
				t.Fatal(err)
			}
			if got := status(t, a, id); got != tc.want {
				var checks string
				a.DB.QueryRow(`SELECT checks FROM audits WHERE invoice_id=$1 ORDER BY id DESC LIMIT 1`, id).Scan(&checks)
				t.Fatalf("status %s, esperado %s; checks=%s", got, tc.want, checks)
			}
			if count(t, a, `SELECT count(*) FROM invoice_events WHERE invoice_id=$1`, id) < 2 {
				t.Fatal("eventos de workflow não registrados")
			}
		})
	}
}

func TestApprovedNotifiesFinanceViaWhatcMock(t *testing.T) {
	a := newApp(t)
	w := setup(t, a)
	ctx := context.Background()
	id := newInvoice(t, a, w, 1497.80, "2026-10-15", "2026-09-01", "F-1")
	if err := a.auditInvoice(ctx, id, "teste"); err != nil {
		t.Fatal(err)
	}
	var body, st string
	a.DB.QueryRow(`SELECT body, status FROM notifications WHERE channel='whatc' AND ref_id=$1`, id).Scan(&body, &st)
	for _, s := range []string{"Fatura validada para pagamento", "Competência: 09/2026", "Vencimento: 15/10/2026", "R$ 1.497,80", fmt.Sprintf("PG-%d", id)} {
		if !strings.Contains(body, s) {
			t.Fatalf("mensagem sem %q:\n%s", s, body)
		}
	}
	if err := a.sendNotifications(ctx); err != nil {
		t.Fatal(err)
	}
	a.DB.QueryRow(`SELECT status FROM notifications WHERE channel='whatc' AND ref_id=$1`, id).Scan(&st)
	if st != "mock" {
		t.Fatalf("dry-run deveria marcar mock, ficou %s", st)
	}
	if count(t, a, `SELECT count(*) FROM expectations WHERE invoice_id=$1 AND status='recebida'`, id) != 1 {
		t.Fatal("expectativa não vinculada")
	}
}

func TestDailyScanMissingAndDueSoon(t *testing.T) {
	a := newApp(t)
	w := setup(t, a)
	ctx := context.Background()
	// fatura aprovada vencendo em 7 dias → um aviso; varredura repetida não duplica
	id := newInvoice(t, a, w, 1500, "2026-10-18", "2026-09-01", "F-1")
	if err := a.auditInvoice(ctx, id, "t"); err != nil {
		t.Fatal(err)
	}
	// outro serviço sem fatura com vencimento esperado em 3 dias → faltante
	s2 := exec(t, a, `INSERT INTO services(supplier_id,name,expected_amount,due_day) VALUES($1,'Link',300,14)`, w.supplier)
	exec(t, a, `INSERT INTO expectations(service_id,competence,expected_amount,expected_due) VALUES($1,'2026-09-01',300,'2026-10-14')`, s2)
	// contrato vencendo em 30 dias
	exec(t, a, `INSERT INTO contracts(number,supplier_id,starts_on,ends_on) VALUES('C-30',$1,'2025-01-01','2026-11-10')`, w.supplier)
	for i := 0; i < 2; i++ {
		if err := a.dailyScan(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if n := count(t, a, `SELECT count(*) FROM notifications WHERE subject='Vencimento próximo' AND ref_id=$1`, id); n != 1 {
		t.Fatalf("avisos de vencimento: %d", n)
	}
	if count(t, a, `SELECT count(*) FROM expectations WHERE service_id=$1 AND status='faltante'`, s2) != 1 {
		t.Fatal("expectativa deveria estar faltante")
	}
	if count(t, a, `SELECT count(*) FROM notifications WHERE subject='Fatura não recebida'`) != 1 {
		t.Fatal("aviso de faltante ausente")
	}
	if count(t, a, `SELECT count(*) FROM notifications WHERE subject='Contrato próximo do vencimento'`) != 1 {
		t.Fatal("aviso de contrato ausente")
	}
	// expectativas geradas para competências 09, 10 e 11 dos serviços ativos
	if n := count(t, a, `SELECT count(*) FROM expectations WHERE service_id=$1`, w.service); n != 3 {
		t.Fatalf("expectativas geradas: %d", n)
	}
}

// --- fluxo HTTP completo: login, cadastro, upload, processamento, dashboard ---

type client struct {
	t   *testing.T
	srv *httptest.Server
	c   *http.Client
}

func (c *client) do(method, path string, body any) (int, map[string]any) {
	c.t.Helper()
	var r io.Reader
	ct := "application/json"
	switch b := body.(type) {
	case nil:
	case *bytes.Buffer:
		r = b
	case [2]any: // multipart: {buffer, contentType}
		r, ct = b[0].(*bytes.Buffer), b[1].(string)
	default:
		j, _ := json.Marshal(b)
		r = bytes.NewReader(j)
	}
	req, _ := http.NewRequest(method, c.srv.URL+path, r)
	req.Header.Set("Content-Type", ct)
	res, err := c.c.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var out map[string]any
	json.Unmarshal(raw, &out)
	if out == nil {
		out = map[string]any{"_raw": string(raw)}
	}
	return res.StatusCode, out
}

func login(t *testing.T, a *App, email, pw string) *client {
	jar, _ := cookiejar.New(nil)
	srv := httptest.NewServer(a.Handler(fstest.MapFS{"index.html": {Data: []byte("<html>app</html>")}}, []byte("openapi: 3.0.3")))
	t.Cleanup(srv.Close)
	c := &client{t: t, srv: srv, c: &http.Client{Jar: jar}}
	if code, out := c.do("POST", "/api/auth/login", map[string]string{"email": email, "password": pw}); code != 200 {
		t.Fatalf("login %d %v", code, out)
	}
	return c
}

// minimalPDF gera um PDF de texto simples (sem dependências) para testar pdftotext.
func minimalPDF(lines []string) []byte {
	var content strings.Builder
	content.WriteString("BT /F1 10 Tf 40 800 Td 14 TL\n")
	for _, l := range lines {
		content.WriteString("(" + strings.NewReplacer(`\`, `\\`, "(", `\(`, ")", `\)`).Replace(l) + ") '\n")
	}
	content.WriteString("ET")
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", content.Len(), content.String()),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>",
	}
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	offs := []int{}
	for i, o := range objs {
		offs = append(offs, b.Len())
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	x := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, o := range offs {
		fmt.Fprintf(&b, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, x)
	return b.Bytes()
}

func TestHTTPFlow(t *testing.T) {
	a := newApp(t)
	c := login(t, a, "admin@test", "senha-de-teste-123")

	if code, _ := c.do("POST", "/api/suppliers", map[string]any{"name": "X", "tax_id": "11.222.333/0001-82"}); code != 400 {
		t.Fatalf("CNPJ inválido aceito: %d", code)
	}
	code, sup := c.do("POST", "/api/suppliers", map[string]any{"name": "Vivo", "tax_id": "11.222.333/0001-81"})
	if code != 201 {
		t.Fatalf("fornecedor: %d %v", code, sup)
	}
	_, ctr := c.do("POST", "/api/contracts", map[string]any{"number": "C-1", "supplier_id": sup["id"], "starts_on": "2025-01-01", "ends_on": "2027-12-31"})
	code, svc := c.do("POST", "/api/services", map[string]any{"name": "Tronco SIP", "supplier_id": sup["id"], "contract_id": ctr["id"],
		"expected_amount": 1500, "tolerance_pct": 5, "due_day": 15, "category": "Telefonia"})
	if code != 201 {
		t.Fatalf("serviço: %d %v", code, svc)
	}
	if code, out := c.do("POST", "/api/expectations", map[string]any{"service_id": svc["id"], "competence": "2026-10", "expected_amount": 1500, "expected_due": "2026-11-15"}); code != 201 {
		t.Fatalf("expectativa: %d %v", code, out)
	}
	if code, out := c.do("PATCH", fmt.Sprintf("/api/services/%v", svc["id"]), map[string]any{"expected_amount": 1500.5, "_reason": "Reajuste contratual"}); code != 200 || out["expected_amount"] != 1500.5 {
		t.Fatalf("patch: %d %v", code, out)
	}
	if count(t, a, `SELECT count(*) FROM audit_log WHERE entity='service' AND action='update' AND reason='Reajuste contratual' AND before->>'expected_amount'='1500'`) != 1 {
		t.Fatal("audit log com antes/depois ausente")
	}

	// upload de PDF fictício → fila → OCR/regras → identificação → auditoria
	pdf := minimalPDF([]string{
		"[DEMO] TELEFONICA BRASIL S.A.", "CNPJ: 11.222.333/0001-81", "FATURA No DEMO-202610-001",
		"Mes de referencia: 10/2026", "Vencimento: 15/11/2026", "TOTAL A PAGAR R$ 1.497,80",
		"00190.00009 02812.345672 89012.345677 1 16310000149780",
	})
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "fatura.pdf")
	fw.Write(pdf)
	mw.Close()
	code, up := c.do("POST", "/api/invoices/upload", [2]any{&buf, mw.FormDataContentType()})
	if code != 201 {
		t.Fatalf("upload: %d %v", code, up)
	}
	for a.RunOne(context.Background()) {
	}
	_, det := c.do("GET", fmt.Sprintf("/api/invoices/%v/detail", up["invoice_id"]), nil)
	inv := det["invoice"].(map[string]any)
	if inv["status"] != "FOR_PAYMENT" || inv["amount"] != 1497.8 || inv["due_on"] != "2026-11-15" || inv["competence"] != "2026-10-01" ||
		inv["service_id"] != svc["id"] {
		t.Fatalf("pipeline: %v\naudits: %v", inv, det["audits"])
	}

	// mesmo arquivo de novo → bloqueado por duplicidade
	buf.Reset()
	mw = multipart.NewWriter(&buf)
	fw, _ = mw.CreateFormFile("file", "copia.pdf")
	fw.Write(pdf)
	mw.Close()
	_, up2 := c.do("POST", "/api/invoices/upload", [2]any{&buf, mw.FormDataContentType()})
	for a.RunOne(context.Background()) {
	}
	if s := status(t, a, int64(up2["invoice_id"].(float64))); s != "BLOCKED" {
		t.Fatalf("duplicata ficou %s", s)
	}

	// arquivo executável disfarçado é recusado
	buf.Reset()
	mw = multipart.NewWriter(&buf)
	fw, _ = mw.CreateFormFile("file", "fatura.pdf")
	fw.Write([]byte("MZ\x90\x00 executável"))
	mw.Close()
	if code, _ := c.do("POST", "/api/invoices/upload", [2]any{&buf, mw.FormDataContentType()}); code != 400 {
		t.Fatalf("upload malicioso aceito: %d", code)
	}

	for _, p := range []string{"/api/dashboard/executive?from=2026-11-01&to=2026-11-30", "/api/dashboard/due", "/api/dashboard/audit", "/api/logs/jobs", "/api/search?q=vivo"} {
		if code, out := c.do("GET", p, nil); code != 200 {
			t.Fatalf("%s: %d %v", p, code, out)
		}
	}
	_, ex := c.do("GET", "/api/dashboard/executive?from=2026-11-01&to=2026-11-30", nil)
	k := ex["kpis"].(map[string]any)
	if k["approved"] != 1497.8 || k["active_services"] != 1.0 {
		t.Fatalf("kpis: %v", k)
	}

	// RBAC: perfil consulta não escreve nem aprova
	exec(t, a, `INSERT INTO users(email,name,role,password_hash) VALUES('leitor@test','Leitor','consulta',$1)`, HashPassword("senha-leitura-123"))
	ro := login(t, a, "leitor@test", "senha-leitura-123")
	if code, _ := ro.do("POST", "/api/suppliers", map[string]any{"name": "Y"}); code != 403 {
		t.Fatalf("consulta escreveu: %d", code)
	}
	if code, _ := ro.do("POST", fmt.Sprintf("/api/invoices/%v/transition", up2["invoice_id"]), map[string]any{"to": "REVIEW", "note": "x"}); code != 403 {
		t.Fatalf("consulta moveu fatura: %d", code)
	}
	if code, _ := ro.do("GET", "/api/invoices", nil); code != 200 {
		t.Fatal("consulta deveria ler faturas")
	}

	// transição inválida é recusada (BLOCKED não vai direto para APPROVED)
	if code, _ := c.do("POST", fmt.Sprintf("/api/invoices/%v/transition", up2["invoice_id"]), map[string]any{"to": "APPROVED", "note": "ok"}); code != 409 {
		t.Fatalf("transição inválida: %d", code)
	}
}

func TestLoginRateLimitAndCSRF(t *testing.T) {
	a := newApp(t)
	c := login(t, a, "admin@test", "senha-de-teste-123")
	req, _ := http.NewRequest("POST", c.srv.URL+"/api/suppliers", strings.NewReader(`{"name":"x"}`))
	req.Header.Set("Origin", "https://evil.example")
	res, _ := c.c.Do(req)
	if res.StatusCode != 403 {
		t.Fatalf("origem externa aceita: %d", res.StatusCode)
	}
	blocked := false
	for i := 0; i < 12; i++ {
		code, _ := c.do("POST", "/api/auth/login", map[string]string{"email": "admin@test", "password": "errada"})
		if code == 429 {
			blocked = true
		}
	}
	if !blocked {
		t.Fatal("rate limit de login não atuou")
	}
}

func TestSeedDemo(t *testing.T) {
	a := newApp(t)
	if err := a.SeedDemo(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	rows, _ := db.Maps(context.Background(), a.DB, `SELECT status, count(*) AS n FROM invoices GROUP BY 1`)
	for _, r := range rows {
		got[r["status"].(string)] = int(r["n"].(int64))
	}
	want := map[string]int{"PAID": 3, "FOR_PAYMENT": 2, "BLOCKED": 2, "REVIEW": 1, "RECEIVED": 1}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("seed: %v, esperado %v", got, want)
		}
	}
	if count(t, a, `SELECT count(*) FROM suppliers WHERE NOT is_demo OR name NOT LIKE '[DEMO]%'`) != 0 {
		t.Fatal("dado de demonstração sem marcação")
	}
}
