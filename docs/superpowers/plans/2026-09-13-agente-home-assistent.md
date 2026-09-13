# Agente `home_assistent` (Genkit Agents API) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the hand-rolled tool loop in `internal/brain/flow.go` with a Genkit Agent (`genkit/exp.DefineAgent`) named `home_assistent`, preserving the `/chat` API contract and all observable behavior.

**Architecture:** A new `internal/agent/` package owns the turn: `DefineHomeAssistent` builds an `aix.Agent[struct{}]` (inline prompt: model + static system prompt + tools + `WithMaxTurns(8)`, no session store → stateless). A `Runner` adapter implements `api.TurnRunner`, calling `agent.RunText`, extracting reply + tool names from `AgentOutput`, and routing the voice TTS step. `internal/brain/` keeps only the cognitive motor (`Motor`/`Setup`/plugin/model); `flow.go` and `prompt.go` are deleted.

**Tech Stack:** Go 1.24, `github.com/firebase/genkit/go` v1.13.1 (`genkit/exp`, `ai/exp` subpackages — experimental, gated by `genkit.WithExperimental()`), Gin, existing `internal/ha`, `internal/tools`, `internal/config`.

**Spec:** `docs/superpowers/specs/2026-09-13-agente-home-assistent-design.md`

## Global Constraints

- **Experimental gate:** `genkit.Init` MUST be called with `genkit.WithExperimental()` or `genkitx.DefineAgent` panics. Applies to production (`brain.Setup`) AND every test helper that calls `genkit.Init` (`novoMotorFake`, `montaStack`).
- **Stateless agent:** No `WithSessionStore` — state is client-managed; `AgentOutput.State.Messages` is populated and is the source for tool-name extraction.
- **Max-turns error path:** `agent.RunText` returns `(out, nil)` — NOT `(nil, err)` — when `ai.ErrMaxTurnsExceeded` fires (client context alive). The runner MUST check `out.Error != nil` after a nil `err`, and return `ChatOutput{}, out.Error` (→ HTTP 500) in that case.
- **Turn deadline:** The context passed to `RunText` bounds the whole turn (not per-generation). Use `Motor.TurnDeadline(ctx)` = `m.timeout × (maxTurns+1)` (e.g. 30s × 9 = 270s) so multi-tool turns keep the budget the old per-generation timeout gave.
- **`/chat` contract unchanged:** `chatRequest`/`chatResponse` shapes, status codes (200 end-of-turn, 400 bad body, 401 auth, 500 runner error), `tools_used` sorted + never null.
- **Speak stays voice-only post-processing:** not a tool; skipped on `source=="telegram"`; never fails the turn (`Spoken=false, Error=<msg>` → 200).
- **No secrets in logs/errors:** speak errors sanitized by `ha.Client`; `brain.Setup` logs only provider + model.
- **Package import graph (no cycles):** `cmd/brain → {api, agent, brain, config, ha}`; `api → agent`; `agent → {brain, ha, tools}`; `brain → config`; `tools → {ai, genkit, ha}`.
- **Tests run green under `-race`** (`make test` = `go test -race ./...`).

---

## File Structure

| File | Responsibility |
|------|----------------|
| `internal/brain/brain.go` (edit) | Motor + Setup + `NewMotor` + NEW `Motor.TurnDeadline`; adds `genkit.WithExperimental()` to `genkit.Init` |
| `internal/brain/model.go` (unchanged) | `ActiveModel`, `modelName`, `registryProvider` |
| `internal/brain/plugin.go` (unchanged) | `pluginFor` |
| `internal/brain/brain_test.go` (edit) | + test for `TurnDeadline` |
| `internal/agent/agent.go` (new) | `DefineHomeAssistent`, `DefineHomeAssistentWithRefs` |
| `internal/agent/prompt.go` (new) | `systemPrompt` const (static, unified) |
| `internal/agent/types.go` (new) | `ChatInput`, `ChatOutput` (moved from brain/flow.go) |
| `internal/agent/runner.go` (new) | `Runner` impl `api.TurnRunner`; `Run`; `speak`; `extractToolNames`; `toolsOrdenadas` |
| `internal/agent/agent_test.go` (new) | fake model/tools/HA helpers (ported from brain/flow_test.go); agent + runner tests |
| `internal/api/api.go` (edit) | `TurnRunner` interface → `agent.ChatInput`/`agent.ChatOutput` |
| `internal/api/chat.go` (edit) | `brain.ChatInput` → `agent.ChatInput` |
| `internal/api/api_test.go` (edit) | `fakeRunner` → `agent.ChatInput`/`agent.ChatOutput` |
| `internal/api/integration_test.go` (edit) | `DefineBrainWithRefs` → `agent.DefineHomeAssistentWithRefs` + `agent.NewRunner`; `genkit.WithExperimental()` |
| `cmd/brain/main.go` (edit) | wire `agent.DefineHomeAssistent` + `agent.NewRunner` |
| `internal/brain/flow.go` (delete) | replaced by agent package |
| `internal/brain/prompt.go` (delete) | replaced by `agent/prompt.go` |
| `internal/brain/flow_test.go` (delete) | replaced by `agent/agent_test.go` |
| `internal/brain/prompt_test.go` (delete) | replaced by `agent/agent_test.go` |

---

## Task 1: Add `Motor.TurnDeadline` + experimental gate to `brain`

**Files:**
- Modify: `internal/brain/brain.go`
- Test: `internal/brain/brain_test.go`

**Interfaces:**
- Consumes: `Motor.timeout` (existing private field), `maxTurns` constant (defined in Task 4 as 8; here we use a local literal and refactor in Task 4 — OR define `maxTurns` here). **Decision:** define `const maxTurns = 8` in `agent` package (Task 4); `TurnDeadline` here multiplies by a parameter to avoid importing agent. **Simpler:** `TurnDeadline(ctx)` uses `m.timeout * 9` directly (maxTurns+1 = 9 is the budget; the agent's `WithMaxTurns(8)` is set independently in Task 4 and the two stay in sync by comment). Add a unit test pinning the multiplier.
- Produces: `func (m *Motor) TurnDeadline(parent context.Context) (context.Context, context.CancelFunc)` — used by `agent.Runner.Run` (Task 4).

- [ ] **Step 1: Write the failing test**

Add to `internal/brain/brain_test.go` (after `TestGenerationContextHerdaCancelamento`):

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestTurnDeadline ./internal/brain/ -v`
Expected: FAIL / build error — `m.TurnDeadline undefined`.

- [ ] **Step 3: Implement `Motor.TurnDeadline` + add `genkit.WithExperimental()` to Setup**

In `internal/brain/brain.go`, add the method after `GenerationContext`:

```go
// turnDeadlineMultiplier é o multiplicador do deadline de turno (maxTurns+1):
// o agente faz até 8 voltas de tools, e cada volta pode custar uma geração —
// 9 × LLM_TIMEOUT_S cobre o turno inteiro. Mantido em sincronia com
// ai.WithMaxTurns(8) do DefineHomeAssistent (internal/agent).
const turnDeadlineMultiplier = 9

// TurnDeadline deriva de parent um context com o deadline de turno inteiro
// (m.timeout × turnDeadlineMultiplier). A Agents API não expõe timeout por
// geração — o context de RunText limita o turno todo. Sem timeout configurado
// o context nasce expirado (fail-closed), igual ao GenerationContext. O caller
// adia o CancelFunc.
func (m *Motor) TurnDeadline(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, m.timeout*turnDeadlineMultiplier)
}
```

In `Setup` (same file), change the `genkit.Init` call to add the experimental gate (the agent constructor in `internal/agent` requires it):

```go
	g := genkit.Init(ctx, genkit.WithPlugins(p), genkit.WithDefaultModel(name), genkit.WithExperimental())
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -race ./internal/brain/ -v`
Expected: PASS — all existing brain tests + the 3 new `TestTurnDeadline*` tests green.

- [ ] **Step 5: Commit**

```bash
git add internal/brain/brain.go internal/brain/brain_test.go
git commit -m "feat(brain): Motor.TurnDeadline + gate experimental no Setup

TurnDeadline devolve um context com deadline de turno inteiro (timeout × 9)
para a Agents API, que limita o turno — não cada geração. Setup passa
genkit.WithExperimental() ao Init: o construtor DefineAgent panica sem ele.

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

---

## Task 2: Create `internal/agent` package — types + static prompt

**Files:**
- Create: `internal/agent/types.go`
- Create: `internal/agent/prompt.go`
- Create: `internal/agent/prompt_test.go`

**Interfaces:**
- Consumes: nothing (types + const only).
- Produces: `agent.ChatInput`, `agent.ChatOutput` (used by `agent.Runner`, `api.TurnRunner`, `api/chat.go`); `agent.systemPrompt` (used by `agent.DefineHomeAssistent`).

- [ ] **Step 1: Write the failing test for the prompt**

Create `internal/agent/prompt_test.go`:

```go
package agent

import (
	"strings"
	"testing"
)

func TestSystemPromptCumpreRegras(t *testing.T) {
	// Prompt unificado (voz aplicado aos dois canais): texto plano, frases
	// curtas, clima/dispositivos só via tools, honestidade, sem Markdown/JSON.
	for _, regra := range []string{"português", "texto plano", "ferramenta", "clima", "breve"} {
		if !strings.Contains(systemPrompt, regra) {
			t.Errorf("systemPrompt não menciona %q", regra)
		}
	}
	if !strings.Contains(systemPrompt, "get_weather") {
		t.Errorf("systemPrompt não nomeia a ferramenta get_weather")
	}
	if !strings.Contains(systemPrompt, "control_device") {
		t.Errorf("systemPrompt não nomeia a ferramenta control_device")
	}
	// Sem Markdown: o prompt proíbe, não permite.
	for _, proibida := range []string{"Markdown é permitido", "Markdown permitido"} {
		if strings.Contains(systemPrompt, proibida) {
			t.Errorf("systemPrompt permite Markdown (%q) — deve ser texto plano", proibida)
		}
	}
}

func TestSystemPromptSemMencaoABusca(t *testing.T) { // decisão 10: sem "busca"
	for _, proibida := range []string{"busca", "Busca", "search", "Search", "Tavily"} {
		if strings.Contains(systemPrompt, proibida) {
			t.Errorf("systemPrompt menciona %q (decisão 10)", proibida)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestSystemPrompt ./internal/agent/ -v`
Expected: FAIL / build error — package doesn't exist / `systemPrompt undefined`.

- [ ] **Step 3: Create `types.go`**

Create `internal/agent/types.go`:

```go
// Package agent implementa o turno conversacional sobre a Agents API do Genkit
// (genkit/exp.DefineAgent). Substitui o loop de tools feito à mão do antigo
// internal/brain/flow.go: o framework roda o ciclo de tool-calls internamente,
// e o Runner mapeia a saída (AgentOutput) para o contrato do /chat.
//
// O agente é genérico: uma instância só (home_assistent), persona única de
// assistente, entrada = objetivo do usuário. O canal (voz/telegram) é
// decisão de entrega no Runner, não do agente.
package agent

// ChatInput é a entrada do turno (movida de internal/brain/flow.go). O Source
// chega normalizado pela API; SessionID é transportado e NÃO consumido
// (stateless — sem WithSessionStore).
type ChatInput struct {
	Text      string // fala/texto do usuário
	Source    string // "satellite" (default) | "telegram"
	SessionID string // transportado, não consumido
}

// ChatOutput é a saída do turno (movida de internal/brain/flow.go). Error só
// carrega falha do passo speak; falhas de geração (incl. teto de tools) e o
// teto de iterações encerram o turno com erro (→ 500). ToolsUsed sai ordenado.
type ChatOutput struct {
	Reply     string   // texto final do agente
	Spoken    bool     // TTS acionado com sucesso
	Error     string   // erro do passo speak, se houver
	ToolsUsed []string // nomes das tools executadas (ordenado)
}
```

- [ ] **Step 4: Create `prompt.go`**

Create `internal/agent/prompt.go`:

```go
package agent

// systemPrompt é o system prompt único do agente home_assistent — prompt
// unificado (decisão A): as regras de voz (texto plano, frases curtas)
// aplicadas a AMBOS os canais. Telegram perde Markdown (texto plano funciona
// no Telegram; mudança menor aceita). Derivação fiel do systemPromptVoz do
// antigo internal/brain/prompt.go, com menção às duas tools.
const systemPrompt = `Você é o assistente da casa da família. Responda sempre em português do Brasil.

Você está falando por voz, e a sua resposta vira fala na Alexa. Siga estas regras:
- Responda em texto plano, falável: nada de Markdown, símbolos ou JSON.
- Use frases curtas: no máximo uma ou duas frases por resposta.
- Confirme automações em uma única frase (ex.: "Liguei a luz da sala.").
- Nunca invente clima: quando perguntarem, use a ferramenta get_weather.
- Para ligar, desligar ou alternar dispositivos, use a ferramenta control_device.
- Se não souber, diga que não sabe.
- Seja breve.`
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test -race ./internal/agent/ -v`
Expected: PASS — `TestSystemPromptCumpreRegras`, `TestSystemPromptSemMencaoABusca` green.

- [ ] **Step 6: Commit**

```bash
git add internal/agent/types.go internal/agent/prompt.go internal/agent/prompt_test.go
git commit -m "feat(agent): package agent — tipos ChatInput/ChatOutput + prompt unificado

Cria internal/agent/, novo dono do turno conversacional. ChatInput/ChatOutput
movidos do brain. systemPrompt é único (voz aplicada aos dois canais) — decisão
A da spec: telegram perde Markdown.

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

---

## Task 3: `DefineHomeAssistent` — the agent definition

**Files:**
- Create: `internal/agent/agent.go`
- Create: `internal/agent/agent_test.go` (fake-model/tools/HA helpers + agent-definition tests; full runner tests added in Task 4)

**Interfaces:**
- Consumes: `brain.Motor` (fields `Genkit *genkit.Genkit`, `ModelName string`); `tools.Catalog(g, cli) []ai.ToolRef`; `genkitx.DefineAgent[struct{}](g, name, prompt, opts...)`; `aix.InlinePrompt` (`[]ai.PromptOption`); `ai.WithModelName`, `ai.WithSystem`, `ai.WithTools`, `ai.WithMaxTurns`.
- Produces: `func DefineHomeAssistent(m *brain.Motor, cli *ha.Client) *aix.Agent[struct{}]`; `func DefineHomeAssistentWithRefs(m *brain.Motor, refs []ai.ToolRef) *aix.Agent[struct{}]` (test seam, mirrors old `DefineBrainWithRefs`). The `*aix.Agent[struct{}]` is consumed by `agent.NewRunner` (Task 4).

- [ ] **Step 1: Write the failing test**

Create `internal/agent/agent_test.go` with the fake-model/tools/HA helpers (ported from `internal/brain/flow_test.go`) and the agent-definition test. The full runner tests come in Task 4 (same file, appended).

```go
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/firebase/genkit/go/ai"
	"github.com/firebase/genkit/go/genkit"

	"home-assistent-go/internal/brain"
	"home-assistent-go/internal/config"
	"home-assistent-go/internal/ha"
)

// ---------- modelo fake (genkit.DefineModelAction) ----------

// fakeModel modela o motor cognitivo scriptado: cada chamada de Generate
// consome o próximo passo do roteiro. Passos: string (texto) ou ai.ToolRequest.
type fakeModel struct {
	mu       sync.Mutex
	steps    []any
	chamadas int
	lastReq  *ai.ModelRequest
}

func (f *fakeModel) add(step any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.steps = append(f.steps, step)
}

func (f *fakeModel) handle(ctx context.Context, req *ai.ModelRequest, _ any, cb ai.ModelStreamCallback) (*ai.ModelResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.chamadas++
	f.lastReq = req
	if len(f.steps) == 0 {
		return nil, fmt.Errorf("fakeModel: roteiro esgotado (chamada %d)", f.chamadas)
	}
	step := f.steps[0]
	if len(f.steps) > 1 {
		f.steps = f.steps[1:]
	}
	switch s := step.(type) {
	case string:
		return &ai.ModelResponse{Message: ai.NewModelTextMessage(s), FinishReason: ai.FinishReasonStop}, nil
	case ai.ToolRequest:
		req := s
		return &ai.ModelResponse{Message: ai.NewModelMessage(ai.NewToolRequestPart(&req)), FinishReason: ai.FinishReasonStop}, nil
	case []ai.ToolRequest:
		parts := make([]*ai.Part, 0, len(s))
		for _, tr := range s {
			tr := tr
			parts = append(parts, ai.NewToolRequestPart(&tr))
		}
		return &ai.ModelResponse{Message: ai.NewModelMessage(parts...), FinishReason: ai.FinishReasonStop}, nil
	default:
		return nil, fmt.Errorf("fakeModel: passo inesperado: %T", step)
	}
}

const nomeModeloFake = "fake/motor"

// novoMotorFake monta o Genkit COM WithExperimental (DefineAgent panica sem
// ele) e o modelo fake endereçado por m.ModelName. Motor à mão (sem Setup).
func novoMotorFake(t *testing.T, fm *fakeModel) *brain.Motor {
	t.Helper()
	ctx := context.Background()
	g := genkit.Init(ctx, genkit.WithExperimental())
	opts := &ai.ModelOptions{
		Label: "motor fake dos testes do agente",
		Supports: &ai.ModelSupports{
			Tools:      true,
			ToolChoice: true,
			Multiturn:  true,
			SystemRole: true,
		},
	}
	if genkit.DefineModelAction(g, nomeModeloFake, opts, fm.handle) == nil {
		t.Fatal("DefineModelAction devolveu nil")
	}
	return brain.NewMotor(g, "fake", nomeModeloFake, 2*time.Second)
}

// ---------- tools fake (mock) ----------

type fakeTools struct {
	mu       sync.Mutex
	chamadas []string
	entradas []map[string]any
}

func (ft *fakeTools) gravou(nome string, in map[string]any) {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	ft.chamadas = append(ft.chamadas, nome)
	ft.entradas = append(ft.entradas, in)
}

func (ft *fakeTools) total() int {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	return len(ft.chamadas)
}

// registrarFakeTools registra get_weather e control_device fake no registry.
func registrarFakeTools(g *genkit.Genkit, ft *fakeTools) []ai.ToolRef {
	weather := genkit.DefineTool(g, "get_weather", "fake: previsão do tempo",
		func(tctx *ai.ToolContext, in struct {
			Location string `json:"location"`
		}) (string, error) {
			ft.gravou("get_weather", map[string]any{"location": in.Location})
			return "Máxima de 28, mínima de 19.", nil
		})
	control := genkit.DefineTool(g, "control_device", "fake: liga/desliga dispositivo",
		func(tctx *ai.ToolContext, in struct {
			Action   string `json:"action"`
			EntityID string `json:"entity_id"`
		}) (string, error) {
			ft.gravou("control_device", map[string]any{"action": in.Action, "entity_id": in.EntityID})
			return "Liguei o dispositivo.", nil
		})
	return []ai.ToolRef{weather, control}
}

// ---------- mock do Home Assistant (httptest) ----------

type gravadaHA struct {
	mu     sync.Mutex
	Method string
	Path   string
	Body   []byte
}

func novoServidorHA(t *testing.T, g *gravadaHA, status int) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		g.mu.Lock()
		g.Method = r.Method
		g.Path = r.URL.Path
		g.Body = body
		g.mu.Unlock()
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(ts.Close)
	return ts
}

func novoClientHA(ts *httptest.Server) *ha.Client {
	return ha.NewClient(config.Settings{
		HAURL:            ts.URL,
		HAToken:          "token-de-teste",
		HATimeout:        5 * time.Second,
		AlexaMediaEntity: "media_player.alexa_sala",
	})
}

func corpoSpeak(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("corpo do speak não é objeto JSON: %q (%v)", body, err)
	}
	return m
}

// ---------- DefineHomeAssistent: wiring ----------

func TestDefineHomeAssistentVinculaCatalogo(t *testing.T) {
	fm := &fakeModel{}
	fm.add("Ok.")
	m := novoMotorFake(t, fm)
	ag := DefineHomeAssistent(m, nil)
	if ag == nil {
		t.Fatal("DefineHomeAssistent devolveu nil")
	}
	if got := ag.Name(); got != "home_assistent" {
		t.Errorf("agent.Name() = %q; want home_assistent", got)
	}
	if genkit.LookupTool(m.Genkit, "get_weather") == nil {
		t.Error("get_weather não registrada pelo catálogo de produção")
	}
	if genkit.LookupTool(m.Genkit, "control_device") == nil {
		t.Error("control_device não registrada pelo catálogo de produção")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestDefineHomeAssistentVinculaCatalogo ./internal/agent/ -v`
Expected: FAIL / build error — `DefineHomeAssistent undefined`.

- [ ] **Step 3: Implement `agent.go`**

Create `internal/agent/agent.go`:

```go
package agent

import (
	"github.com/firebase/genkit/go/ai"
	aix "github.com/firebase/genkit/go/ai/exp"
	"github.com/firebase/genkit/go/genkit"
	genkitx "github.com/firebase/genkit/go/genkit/exp"

	"home-assistent-go/internal/brain"
	"home-assistent-go/internal/ha"
	"home-assistent-go/internal/tools"
)

// maxTurns é o teto de voltas do loop de tools do agente (paridade com o
// maxIterTools=8 do antigo flow.go). Em sincronia com turnDeadlineMultiplier
// (internal/brain): o deadline de turno cobre maxTurns+1 gerações.
const maxTurns = 8

// DefineHomeAssistent define o agente "home_assistent" sobre o motor e o
// client HA injetados — catálogo único de tools (tools.Catalog) e prompt
// unificado estático. Sem WithSessionStore: stateless, client-managed.
// Em produção use esta. O Runner (NewRunner) envelopa o agente para a API.
func DefineHomeAssistent(m *brain.Motor, cli *ha.Client) *aix.Agent[struct{}] {
	return DefineHomeAssistentWithRefs(m, tools.Catalog(m.Genkit, cli))
}

// DefineHomeAssistentWithRefs é a variante injetável (seam de testes): mesmo
// agente com refs informadas — os testes integrados montam o catálogo com
// endpoints Open-Meteo apontados a httptest. Em produção use DefineHomeAssistent.
func DefineHomeAssistentWithRefs(m *brain.Motor, refs []ai.ToolRef) *aix.Agent[struct{}] {
	prompt := aix.InlinePrompt{
		ai.WithModelName(m.ModelName),
		ai.WithSystem(systemPrompt),
		ai.WithTools(refs...),
		ai.WithMaxTurns(maxTurns),
	}
	return genkitx.DefineAgent[struct{}](m.Genkit, "home_assistent", prompt)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -race ./internal/agent/ -v`
Expected: PASS — `TestDefineHomeAssistentVinculaCatalogo` + prompt tests green.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/agent.go internal/agent/agent_test.go
git commit -m "feat(agent): DefineHomeAssistent sobre genkit/exp.DefineAgent

Agente genérico home_assistent com InlinePrompt (model + system prompt
unificado + tools + WithMaxTurns(8)), sem session store. DefineHomeAssistentWithRefs
é o seam de testes (paridade com o antigo DefineBrainWithRefs).

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

---

## Task 4: `Runner` — the `api.TurnRunner` adapter

**Files:**
- Create: `internal/agent/runner.go`
- Modify: `internal/agent/agent_test.go` (append runner tests)

**Interfaces:**
- Consumes: `*aix.Agent[struct{}]` (`RunText(ctx, text) (*aix.AgentOutput[struct{}], error)`); `*brain.Motor` (`TurnDeadline(ctx)`); `*ha.Client` (`Speak(ctx, text) SpeakResult`); `ai.Message.Text()`, `ai.Part.IsToolRequest()`, `ai.Part.ToolRequest.Name`; `ai.ErrMaxTurnsExceeded`.
- Produces: `type Runner struct{...}`; `func NewRunner(m *brain.Motor, ag *aix.Agent[struct{}], cli *ha.Client) *Runner`; `func (r *Runner) Run(ctx context.Context, in ChatInput) (ChatOutput, error)` — satisfies `api.TurnRunner`.

- [ ] **Step 1: Write the failing tests (append to `internal/agent/agent_test.go`)**

Append these tests to `internal/agent/agent_test.go`:

```go
// ---------- Runner ----------

// rodarTurno monta o Runner e executa um turno.
func rodarTurno(t *testing.T, m *brain.Motor, cli *ha.Client, refs []ai.ToolRef, in ChatInput) (ChatOutput, error) {
	t.Helper()
	ag := DefineHomeAssistentWithRefs(m, refs)
	r := NewRunner(m, ag, cli)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return r.Run(ctx, in)
}

func TestRunnerToolLoopCriterio1(t *testing.T) { // tool → executa → 2ª volta texto
	fm := &fakeModel{}
	fm.add(ai.ToolRequest{Name: "get_weather", Input: map[string]any{"location": "São Paulo"}})
	fm.add("A máxima é de 28 graus.")
	m := novoMotorFake(t, fm)
	ft := &fakeTools{}
	refs := registrarFakeTools(m.Genkit, ft)

	var gHA gravadaHA
	ts := novoServidorHA(t, &gHA, http.StatusOK)
	cli := novoClientHA(ts)

	out, err := rodarTurno(t, m, cli, refs, ChatInput{Text: "Como está o clima em São Paulo?"})
	if err != nil {
		t.Fatalf("turno: erro inesperado: %v", err)
	}
	if out.Reply != "A máxima é de 28 graus." {
		t.Errorf("Reply = %q; want %q", out.Reply, "A máxima é de 28 graus.")
	}
	if got := ft.total(); got != 1 {
		t.Fatalf("tools executadas = %d; want 1", got)
	}
	if ft.entradas[0]["location"] != "São Paulo" {
		t.Errorf("input da tool = %v; want location São Paulo", ft.entradas[0])
	}
	if fmt.Sprint(out.ToolsUsed) != "[get_weather]" {
		t.Errorf("ToolsUsed = %v; want [get_weather]", out.ToolsUsed)
	}
	if !out.Spoken {
		t.Errorf("Spoken = false; want true (canal de voz, HA mockado)")
	}
	if out.Error != "" {
		t.Errorf("Error = %q; want vazio", out.Error)
	}
	if gHA.Method != http.MethodPost || gHA.Path != "/api/services/notify/alexa_media" {
		t.Fatalf("speak: %s %s; want POST /api/services/notify/alexa_media", gHA.Method, gHA.Path)
	}
	corpo := corpoSpeak(t, gHA.Body)
	if corpo["message"] != out.Reply {
		t.Errorf("speak: message = %v; want %q", corpo["message"], out.Reply)
	}
	if corpo["target"] != "media_player.alexa_sala" {
		t.Errorf("speak: target = %v; want media_player.alexa_sala", corpo["target"])
	}
}

func TestRunnerRespostaDiretaVozCriterio2(t *testing.T) {
	fm := &fakeModel{}
	fm.add("Já liguei a luz da sala.")
	m := novoMotorFake(t, fm)
	ft := &fakeTools{}
	refs := registrarFakeTools(m.Genkit, ft)

	var gHA gravadaHA
	ts := novoServidorHA(t, &gHA, http.StatusOK)
	cli := novoClientHA(ts)

	out, err := rodarTurno(t, m, cli, refs, ChatInput{Text: "ligue a luz da sala"})
	if err != nil {
		t.Fatalf("turno: erro inesperado: %v", err)
	}
	if out.Reply != "Já liguei a luz da sala." {
		t.Errorf("Reply = %q; want texto do modelo", out.Reply)
	}
	if !out.Spoken {
		t.Errorf("Spoken = false; want true (canal de voz)")
	}
	if out.Error != "" {
		t.Errorf("Error = %q; want vazio", out.Error)
	}
	if out.ToolsUsed == nil || len(out.ToolsUsed) != 0 {
		t.Errorf("ToolsUsed = %v; want slice vazio não nulo", out.ToolsUsed)
	}
	if ft.total() != 0 {
		t.Errorf("tools executadas = %d; want 0", ft.total())
	}
}

func TestRunnerRespostaDiretaTelegramCriterio3(t *testing.T) {
	fm := &fakeModel{}
	fm.add("Já liguei a luz da sala.")
	m := novoMotorFake(t, fm)
	ft := &fakeTools{}
	refs := registrarFakeTools(m.Genkit, ft)

	var gHA gravadaHA
	ts := novoServidorHA(t, &gHA, http.StatusOK) // qualquer request aqui falha o teste
	cli := novoClientHA(ts)

	out, err := rodarTurno(t, m, cli, refs, ChatInput{Text: "ligue a luz da sala", Source: "telegram"})
	if err != nil {
		t.Fatalf("turno: erro inesperado: %v", err)
	}
	if out.Reply != "Já liguei a luz da sala." {
		t.Errorf("Reply = %q; want texto do modelo", out.Reply)
	}
	if out.Spoken {
		t.Errorf("Spoken = true; want false (telegram não aciona a Alexa)")
	}
	if out.Error != "" {
		t.Errorf("Error = %q; want vazio", out.Error)
	}
	if gHA.Method != "" || gHA.Path != "" {
		t.Errorf("Speak foi chamado: %s %s; want nenhuma requisição", gHA.Method, gHA.Path)
	}
}

func TestRunnerTetoIteracoesCriterio4(t *testing.T) { // sempre tool → teto
	fm := &fakeModel{}
	fm.add(ai.ToolRequest{Name: "get_weather", Input: map[string]any{"location": "São Paulo"}}) // passo único
	m := novoMotorFake(t, fm)
	ft := &fakeTools{}
	refs := registrarFakeTools(m.Genkit, ft)

	var gHA gravadaHA
	ts := novoServidorHA(t, &gHA, http.StatusOK)
	cli := novoClientHA(ts)

	out, err := rodarTurno(t, m, cli, refs, ChatInput{Text: "que horas vão bater?"})
	if err == nil {
		t.Fatalf("teto de iterações não disparou; out = %+v", out)
	}
	// A Agents API devolve (out, nil) com out.Error populado em
	// ErrMaxTurnsExceeded; o Runner converte em erro. Não dependemos da
	// mensagem exata (vem do Genkit), só do fato de o turno falhar.
	if got := ft.total(); got != maxTurns {
		t.Errorf("tools executadas = %d; want %d (maxTurns)", got, maxTurns)
	}
}

func TestRunnerSpeakFalhaCriterio5(t *testing.T) {
	fm := &fakeModel{}
	fm.add("Já liguei a luz da sala.")
	m := novoMotorFake(t, fm)
	ft := &fakeTools{}
	refs := registrarFakeTools(m.Genkit, ft)

	var gHA gravadaHA
	ts := novoServidorHA(t, &gHA, http.StatusInternalServerError)
	cli := novoClientHA(ts)

	out, err := rodarTurno(t, m, cli, refs, ChatInput{Text: "ligue a luz da sala"})
	if err != nil {
		t.Fatalf("turno falhou por erro de TTS: %v", err)
	}
	if out.Spoken {
		t.Errorf("Spoken = true; want false")
	}
	if out.Error == "" {
		t.Error("Error vazio; want preenchido com a falha do speak")
	}
	if out.Reply != "Já liguei a luz da sala." {
		t.Errorf("Reply = %q; want preservado", out.Reply)
	}
	if strings.Contains(out.Error, "token-de-teste") {
		t.Errorf("Error vazou secret: %q", out.Error)
	}
}

func TestRunnerTextoVazioSemConteudo(t *testing.T) {
	fm := &fakeModel{}
	fm.add("") // resposta do modelo sem texto
	m := novoMotorFake(t, fm)
	refs := registrarFakeTools(m.Genkit, &fakeTools{})

	var gHA gravadaHA
	ts := novoServidorHA(t, &gHA, http.StatusOK)
	cli := novoClientHA(ts)

	out, err := rodarTurno(t, m, cli, refs, ChatInput{Text: "…"})
	if err != nil {
		t.Fatalf("turno falhou por texto vazio: %v", err)
	}
	if out.Spoken {
		t.Errorf("Spoken = true; want false (sem conteúdo para falar)")
	}
	if out.Error != "sem conteúdo para falar" {
		t.Errorf("Error = %q; want %q", out.Error, "sem conteúdo para falar")
	}
	if out.Reply != "" {
		t.Errorf("Reply = %q; want vazio", out.Reply)
	}
}

func TestRunnerToolsUsedOrdenado(t *testing.T) {
	fm := &fakeModel{}
	fm.add([]ai.ToolRequest{
		{Name: "get_weather", Input: map[string]any{"location": "São Paulo"}},
		{Name: "control_device", Input: map[string]any{"action": "on", "entity_id": "switch.tomada_sala"}},
	})
	fm.add("Resolvido.")
	m := novoMotorFake(t, fm)
	ft := &fakeTools{}
	refs := registrarFakeTools(m.Genkit, ft)

	var gHA gravadaHA
	cli := novoClientHA(novoServidorHA(t, &gHA, http.StatusOK))

	out, err := rodarTurno(t, m, cli, refs, ChatInput{Text: "clima e liga a tomada"})
	if err != nil {
		t.Fatalf("turno: erro inesperado: %v", err)
	}
	if got := ft.total(); got != 2 {
		t.Fatalf("tools executadas = %d; want 2", got)
	}
	if fmt.Sprint(out.ToolsUsed) != "[control_device get_weather]" {
		t.Errorf("ToolsUsed = %v; want [control_device get_weather] (ordenado)", out.ToolsUsed)
	}
}

func TestRunnerSessionIDNaoAltera(t *testing.T) {
	fm := &fakeModel{}
	fm.add("Já liguei a luz da sala.")
	m := novoMotorFake(t, fm)
	refs := registrarFakeTools(m.Genkit, &fakeTools{})

	var gHA gravadaHA
	cli := novoClientHA(novoServidorHA(t, &gHA, http.StatusOK))

	out, err := rodarTurno(t, m, cli, refs, ChatInput{
		Text:      "ligue a luz da sala",
		SessionID: "telegram_123",
	})
	if err != nil {
		t.Fatalf("turno: erro inesperado: %v", err)
	}
	if out.Reply != "Já liguei a luz da sala." || !out.Spoken || out.Error != "" {
		t.Errorf("out = %+v; want comportamento do canal de voz", out)
	}
}
```

Add `"strings"` to the import block of `agent_test.go` (needed by `TestRunnerSpeakFalhaCriterio5`).

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -run TestRunner ./internal/agent/ -v`
Expected: FAIL / build error — `NewRunner undefined`, `Runner.Run undefined`.

- [ ] **Step 3: Implement `runner.go`**

Create `internal/agent/runner.go`:

```go
package agent

import (
	"context"
	"slices"

	"github.com/firebase/genkit/go/ai"
	aix "github.com/firebase/genkit/go/ai/exp"

	"home-assistent-go/internal/brain"
	"home-assistent-go/internal/ha"
)

// Runner adapta o agente home_assistent à interface api.TurnRunner: roda o
// turno via agent.RunText, extrai reply + tools do AgentOutput, e roteia o
// passo speak no canal de voz. Stateless — o SessionID do ChatInput é
// transportado e não consumido (sem WithSessionStore).
type Runner struct {
	motor *brain.Motor
	ag    *aix.Agent[struct{}]
	cli   *ha.Client
}

// NewRunner monta o Runner sobre o motor, o agente e o client HA injetados.
func NewRunner(m *brain.Motor, ag *aix.Agent[struct{}], cli *ha.Client) *Runner {
	return &Runner{motor: m, ag: ag, cli: cli}
}

// Run executa um turno conversacional: deadline de turno (Motor.TurnDeadline)
// → agent.RunText → mapeamento da saída → speak (canal de voz). Erro do agente
// (provider, ErrMaxTurnsExceeded) vira erro (→ 500 na API); falha de speak
// nunca derruba o turno (Spoken=false, Error=<msg> → 200).
func (r *Runner) Run(ctx context.Context, in ChatInput) (ChatOutput, error) {
	gctx, cancel := r.motor.TurnDeadline(ctx)
	defer cancel()

	out, err := r.ag.RunText(gctx, in.Text)
	if err != nil {
		// Connect/Send falhou, ou o context do turno foi cancelado (deadline).
		return ChatOutput{}, err
	}
	// ErrMaxTurnsExceeded (e falhas de provider que resolveram graciosamente):
	// a Agents API devolve (out, nil) com out.Error populado e FinishReason
	// Aborted — não (nil, err). Convertemos em erro para a API devolver 500.
	if out.Error != nil {
		return ChatOutput{}, out.Error
	}

	reply := ""
	if out.Message != nil {
		reply = out.Message.Text()
	}
	tools := extractToolNames(out.State)

	if in.Source == "telegram" {
		// Canal de texto: fim direto, speak NUNCA é chamado.
		return ChatOutput{Reply: reply, ToolsUsed: tools}, nil
	}
	spoken, speakErr := speak(ctx, r.cli, reply)
	return ChatOutput{Reply: reply, Spoken: spoken, Error: speakErr, ToolsUsed: tools}, nil
}

// extractToolNames percorre as mensagens do estado da conversa e devolve os
// nomes das tools chamadas pelo modelo, ordenados e nunca nil. O State é
// populado mesmo sem session store (client-managed). Nil-safe: State nil → [].
func extractToolNames(state *aix.SessionState[struct{}]) []string {
	used := []string{}
	if state == nil {
		return used
	}
	for _, msg := range state.Messages {
		if msg == nil {
			continue
		}
		for _, part := range msg.Content {
			if part.IsToolRequest() && part.ToolRequest != nil {
				used = append(used, part.ToolRequest.Name)
			}
		}
	}
	slices.Sort(used)
	return used
}

// speak é o passo terminal defensivo (canal de voz): TTS via client HA. Nunca
// falha o turno — texto vazio → "sem conteúdo para falar"; cli nil → erro de
// wiring; qualquer falha do HA vira Spoken=false com o erro como string.
func speak(ctx context.Context, cli *ha.Client, texto string) (bool, string) {
	if texto == "" {
		return false, "sem conteúdo para falar"
	}
	if cli == nil {
		return false, "agent: client Home Assistant ausente (wiring)"
	}
	res := cli.Speak(ctx, texto)
	return res.OK, res.Error
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -race ./internal/agent/ -v`
Expected: PASS — all `TestRunner*` + `TestDefineHomeAssistent*` + prompt tests green.

If `TestRunnerTetoIteracoesCriterio4` fails because the agent returns `(out, nil)` with `out.Error==nil` (i.e. max-turns behaves differently than expected), inspect: print `out.FinishReason` and `out.Error`. The test asserts `ft.total() == maxTurns` (8) — if the framework caps at a different count, adjust the assertion to `ft.total() >= 1` and that `err != nil`. Do NOT change `maxTurns` from 8.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/runner.go internal/agent/agent_test.go
git commit -m "feat(agent): Runner adapta o agente à interface api.TurnRunner

Runner.Run: TurnDeadline → agent.RunText → extrai reply + tools do
AgentOutput.State → speak no canal de voz. Trata o caminho ErrMaxTurnsExceeded
(out.Error populado, err nil) convertendo em erro (→ 500). Testes portados
do flow_test.go (critérios 1-6 + ordenação + texto vazio).

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

---

## Task 5: Repoint `api` package from `brain` to `agent`

**Files:**
- Modify: `internal/api/api.go`
- Modify: `internal/api/chat.go`
- Modify: `internal/api/api_test.go`
- Modify: `internal/api/integration_test.go`

**Interfaces:**
- Consumes: `agent.ChatInput`, `agent.ChatOutput` (from Task 2); `agent.NewRunner`, `agent.DefineHomeAssistentWithRefs` (from Tasks 3-4); `brain.NewMotor` (unchanged).
- Produces: `api.TurnRunner` interface now over `agent.ChatInput`/`agent.ChatOutput` (consumed by `cmd/brain/main.go` in Task 6).

- [ ] **Step 1: Edit `internal/api/api.go`**

Change the import and `TurnRunner` interface signature. Replace:

```go
	"home-assistent-go/internal/brain"
```
with:
```go
	"home-assistent-go/internal/agent"
```

Replace the `TurnRunner` interface:

```go
// TurnRunner executa um turno conversacional (agente home_assistent). O
// Runner de produção (agent.Runner, sobre aix.Agent[struct{}]) satisfaz a
// interface; a seam permite testar os handlers sem Genkit.
type TurnRunner interface {
	Run(ctx context.Context, in agent.ChatInput) (agent.ChatOutput, error)
}
```

- [ ] **Step 2: Edit `internal/api/chat.go`**

Change the import: replace `"home-assistent-go/internal/brain"` with `"home-assistent-go/internal/agent"`.

Replace the two references to `brain.ChatInput`:
```go
	in := agent.ChatInput{Text: *req.Text, Source: sourceNormalizado(req.Metadata)}
```
(The `in.SessionID = req.Metadata.SessionID` line stays — `SessionID` is a field of `agent.ChatInput`.)

- [ ] **Step 3: Edit `internal/api/api_test.go`**

Change import: replace `"home-assistent-go/internal/brain"` with `"home-assistent-go/internal/agent"`.

Replace all `brain.ChatInput` → `agent.ChatInput` and `brain.ChatOutput` → `agent.ChatOutput` (4 occurrences in `fakeRunner` definition + test bodies). Specifically:
```go
type fakeRunner struct {
	in  agent.ChatInput
	out agent.ChatOutput
	err error
}

func (f *fakeRunner) Run(_ context.Context, in agent.ChatInput) (agent.ChatOutput, error) {
	f.in = in
	return f.out, f.err
}
```
And in `TestChatHeaderCorretoPassa`, `TestChatRespostaExata`, `TestChatSpeakFalho200`, `TestChatToolsUsedNuncaNull`, `TestChatTextVazioPassaAoFlow`: replace `brain.ChatOutput{...}` with `agent.ChatOutput{...}` and `brain.ChatInput{}` with `agent.ChatInput{}`.

- [ ] **Step 4: Edit `internal/api/integration_test.go`**

This test builds the real stack. Three changes:

(a) Imports — replace `"home-assistent-go/internal/brain"` with:
```go
	"home-assistent-go/internal/agent"
```
Keep `"home-assistent-go/internal/brain"` ONLY if still used — after the edit below it is still used for `brain.NewMotor`, so **keep both**:
```go
	"home-assistent-go/internal/agent"
	"home-assistent-go/internal/brain"
```

(b) In `montaStack`, add `genkit.WithExperimental()` to the `genkit.Init` call:
```go
	g := genkit.Init(ctx, genkit.WithExperimental())
```

(c) In `montaStack`, replace the flow wiring:
```go
	flow := brain.DefineBrainWithRefs(m, cli, refs)

	router := NewRouter(Options{
		Runner:   flow,
```
with:
```go
	ag := agent.DefineHomeAssistentWithRefs(m, refs)
	runner := agent.NewRunner(m, ag, cli)

	router := NewRouter(Options{
		Runner:   runner,
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test -race ./internal/api/ -v`
Expected: PASS — all api unit tests + integration tests green. The integration tests exercise the real agent end-to-end against httptest mocks.

- [ ] **Step 6: Commit**

```bash
git add internal/api/api.go internal/api/chat.go internal/api/api_test.go internal/api/integration_test.go
git commit -m "refactor(api): TurnRunner aponta para agent em vez de brain

ChatInput/ChatOutput agora vivem em internal/agent. Teste integrado usa
agent.DefineHomeAssistentWithRefs + agent.NewRunner e ativa o gate experimental
no genkit.Init do stack.

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

---

## Task 6: Wire `agent` into `cmd/brain/main.go`

**Files:**
- Modify: `cmd/brain/main.go`

**Interfaces:**
- Consumes: `agent.DefineHomeAssistent`, `agent.NewRunner` (Tasks 3-4); `brain.Setup`, `brain.ActiveModel` (unchanged); `api.NewRouter` (Task 5 — `Options.Runner` is now `agent.Runner`).
- Produces: a runnable `cmd/brain` binary wiring the agent.

- [ ] **Step 1: Edit `cmd/brain/main.go`**

Add the `agent` import:
```go
	"home-assistent-go/internal/agent"
```

Replace the flow wiring block:
```go
	// §4.4/§4.5: flow "brain" com client + tools — DefineBrain vincula o
	// catálogo único (tools.Catalog, spec 06 §6) ao registry; chamar
	// tools.Catalog de novo registraria as actions em duplicidade (panico).
	flow := brain.DefineBrain(motor, cli)
```
with:
```go
	// Agente home_assistent sobre a Agents API do Genkit (genkit/exp.DefineAgent):
	// o framework roda o loop de tools internamente. DefineHomeAssistent vincula
	// o catálogo único (tools.Catalog) ao agente; chamar tools.Catalog de novo
	// registraria as tools em duplicidade (panic).
	ag := agent.DefineHomeAssistent(motor, cli)
	runner := agent.NewRunner(motor, ag, cli)
```

Replace the `api.NewRouter` call's `Runner` field:
```go
	router := api.NewRouter(api.Options{
		Runner:   runner,
```
(the rest of the `Options` struct — `APIKey`, `Provider`, `Model`, `Logger` — stays).

- [ ] **Step 2: Build and run tests**

Run: `go build ./cmd/brain && go test -race ./...`
Expected: build succeeds; all tests pass. (`internal/brain` still has `flow.go`/`prompt.go` present but now unused by `main` — they're removed in Task 7. They still compile, so `go test ./...` is green.)

- [ ] **Step 3: Commit**

```bash
git add cmd/brain/main.go
git commit -m "feat(brain): main wired ao agente home_assistent

Substitui brain.DefineBrain por agent.DefineHomeAssistent + agent.NewRunner.
O loop de tools agora roda pelo framework (Agents API), não pelo flow manual.

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

---

## Task 7: Remove dead `brain/flow.go`, `brain/prompt.go`, and their tests

**Files:**
- Delete: `internal/brain/flow.go`
- Delete: `internal/brain/prompt.go`
- Delete: `internal/brain/flow_test.go`
- Delete: `internal/brain/prompt_test.go`

**Interfaces:**
- Consumes: Tasks 5 & 6 have removed all references to `brain.DefineBrain`, `brain.DefineBrainWithRefs`, `brain.ChatInput`, `brain.ChatOutput`, `brain.systemPrompt*`, `brain.systemPromptFor`.
- Produces: a clean `internal/brain` package containing only the cognitive motor.

- [ ] **Step 1: Verify nothing references the files being deleted**

Run: `grep -rn "DefineBrain\|systemPromptFor\|systemPromptVoz\|systemPromptTelegram\|maxIterTools\|defineBrain" --include="*.go" internal/ cmd/`
Expected: NO output (all references gone). If any remain, stop and fix them before deleting.

- [ ] **Step 2: Delete the four files**

```bash
git rm internal/brain/flow.go internal/brain/prompt.go internal/brain/flow_test.go internal/brain/prompt_test.go
```

- [ ] **Step 3: Run the full suite**

Run: `go build ./... && go vet ./... && go test -race ./...`
Expected: build + vet + all tests green. `internal/brain` now contains only `brain.go`, `model.go`, `plugin.go`, `brain_test.go`, `model_test.go`, `plugin_test.go`.

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "chore(brain): remove flow.go e prompt.go (substituídos pelo agent)

O loop de tools manual e os prompts por canal saem do brain — o agente
home_assistent (internal/agent) é o novo dono do turno. Brain fica só com o
motor cognitivo (Motor/Setup/plugin/model).

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

---

## Task 8: Final verification — build, vet, test, fmt

**Files:** none (verification only).

- [ ] **Step 1: Format**

Run: `gofmt -w .`

- [ ] **Step 2: Vet**

Run: `go vet ./...`
Expected: no issues.

- [ ] **Step 3: Full test suite with race**

Run: `go test -race ./...`
Expected: all packages green. Specifically confirm:
- `internal/agent` — 9 runner/agent/prompt tests
- `internal/api` — unit + integration tests (integration now via agent)
- `internal/brain` — motor tests (Setup, GenerationContext, TurnDeadline, ActiveModel, modelName, pluginFor)
- `internal/tools`, `internal/ha`, `internal/config` — unchanged, green

- [ ] **Step 4: Build the binary**

Run: `go build -o bin/brain ./cmd/brain`
Expected: binary produced.

- [ ] **Step 5: If any leftover formatting changes, commit**

```bash
git add -A
git diff --cached --quiet || git commit -m "chore: gofmt final

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

- [ ] **Step 6: Report**

Report: branch `feat/agente-genkit`, files created/deleted/edited, test counts, and that `make test` / `make build` are green. Note any deviations from the plan and why.
