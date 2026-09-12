package config

import (
	"strings"
	"testing"
	"time"
)

func TestParseTimeoutAceitaFormatos(t *testing.T) { // critério 5
	casos := []struct {
		raw  string
		want time.Duration
	}{
		{"30", 30 * time.Second},    // fallback numérico em segundos
		{"30.0", 30 * time.Second},  // formato do .env do Python
		{"90s", 90 * time.Second},   // time.ParseDuration
		{"1m30s", 90 * time.Second}, // ParseDuration composto
		{" 5s ", 5 * time.Second},   // tolera espaços
	}
	for _, tc := range casos {
		d, err := parseTimeout("LLM_TIMEOUT_S", tc.raw)
		if err != nil {
			t.Fatalf("parseTimeout(%q): erro inesperado: %v", tc.raw, err)
		}
		if d != tc.want {
			t.Errorf("parseTimeout(%q) = %v; want %v", tc.raw, d, tc.want)
		}
	}
}

func TestParseTimeoutRejeitaInvalidos(t *testing.T) { // critério 5
	for _, raw := range []string{"0", "-5", "-5s", "0s", "abc", ""} {
		d, err := parseTimeout("LLM_TIMEOUT_S", raw)
		if err == nil {
			t.Errorf("parseTimeout(%q) = %v; want erro", raw, d)
			continue
		}
		if !strings.Contains(err.Error(), "LLM_TIMEOUT_S") {
			t.Errorf("erro não cita a variável: %v", err)
		}
	}
}

func TestParseURL(t *testing.T) {
	if _, err := parseURL("HA_URL", "http://home.local:8123"); err != nil {
		t.Errorf("http válido rejeitado: %v", err)
	}
	if _, err := parseURL("HA_URL", "https://ha.example.com"); err != nil {
		t.Errorf("https válido rejeitado: %v", err)
	}
	for _, raw := range []string{"notaurl", "home.local:8123", "ftp://home.local"} {
		if _, err := parseURL("HA_URL", raw); err == nil {
			t.Errorf("parseURL(%q): want erro, veio nil", raw)
		}
	}
}
