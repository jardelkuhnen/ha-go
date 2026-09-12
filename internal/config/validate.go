package config

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

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

// parseTimeout aceita duração do Go ("90s", "1m30s") e, em fallback, número
// puro em segundos ("30" ⇒ 30s; "30.0" ok — formato do .env do Python).
// Valores ≤ 0 ou lixo viram erro citando a variável (§4).
func parseTimeout(key, raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if d, err := time.ParseDuration(raw); err == nil {
		if d <= 0 {
			return 0, fmt.Errorf("config: %s deve ser > 0: %q", key, raw)
		}
		return d, nil
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("config: %s inválido: %q", key, raw)
	}
	if f <= 0 {
		return 0, fmt.Errorf("config: %s deve ser > 0: %q", key, raw)
	}
	return time.Duration(f * float64(time.Second)), nil
}

// parseURL valida URL http(s) (paridade com AnyHttpUrl do pydantic);
// erro cita a variável (§4) e nunca o valor de secrets.
func parseURL(key, raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("config: %s inválida: %q", key, raw)
	}
	return raw, nil
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
	var err error
	if s.LLMTimeout, err = parseTimeout("LLM_TIMEOUT_S", v.GetString("LLM_TIMEOUT_S")); err != nil {
		return err
	}
	if s.HATimeout, err = parseTimeout("HA_TIMEOUT_S", v.GetString("HA_TIMEOUT_S")); err != nil {
		return err
	}
	if s.HAURL, err = parseURL("HA_URL", s.HAURL); err != nil {
		return err
	}
	port, err := strconv.Atoi(v.GetString("BRAIN_PORT"))
	if err != nil {
		return fmt.Errorf("config: BRAIN_PORT inválido: %q", v.GetString("BRAIN_PORT"))
	}
	s.BrainPort = port
	return nil
}
