package brain

import (
	"strings"
	"testing"
	"time"

	"github.com/firebase/genkit/go/plugins/googlegenai"
	"github.com/firebase/genkit/go/plugins/ollama"

	openai "github.com/firebase/genkit/go/plugins/compat_oai/openai"

	"home-assistent-go/internal/config"
)

func cfgBrain(provider string) config.Settings {
	return config.Settings{
		LLMProvider:   provider,
		GeminiAPIKey:  "gk-test",
		OpenAIAPIKey:  "sk-test",
		OpenAIAPIBase: "https://proxy.example.com/v1",
		OllamaBaseURL: "http://127.0.0.1:11434",
		LLMTimeout:    30 * time.Second,
	}
}

func TestPluginForMontaSoOPluginEscolhido(t *testing.T) { // registro condicional (§2)
	// gemini
	p, err := pluginFor(cfgBrain("gemini"))
	if err != nil {
		t.Fatalf("gemini: pluginFor: erro inesperado: %v", err)
	}
	if got := p.Name(); got != "googleai" {
		t.Errorf("gemini: Name() = %q; want googleai", got)
	}
	g, ok := p.(*googlegenai.GoogleAI)
	if !ok {
		t.Fatalf("gemini: tipo = %T; want *googlegenai.GoogleAI", p)
	}
	if g.APIKey != "gk-test" {
		t.Errorf("gemini: APIKey não propagada")
	}

	// openai (caminho real v1.13.1: compat_oai/openai; BaseURL via Opts)
	p, err = pluginFor(cfgBrain("openai"))
	if err != nil {
		t.Fatalf("openai: pluginFor: erro inesperado: %v", err)
	}
	if got := p.Name(); got != "openai" {
		t.Errorf("openai: Name() = %q; want openai", got)
	}
	o, ok := p.(*openai.OpenAI)
	if !ok {
		t.Fatalf("openai: tipo = %T; want *openai.OpenAI", p)
	}
	if o.APIKey != "sk-test" {
		t.Errorf("openai: APIKey não propagada")
	}
	if len(o.Opts) != 1 { // option.WithBaseURL(OPENAI_API_BASE); o valor é opaco ao SDK
		t.Errorf("openai: Opts = %d itens; want 1 (WithBaseURL)", len(o.Opts))
	}

	// ollama
	p, err = pluginFor(cfgBrain("ollama"))
	if err != nil {
		t.Fatalf("ollama: pluginFor: erro inesperado: %v", err)
	}
	if got := p.Name(); got != "ollama" {
		t.Errorf("ollama: Name() = %q; want ollama", got)
	}
	ol, ok := p.(*ollama.Ollama)
	if !ok {
		t.Fatalf("ollama: tipo = %T; want *ollama.Ollama", p)
	}
	if ol.ServerAddress != "http://127.0.0.1:11434" {
		t.Errorf("ollama: ServerAddress não propagado")
	}
	if ol.Timeout != 30 { // Timeout é int em segundos
		t.Errorf("ollama: Timeout = %d; want 30", ol.Timeout)
	}
}

func TestPluginForOpenAISemBaseURL(t *testing.T) { // OPENAI_API_BASE é opcional (§2)
	cfg := cfgBrain("openai")
	cfg.OpenAIAPIBase = ""
	p, err := pluginFor(cfg)
	if err != nil {
		t.Fatalf("pluginFor: erro inesperado: %v", err)
	}
	if o := p.(*openai.OpenAI); len(o.Opts) != 0 {
		t.Errorf("Opts = %d itens; want 0 sem OPENAI_API_BASE", len(o.Opts))
	}
}

func TestPluginForFalhaRapidaSemCredencial(t *testing.T) { // §2 critério 2: erro claro, não panico
	casos := []struct {
		nome string
		cfg  func() config.Settings
		want string
	}{
		{"gemini sem GEMINI_API_KEY", func() config.Settings { c := cfgBrain("gemini"); c.GeminiAPIKey = ""; return c },
			"brain: GEMINI_API_KEY é obrigatória quando LLM_PROVIDER=gemini"},
		{"openai sem OPENAI_API_KEY", func() config.Settings { c := cfgBrain("openai"); c.OpenAIAPIKey = ""; return c },
			"brain: OPENAI_API_KEY é obrigatória quando LLM_PROVIDER=openai"},
		{"ollama sem OLLAMA_BASE_URL", func() config.Settings { c := cfgBrain("ollama"); c.OllamaBaseURL = ""; return c },
			"brain: OLLAMA_BASE_URL é obrigatória quando LLM_PROVIDER=ollama"},
		{"provider desconhecido", func() config.Settings { return cfgBrain("vertex") },
			`brain: LLM_PROVIDER inválido: "vertex"`},
	}
	for _, tc := range casos {
		p, err := pluginFor(tc.cfg())
		if p != nil || err == nil || err.Error() != tc.want {
			t.Errorf("%s: pluginFor = (%v, %v); want (nil, %q)", tc.nome, p, err, tc.want)
		}
		if err != nil && (strings.Contains(err.Error(), "gk-test") || strings.Contains(err.Error(), "sk-test")) {
			t.Errorf("%s: erro vazou secret: %v", tc.nome, err)
		}
	}
}

func TestTimeoutSeconds(t *testing.T) { // §4: Timeout do plugin ollama em segundos inteiros
	casos := []struct {
		nome string
		d    time.Duration
		want int
	}{
		{"30s → 30", 30 * time.Second, 30},
		{"90.5s trunca para 90", 90500 * time.Millisecond, 90},
		{"sub-segundo → piso 1 (0 faria o plugin aplicar o default 30s dele)", 500 * time.Millisecond, 1},
		{"zero → piso 1", 0, 1},
	}
	for _, tc := range casos {
		if got := timeoutSeconds(tc.d); got != tc.want {
			t.Errorf("%s: timeoutSeconds(%v) = %d; want %d", tc.nome, tc.d, got, tc.want)
		}
	}
}
