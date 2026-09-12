package brain

import (
	"fmt"
	"time"

	"github.com/firebase/genkit/go/core/api"
	"github.com/firebase/genkit/go/plugins/googlegenai"
	"github.com/firebase/genkit/go/plugins/ollama"

	openai "github.com/firebase/genkit/go/plugins/compat_oai/openai"

	"github.com/openai/openai-go/option"

	"home-assistent-go/internal/config"
)

// pluginFor monta o plugin do provider escolhido — registro condicional
// (decisão 4, §2): só o plugin do LLM_PROVIDER é construído, e os outros
// provedores não precisam de credencial no startup. Antes de montar, valida
// a credencial mínima: os plugins fazem panic no Init sem credencial, e o
// startup precisa de erro claro, não panico (critério 2). Erros citam
// variável + motivo, nunca o valor dos secrets.
func pluginFor(cfg config.Settings) (api.Plugin, error) {
	switch cfg.LLMProvider {
	case "gemini":
		if cfg.GeminiAPIKey == "" {
			return nil, fmt.Errorf("brain: GEMINI_API_KEY é obrigatória quando LLM_PROVIDER=gemini")
		}
		return &googlegenai.GoogleAI{APIKey: cfg.GeminiAPIKey}, nil
	case "openai":
		if cfg.OpenAIAPIKey == "" {
			return nil, fmt.Errorf("brain: OPENAI_API_KEY é obrigatória quando LLM_PROVIDER=openai")
		}
		var opts []option.RequestOption
		if cfg.OpenAIAPIBase != "" {
			// Adaptação isolada (Global Constraints): o plugin v1.13.1 não tem
			// campo BaseURL — o endpoint entra como opção do SDK OpenAI.
			opts = append(opts, option.WithBaseURL(cfg.OpenAIAPIBase))
		}
		return &openai.OpenAI{APIKey: cfg.OpenAIAPIKey, Opts: opts}, nil
	case "ollama":
		if cfg.OllamaBaseURL == "" {
			return nil, fmt.Errorf("brain: OLLAMA_BASE_URL é obrigatória quando LLM_PROVIDER=ollama")
		}
		return &ollama.Ollama{
			ServerAddress: cfg.OllamaBaseURL,
			Timeout:       timeoutSeconds(cfg.LLMTimeout),
		}, nil
	default:
		// Config.Load já rejeita; aqui é a defesa do package (Setup é a
		// única porta de entrada do motor).
		return nil, fmt.Errorf("brain: LLM_PROVIDER inválido: %q", cfg.LLMProvider)
	}
}

// timeoutSeconds converte o deadline da config para o Timeout do plugin
// ollama, que é em segundos inteiros. Piso de 1s: truncar para 0 faria o
// plugin ignorar o valor e aplicar silenciosamente o default dele (30s). O
// mecanismo primário segue sendo o deadline do context (§4) — o Timeout do
// plugin é redonde de segurança do cliente HTTP.
func timeoutSeconds(d time.Duration) int {
	if s := int(d.Seconds()); s >= 1 {
		return s
	}
	return 1
}
