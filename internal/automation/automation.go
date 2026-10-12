// Package automation avalia regras WHEN (evento + condições) THEN (ação).
// As regras ficam no banco; este pacote só decide se uma regra casa e
// renderiza o texto. A execução das ações fica na aplicação.
package automation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"text/template"
)

type Cond struct {
	Field string `json:"field"`
	Op    string `json:"op"` // eq ne gt gte lt lte in contains
	Value any    `json:"value"`
}

type Rule struct {
	ID         int64           `json:"id"`
	Name       string          `json:"name"`
	Event      string          `json:"event"`
	Conditions []Cond          `json:"conditions"`
	Action     string          `json:"action"` // notify | transition
	Params     json.RawMessage `json:"params"`
}

// Match é verdadeiro quando todas as condições valem para os fatos.
func Match(conds []Cond, facts map[string]any) (bool, error) {
	for _, c := range conds {
		ok, err := eval(c, facts[c.Field])
		if err != nil || !ok {
			return false, err
		}
	}
	return true, nil
}

func eval(c Cond, got any) (bool, error) {
	switch c.Op {
	case "eq":
		return equal(got, c.Value), nil
	case "ne":
		return !equal(got, c.Value), nil
	case "in":
		list, ok := c.Value.([]any)
		if !ok {
			return false, fmt.Errorf("op in exige lista")
		}
		for _, v := range list {
			if equal(got, v) {
				return true, nil
			}
		}
		return false, nil
	case "gt", "gte", "lt", "lte":
		a, ok1 := num(got)
		b, ok2 := num(c.Value)
		if !ok1 || !ok2 {
			return false, nil
		}
		switch c.Op {
		case "gt":
			return a > b, nil
		case "gte":
			return a >= b, nil
		case "lt":
			return a < b, nil
		}
		return a <= b, nil
	}
	return false, fmt.Errorf("operador desconhecido: %s", c.Op)
}

func equal(a, b any) bool {
	if x, ok := num(a); ok {
		if y, ok := num(b); ok {
			return x == y
		}
	}
	return fmt.Sprint(a) == fmt.Sprint(b)
}

func num(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(x, 64)
		return f, err == nil
	}
	return 0, false
}

// Render aplica os fatos ao template de texto da regra.
func Render(tpl string, facts map[string]any) (string, error) {
	t, err := template.New("").Option("missingkey=zero").Parse(tpl)
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	err = t.Execute(&b, facts)
	return b.String(), err
}
