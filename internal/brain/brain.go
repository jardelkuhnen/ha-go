// Package brain inicializa o motor cognitivo trocável (spec 02): o fluxo
// nunca instancia o LLM diretamente — o motor é selecionado por configuração
// (só o plugin do LLM_PROVIDER é registrado) e endereçado por nome de modelo
// no registry do Genkit, substituindo o Protocol CognitiveMotor do Python
// (FEATURES.md §F02, ADR-0001). Trocar de motor é editar .env e reiniciar
// (§5) — zero mudança de código ou de binding do fluxo.
package brain

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync/atomic"
	"time"

	"github.com/firebase/genkit/go/genkit"

	"home-assistent-go/internal/config"
)

// Motor é o motor cognitivo inicializado. O fluxo (spec 06) gera com
// genkit.Generate(ctx, m.Genkit, …) endereçando m.ModelName — ou sem modelo
// explícito, já que o Setup o fixa como default — e deriva o deadline de
// cada chamada via m.GenerationContext. As tools entram como opção de
// geração (ai.WithTools(...)), nunca hardcoded no motor (§4).
type Motor struct {
	// Genkit é a instância inicializada, com o plugin do provider no registry.
	Genkit *genkit.Genkit
	// Provider é o LLM_PROVIDER resolvido ("gemini" | "openai" | "ollama").
	Provider string
	// ModelName é o endereçamento do modelo ativo no registry (§2):
	// "googleai/<model>", "openai/<model>" ou "ollama/<model>".
	ModelName string

	timeout time.Duration // LLM_TIMEOUT_S: deadline de toda geração (§4)
}

// GenerationContext deriva de parent um context com o deadline de
// LLM_TIMEOUT_S (§4) para uma chamada de geração; o caller adia o CancelFunc.
// Cancelamento do parent propaga; sem timeout configurado (Motor construído
// à mão), o context nasce expirado — falha fechada, geração nunca corre sem
// deadline.
func (m *Motor) GenerationContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, m.timeout)
}

// setupDone guarda a unicidade do Setup: genkit.Init é por-processo (instala
// handlers de sinal e um registry global), então o motor só pode ser
// inicializado uma vez por processo — o main (spec 07) chama Setup uma vez.
var setupDone atomic.Bool

// NewMotor monta um Motor sobre uma instância Genkit já inicializada —
// variante injetável exportada (decisão 12) para os testes integrados da API
// (spec 07 §5), que não conseguem acessar o campo timeout privado: sem ele o
// context de geração nasce expirado (falha fechada). Em produção o Motor
// nasce de Setup.
func NewMotor(g *genkit.Genkit, provider, modelName string, timeout time.Duration) *Motor {
	return &Motor{Genkit: g, Provider: provider, ModelName: modelName, timeout: timeout}
}

// Setup inicializa o motor cognitivo uma única vez por processo: monta só o
// plugin do LLM_PROVIDER com a credencial pré-validada (registro condicional,
// §2), inicia o Genkit com o modelo ativo como default (§3), confirma que o
// modelo responde no registry e loga provider + model. Falha rápida com erro
// claro — nunca panico — se a credencial mínima faltar; nesse caso o guard
// de unicidade é liberado (falha antes do Init) para permitir retry.
func Setup(ctx context.Context, cfg config.Settings) (*Motor, error) {
	if setupDone.Swap(true) {
		return nil, errors.New("brain: Setup já foi chamada — genkit.Init é por-processo e o motor só pode ser inicializado uma vez")
	}
	p, err := pluginFor(cfg)
	if err != nil {
		setupDone.Store(false) // falha antes do Init: libera retry
		return nil, err
	}
	name := modelName(cfg.LLMProvider, ActiveModel(cfg))
	g := genkit.Init(ctx, genkit.WithPlugins(p), genkit.WithDefaultModel(name))
	// A partir daqui o processo já tem o Genkit instalado (handlers de
	// sinal): não há retry — o guard permanece ocupado.
	if genkit.LookupModel(g, name) == nil {
		return nil, fmt.Errorf("brain: modelo %q não registrado pelo plugin %q", name, cfg.LLMProvider)
	}
	m := &Motor{
		Genkit:    g,
		Provider:  cfg.LLMProvider,
		ModelName: name,
		timeout:   cfg.LLMTimeout,
	}
	log.Printf("brain: motor cognitivo: provider=%s model=%s", m.Provider, m.ModelName)
	return m, nil
}
