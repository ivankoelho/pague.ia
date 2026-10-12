package automation

import (
	"encoding/json"
	"testing"
)

func TestMatch(t *testing.T) {
	var conds []Cond
	json.Unmarshal([]byte(`[{"field":"to","op":"in","value":["BLOCKED","REVIEW"]},{"field":"days_left","op":"lte","value":7}]`), &conds)
	cases := []struct {
		facts map[string]any
		want  bool
	}{
		{map[string]any{"to": "BLOCKED", "days_left": int64(3)}, true},
		{map[string]any{"to": "REVIEW", "days_left": 7.0}, true},
		{map[string]any{"to": "APPROVED", "days_left": 1}, false},
		{map[string]any{"to": "BLOCKED", "days_left": 10}, false},
		{map[string]any{"to": "BLOCKED"}, false},
	}
	for i, c := range cases {
		if got, err := Match(conds, c.facts); err != nil || got != c.want {
			t.Errorf("caso %d: got %v err %v", i, got, err)
		}
	}
	if _, err := Match([]Cond{{Field: "x", Op: "??"}}, nil); err == nil {
		t.Error("operador inválido deveria dar erro")
	}
}

func TestRender(t *testing.T) {
	s, err := Render("Serviço: {{.service_name}} vence {{.due_on}}", map[string]any{"service_name": "SIP", "due_on": "2026-11-15"})
	if err != nil || s != "Serviço: SIP vence 2026-11-15" {
		t.Fatalf("%q %v", s, err)
	}
}
