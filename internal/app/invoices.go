package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ivankoelho/pague.ia/internal/db"
	"github.com/ivankoelho/pague.ia/internal/workflow"
)

var allowedMime = map[string]string{
	"application/pdf": ".pdf",
	"text/xml":        ".xml",
	"application/xml": ".xml",
	"image/png":       ".png",
	"image/jpeg":      ".jpg",
}

// sniff decide o tipo pelo conteúdo, nunca pelo nome ou cabeçalho enviado.
func sniff(data []byte) (string, bool) {
	ct := strings.Split(http.DetectContentType(data), ";")[0]
	if ct == "text/plain" && bytes.HasPrefix(bytes.TrimSpace(data), []byte("<")) {
		ct = "text/xml"
	}
	_, ok := allowedMime[ct]
	return ct, ok
}

type ingestResult struct {
	InvoiceID   int64   `json:"invoice_id"`
	DocumentID  int64   `json:"document_id"`
	SameFileIDs []int64 `json:"same_file_invoice_ids,omitempty"`
}

// ingestFile guarda o arquivo, cria (ou usa) a fatura e enfileira o processamento.
func (a *App) ingestFile(ctx context.Context, filename string, data []byte, source, note string, invoiceID int64, u User) (*ingestResult, error) {
	mime, ok := sniff(data)
	if !ok {
		return nil, fmt.Errorf("tipo de arquivo não permitido (%s)", mime)
	}
	if int64(len(data)) > a.Cfg.MaxUploadMB<<20 {
		return nil, fmt.Errorf("arquivo maior que %d MB", a.Cfg.MaxUploadMB)
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	key := hash[:2] + "/" + hash
	if err := a.Store.Put(key, bytes.NewReader(data)); err != nil {
		return nil, err
	}
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	res := &ingestResult{InvoiceID: invoiceID}
	res.SameFileIDs = ids(ctx, tx, `SELECT DISTINCT invoice_id FROM documents WHERE sha256=$1 AND invoice_id IS NOT NULL`, hash)
	if invoiceID == 0 {
		if err := tx.QueryRowContext(ctx, `INSERT INTO invoices(source,notes) VALUES($1,NULLIF($2,'')) RETURNING id`, source, note).Scan(&res.InvoiceID); err != nil {
			return nil, err
		}
		tx.ExecContext(ctx, `INSERT INTO invoice_events(invoice_id,to_status,actor,note) VALUES($1,'RECEIVED',$2,$3)`,
			res.InvoiceID, u.Name, "recebida via "+source+": "+filepath.Base(filename))
	}
	name := filepath.Base(filename)
	if name == "." || name == "/" {
		name = "documento" + allowedMime[mime]
	}
	if err := tx.QueryRowContext(ctx, `INSERT INTO documents(invoice_id,filename,mime,size,sha256,source,storage_key,created_by)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`, res.InvoiceID, name, mime, len(data), hash, source, key, u.Name).Scan(&res.DocumentID); err != nil {
		return nil, err
	}
	if err := a.enqueue(ctx, tx, "process_invoice", map[string]int64{"id": res.InvoiceID}); err != nil {
		return nil, err
	}
	return res, tx.Commit()
}

func (a *App) upload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, a.Cfg.MaxUploadMB<<20+1<<20)
	f, hdr, err := r.FormFile("file")
	if err != nil {
		httpErr(w, 400, "arquivo ausente ou grande demais")
		return
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		httpErr(w, 400, "falha ao ler arquivo")
		return
	}
	invID, _ := strconv.ParseInt(r.FormValue("invoice_id"), 10, 64)
	u := userFrom(r.Context())
	res, err := a.ingestFile(r.Context(), hdr.Filename, data, "manual", "", invID, u)
	if err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	a.logChange(r.Context(), u, "upload", "invoice", res.InvoiceID, nil, map[string]any{"document_id": res.DocumentID, "filename": hdr.Filename}, "", clientIP(r))
	writeJSON(w, 201, res)
}

func pathID(r *http.Request) int64 { n, _ := strconv.ParseInt(r.PathValue("id"), 10, 64); return n }

func (a *App) reprocess(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	if err := a.enqueue(r.Context(), a.DB, "process_invoice", map[string]int64{"id": id}); err != nil {
		serverErr(w, err)
		return
	}
	a.logChange(r.Context(), userFrom(r.Context()), "reprocess", "invoice", id, nil, nil, "", clientIP(r))
	writeJSON(w, 202, map[string]any{"queued": true})
}

// runAudit reexecuta a auditoria na hora (útil após corrigir cadastro ou dados da fatura).
func (a *App) runAudit(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	u := userFrom(r.Context())
	if err := a.auditInvoice(r.Context(), id, u.Name); err != nil {
		transitionErr(w, err)
		return
	}
	a.logChange(r.Context(), u, "audit", "invoice", id, nil, nil, "", clientIP(r))
	a.invoiceDetail(w, r)
}

func transitionErr(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrTransition) {
		httpErr(w, 409, err.Error())
		return
	}
	serverErr(w, err)
}

func (a *App) manualTransition(w http.ResponseWriter, r *http.Request) {
	var in struct{ To, Note string }
	if !readJSON(w, r, &in) {
		return
	}
	perm, ok := workflow.Manual[in.To]
	u := userFrom(r.Context())
	if !ok || !Can(u.Role, perm) {
		httpErr(w, 403, "sem permissão para mover para "+in.To)
		return
	}
	if (in.To == workflow.Approved || in.To == workflow.Rejected) && strings.TrimSpace(in.Note) == "" {
		httpErr(w, 400, "justificativa obrigatória para aprovar ou rejeitar manualmente")
		return
	}
	id := pathID(r)
	if in.To == workflow.Processing {
		// reprocessar passa pela fila para não travar a requisição em OCR/IA
		if err := a.transition(r.Context(), id, in.To, u.Name, in.Note); err != nil {
			transitionErr(w, err)
			return
		}
		a.enqueue(r.Context(), a.DB, "process_invoice", map[string]int64{"id": id})
	} else if in.To == workflow.Auditing {
		if err := a.auditInvoice(r.Context(), id, u.Name); err != nil {
			transitionErr(w, err)
			return
		}
	} else if err := a.transition(r.Context(), id, in.To, u.Name, in.Note); err != nil {
		transitionErr(w, err)
		return
	}
	a.logChange(r.Context(), u, "transition", "invoice", id, nil, map[string]string{"to": in.To}, in.Note, clientIP(r))
	a.invoiceDetail(w, r)
}

func (a *App) invoiceDetail(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	ctx := r.Context()
	inv, err := db.One(ctx, a.DB, `SELECT * FROM invoices_v WHERE id=$1`, id)
	if err != nil || inv == nil {
		httpErr(w, 404, "fatura não encontrada")
		return
	}
	out := map[string]any{"invoice": inv}
	for k, q := range map[string]string{
		"events":        `SELECT * FROM invoice_events WHERE invoice_id=$1 ORDER BY at, id`,
		"audits":        `SELECT * FROM audits WHERE invoice_id=$1 ORDER BY id DESC`,
		"ai_runs":       `SELECT id,provider,model,prompt_version,ok,error,duration_ms,output,created_at FROM ai_runs WHERE invoice_id=$1 ORDER BY id DESC`,
		"documents":     `SELECT id,filename,mime,size,sha256,source,created_by,created_at FROM documents WHERE invoice_id=$1 ORDER BY id DESC`,
		"notifications": `SELECT id,channel,target,subject,body,status,attempts,last_error,created_at,sent_at FROM notifications WHERE ref_entity='invoice' AND ref_id=$1 ORDER BY id DESC`,
		"changes":       `SELECT * FROM audit_log WHERE entity='invoice' AND entity_id=$1 ORDER BY id DESC LIMIT 50`,
	} {
		rows, err := db.Maps(ctx, a.DB, q, id)
		if err != nil {
			serverErr(w, err)
			return
		}
		out[k] = rows
	}
	writeJSON(w, 200, out)
}

func (a *App) downloadDocument(w http.ResponseWriter, r *http.Request) {
	d, err := db.One(r.Context(), a.DB, `SELECT filename, mime, storage_key FROM documents WHERE id=$1`, pathID(r))
	if err != nil || d == nil {
		httpErr(w, 404, "documento não encontrado")
		return
	}
	f, err := a.Store.Open(d["storage_key"].(string))
	if err != nil {
		httpErr(w, 404, "arquivo indisponível")
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", d["mime"].(string))
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", d["filename"]))
	w.Header().Set("Content-Security-Policy", "sandbox")
	io.Copy(w, f)
}

// --- notificações, logs, busca, regras ---

func (a *App) listNotifications(w http.ResponseWriter, r *http.Request) {
	q := `SELECT * FROM notifications`
	if ch := r.URL.Query().Get("channel"); ch != "" {
		a.respond(w, a.maps(r, q+` WHERE channel=$1 ORDER BY id DESC LIMIT 200`, ch))
		return
	}
	a.respond(w, a.maps(r, q+` ORDER BY id DESC LIMIT 200`))
}

func (a *App) readNotification(w http.ResponseWriter, r *http.Request) {
	a.DB.ExecContext(r.Context(), `UPDATE notifications SET read_at=now() WHERE id=$1 AND read_at IS NULL`, pathID(r))
	w.WriteHeader(204)
}

func (a *App) retryNotification(w http.ResponseWriter, r *http.Request) {
	a.DB.ExecContext(r.Context(), `UPDATE notifications SET status='pending', attempts=0 WHERE id=$1 AND status IN ('failed','mock')`, pathID(r))
	a.logChange(r.Context(), userFrom(r.Context()), "retry", "notification", pathID(r), nil, nil, "", clientIP(r))
	w.WriteHeader(204)
}

func (a *App) auditLog(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	a.respond(w, a.maps(r, `SELECT * FROM audit_log WHERE ($1='' OR entity=$1) AND ($2='' OR entity_id::text=$2)
		ORDER BY id DESC LIMIT 300`, q.Get("entity"), q.Get("entity_id")))
}

func (a *App) listJobs(w http.ResponseWriter, r *http.Request) {
	a.respond(w, a.maps(r, `SELECT id,kind,payload,status,run_at,attempts,max_attempts,last_error,duration_ms,created_at,finished_at
		FROM jobs WHERE ($1='' OR status=$1) ORDER BY id DESC LIMIT 200`, r.URL.Query().Get("status")))
}

func (a *App) listRules(w http.ResponseWriter, r *http.Request) {
	a.respond(w, a.maps(r, `SELECT r.*, (SELECT count(*) FROM automation_runs x WHERE x.rule_id=r.id) AS runs,
		(SELECT max(at) FROM automation_runs x WHERE x.rule_id=r.id) AS last_run FROM automation_rules r ORDER BY event, priority`))
}

func (a *App) toggleRule(w http.ResponseWriter, r *http.Request) {
	var in struct{ Enabled bool }
	if !readJSON(w, r, &in) {
		return
	}
	a.DB.ExecContext(r.Context(), `UPDATE automation_rules SET enabled=$2 WHERE id=$1`, pathID(r), in.Enabled)
	a.logChange(r.Context(), userFrom(r.Context()), "update", "automation_rule", pathID(r), nil, in, "", clientIP(r))
	w.WriteHeader(204)
}

func (a *App) runScan(w http.ResponseWriter, r *http.Request) {
	if err := a.dailyScan(r.Context()); err != nil {
		serverErr(w, err)
		return
	}
	a.enqueue(r.Context(), a.DB, "send_notifications", map[string]any{})
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *App) search(w http.ResponseWriter, r *http.Request) {
	s := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(s) < 2 {
		writeJSON(w, 200, []any{})
		return
	}
	p := "%" + s + "%"
	a.respond(w, a.maps(r, `
		(SELECT 'supplier' AS type, id, name AS title, coalesce(tax_id,'') AS subtitle FROM suppliers WHERE name ILIKE $1 OR legal_name ILIKE $1 OR tax_id LIKE $1 LIMIT 5)
		UNION ALL
		(SELECT 'service', id, name, supplier_name FROM services_v WHERE name ILIKE $1 OR supplier_name ILIKE $1 LIMIT 5)
		UNION ALL
		(SELECT 'contract', id, number, supplier_name FROM contracts_v WHERE number ILIKE $1 LIMIT 5)
		UNION ALL
		(SELECT 'invoice', id, coalesce(supplier_name,'Fatura') || ' #' || id, coalesce(service_name,'') || ' ' || status
		   FROM invoices_v WHERE doc_number ILIKE $1 OR supplier_name ILIKE $1 OR service_name ILIKE $1 OR id::text = $2 LIMIT 8)`, p, s))
}
