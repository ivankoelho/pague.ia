// Package ai extrai dados estruturados de faturas. A IA é opcional: sem
// provider configurado (ou com falha), Extract usa regras por regex e a
// linha digitável. O resultado sempre passa pelo motor de auditoria depois.
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ivankoelho/pague.ia/internal/br"
)

// Provider é qualquer LLM que responda JSON a um par system/user.
// Groq implementado; OpenAI usa o mesmo cliente (API compatível); Anthropic/local entram aqui.
type Provider interface {
	Name() string
	Model() string
	CompleteJSON(ctx context.Context, system, user string) (string, error)
}

// OpenAICompat atende Groq e qualquer API no formato /chat/completions da OpenAI.
type OpenAICompat struct {
	Provider_ string
	BaseURL   string
	APIKey    string
	Model_    string
	HTTP      *http.Client
}

func NewGroq(key, model string) *OpenAICompat {
	if model == "" {
		model = "llama-3.3-70b-versatile"
	}
	return &OpenAICompat{Provider_: "groq", BaseURL: "https://api.groq.com/openai/v1", APIKey: key, Model_: model,
		HTTP: &http.Client{Timeout: 60 * time.Second}}
}

func (o *OpenAICompat) Name() string  { return o.Provider_ }
func (o *OpenAICompat) Model() string { return o.Model_ }

func (o *OpenAICompat) CompleteJSON(ctx context.Context, system, user string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"model":           o.Model_,
		"temperature":     0,
		"response_format": map[string]string{"type": "json_object"},
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
	})
	req, _ := http.NewRequestWithContext(ctx, "POST", o.BaseURL+"/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+o.APIKey)
	req.Header.Set("Content-Type", "application/json")
	res, err := o.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode != 200 {
		return "", fmt.Errorf("%s: HTTP %d: %.300s", o.Provider_, res.StatusCode, raw)
	}
	var out struct {
		Choices []struct {
			Message struct{ Content string } `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || len(out.Choices) == 0 {
		return "", fmt.Errorf("%s: resposta inesperada: %.300s", o.Provider_, raw)
	}
	return out.Choices[0].Message.Content, nil
}

// Extraction são os campos que interessam para identificar e auditar a fatura.
type Extraction struct {
	DocType       string  `json:"doc_type"`
	SupplierName  string  `json:"supplier_name"`
	TaxID         string  `json:"tax_id"`
	Competence    string  `json:"competence"` // YYYY-MM
	IssuedOn      string  `json:"issued_on"`  // YYYY-MM-DD
	DueOn         string  `json:"due_on"`     // YYYY-MM-DD
	Amount        float64 `json:"amount"`
	DocNumber     string  `json:"doc_number"`
	DigitableLine string  `json:"digitable_line"`
	Pix           string  `json:"pix"`
	ServiceHint   string  `json:"service_hint"`
	Confidence    float64 `json:"confidence"`
}

const PromptVersion = "extract-v1"

const systemPrompt = `Você extrai dados de faturas, boletos e notas fiscais brasileiras.
Responda SOMENTE um objeto JSON com as chaves:
doc_type ("fatura"|"boleto"|"nf"|"nfse"|"recibo"|"outro"), supplier_name, tax_id (CNPJ do emissor/beneficiário, só dígitos),
competence (mês de referência do serviço, "YYYY-MM"), issued_on ("YYYY-MM-DD"), due_on ("YYYY-MM-DD"),
amount (número, total a pagar, ponto decimal), doc_number, digitable_line (só dígitos), pix (copia-e-cola ou chave),
service_hint (descrição curta do serviço cobrado), confidence (0 a 1).
Use "" ou 0 quando o dado não estiver no texto. Nunca invente valores.`

// Run registra uma chamada de IA (o que a aplicação grava em ai_runs).
type Run struct {
	Provider, Model, PromptVersion, Input, Output, Error string
	OK                                                   bool
	Duration                                             time.Duration
}

// Extract combina IA (se houver) com regras determinísticas. Campos que a
// linha digitável comprova (valor, vencimento) prevalecem sobre a IA.
func Extract(ctx context.Context, p Provider, text string) (Extraction, *Run) {
	ex := Rules(text)
	if p == nil || strings.TrimSpace(text) == "" {
		return ex, nil
	}
	input := text
	if len(input) > 24000 {
		input = input[:24000]
	}
	run := &Run{Provider: p.Name(), Model: p.Model(), PromptVersion: PromptVersion, Input: input}
	start := time.Now()
	out, err := p.CompleteJSON(ctx, systemPrompt, input)
	run.Duration, run.Output = time.Since(start), out
	var got Extraction
	if err == nil {
		err = json.Unmarshal([]byte(out), &got)
	}
	if err != nil {
		run.Error = err.Error()
		return ex, run
	}
	run.OK = true
	return merge(got, ex), run
}

func merge(ai, rules Extraction) Extraction {
	ai.TaxID = br.Digits(ai.TaxID)
	ai.DigitableLine = br.Digits(ai.DigitableLine)
	if ai.DigitableLine == "" || !validLine(ai.DigitableLine) {
		ai.DigitableLine = rules.DigitableLine
	}
	if ai.TaxID == "" || !br.ValidCNPJ(ai.TaxID) {
		ai.TaxID = rules.TaxID
	}
	if b, err := br.ParseLine(ai.DigitableLine); err == nil {
		if b.Amount > 0 {
			ai.Amount = b.Amount
		}
		if b.Due != nil {
			ai.DueOn = b.Due.Format("2006-01-02")
		}
	}
	if ai.Amount == 0 {
		ai.Amount = rules.Amount
	}
	for _, f := range [][2]*string{{&ai.DueOn, &rules.DueOn}, {&ai.Competence, &rules.Competence}, {&ai.Pix, &rules.Pix}, {&ai.DocNumber, &rules.DocNumber}} {
		if *f[0] == "" {
			*f[0] = *f[1]
		}
	}
	if !validDate(ai.DueOn) {
		ai.DueOn = rules.DueOn
	}
	if !validMonth(ai.Competence) {
		ai.Competence = rules.Competence
	}
	return ai
}

var (
	reCNPJ      = regexp.MustCompile(`\b\d{2}\.?\d{3}\.?\d{3}/?\d{4}-?\d{2}\b`)
	reBancario  = regexp.MustCompile(`\d{5}[.\s]?\d{5}\s*\d{5}[.\s]?\d{6}\s*\d{5}[.\s]?\d{6}\s*\d\s*\d{14}`)
	reArrecad   = regexp.MustCompile(`8\d{10}[-\s]?\d\s*\d{11}[-\s]?\d\s*\d{11}[-\s]?\d\s*\d{11}[-\s]?\d`)
	reMoney     = regexp.MustCompile(`(?i)(total a pagar|valor a pagar|valor do documento|valor total|total)[^\d\n]{0,30}?(\d{1,3}(?:\.\d{3})*,\d{2})`)
	reAnyMoney  = regexp.MustCompile(`R\$\s*(\d{1,3}(?:\.\d{3})*,\d{2})`)
	reDue       = regexp.MustCompile(`(?i)vencimento[^\d\n]{0,30}?(\d{2})/(\d{2})/(\d{4})`)
	reComp      = regexp.MustCompile(`(?i)(refer[êe]ncia|compet[êe]ncia|m[êe]s ref\.?|per[íi]odo)[^\d\n]{0,20}?(\d{2})/(\d{4})`)
	reDocNumber = regexp.MustCompile(`(?i)(fatura|n[ºo°]\.?\s*(?:do\s*)?documento|nota fiscal)\s*(?:n[ºo°.]*)?\s*:?\s*([A-Z0-9-]{4,20})`)
	rePix       = regexp.MustCompile(`000201\S{40,}`)
)

// Rules extrai por regex, sem IA. É o fallback e também a âncora de validação.
func Rules(text string) Extraction {
	var ex Extraction
	for _, m := range reCNPJ.FindAllString(text, -1) {
		if br.ValidCNPJ(m) {
			ex.TaxID = br.Digits(m)
			break
		}
	}
	for _, re := range []*regexp.Regexp{reBancario, reArrecad} {
		for _, m := range re.FindAllString(text, -1) {
			if validLine(m) {
				ex.DigitableLine = br.Digits(m)
				break
			}
		}
		if ex.DigitableLine != "" {
			break
		}
	}
	if m := reMoney.FindStringSubmatch(text); m != nil {
		ex.Amount = brMoney(m[2])
	} else {
		var vals []float64
		for _, m := range reAnyMoney.FindAllStringSubmatch(text, -1) {
			vals = append(vals, brMoney(m[1]))
		}
		if len(vals) > 0 {
			sort.Float64s(vals)
			ex.Amount = vals[len(vals)-1] // ponytail: maior R$ do texto, bom o bastante como fallback
		}
	}
	if m := reDue.FindStringSubmatch(text); m != nil {
		ex.DueOn = m[3] + "-" + m[2] + "-" + m[1]
	}
	if m := reComp.FindStringSubmatch(text); m != nil {
		ex.Competence = m[3] + "-" + m[2]
	}
	if m := reDocNumber.FindStringSubmatch(text); m != nil {
		ex.DocNumber = m[2]
	}
	ex.Pix = rePix.FindString(text)
	if b, err := br.ParseLine(ex.DigitableLine); err == nil {
		if b.Amount > 0 {
			ex.Amount = b.Amount
		}
		if b.Due != nil {
			ex.DueOn = b.Due.Format("2006-01-02")
		}
	}
	return ex
}

func validLine(s string) bool { _, err := br.ParseLine(s); return err == nil }

func validDate(s string) bool { _, err := time.Parse("2006-01-02", s); return err == nil }

func validMonth(s string) bool { _, err := time.Parse("2006-01", s); return err == nil }

func brMoney(s string) float64 {
	f, _ := strconv.ParseFloat(strings.ReplaceAll(strings.ReplaceAll(s, ".", ""), ",", "."), 64)
	return f
}
