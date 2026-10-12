package app

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ivankoelho/pague.ia/internal/br"
	"github.com/ivankoelho/pague.ia/internal/db"
)

// Cadastros declarativos: uma definição por entidade gera list/get/create/update
// com validação de tipos, filtros, busca e trilha de auditoria.

type col struct {
	name, typ string // text int numeric date month bool
	required  bool
}

type resource struct {
	name, table, view, perm string
	cols                    []col
	search                  []string // colunas da view usadas na busca livre (?q=)
	filters                 []string // colunas filtráveis (?col=, ?col_from=, ?col_to=)
	order                   string
	normalize               func(map[string]any) error
}

func c(name, typ string) col   { return col{name: name, typ: typ} }
func req(name, typ string) col { return col{name: name, typ: typ, required: true} }

var resources = []resource{
	{name: "units", table: "units", view: "units", perm: "settings", order: "name",
		cols: []col{req("name", "text"), c("kind", "text"), c("parent_id", "int")}, search: []string{"name"}},
	{name: "suppliers", table: "suppliers", view: "suppliers", perm: "suppliers", order: "name",
		cols: []col{req("name", "text"), c("legal_name", "text"), c("trade_name", "text"), c("tax_id", "text"),
			c("contacts", "text"), c("email", "text"), c("phone", "text"), c("address", "text"), c("status", "text"), c("notes", "text")},
		search: []string{"name", "legal_name", "trade_name", "tax_id"}, filters: []string{"status"},
		normalize: func(m map[string]any) error {
			if v, ok := m["tax_id"].(string); ok && v != "" {
				if !br.ValidCNPJ(v) {
					return fmt.Errorf("CNPJ/CPF inválido")
				}
				m["tax_id"] = br.Digits(v)
			}
			return nil
		}},
	{name: "services", table: "services", view: "services_v", perm: "services", order: "supplier_name, name",
		cols: []col{req("supplier_id", "int"), c("contract_id", "int"), c("unit_id", "int"), req("name", "text"), c("description", "text"),
			c("category", "text"), c("owner", "text"), c("cost_center", "text"), c("periodicity", "text"),
			c("expected_amount", "numeric"), c("tolerance_pct", "numeric"), c("due_day", "int"), c("status", "text"),
			c("criticality", "text"), c("starts_on", "date"), c("ends_on", "date"), c("renewal_on", "date"), c("notes", "text")},
		search:  []string{"name", "supplier_name", "category", "owner"},
		filters: []string{"supplier_id", "status", "category", "unit_id", "owner", "cost_center", "criticality"}},
	{name: "contracts", table: "contracts", view: "contracts_v", perm: "contracts", order: "ends_on NULLS LAST",
		cols: []col{req("number", "text"), req("supplier_id", "int"), req("starts_on", "date"), c("ends_on", "date"),
			c("auto_renew", "bool"), c("periodicity", "text"), c("amount", "numeric"), c("adjustment_index", "text"),
			c("adjustment_pct", "numeric"), c("owner", "text"), c("document_id", "int"), c("status", "text"), c("notes", "text")},
		search: []string{"number", "supplier_name", "owner"}, filters: []string{"supplier_id", "status", "ends_on"}},
	{name: "expectations", table: "expectations", view: "expectations_v", perm: "services", order: "competence DESC, supplier_name",
		cols: []col{req("service_id", "int"), req("competence", "month"), c("expected_amount", "numeric"),
			c("expected_due", "date"), c("status", "text"), c("notes", "text")},
		search: []string{"service_name", "supplier_name"}, filters: []string{"service_id", "supplier_id", "status", "competence", "expected_due"}},
	{name: "invoices", table: "invoices", view: "invoices_v", perm: "invoices", order: "due_on DESC NULLS LAST, id DESC",
		cols: []col{c("supplier_id", "int"), c("service_id", "int"), c("contract_id", "int"), c("unit_id", "int"),
			c("competence", "month"), c("issued_on", "date"), c("due_on", "date"), c("amount", "numeric"), c("doc_number", "text"),
			c("tax_id", "text"), c("kind", "text"), c("digitable_line", "text"), c("barcode", "text"), c("pix", "text"), c("notes", "text"),
			c("payment_scheduled_on", "date"), c("paid_on", "date"), c("paid_amount", "numeric")},
		search:  []string{"supplier_name", "service_name", "doc_number"},
		filters: []string{"status", "supplier_id", "service_id", "unit_id", "category", "owner", "due_on", "competence", "audit_result", "source"},
		normalize: func(m map[string]any) error {
			for _, k := range []string{"tax_id", "digitable_line", "barcode"} {
				if v, ok := m[k].(string); ok {
					m[k] = br.Digits(v)
				}
			}
			return nil
		}},
}

func (a *App) routeResources(mux *http.ServeMux) {
	for _, rs := range resources {
		rs := rs
		mux.HandleFunc("GET /api/"+rs.name, a.require(rs.perm+".read", rs.list(a)))
		mux.HandleFunc("GET /api/"+rs.name+"/{id}", a.require(rs.perm+".read", rs.get(a)))
		mux.HandleFunc("POST /api/"+rs.name, a.require(rs.perm+".write", rs.save(a)))
		mux.HandleFunc("PATCH /api/"+rs.name+"/{id}", a.require(rs.perm+".write", rs.save(a)))
	}
}

func (rs resource) colByName(n string) (col, bool) {
	for _, c := range rs.cols {
		if c.name == n {
			return c, true
		}
	}
	return col{}, false
}

func (rs resource) list(a *App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		var where []string
		var args []any
		arg := func(v any) string { args = append(args, v); return "$" + strconv.Itoa(len(args)) }
		if s := strings.TrimSpace(q.Get("q")); s != "" && len(rs.search) > 0 {
			p := arg("%" + s + "%")
			var ors []string
			for _, c := range rs.search {
				ors = append(ors, c+"::text ILIKE "+p)
			}
			where = append(where, "("+strings.Join(ors, " OR ")+")")
		}
		for _, f := range rs.filters { // nomes vêm da whitelist, valores sempre parametrizados
			if v := q.Get(f); v != "" {
				if vs := strings.Split(v, ","); len(vs) > 1 {
					where = append(where, f+"::text = ANY("+arg(pqArray(vs))+"::text[])")
				} else {
					where = append(where, f+"::text = "+arg(v))
				}
			}
			if v := q.Get(f + "_from"); v != "" {
				where = append(where, f+" >= "+arg(v))
			}
			if v := q.Get(f + "_to"); v != "" {
				where = append(where, f+" <= "+arg(v))
			}
		}
		cond := ""
		if len(where) > 0 {
			cond = " WHERE " + strings.Join(where, " AND ")
		}
		limit, _ := strconv.Atoi(q.Get("limit"))
		if limit <= 0 || limit > 500 {
			limit = 100
		}
		offset, _ := strconv.Atoi(q.Get("offset"))
		var total int
		if err := a.DB.QueryRowContext(r.Context(), "SELECT count(*) FROM "+rs.view+cond, args...).Scan(&total); err != nil {
			dbErr(w, err)
			return
		}
		items, err := db.Maps(r.Context(), a.DB, fmt.Sprintf("SELECT * FROM %s%s ORDER BY %s LIMIT %d OFFSET %d", rs.view, cond, rs.order, limit, max(offset, 0)), args...)
		if err != nil {
			dbErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"items": items, "total": total})
	}
}

func pqArray(vs []string) string {
	for i, v := range vs {
		vs[i] = `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(v) + `"`
	}
	return "{" + strings.Join(vs, ",") + "}"
}

func (rs resource) get(a *App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m, err := db.One(r.Context(), a.DB, "SELECT * FROM "+rs.view+" WHERE id=$1", r.PathValue("id"))
		if err != nil {
			dbErr(w, err)
			return
		}
		if m == nil {
			httpErr(w, 404, "não encontrado")
			return
		}
		writeJSON(w, 200, m)
	}
}

// save trata POST (criação) e PATCH (atualização parcial).
func (rs resource) save(a *App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		if !readJSON(w, r, &in) {
			return
		}
		reason, _ := in["_reason"].(string)
		delete(in, "_reason")
		if rs.normalize != nil {
			if err := rs.normalize(in); err != nil {
				httpErr(w, 400, err.Error())
				return
			}
		}
		id := r.PathValue("id")
		var names, ph []string
		var args []any
		for k, v := range in {
			c, ok := rs.colByName(k)
			if !ok {
				continue // ignora campos não editáveis (status, ids, colunas da view)
			}
			s, err := toSQL(c, v)
			if err != nil {
				httpErr(w, 400, k+": "+err.Error())
				return
			}
			if s == nil && c.required {
				httpErr(w, 400, k+" é obrigatório")
				return
			}
			args = append(args, s)
			names = append(names, c.name)
			ph = append(ph, fmt.Sprintf("$%d::text::%s", len(args), sqlType(c.typ)))
		}
		if id == "" {
			for _, c := range rs.cols {
				if _, ok := in[c.name]; c.required && !ok {
					httpErr(w, 400, c.name+" é obrigatório")
					return
				}
			}
		}
		if len(names) == 0 {
			httpErr(w, 400, "nenhum campo válido")
			return
		}
		u := userFrom(r.Context())
		tx, err := a.DB.BeginTx(r.Context(), nil)
		if err != nil {
			serverErr(w, err)
			return
		}
		defer tx.Rollback()
		var before map[string]any
		var newID int64
		if id == "" {
			err = tx.QueryRowContext(r.Context(), fmt.Sprintf("INSERT INTO %s(%s) VALUES(%s) RETURNING id",
				rs.table, strings.Join(names, ","), strings.Join(ph, ",")), args...).Scan(&newID)
		} else {
			newID, _ = strconv.ParseInt(id, 10, 64)
			before, err = db.One(r.Context(), tx, "SELECT * FROM "+rs.table+" WHERE id=$1 FOR UPDATE", newID)
			if err == nil && before == nil {
				httpErr(w, 404, "não encontrado")
				return
			}
			if err == nil {
				sets := make([]string, len(names))
				for i := range names {
					sets[i] = names[i] + "=" + ph[i]
				}
				upd := ""
				if rs.table != "units" {
					upd = ", updated_at=now()"
				}
				_, err = tx.ExecContext(r.Context(), fmt.Sprintf("UPDATE %s SET %s%s WHERE id=%d", rs.table, strings.Join(sets, ","), upd, newID), args...)
			}
		}
		if err != nil {
			dbErr(w, err)
			return
		}
		after, err := db.One(r.Context(), tx, "SELECT * FROM "+rs.table+" WHERE id=$1", newID)
		if err != nil {
			dbErr(w, err)
			return
		}
		if err := tx.Commit(); err != nil {
			dbErr(w, err)
			return
		}
		action := "create"
		if before != nil {
			action = "update"
			before, after = diff(before, after)
		}
		a.logChange(r.Context(), u, action, strings.TrimSuffix(rs.name, "s"), newID, before, after, reason, clientIP(r))
		m, _ := db.One(r.Context(), a.DB, "SELECT * FROM "+rs.view+" WHERE id=$1", newID)
		writeJSON(w, map[bool]int{true: 201, false: 200}[before == nil], m)
	}
}

// diff mantém só os campos que mudaram, para o audit log ficar legível.
func diff(before, after map[string]any) (map[string]any, map[string]any) {
	b, a := map[string]any{}, map[string]any{}
	for k, v := range after {
		if k == "updated_at" {
			continue
		}
		if fmt.Sprint(before[k]) != fmt.Sprint(v) {
			b[k], a[k] = before[k], v
		}
	}
	return b, a
}

func sqlType(t string) string {
	switch t {
	case "month":
		return "date"
	case "int":
		return "bigint"
	case "bool":
		return "boolean"
	}
	return t
}

// toSQL valida e converte o valor JSON para texto que o Postgres converte no tipo da coluna.
func toSQL(c col, v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	s := strings.TrimSpace(fmt.Sprint(v))
	if s == "" {
		return nil, nil
	}
	switch c.typ {
	case "int":
		if f, ok := v.(float64); ok {
			s = strconv.FormatInt(int64(f), 10)
		}
		if _, err := strconv.ParseInt(s, 10, 64); err != nil {
			return nil, fmt.Errorf("inteiro inválido")
		}
	case "numeric":
		if f, ok := v.(float64); ok {
			s = strconv.FormatFloat(f, 'f', -1, 64)
		}
		if _, err := strconv.ParseFloat(s, 64); err != nil {
			return nil, fmt.Errorf("número inválido")
		}
	case "date":
		if _, err := time.Parse("2006-01-02", s); err != nil {
			return nil, fmt.Errorf("data inválida (AAAA-MM-DD)")
		}
	case "month": // aceita AAAA-MM ou data; grava o 1º dia do mês
		if len(s) >= 7 {
			s = s[:7] + "-01"
		}
		if _, err := time.Parse("2006-01-02", s); err != nil {
			return nil, fmt.Errorf("competência inválida (AAAA-MM)")
		}
	case "bool":
		if s != "true" && s != "false" {
			return nil, fmt.Errorf("booleano inválido")
		}
	default:
		if len(s) > 10000 {
			return nil, fmt.Errorf("texto muito longo")
		}
	}
	return s, nil
}
