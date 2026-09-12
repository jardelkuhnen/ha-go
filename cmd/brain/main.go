// Command brain é o Cérebro do assistente (spec 07 §4): API conversacional
// que orquestra o motor cognitivo, as tools e o TTS via Home Assistant.
// Ordem de montagem (§4): config → motor cognitivo (genkit.Init + plugin do
// provider, spec 02) → client HA único (spec 03) → flow "brain" com o
// catálogo único de tools (specs 04/05/06) → router Gin com auth + http.Server
// em :BRAIN_PORT. Shutdown (SIGINT/SIGTERM): dreno dos requests em andamento
// → client HA fechado — sem perda de turno. Erros de startup → log claro +
// exit 1. NUNCA loga Settings inteiro: os campos de secret ficam em string
// e nada deles chega a log (risco documentado na spec 01) — só campos
// individuais não-sensíveis (porta, provider, model).
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"home-assistent-go/internal/api"
	"home-assistent-go/internal/brain"
	"home-assistent-go/internal/config"
	"home-assistent-go/internal/ha"
)

// shutdownTimeout é o teto do dreno dos requests em andamento no shutdown
// (§4): um turno é limitado por LLM_TIMEOUT_S (deadline dominante de cada
// geração, default 30s) mais os timeouts próprios das tools; 30s cobre o
// default. Ultrapassado o teto, Shutdown devolve erro e o processo encerra —
// sem pendurar o desligamento.
const shutdownTimeout = 30 * time.Second

func main() {
	// §4.1: configuração única, checagem estrita (spec 01) — em erro, log
	// claro e exit 1 (log.Fatalf).
	cfg := config.MustLoad()

	// §4.2: motor cognitivo — genkit.Init + plugin do provider ativo (spec 02).
	motor, err := brain.Setup(context.Background(), cfg)
	if err != nil {
		log.Fatalf("brain: motor cognitivo: %v", err)
	}

	// §4.3: client HA único (spec 03) — fechado só depois do dreno.
	cli := ha.NewClient(cfg)

	// §4.4/§4.5: flow "brain" com client + tools — DefineBrain vincula o
	// catálogo único (tools.Catalog, spec 06 §6) ao registry; chamar
	// tools.Catalog de novo registraria as actions em duplicidade (panico).
	flow := brain.DefineBrain(motor, cli)

	// Logger estruturado da timeline (§3) — texto no stderr.
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	// §4.6: router Gin + middleware auth (X-API-Key em tempo constante).
	modelo := brain.ActiveModel(cfg)
	router := api.NewRouter(api.Options{
		Runner:   flow,
		APIKey:   cfg.BrainAPIKey,
		Provider: motor.Provider,
		Model:    modelo,
		Logger:   logger,
	})
	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.BrainPort),
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second, // slowloris; SEM WriteTimeout (turnos longos)
	}

	// Shutdown (§4): SIGINT/SIGTERM → dreno dos requests → client HA fechado.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()
	// Log de startup (§3): provider e model ativos + porta (não-sensível).
	logger.Info("brain: API do Cérebro ouvindo",
		"port", cfg.BrainPort,
		"provider", motor.Provider,
		"model", modelo,
	)

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("brain: servidor em :%d: %v", cfg.BrainPort, err)
		}
	case <-ctx.Done():
	}

	shutCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		logger.Error("brain: shutdown excedeu o teto com turnos pendentes", "error", err)
	}
	// §4: o client HA só fecha depois dos requests (turnos completam antes).
	cli.Close()
	logger.Info("brain: encerrado com shutdown grace")
}
