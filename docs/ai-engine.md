# Motor de IA

## Papel

A IA **interpreta, classifica e extrai**. Ela não aprova nem bloqueia: o resultado sempre passa pelo motor de auditoria.

## Interface

```go
type Provider interface {
    Name() string
    Model() string
    CompleteJSON(ctx context.Context, system, user string) (string, error)
}
```

- **Groq**: `ai.NewGroq(GROQ_API_KEY, GROQ_MODEL)` (padrão `llama-3.3-70b-versatile`), saída em JSON (`response_format: json_object`), temperatura 0.
- **OpenAI** e outros compatíveis: o mesmo cliente `OpenAICompat` com outra `BaseURL`.
- **Anthropic / modelo local**: nova implementação da interface.

Sem `GROQ_API_KEY`, `App.AI` é `nil` e a extração usa só regras.

## Extração (`ai.Extract`)

1. `Rules(text)`: regex para CNPJ (com DV válido), linha digitável (válida), total, vencimento, competência, nº do documento e Pix.
2. Se houver provider: prompt `extract-v1` pede `doc_type, supplier_name, tax_id, competence, issued_on, due_on, amount, doc_number, digitable_line, pix, service_hint, confidence`.
3. `merge`: CNPJ e linha digitável só são aceitos se válidos; **valor e vencimento da linha digitável prevalecem** sobre a IA; campos vazios ou inválidos caem para as regras.
4. Falha do provider (rede, HTTP, JSON inválido) é registrada e a extração segue só com regras.

## Rastreabilidade

Cada chamada grava em `ai_runs`: provedor, modelo, `prompt_version`, entrada (até 24 mil caracteres), resposta, sucesso/erro e duração. A tela da fatura mostra as execuções e a resposta bruta.

## Testes

`internal/ai/testdata/fatura_vivo_demo.txt` é o documento de referência (fictício). Os testes validam a extração por regras, a extração com um Groq simulado que erra valor e vencimento (a linha digitável corrige) e o fallback quando o provider falha.

## Próximos usos (roadmap)

Explicação de divergências e resumo da auditoria em linguagem natural, sempre como texto auxiliar, nunca como decisão.
