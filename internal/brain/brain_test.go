package brain

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"home-assistent-go/internal/config"
)

func TestSetupFalhaSemCredencialELiberaRetry(t *testing.T) { // §2 critério 2 (caminho de erro)
	cfg := config.Settings{LLMProvider: "gemini"} // sem GEMINI_API_KEY
	_, err := Setup(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "GEMINI_API_KEY") {
		t.Fatalf("Setup sem credencial: err = %v; want erro citando GEMINI_API_KEY", err)
	}
	// Guard precisa estar liberado após falha antes do Init: a segunda
	// chamada devolve o mesmo erro de credencial, não o de unicidade.
	_, err2 := Setup(context.Background(), cfg)
	if err2 == nil || !strings.Contains(err2.Error(), "GEMINI_API_KEY") {
		t.Fatalf("retry após falha de validação: err = %v; want erro de credencial de novo", err2)
	}
}

func TestSetupRecusaSegundaChamada(t *testing.T) { // genkit.Init é por-processo
	setupDone.Store(true)
	defer setupDone.Store(false)
	cfg := config.Settings{
		LLMProvider:   "ollama",
		OllamaBaseURL: "http://127.0.0.1:11434",
		LLMTimeout:    30 * time.Second,
	}
	_, err := Setup(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "uma vez") {
		t.Fatalf("segunda Setup: err = %v; want erro de unicidade", err)
	}
}

func TestGenerationContextDefineDeadline(t *testing.T) { // §4
	m := &Motor{timeout: 50 * time.Millisecond}
	ctx, cancel := m.GenerationContext(context.Background())
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("GenerationContext: context sem deadline")
	}
	if d := time.Until(deadline); d <= 0 || d > 50*time.Millisecond {
		t.Errorf("deadline em %v; want (0, 50ms]", d)
	}
}

func TestGenerationContextExpira(t *testing.T) { // §4 critério 4 (mecânica do deadline)
	m := &Motor{timeout: 20 * time.Millisecond}
	ctx, cancel := m.GenerationContext(context.Background())
	defer cancel()
	select {
	case <-ctx.Done():
		if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			t.Fatalf("ctx.Err() = %v; want DeadlineExceeded", ctx.Err())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("context não expirou com timeout de 20ms")
	}
}

func TestGenerationContextTimeoutZeroFalhaFechado(t *testing.T) {
	// Sem timeout configurado: o context nasce expirado — geração nunca
	// corre sem deadline (baseline item 2 da spec).
	m := &Motor{}
	ctx, cancel := m.GenerationContext(context.Background())
	defer cancel()
	select {
	case <-ctx.Done():
		if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			t.Fatalf("ctx.Err() = %v; want DeadlineExceeded", ctx.Err())
		}
	case <-time.After(time.Second):
		t.Fatal("Motor sem timeout deveria nascer expirado (fail-closed)")
	}
}

func TestGenerationContextHerdaCancelamento(t *testing.T) {
	m := &Motor{timeout: time.Hour}
	parent, pcancel := context.WithCancel(context.Background())
	ctx, cancel := m.GenerationContext(parent)
	defer cancel()
	pcancel()
	select {
	case <-ctx.Done():
		if !errors.Is(ctx.Err(), context.Canceled) {
			t.Fatalf("ctx.Err() = %v; want Canceled", ctx.Err())
		}
	case <-time.After(time.Second):
		t.Fatal("cancelamento do parent não propagou para o context de geração")
	}
}

func TestTurnDeadlineDefineDeadline(t *testing.T) {
	// TurnDeadline = timeout × 9 (maxTurns(8) + 1): o orçamento de um turno
	// multi-tool. Sem timeout → fail-closed (context nasce expirado), igual
	// ao GenerationContext.
	m := &Motor{timeout: 50 * time.Millisecond}
	ctx, cancel := m.TurnDeadline(context.Background())
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("TurnDeadline: context sem deadline")
	}
	if got := time.Until(deadline); got <= 0 || got > 450*time.Millisecond {
		t.Errorf("deadline em %v; want (0, 450ms] (50ms × 9)", got)
	}
}

func TestTurnDeadlineTimeoutZeroFalhaFechado(t *testing.T) {
	m := &Motor{}
	ctx, cancel := m.TurnDeadline(context.Background())
	defer cancel()
	select {
	case <-ctx.Done():
		if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			t.Fatalf("ctx.Err() = %v; want DeadlineExceeded", ctx.Err())
		}
	case <-time.After(time.Second):
		t.Fatal("Motor sem timeout deveria nascer expirado (fail-closed)")
	}
}

func TestTurnDeadlineHerdaCancelamento(t *testing.T) {
	m := &Motor{timeout: time.Hour}
	parent, pcancel := context.WithCancel(context.Background())
	ctx, cancel := m.TurnDeadline(parent)
	defer cancel()
	pcancel()
	select {
	case <-ctx.Done():
		if !errors.Is(ctx.Err(), context.Canceled) {
			t.Fatalf("ctx.Err() = %v; want Canceled", ctx.Err())
		}
	case <-time.After(time.Second):
		t.Fatal("cancelamento do parent não propagou para o TurnDeadline")
	}
}
