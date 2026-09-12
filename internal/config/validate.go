package config

import (
	"fmt"

	"github.com/spf13/viper"
)

// appNamespaces são os prefixos da aplicação (§3): variável com um desses
// prefixos precisa ser reconhecida, senão o startup falha.
var appNamespaces = []string{
	"LLM_", "GEMINI_", "OPENAI_", "OLLAMA_", "HA_",
	"ALEXA_", "BRAIN_", "TAVILY_", "TELEGRAM_", "WHISPER_",
}

// knownIgnored: reconhecidas mas não consumidas — convivência com o .env do
// projeto Python durante a migração (§3).
var knownIgnored = map[string]bool{
	"TAVILY_API_KEY":     true,
	"TELEGRAM_BOT_TOKEN": true,
	"WHISPER_MODEL":      true,
	"ALLOWED_USERS":      true,
	"BRAIN_URL":          true,
	"BRAIN_TIMEOUT_S":    true,
}

// validate concentra todas as checagens do Load (§3 e §4). Cada task seguinte
// adiciona suas regras aqui, sempre citando variável e motivo no erro.
func validate(s *Settings, v *viper.Viper) error {
	switch s.LLMProvider {
	case "gemini":
		if s.GeminiAPIKey == "" {
			return fmt.Errorf("config: GEMINI_API_KEY é obrigatória quando LLM_PROVIDER=gemini")
		}
	case "openai":
		if s.OpenAIAPIKey == "" {
			return fmt.Errorf("config: OPENAI_API_KEY é obrigatória quando LLM_PROVIDER=openai")
		}
	case "ollama":
		// Motor local: não exige chave.
	default:
		if s.LLMProvider == "" {
			return fmt.Errorf("config: LLM_PROVIDER é obrigatória")
		}
		return fmt.Errorf("config: LLM_PROVIDER inválido: %q", s.LLMProvider)
	}
	if s.HAURL == "" {
		return fmt.Errorf("config: HA_URL é obrigatória")
	}
	if s.HAToken == "" {
		return fmt.Errorf("config: HA_TOKEN é obrigatória")
	}
	if s.AlexaMediaEntity == "" {
		return fmt.Errorf("config: ALEXA_MEDIA_ENTITY é obrigatória")
	}
	if s.BrainAPIKey == "" {
		return fmt.Errorf("config: BRAIN_API_KEY é obrigatória")
	}
	return nil
}
