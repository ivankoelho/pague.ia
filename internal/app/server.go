package app

import (
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"time"
)

// Handler monta as rotas da API, observabilidade e o frontend embutido.
func (a *App) Handler(web fs.FS, openapi []byte) http.Handler {
	mux := http.NewServeMux()
	r := a.require

	mux.HandleFunc("POST /api/auth/login", a.login)
	mux.HandleFunc("POST /api/auth/logout", a.logout)
	mux.HandleFunc("GET /api/auth/me", a.me)

	a.routeResources(mux)
	mux.HandleFunc("POST /api/invoices/upload", r("invoices.write", a.upload))
	mux.HandleFunc("GET /api/invoices/{id}/detail", r("invoices.read", a.invoiceDetail))
	mux.HandleFunc("POST /api/invoices/{id}/process", r("invoices.write", a.reprocess))
	mux.HandleFunc("POST /api/invoices/{id}/audit", r("audits.execute", a.runAudit))
	mux.HandleFunc("POST /api/invoices/{id}/transition", r("invoices.read", a.manualTransition)) // permissão por destino
	mux.HandleFunc("GET /api/documents/{id}", r("invoices.read", a.downloadDocument))

	mux.HandleFunc("GET /api/dashboard/executive", r("invoices.read", a.dashExecutive))
	mux.HandleFunc("GET /api/dashboard/due", r("invoices.read", a.dashDue))
	mux.HandleFunc("GET /api/dashboard/audit", r("audits.read", a.dashAudit))

	mux.HandleFunc("GET /api/notifications", r("invoices.read", a.listNotifications))
	mux.HandleFunc("POST /api/notifications/{id}/read", r("invoices.read", a.readNotification))
	mux.HandleFunc("POST /api/notifications/{id}/retry", r("settings.write", a.retryNotification))
	mux.HandleFunc("GET /api/automation/rules", r("settings.read", a.listRules))
	mux.HandleFunc("POST /api/automation/rules/{id}", r("settings.write", a.toggleRule))
	mux.HandleFunc("POST /api/automation/scan", r("settings.write", a.runScan))
	mux.HandleFunc("GET /api/logs/audit", r("logs.read", a.auditLog))
	mux.HandleFunc("GET /api/logs/jobs", r("logs.read", a.listJobs))
	mux.HandleFunc("GET /api/search", r("invoices.read", a.search))
	mux.HandleFunc("GET /api/users", r("users.read", a.listUsers))
	mux.HandleFunc("POST /api/users", r("users.write", a.saveUser))
	mux.HandleFunc("POST /api/users/{id}", r("users.write", a.saveUser))

	mux.HandleFunc("GET /api/openapi.yaml", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		w.Write(openapi)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := a.DB.PingContext(r.Context()); err != nil {
			http.Error(w, "db indisponível", 503)
			return
		}
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /metrics", a.metrics)

	mux.Handle("/", spa(web))
	return a.middleware(mux)
}

// spa serve os arquivos do build React e devolve index.html para rotas do cliente.
func spa(web fs.FS) http.Handler {
	files := http.FileServerFS(web)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" || strings.HasPrefix(p, "api/") {
			if p != "" {
				httpErr(w, 404, "rota inexistente")
				return
			}
		}
		if _, err := fs.Stat(web, p); p != "" && err != nil {
			r.URL.Path = "/"
		}
		if strings.HasPrefix(p, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		files.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	code int
}

func (s *statusWriter) WriteHeader(c int) { s.code = c; s.ResponseWriter.WriteHeader(c) }

func (a *App) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		if h.Get("Content-Security-Policy") == "" {
			h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; frame-src 'self'; object-src 'self'")
		}
		// CSRF: além do cookie SameSite=Strict, escrita na API exige origem própria.
		if r.Method != "GET" && strings.HasPrefix(r.URL.Path, "/api/") {
			if o := r.Header.Get("Origin"); o != "" && !sameOrigin(o, r) {
				httpErr(w, 403, "origem não permitida")
				return
			}
		}
		sw := &statusWriter{ResponseWriter: w, code: 200}
		defer func() {
			if rec := recover(); rec != nil {
				a.Log.Error("panic", "err", rec, "path", r.URL.Path)
				httpErr(sw, 500, "erro interno")
			}
			if strings.HasPrefix(r.URL.Path, "/api/") {
				a.Log.Info("http", "method", r.Method, "path", r.URL.Path, "status", sw.code, "ms", time.Since(start).Milliseconds())
			}
		}()
		next.ServeHTTP(sw, r)
	})
}

func sameOrigin(origin string, r *http.Request) bool {
	host := r.Host
	if f := r.Header.Get("X-Forwarded-Host"); f != "" && trustProxy {
		host = f
	}
	o := strings.TrimPrefix(strings.TrimPrefix(origin, "https://"), "http://")
	return o == host
}

// metrics expõe contadores no formato texto do Prometheus (Zabbix também consome via HTTP agent).
func (a *App) metrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	q := func(sql string) (rows [][2]string) {
		rs, err := a.DB.QueryContext(r.Context(), sql)
		if err != nil {
			return nil
		}
		defer rs.Close()
		for rs.Next() {
			var k, v string
			rs.Scan(&k, &v)
			rows = append(rows, [2]string{k, v})
		}
		return
	}
	for _, kv := range q(`SELECT status, count(*) FROM jobs GROUP BY 1`) {
		fmt.Fprintf(w, "pague_jobs{status=%q} %s\n", kv[0], kv[1])
	}
	for _, kv := range q(`SELECT status, count(*) FROM invoices GROUP BY 1`) {
		fmt.Fprintf(w, "pague_invoices{status=%q} %s\n", kv[0], kv[1])
	}
	for _, kv := range q(`SELECT status, count(*) FROM notifications GROUP BY 1`) {
		fmt.Fprintf(w, "pague_notifications{status=%q} %s\n", kv[0], kv[1])
	}
	for _, kv := range q(`SELECT 'oldest_pending_seconds', coalesce(extract(epoch FROM now()-min(run_at))::bigint,0)::text FROM jobs WHERE status='pending'`) {
		fmt.Fprintf(w, "pague_jobs_%s %s\n", kv[0], kv[1])
	}
}
