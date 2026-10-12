package app

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/ivankoelho/pague.ia/internal/db"
	"github.com/lib/pq"
)

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func httpErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func serverErr(w http.ResponseWriter, err error) {
	slog.Error("erro interno", "err", err)
	httpErr(w, 500, "erro interno")
}

// dbErr traduz violações de constraint em erros 4xx legíveis.
func dbErr(w http.ResponseWriter, err error) {
	var pe *pq.Error
	if errors.As(err, &pe) {
		switch pe.Code {
		case "23505":
			httpErr(w, 409, "registro duplicado: "+pe.Detail)
			return
		case "23503":
			httpErr(w, 400, "referência inválida: "+pe.Detail)
			return
		case "23514", "22P02", "22007", "22008", "22003":
			httpErr(w, 400, "valor inválido: "+pe.Message)
			return
		}
	}
	serverErr(w, err)
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		httpErr(w, 400, "JSON inválido: "+err.Error())
		return false
	}
	return true
}

type result struct {
	v   any
	err error
}

func (a *App) maps(r *http.Request, q string, args ...any) result {
	v, err := db.Maps(r.Context(), a.DB, q, args...)
	return result{v, err}
}

func (a *App) respond(w http.ResponseWriter, res result) {
	if res.err != nil {
		dbErr(w, res.err)
		return
	}
	writeJSON(w, 200, res.v)
}

// logChange grava a trilha de auditoria (quem, quando, o quê, antes/depois).
func (a *App) logChange(ctx context.Context, u User, action, entity string, id int64, before, after any, reason, ip string) {
	j := func(v any) any {
		if v == nil {
			return nil
		}
		b, _ := json.Marshal(v)
		return string(b)
	}
	var uid any
	if u.ID != 0 {
		uid = u.ID
	}
	name := u.Name
	if name == "" {
		name = "sistema"
	}
	src := "api"
	if u.ID == 0 && u.Name == "" {
		src = "sistema"
	}
	if _, err := a.DB.ExecContext(ctx, `INSERT INTO audit_log(user_id,user_name,action,entity,entity_id,before,after,reason,source,ip)
		VALUES($1,$2,$3,$4,$5,$6,$7,NULLIF($8,''),$9,NULLIF($10,''))`,
		uid, name, action, entity, id, j(before), j(after), reason, src, ip); err != nil {
		a.Log.Error("audit_log", "err", err)
	}
}
