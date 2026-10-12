// Package db abre a conexão PostgreSQL, aplica migrations embutidas e
// converte linhas em mapas JSON-friendly.
package db

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"time"

	_ "github.com/lib/pq"
)

//go:embed migrations/*.sql
var migrations embed.FS

func Open(ctx context.Context, url string) (*sql.DB, error) {
	d, err := sql.Open("postgres", url)
	if err != nil {
		return nil, err
	}
	d.SetMaxOpenConns(20)
	d.SetConnMaxIdleTime(5 * time.Minute)
	for i := 0; ; i++ { // espera o Postgres subir (docker compose)
		if err = d.PingContext(ctx); err == nil || i == 30 {
			return d, err
		}
		time.Sleep(time.Second)
	}
}

// Migrate aplica, em ordem e uma única vez, cada arquivo de migrations/.
func Migrate(ctx context.Context, d *sql.DB) error {
	if _, err := d.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations(name text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	names, _ := fs.Glob(migrations, "migrations/*.sql")
	sort.Strings(names)
	for _, n := range names {
		tx, err := d.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		// lock global: duas instâncias subindo juntas não aplicam a mesma migration
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(7426)`); err != nil {
			tx.Rollback()
			return err
		}
		var done bool
		tx.QueryRowContext(ctx, `SELECT true FROM schema_migrations WHERE name=$1`, n).Scan(&done)
		if !done {
			b, _ := migrations.ReadFile(n)
			if _, err := tx.ExecContext(ctx, string(b)); err != nil {
				tx.Rollback()
				return fmt.Errorf("%s: %w", n, err)
			}
			tx.ExecContext(ctx, `INSERT INTO schema_migrations(name) VALUES($1)`, n)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// Queryer é satisfeito por *sql.DB e *sql.Tx.
type Queryer interface {
	QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row
}

// Maps executa a consulta e devolve cada linha como map coluna→valor,
// com numeric como float64, date como "YYYY-MM-DD" e jsonb como JSON cru.
func Maps(ctx context.Context, q Queryer, query string, args ...any) ([]map[string]any, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, _ := rows.ColumnTypes()
	out := []map[string]any{}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		m := make(map[string]any, len(cols))
		for i, c := range cols {
			m[c.Name()] = convert(c.DatabaseTypeName(), vals[i])
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// One devolve a primeira linha ou nil.
func One(ctx context.Context, q Queryer, query string, args ...any) (map[string]any, error) {
	rs, err := Maps(ctx, q, query, args...)
	if err != nil || len(rs) == 0 {
		return nil, err
	}
	return rs[0], nil
}

func convert(typ string, v any) any {
	switch x := v.(type) {
	case []byte:
		switch typ {
		case "NUMERIC":
			f, _ := strconv.ParseFloat(string(x), 64)
			return f
		case "JSONB", "JSON":
			return json.RawMessage(x)
		}
		return string(x)
	case time.Time:
		if typ == "DATE" {
			return x.Format("2006-01-02")
		}
		return x
	}
	return v
}
