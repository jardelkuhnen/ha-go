package brain

import (
	"strings"
	"testing"
)

func TestSystemPromptFor(t *testing.T) { // §4/§5: roteamento literal por source
	casos := []struct {
		source string
		want   string
	}{
		{"telegram", systemPromptTelegram},
		{"satellite", systemPromptVoz},
		{"", systemPromptVoz},          // default voz
		{"alexa", systemPromptVoz},     // valor desconhecido → voz (regressão zero)
		{"Telegram", systemPromptVoz},  // sem normalização: comparação literal
		{" telegram", systemPromptVoz}, // sem trim: o flow não normaliza
	}
	for _, tc := range casos {
		if got := systemPromptFor(tc.source); got != tc.want {
			t.Errorf("systemPromptFor(%q) devolveu o prompt errado (voz=%v)", tc.source, got == systemPromptVoz)
		}
	}
}

func TestPromptsCumpremA5(t *testing.T) { // §5: regras obrigatórias nos textos
	// Voz: texto plano, frases curtas, clima por ferramenta, honestidade.
	for _, regra := range []string{"Markdown", "ferramenta", "clima", "breve"} {
		if !strings.Contains(systemPromptVoz, regra) {
			t.Errorf("systemPromptVoz não menciona %q", regra)
		}
	}
	if !strings.Contains(systemPromptVoz, "get_weather") {
		t.Errorf("systemPromptVoz não nomeia a ferramenta get_weather")
	}
	// Telegram: Markdown permitido, sem JSON, mesmas regras de clima e honestidade.
	for _, regra := range []string{"Markdown", "JSON", "ferramenta", "clima"} {
		if !strings.Contains(systemPromptTelegram, regra) {
			t.Errorf("systemPromptTelegram não menciona %q", regra)
		}
	}
	if !strings.Contains(systemPromptTelegram, "get_weather") {
		t.Errorf("systemPromptTelegram não nomeia a ferramenta get_weather")
	}
}

func TestPromptsSemMencaoABusca(t *testing.T) { // decisão 10: sem "busca"
	for nome, p := range map[string]string{"voz": systemPromptVoz, "telegram": systemPromptTelegram} {
		for _, proibida := range []string{"busca", "Busca", "search", "Search", "Tavily"} {
			if strings.Contains(p, proibida) {
				t.Errorf("prompt %s menciona %q (decisão 10)", nome, proibida)
			}
		}
	}
}
