package brain

import (
	"testing"
	"time"

	"home-assistent-go/internal/config"
)

func TestActiveModel(t *testing.T) { // §3: LLM_MODEL > OLLAMA_MODEL (ollama) > default
	casos := []struct {
		nome        string
		provider    string
		llmModel    string
		ollamaModel string
		want        string
	}{
		{"LLM_MODEL vence (gemini)", "gemini", "xpto", "", "xpto"},
		{"LLM_MODEL vence (ollama)", "ollama", "xpto", "llama-velho", "xpto"},
		{"OLLAMA_MODEL é fallback histórico (ollama)", "ollama", "", "llama-2", "llama-2"},
		{"default gemini (§3)", "gemini", "", "", "gemini-3.5-flash"},
		{"default openai (§3)", "openai", "", "", "gpt-4o-mini"},
		{"default ollama (§3)", "ollama", "", "", "llama3.2:3b"},
		{"provider desconhecido devolve vazio (Setup valida antes)", "vertex", "", "", ""},
	}
	for _, tc := range casos {
		cfg := config.Settings{
			LLMProvider:   tc.provider,
			LLMModel:      tc.llmModel,
			OllamaModel:   tc.ollamaModel,
			OllamaBaseURL: "http://127.0.0.1:11434",
			LLMTimeout:    30 * time.Second,
		}
		if got := ActiveModel(cfg); got != tc.want {
			t.Errorf("%s: ActiveModel() = %q; want %q", tc.nome, got, tc.want)
		}
	}
}

func TestModelName(t *testing.T) { // §2: endereçamento por nome no registry
	casos := []struct{ provider, model, want string }{
		{"gemini", "gemini-3.5-flash", "googleai/gemini-3.5-flash"},
		{"openai", "gpt-4o-mini", "openai/gpt-4o-mini"},
		{"ollama", "llama3.2:3b", "ollama/llama3.2:3b"},
		{"gemini", "xpto", "googleai/xpto"}, // gemini com LLM_MODEL=xpto (critério 3)
	}
	for _, tc := range casos {
		if got := modelName(tc.provider, tc.model); got != tc.want {
			t.Errorf("modelName(%q, %q) = %q; want %q", tc.provider, tc.model, got, tc.want)
		}
	}
}
