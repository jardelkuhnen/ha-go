package brain

import (
	"home-assistent-go/internal/config"
)

// Defaults por provedor (spec 02 §3, paridade com get_llm em src/config.py):
// gemini e openai têm o modelo fixado no código; o do ollama replica o
// default de OLLAMA_MODEL em internal/config.
const (
	defaultGeminiModel = "gemini-3.5-flash"
	defaultOpenAIModel = "gpt-4o-mini"
	defaultOllamaModel = "llama3.2:3b"
)

// ActiveModel devolve o modelo ativo (§3): LLM_MODEL, se setada; senão
// OLLAMA_MODEL quando o provider é ollama (fallback histórico, via
// config.ResolvedModel); senão o default do provedor — inclusive quando
// OLLAMA_MODEL veio explicitamente vazia. Provider desconhecido devolve "":
// o Setup valida o provider antes (pluginFor) e falha rápido.
func ActiveModel(cfg config.Settings) string {
	if m := cfg.ResolvedModel(); m != "" {
		return m
	}
	switch cfg.LLMProvider {
	case "gemini":
		return defaultGeminiModel
	case "openai":
		return defaultOpenAIModel
	case "ollama":
		return defaultOllamaModel
	}
	return ""
}

// registryProvider devolve o prefixo do registry para o provider (§2). O
// prefixo é o Name() do plugin, que só difere do LLM_PROVIDER no gemini
// (plugin googlegenai, modelos "googleai/...").
func registryProvider(provider string) string {
	if provider == "gemini" {
		return "googleai"
	}
	return provider
}

// modelName devolve o endereçamento do modelo ativo no registry do Genkit
// (§2): "googleai/<model>", "openai/<model>" ou "ollama/<model>". O fluxo
// (spec 06) resolve o modelo por esse nome — trocar de motor não muda código.
func modelName(provider, model string) string {
	return registryProvider(provider) + "/" + model
}
