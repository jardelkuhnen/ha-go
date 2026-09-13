package agent

import (
	"strings"
	"testing"
)

func TestSystemPromptCumpreRegras(t *testing.T) {
	// Prompt unificado (voz aplicado aos dois canais): texto plano, frases
	// curtas, clima/dispositivos só via tools, honestidade, sem Markdown/JSON.
	for _, regra := range []string{"português", "texto plano", "ferramenta", "clima", "breve"} {
		if !strings.Contains(systemPrompt, regra) {
			t.Errorf("systemPrompt não menciona %q", regra)
		}
	}
	if !strings.Contains(systemPrompt, "get_weather") {
		t.Errorf("systemPrompt não nomeia a ferramenta get_weather")
	}
	if !strings.Contains(systemPrompt, "control_device") {
		t.Errorf("systemPrompt não nomeia a ferramenta control_device")
	}
	// Sem Markdown: o prompt proíbe, não permite.
	for _, proibida := range []string{"Markdown é permitido", "Markdown permitido"} {
		if strings.Contains(systemPrompt, proibida) {
			t.Errorf("systemPrompt permite Markdown (%q) — deve ser texto plano", proibida)
		}
	}
}

func TestSystemPromptSemMencaoABusca(t *testing.T) { // decisão 10: sem "busca"
	for _, proibida := range []string{"busca", "Busca", "search", "Search", "Tavily"} {
		if strings.Contains(systemPrompt, proibida) {
			t.Errorf("systemPrompt menciona %q (decisão 10)", proibida)
		}
	}
}
