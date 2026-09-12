// Package config faz a leitura única e validada das variáveis de ambiente do
// Cérebro (paridade com src/config.py do projeto Python): lê `.env` do
// diretório corrente + ambiente do processo, rejeita variáveis desconhecidas
// dos namespaces da aplicação (paridade com extra="forbid") e expõe acesso
// singleton. Motor de leitura: Viper.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"sync"
	"time"

	"github.com/spf13/viper"
)

// Settings é a configuração tipada do Cérebro. Campos de secret ficam como
// string e nunca entram em log nem em mensagens de erro — os erros deste
// package citam apenas o nome da variável.
type Settings struct {
	// Motor cognitivo (§2).
	LLMProvider   string // "gemini" | "openai" | "ollama"
	LLMModel      string // LLM_MODEL cru; pode ser vazio
	OllamaModel   string // OLLAMA_MODEL
	GeminiAPIKey  string // secret
	OpenAIAPIKey  string // secret
	OpenAIAPIBase string
	OllamaBaseURL string
	LLMTimeout    time.Duration

	// Home Assistant / Alexa (§2).
	HAURL            string
	HAToken          string // secret
	HATimeout        time.Duration
	AlexaMediaEntity string

	// API do Cérebro (auth, §2).
	BrainAPIKey string // secret
	BrainPort   int
}

var (
	once      sync.Once
	singleton Settings
	loadErr   error
)

// Load lê e valida a configuração uma única vez (singleton): a primeira
// chamada lê `.env` do diretório corrente + o ambiente do processo (o ambiente
// vence sobre `.env`), valida tudo e memoriza o resultado — inclusive o erro.
// Chamadas seguintes devolvem o valor memorizado sem recarregar.
func Load() (Settings, error) {
	once.Do(func() { singleton, loadErr = load() })
	return singleton, loadErr
}

// fatalf existe para os testes substituírem o aborto do MustLoad.
var fatalf = log.Fatalf

// MustLoad é a variante de conveniência para o startup: em erro, loga e
// encerra o processo com código 1 (log.Fatalf).
func MustLoad() Settings {
	s, err := Load()
	if err != nil {
		fatalf("%v", err)
	}
	return s
}

func load() (Settings, error) {
	v := viper.New()
	v.SetConfigFile(".env")
	v.SetConfigType("env")
	v.AutomaticEnv()

	// Defaults da §2. Timeouts/porta ficam como string e são parseados em
	// validate (parseTimeout aceita "30", "30.0" e "30s").
	v.SetDefault("LLM_MODEL", "")
	v.SetDefault("GEMINI_API_KEY", "")
	v.SetDefault("OPENAI_API_KEY", "")
	v.SetDefault("OPENAI_API_BASE", "")
	v.SetDefault("OLLAMA_BASE_URL", "http://127.0.0.1:11434")
	v.SetDefault("OLLAMA_MODEL", "llama3.2:3b")
	v.SetDefault("LLM_TIMEOUT_S", "30s")
	v.SetDefault("HA_TIMEOUT_S", "5s")
	v.SetDefault("BRAIN_PORT", 8000)

	// .env ausente é válido (defaults + ambiente bastam).
	if err := v.ReadInConfig(); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Settings{}, fmt.Errorf("config: lendo .env: %w", err)
	}

	// Varredura estrita (§3): var de namespace do app não reconhecida → erro.
	if unknown, ok := unknownAppVar(v); ok {
		return Settings{}, fmt.Errorf("config: variável desconhecida do app: %s", unknown)
	}

	s := Settings{
		LLMProvider:      v.GetString("LLM_PROVIDER"),
		LLMModel:         v.GetString("LLM_MODEL"),
		OllamaModel:      v.GetString("OLLAMA_MODEL"),
		GeminiAPIKey:     v.GetString("GEMINI_API_KEY"),
		OpenAIAPIKey:     v.GetString("OPENAI_API_KEY"),
		OpenAIAPIBase:    v.GetString("OPENAI_API_BASE"),
		OllamaBaseURL:    v.GetString("OLLAMA_BASE_URL"),
		HAURL:            v.GetString("HA_URL"),
		HAToken:          v.GetString("HA_TOKEN"),
		AlexaMediaEntity: v.GetString("ALEXA_MEDIA_ENTITY"),
		BrainAPIKey:      v.GetString("BRAIN_API_KEY"),
	}
	if err := validate(&s, v); err != nil {
		return Settings{}, err
	}
	return s, nil
}
