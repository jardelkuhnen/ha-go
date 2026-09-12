// Package api implementa a API do Cérebro (spec 07): servidor Gin expondo o
// turno conversacional do flow "brain" (spec 06) com auth por API key e
// contrato idêntico ao do projeto Python (src/api.py) — paridade §2. A
// timeline de auditoria (§3) é emitida aqui: o flow do brain não precisa
// saber dela.
package api

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"home-assistent-go/internal/brain"
)

// TurnRunner executa um turno conversacional (flow "brain" da spec 06 —
// §2). O flow de produção (*core.Flow[brain.ChatInput, brain.ChatOutput,
// struct{}], de brain.DefineBrain) satisfaz a interface; a seam permite
// testar os handlers sem Genkit.
type TurnRunner interface {
	Run(ctx context.Context, in brain.ChatInput) (brain.ChatOutput, error)
}

// Options são as dependências injetadas do router (decisão 12). Logger nil →
// slog.Default(). Provider/Model só alimentam o detalhe da timeline do
// Chatbot Agent (§3) — nunca secrets.
type Options struct {
	Runner   TurnRunner // flow "brain" de produção
	APIKey   string     // BRAIN_API_KEY — secret, comparada em tempo constante
	Provider string     // LLM_PROVIDER ativo (§3)
	Model    string     // modelo ativo sem prefixo do registry (§3)
	Logger   *slog.Logger
}

// server carrega o estado dos handlers.
type server struct {
	runner   TurnRunner
	apiKey   string
	provider string
	model    string
	logger   *slog.Logger
}

// NewRouter monta o router Gin da §2: GET /health sem auth e POST /chat com
// o middleware de auth. Sem o Logger default do Gin — o log do package é a
// timeline slog (§3); Recovery transforma panics em 500 em vez de crash.
func NewRouter(o Options) *gin.Engine {
	logger := o.Logger
	if logger == nil {
		logger = slog.Default()
	}
	gin.SetMode(gin.ReleaseMode)
	s := &server{runner: o.Runner, apiKey: o.APIKey, provider: o.Provider, model: o.Model, logger: logger}
	r := gin.New()
	r.Use(gin.Recovery())
	r.GET("/health", s.health)
	r.POST("/chat", s.authMiddleware(), s.chat)
	return r
}

// health responde o liveness da §2 — sem auth.
func (s *server) health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
