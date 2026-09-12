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
