package main

import (
	"regexp"
	"testing"
)

func TestNormalizeSub(t *testing.T) {
	cases := map[string]string{
		"lucas":                     "lucas",
		"lucas.uberlandia.dev.br":   "lucas",
		"  Lucas  ":                 "lucas",
		"MeuApp.example.com":        "meuapp",
		"https://lucas.example.com": "lucas",
		"":                          "",
	}
	for in, want := range cases {
		if got := normalizeSub(in); got != want {
			t.Errorf("normalizeSub(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAnimalName(t *testing.T) {
	re := regexp.MustCompile(`^[a-z]+-[1-9][0-9]{3}$`) // ex.: capivara-4821
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		n := animalName()
		if !re.MatchString(n) {
			t.Fatalf("nome inválido: %q", n)
		}
		if normalizeSub(n) != n {
			t.Fatalf("animalName produziu algo que normalizeSub altera: %q", n)
		}
		seen[n] = true
	}
	if len(seen) < 10 { // deve variar
		t.Fatalf("pouca variação: só %d nomes distintos em 50", len(seen))
	}
}
