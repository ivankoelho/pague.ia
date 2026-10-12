package br

import (
	"fmt"
	"testing"
	"time"
)

// makeBancario monta uma linha digitável válida a partir dos campos do código de barras.
func makeBancario(bankCur string, factor int, value int64, free string) string {
	rest := fmt.Sprintf("%04d%010d%s", factor, value, free)
	dv := mod11(bankCur+rest, true)
	bc := bankCur + string(dv) + rest
	f1 := bc[0:4] + bc[19:24]
	f2 := bc[24:34]
	f3 := bc[34:44]
	return f1 + string(mod10(f1)) + f2 + string(mod10(f2)) + f3 + string(mod10(f3)) + string(bc[4]) + bc[5:19]
}

func makeArrecadacao(segVal string, value int64, rest string, useMod10 bool) string {
	head := "8" + segVal // ex.: "36" → segmento 3, ident. valor 6
	body := fmt.Sprintf("%011d%s", value, rest)
	// o DV geral (posição 4) é calculado sobre o código sem ele
	pre := head + body
	dv := mod11(pre, false)
	if useMod10 {
		dv = mod10(pre)
	}
	bc := head + string(dv) + body
	line := ""
	for i := 0; i < 4; i++ {
		blk := bc[i*11 : i*11+11]
		c := mod11(blk, false)
		if useMod10 {
			c = mod10(blk)
		}
		line += blk + string(c)
	}
	return line
}

func TestBoletoBancario(t *testing.T) {
	due := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	factor := 1000 + int(due.Sub(time.Date(2025, 2, 22, 0, 0, 0, 0, time.UTC)).Hours()/24)
	line := makeBancario("0019", factor, 149780, "0000002812345678901234567")
	b, err := ParseLine(line)
	if err != nil {
		t.Fatal(err)
	}
	if b.Amount != 1497.80 || !b.Due.Equal(due) || len(b.Barcode) != 44 {
		t.Fatalf("got %+v", b)
	}
	// qualquer dígito alterado tem de invalidar
	for _, pos := range []int{0, 12, 25, 40, 46} {
		bad := []byte(line)
		bad[pos] = '0' + (bad[pos]-'0'+1)%10
		if _, err := ParseLine(string(bad)); err == nil {
			t.Fatalf("mutação na posição %d passou", pos)
		}
	}
}

func TestBoletoArrecadacao(t *testing.T) {
	for _, mod10 := range []bool{true, false} {
		seg := "8" // mod11
		if mod10 {
			seg = "6"
		}
		line := makeArrecadacao("3"+seg, 25990, "00010123456789012345678901234"[:29], mod10)
		b, err := ParseLine(line)
		if err != nil {
			t.Fatalf("mod10=%v: %v", mod10, err)
		}
		if b.Amount != 259.90 || b.Due != nil {
			t.Fatalf("got %+v", b)
		}
		bad := []byte(line)
		bad[5] = '0' + (bad[5]-'0'+1)%10
		if _, err := ParseLine(string(bad)); err == nil {
			t.Fatal("mutação passou")
		}
	}
}

func TestCNPJ(t *testing.T) {
	cases := map[string]bool{
		"11.222.333/0001-81": true,
		"11.222.333/0001-82": false,
		"00000000000000":     false,
		"529.982.247-25":     true, // CPF
		"123":                false,
	}
	for in, want := range cases {
		if ValidCNPJ(in) != want {
			t.Errorf("%s: want %v", in, want)
		}
	}
}

func TestWithCNPJDV(t *testing.T) {
	if got := WithCNPJDV("112223330001"); got != "11222333000181" || !ValidCNPJ(got) {
		t.Fatal(got)
	}
}
