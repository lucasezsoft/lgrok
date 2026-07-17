package main

import "testing"

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
