package app

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// RBAC: papel → permissões. Mudou a política? Mude aqui (e na doc security.md).
var rolePerms = map[string][]string{
	"admin": {"*"},
	"gestor": {"suppliers.read", "suppliers.write", "services.read", "services.write", "contracts.read", "contracts.write",
		"invoices.read", "invoices.write", "audits.read", "audits.execute", "payments.read", "settings.read", "logs.read"},
	"auditor": {"suppliers.read", "services.read", "contracts.read", "invoices.read", "audits.read", "audits.execute", "payments.read", "logs.read"},
	"ti": {"suppliers.read", "suppliers.write", "services.read", "services.write", "contracts.read", "contracts.write",
		"invoices.read", "invoices.write", "audits.read", "audits.execute", "settings.read", "logs.read"},
	"financeiro": {"suppliers.read", "services.read", "contracts.read", "invoices.read", "audits.read", "payments.read", "payments.write"},
	"consulta":   {"suppliers.read", "services.read", "contracts.read", "invoices.read", "audits.read", "payments.read"},
}

func Can(role, perm string) bool {
	for _, p := range rolePerms[role] {
		if p == "*" || p == perm {
			return true
		}
	}
	return false
}

type User struct {
	ID    int64  `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
	Role  string `json:"role"`
}

func (u User) Perms() []string {
	if Can(u.Role, "*") {
		var all []string
		seen := map[string]bool{}
		for _, ps := range rolePerms {
			for _, p := range ps {
				if p != "*" && !seen[p] {
					seen[p] = true
					all = append(all, p)
				}
			}
		}
		return append(all, "users.read", "users.write", "settings.write")
	}
	return rolePerms[u.Role]
}

const iterations = 600_000

func HashPassword(pw string) string {
	salt := make([]byte, 16)
	rand.Read(salt)
	k, _ := pbkdf2.Key(sha256.New, pw, salt, iterations, 32)
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", iterations, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(k))
}

func CheckPassword(hash, pw string) bool {
	p := strings.Split(hash, "$")
	if len(p) != 4 {
		return false
	}
	it, _ := strconv.Atoi(p[1])
	salt, _ := base64.RawStdEncoding.DecodeString(p[2])
	want, _ := base64.RawStdEncoding.DecodeString(p[3])
	got, err := pbkdf2.Key(sha256.New, pw, salt, it, len(want))
	return err == nil && subtle.ConstantTimeCompare(got, want) == 1
}

type ctxKey struct{}

func userFrom(ctx context.Context) User {
	u, _ := ctx.Value(ctxKey{}).(User)
	return u
}

func tokenHash(t string) string { s := sha256.Sum256([]byte(t)); return hex.EncodeToString(s[:]) }

const sessionTTL = 12 * time.Hour

func (a *App) session(r *http.Request) (User, bool) {
	c, err := r.Cookie("sid")
	if err != nil {
		return User{}, false
	}
	var u User
	err = a.DB.QueryRowContext(r.Context(), `
		SELECT u.id, u.email, u.name, u.role FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.expires_at > now() AND u.active`, tokenHash(c.Value)).
		Scan(&u.ID, &u.Email, &u.Name, &u.Role)
	return u, err == nil
}

// require autentica e confere a permissão antes de chamar h.
func (a *App) require(perm string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := a.session(r)
		if !ok {
			httpErr(w, 401, "não autenticado")
			return
		}
		if perm != "" && !Can(u.Role, perm) {
			httpErr(w, 403, "sem permissão: "+perm)
			return
		}
		h(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, u)))
	}
}

func (a *App) login(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if !a.limiter.allow(ip) {
		httpErr(w, 429, "muitas tentativas, aguarde um minuto")
		return
	}
	var in struct{ Email, Password string }
	if !readJSON(w, r, &in) {
		return
	}
	var id int64
	var hash string
	err := a.DB.QueryRowContext(r.Context(), `SELECT id, password_hash FROM users WHERE lower(email)=lower($1) AND active`, in.Email).Scan(&id, &hash)
	if err != nil || !CheckPassword(hash, in.Password) {
		a.logChange(r.Context(), User{Name: in.Email}, "login_failed", "user", 0, nil, nil, "", ip)
		httpErr(w, 401, "e-mail ou senha inválidos")
		return
	}
	tok := make([]byte, 32)
	rand.Read(tok)
	t := base64.RawURLEncoding.EncodeToString(tok)
	a.DB.ExecContext(r.Context(), `DELETE FROM sessions WHERE expires_at < now()`)
	if _, err := a.DB.ExecContext(r.Context(), `INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2,$3)`, tokenHash(t), id, time.Now().Add(sessionTTL)); err != nil {
		serverErr(w, err)
		return
	}
	// SameSite=Strict + HttpOnly: o cookie não vai em requisições de outros sites (CSRF) nem é lido por JS.
	http.SetCookie(w, &http.Cookie{Name: "sid", Value: t, Path: "/", HttpOnly: true, Secure: a.Cfg.CookieSecure,
		SameSite: http.SameSiteStrictMode, MaxAge: int(sessionTTL.Seconds())})
	a.logChange(r.Context(), User{ID: id, Name: in.Email}, "login", "user", id, nil, nil, "", ip)
	r.AddCookie(&http.Cookie{Name: "sid", Value: t})
	a.me(w, r)
}

func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("sid"); err == nil {
		a.DB.ExecContext(r.Context(), `DELETE FROM sessions WHERE token_hash=$1`, tokenHash(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: "sid", Path: "/", MaxAge: -1, HttpOnly: true})
	w.WriteHeader(204)
}

func (a *App) me(w http.ResponseWriter, r *http.Request) {
	u, ok := a.session(r)
	if !ok {
		httpErr(w, 401, "não autenticado")
		return
	}
	writeJSON(w, 200, map[string]any{"user": u, "permissions": u.Perms(), "dry_run": a.Cfg.DryRun, "ai": a.AI != nil})
}

// EnsureAdmin cria o primeiro administrador quando não há usuários.
func (a *App) EnsureAdmin(ctx context.Context) error {
	var n int
	a.DB.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n)
	if n > 0 {
		return nil
	}
	pw := a.Cfg.AdminPassword
	if pw == "" {
		return errors.New("nenhum usuário cadastrado: defina ADMIN_PASSWORD para criar o administrador inicial")
	}
	_, err := a.DB.ExecContext(ctx, `INSERT INTO users(email,name,password_hash,role) VALUES($1,'Administrador',$2,'admin')`, a.Cfg.AdminEmail, HashPassword(pw))
	return err
}

// --- usuários (admin) ---

func (a *App) listUsers(w http.ResponseWriter, r *http.Request) {
	a.respond(w, a.maps(r, `SELECT id,email,name,role,active,created_at FROM users ORDER BY name`))
}

func (a *App) saveUser(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email, Name, Role, Password string
		Active                      *bool
	}
	if !readJSON(w, r, &in) {
		return
	}
	if _, ok := rolePerms[in.Role]; in.Role != "" && !ok {
		httpErr(w, 400, "papel inválido")
		return
	}
	if in.Password != "" && len(in.Password) < 10 {
		httpErr(w, 400, "senha deve ter ao menos 10 caracteres")
		return
	}
	u := userFrom(r.Context())
	id := r.PathValue("id")
	var err error
	var newID int64
	if id == "" {
		if in.Email == "" || in.Name == "" || in.Role == "" || in.Password == "" {
			httpErr(w, 400, "email, nome, papel e senha são obrigatórios")
			return
		}
		err = a.DB.QueryRowContext(r.Context(), `INSERT INTO users(email,name,role,password_hash) VALUES($1,$2,$3,$4) RETURNING id`,
			in.Email, in.Name, in.Role, HashPassword(in.Password)).Scan(&newID)
	} else {
		newID, _ = strconv.ParseInt(id, 10, 64)
		hash := ""
		if in.Password != "" {
			hash = HashPassword(in.Password)
		}
		_, err = a.DB.ExecContext(r.Context(), `UPDATE users SET
			name = COALESCE(NULLIF($2,''), name), role = COALESCE(NULLIF($3,''), role),
			password_hash = COALESCE(NULLIF($4,''), password_hash), active = COALESCE($5, active) WHERE id=$1`,
			newID, in.Name, in.Role, hash, in.Active)
		if in.Password != "" || (in.Active != nil && !*in.Active) {
			a.DB.ExecContext(r.Context(), `DELETE FROM sessions WHERE user_id=$1`, newID)
		}
	}
	if err != nil {
		dbErr(w, err)
		return
	}
	a.logChange(r.Context(), u, map[bool]string{true: "create", false: "update"}[id == ""], "user", newID, nil,
		map[string]any{"email": in.Email, "name": in.Name, "role": in.Role, "active": in.Active, "password_changed": in.Password != ""}, "", clientIP(r))
	writeJSON(w, 200, map[string]any{"id": newID})
}

// --- rate limit simples por IP (login) ---

type limiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	hits   map[string][]time.Time
}

func newLimiter(max int, window time.Duration) *limiter {
	return &limiter{max: max, window: window, hits: map[string][]time.Time{}}
}

// ponytail: em memória e por instância; com várias réplicas, mover para o Postgres.
func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	h := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if now.Sub(t) < l.window {
			h = append(h, t)
		}
	}
	if len(h) >= l.max {
		l.hits[key] = h
		return false
	}
	l.hits[key] = append(h, now)
	return true
}

func clientIP(r *http.Request) string {
	if f := r.Header.Get("X-Forwarded-For"); f != "" && trustProxy {
		return strings.TrimSpace(strings.Split(f, ",")[0])
	}
	h, _, _ := net.SplitHostPort(r.RemoteAddr)
	return h
}

var trustProxy = env("TRUST_PROXY", "false") == "true"
