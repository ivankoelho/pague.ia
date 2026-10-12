// Package br valida documentos brasileiros: CNPJ/CPF e linhas digitáveis
// de boleto bancário (47 dígitos) e de arrecadação/concessionárias (48 dígitos).
// É a verificação determinística que confere valor e vencimento sem depender da IA.
package br

import (
	"errors"
	"strings"
	"time"
)

func Digits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ValidCNPJ confere os dois dígitos verificadores. Aceita CPF (11 dígitos) também.
func ValidCNPJ(s string) bool {
	d := Digits(s)
	if len(d) == 11 {
		return validCPF(d)
	}
	if len(d) != 14 || strings.Count(d, d[:1]) == 14 {
		return false
	}
	calc := func(n int) byte {
		w := []int{6, 5, 4, 3, 2, 9, 8, 7, 6, 5, 4, 3, 2}[13-n:]
		sum := 0
		for i := 0; i < n; i++ {
			sum += int(d[i]-'0') * w[i]
		}
		r := sum % 11
		if r < 2 {
			return '0'
		}
		return byte('0' + 11 - r)
	}
	return calc(12) == d[12] && calc(13) == d[13]
}

func validCPF(d string) bool {
	if strings.Count(d, d[:1]) == 11 {
		return false
	}
	for n := 9; n <= 10; n++ {
		sum := 0
		for i := 0; i < n; i++ {
			sum += int(d[i]-'0') * (n + 1 - i)
		}
		r := sum * 10 % 11 % 10
		if byte('0'+r) != d[n] {
			return false
		}
	}
	return true
}

// Boleto é o resultado da leitura de uma linha digitável.
type Boleto struct {
	Kind    string     // "bancario" ou "arrecadacao"
	Barcode string     // 44 dígitos
	Amount  float64    // 0 quando o boleto não informa valor
	Due     *time.Time // nil quando não informado (arrecadação nunca traz)
}

var ErrInvalid = errors.New("linha digitável inválida")

// ParseLine valida os dígitos verificadores e extrai valor e vencimento.
func ParseLine(line string) (*Boleto, error) {
	d := Digits(line)
	switch len(d) {
	case 47:
		return parseBancario(d)
	case 48:
		return parseArrecadacao(d)
	}
	return nil, ErrInvalid
}

func mod10(s string) byte {
	sum, w := 0, 2
	for i := len(s) - 1; i >= 0; i-- {
		p := int(s[i]-'0') * w
		sum += p/10 + p%10
		w = 3 - w
	}
	return byte('0' + (10-sum%10)%10)
}

// mod11 com pesos 2..9; bancario=true aplica a regra do DV geral do boleto
// (resultado 0, 10 ou 11 vira 1); senão a regra de arrecadação (resto 0/1 → 0, 10 → 1).
func mod11(s string, bancario bool) byte {
	sum, w := 0, 2
	for i := len(s) - 1; i >= 0; i-- {
		sum += int(s[i]-'0') * w
		if w++; w > 9 {
			w = 2
		}
	}
	r := sum % 11
	if bancario {
		dv := 11 - r
		if dv == 0 || dv >= 10 {
			return '1'
		}
		return byte('0' + dv)
	}
	switch r {
	case 0, 1:
		return '0'
	case 10:
		return '1'
	}
	return byte('0' + 11 - r)
}

func parseBancario(d string) (*Boleto, error) {
	if mod10(d[0:9]) != d[9] || mod10(d[10:20]) != d[20] || mod10(d[21:31]) != d[31] {
		return nil, ErrInvalid
	}
	bc := d[0:4] + d[32:33] + d[33:47] + d[4:9] + d[10:20] + d[21:31]
	if mod11(bc[:4]+bc[5:], true) != bc[4] {
		return nil, ErrInvalid
	}
	b := &Boleto{Kind: "bancario", Barcode: bc, Amount: cents(bc[9:19])}
	if f := atoi(bc[5:9]); f > 0 {
		t := FactorDate(f)
		b.Due = &t
	}
	return b, nil
}

func parseArrecadacao(d string) (*Boleto, error) {
	if d[0] != '8' {
		return nil, ErrInvalid
	}
	byMod10 := d[2] == '6' || d[2] == '7'
	var bc string
	for i := 0; i < 4; i++ {
		blk, dv := d[i*12:i*12+11], d[i*12+11]
		calc := mod10(blk)
		if !byMod10 {
			calc = mod11(blk, false)
		}
		if calc != dv {
			return nil, ErrInvalid
		}
		bc += blk
	}
	b := &Boleto{Kind: "arrecadacao", Barcode: bc}
	if d[2] == '6' || d[2] == '8' { // valor efetivo em reais
		b.Amount = cents(bc[4:15])
	}
	return b, nil
}

// FactorDate converte o fator de vencimento. A FEBRABAN reiniciou o fator em
// 22/02/2025 (fator 1000), depois que 9999 foi atingido em 21/02/2025.
func FactorDate(f int) time.Time {
	base := time.Date(2025, 2, 22, 0, 0, 0, 0, time.UTC)
	return base.AddDate(0, 0, f-1000)
}

func cents(s string) float64 { return float64(atoi(s)) / 100 }

func atoi(s string) int {
	n := 0
	for _, r := range s {
		n = n*10 + int(r-'0')
	}
	return n
}

// WithCNPJDV completa 12 dígitos-base com os dois verificadores (usado em dados de demonstração).
func WithCNPJDV(base12 string) string {
	d := Digits(base12)
	for n := 12; n <= 13; n++ {
		w := []int{6, 5, 4, 3, 2, 9, 8, 7, 6, 5, 4, 3, 2}[13-n:]
		sum := 0
		for i := 0; i < n; i++ {
			sum += int(d[i]-'0') * w[i]
		}
		r := sum % 11
		dv := byte('0')
		if r >= 2 {
			dv = byte('0' + 11 - r)
		}
		d += string(dv)
	}
	return d
}
