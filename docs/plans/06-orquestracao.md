# Spec 06 — Orquestração dos fluxos (F07) — Plano de Implementação

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implementar o flow Genkit `"brain"` em `internal/brain` — o turno completo `chatbot → (executarTools → chatbot)* → speak → fim`, com roteamento por canal (§4), teto de 8 iterações de tools, TTS terminal nunca-falha (ADR-0002) e system prompt por canal (§5); e fechar o catálogo único de tools em `internal/tools/catalog.go` com `get_weather + control_device` (§6).

**Architecture:** Package `internal/brain`. `prompt.go` carrega as duas constantes de system prompt (pt-BR) e o roteador `systemPromptFor(source)`. `flow.go` carrega `ChatInput`/`ChatOutput` (§2, contrato exato) e o flow `DefineBrain(m *Motor, cli *ha.Client)` — os nós do LangGraph viram funções nomeadas (`chatbot`, `executarTools`, `speak`) dentro do flow. Loop manual de tools: cada `Generate` roda com `ai.WithReturnToolRequests(true)` e, havendo `ToolRequests()`, cada request executa via `genkit.LookupTool(...).RunRaw(...)` e retorna ao histórico como mensagem `RoleTool` — nova `Generate` (de volta ao chatbot). Deadline de cada geração vem de `m.GenerationContext(ctx)` (LLM_TIMEOUT_S, spec 02). Speak via `ha.Client` injetado, resultado defensivo (`Spoken`/`Error`), nunca falha o flow. Testes em `flow_test.go`/`prompt_test.go` (mesmo package) com modelo fake via `genkit.DefineModel`, tools fake via `genkit.DefineTool` e client HA mockado por `ha.NewClient(config.Settings{HAURL: ts.URL, ...})` + httptest.

**Tech Stack:** Go 1.25, stdlib (`context`, `slices`, `fmt`, `errors`) + `github.com/firebase/genkit/go` v1.13.1 (dependência direta já em `go.mod`). Testes com `-race`. Zero dependência nova.

**Spec:** `docs/specs/06-orquestracao.md` (fonte da verdade — §2 tipos, §3 passos, §4 roteamento, §5 prompts, §6 catálogo, §8 critérios de aceite).

## Global Constraints

- Go 1.25; module `home-assistent-go`; arquivos de produção em `internal/brain` (`prompt.go`, `flow.go`) + EXCEÇÃO única `internal/tools/catalog.go`. Testes ao lado (`prompt_test.go`, `flow_test.go`, `catalog_test.go`). NÃO tocar em `internal/config`, `internal/ha`, `cmd/`, `docs/specs/*`, `.env`/`.env.example`. Nenhuma dependência nova em `go.mod`.
- **Contrato de tipos (§2, exato):**
  ```go
  type ChatInput struct {
      Text      string // fala/texto do usuário
      Source    string // "satellite" (default) | "telegram"
      SessionID string // carregado ao estado, NÃO consumido (§7)
  }
  type ChatOutput struct {
      Reply     string   // texto final do motor cognitivo
      Spoken    bool     // TTS acionado com sucesso
      Error     string   // erro do passo speak, se houver
      ToolsUsed []string // nomes das tools executadas (ordenado)
  }
  ```
- **Flow obrigatoriamente `genkit.DefineFlow(g, "brain", …)`** com `ChatInput → ChatOutput`. Assinatura de produção consumida pela spec 07: `func DefineBrain(m *Motor, cli *ha.Client) *core.Flow[ChatInput, ChatOutput, struct{}]` — interna ao closure: `m.Genkit`, `m.ModelName`, `m.GenerationContext` (nunca `genkit.Init` novo). Costura de catálogo: `DefineBrain` vincula `tools.Catalog(m.Genkit, cli)` — o catálogo continua a única fonte de verdade (§6); variante injetável `defineBrain(m, cli, refs)` (mesmo package) existe APENAS como seam dos testes (decisão 12, paridade com `newWeatherTool`/`newWeatherAPI`).
- **Loop manual de tools (§3)** — cada Generate com `ai.WithReturnToolRequests(true)`; tools vinculadas com `ai.WithTools(refs...)`. Enquanto `resp.ToolRequests()` não vazio: executa CADA request via `genkit.LookupTool(g, req.Name)` + `tool.RunRaw(ctx, req.Input)` (context do turno, NÃO o context de geração — deadline do LLM é da geração; tools têm timeout próprio), acumula `ToolsUsed`, anexa ao histórico `resp.Message` + mensagem `ai.RoleTool` com `ai.NewToolResponsePart(&ai.ToolResponse{Name, Ref, Output})` e nova Generate. Tool ausente no registry (alucinação do LLM) ou `RunRaw` com erro → flow termina com erro (falha de wiring, não de negócio).
- **Teto de segurança (§3):** no máximo 8 iterações de execução de tools; a 9ª rodada de ToolRequests → erro EXATO `"limite de 8 iterações de ferramentas excedido"` — o turno termina com erro (não arrasta até o timeout do LLM). Constante `maxIterTools = 8` única fonte do número.
- **Roteamento (§4, paridade `route_tools`):** (1) tool requests pendentes → `executarTools` → volta ao chatbot; (2) sem tool calls e `source == "telegram"` → fim direto, `Spoken=false`, `Error=""`, `Speak` NUNCA chamado; (3) sem tool calls e canal de voz (source vazio/"satellite"/outros) → `speak` → fim. `source` chega JÁ normalizado pela API — o flow NÃO normaliza (comparação literal com `"telegram"`).
- **Speak (§3, ADR-0002):** texto = `resp.Text()` da última resposta (sem `content_to_text` — Genkit já devolve texto limpo). `cli.Speak(ctx, texto)`: OK → `Spoken=true`, `Error=""`; falha → `Spoken=false`, `Error=res.Error` — NUNCA falha o flow por TTS. Texto vazio → `Spoken=false`, `Error="sem conteúdo para falar"`. `Reply` sempre preservado. Client nil → falha defensiva de speak (mesma forma).
- **Deadline do chatbot (§3):** cada `Generate` roda em context derivado de `m.GenerationContext(ctx)` (LLM_TIMEOUT_S) com CancelFunc adiada; erro de geração (inclui timeout) encerra o flow com erro. Tools rodam no context do turno (não derivado).
- **System prompts por canal (§5)** em `internal/brain/prompt.go` como constantes pt-BR. O `prompt.py` NÃO está no repo — textos derivados da §5 (voz: texto plano falável, sem Markdown/símbolos/JSON, frases curtas 1–2 frases, confirmação de automação em uma frase, nunca inventar clima → usar a ferramenta, não sabe → diz, breve; telegram: Markdown permitido, um pouco mais longas, direto, sem JSON/estruturas, mesmas regras de clima e honestidade). **Sem menção alguma a busca/resultados de busca (decisão 10).** Roteador `systemPromptFor(source)`: `source == "telegram"` → telegram; caso contrário voz.
- **Estado (§7):** histórico de mensagens local ao turno (`[]*ai.Message`), sem persistência; `SessionID` apenas recebido em `ChatInput` e não consumido (estado local; paridade com o Python sem checkpointer).
- **`ToolsUsed` (§2):** acumulado na ordem de execução e devolvido ORDENADO (`slices.Sort` em cópia); vazio → slice vazio (não nil) para o JSON da API.
- **Catálogo (§6):** `Catalog(g *genkit.Genkit, cli *ha.Client) []ai.ToolRef` devolvendo `[]ai.ToolRef{NewWeather(g), DefineControlDevice(g, cli)}` — única fonte de verdade vinculada ao chatbot (paridade `ALL_TOOLS`). Mudança assinada do parâmetro (client injetado entra porque `control_device` exige `*ha.Client` — decisão 7 da spec 05).
- **Testes (§8):** modelo fake via `genkit.DefineModel` (endereçado por `m.ModelName` do Motor construído à mão no mesmo package — paridade com `brain_test.go`); TTS mockado apontando `ha.NewClient(config.Settings{HAURL: ts.URL, ...})` para servidor httptest (padrão de `internal/ha` e `internal/tools`); reutilizar `config.Settings` (nada de parsing novo); zero secrets em logs/erros; cada `genkit.Init` por teste (registry é por instância). Cobrir os 6 critérios de aceite + roteamento de prompt por canal.
- Gates antes de CADA commit: `gofmt -l .` vazio; `go vet ./...` limpo; `go test -race ./...` verde (módulo inteiro).
- Commits locais apenas — NUNCA push, NUNCA merge. Worktree: `/Users/mac01/workspace/home-assistent-go/.worktrees/spec-06`, branch `spec/06-orquestracao`. Mensagens no padrão `feat(brain): …`/`feat(tools): …`/`docs(plans): …` + linha final `Co-Authored-By: Claude Code <noreply@anthropic.com>`.
- Sem revisão (política do usuário): sem reviewer, sem fix loop, sem `/code-review`.

---

### Task 0: Plano

**Files:**
- Create: `docs/plans/06-orquestracao.md` (este arquivo).

- [x] **Step 1: Escrever o plano** e commitar.

```bash
git add docs/plans/06-orquestracao.md
git commit -m "docs(plans): plano da spec 06

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

---

### Task 1: System prompts por canal — `prompt.go` (§5)

**Files:**
- Create: `internal/brain/prompt.go`
- Create: `internal/brain/prompt_test.go`

**Interfaces:**
- Consumes: nada (constantes + função pura).
- Produces (Task 3 consome): `const systemPromptVoz`, `const systemPromptTelegram` (pt-BR, §5) e `func systemPromptFor(source string) string` — `source == "telegram"` → telegram; caso contrário voz.

- [ ] **Step 1: Escrever os testes que falham** — criar `internal/brain/prompt_test.go`:

```go
package brain

import (
	"strings"
	"testing"
)

func TestSystemPromptFor(t *testing.T) { // §4/§5: roteamento literal por source
	casos := []struct{ source, want string }{
		{"telegram", systemPromptTelegram},
		{"satellite", systemPromptVoz},
		{"", systemPromptVoz},        // default voz
		{"alexa", systemPromptVoz},   // valor desconhecido → voz (regressão zero)
		{"Telegram", systemPromptVoz}, // sem normalização: comparação literal
	}
	for _, tc := range casos {
		if got := systemPromptFor(tc.source); got != tc.want {
			t.Errorf("systemPromptFor(%q) não devolveu o prompt esperado (got len=%d want len=%d)", tc.source, len(got), len(tc.want))
		}
	}
}

func TestPromptsCumpremA5(t *testing.T) { // §5: regras obrigatórias nos textos
	// Voz: texto plano, frases curtas, clima por ferramenta, honestidade.
	for _, regra := range []string{"Markdown", "ferramenta", "clima"} {
		if !strings.Contains(systemPromptVoz, regra) {
			t.Errorf("systemPromptVoz não menciona %q", regra)
		}
	}
	if !strings.Contains(systemPromptVoz, "get_weather") {
		t.Errorf("systemPromptVoz não nomeia a ferramenta get_weather")
	}
	// Telegram: mesmas regras de clima e honestidade.
	for _, regra := range []string{"Markdown", "JSON", "ferramenta", "clima"} {
		if !strings.Contains(systemPromptTelegram, regra) {
			t.Errorf("systemPromptTelegram não menciona %q", regra)
		}
	}
}

func TestPromptsSemMencaoABusca(t *testing.T) { // decisão 10: sem "busca"
	for nome, p := range map[string]string{"voz": systemPromptVoz, "telegram": systemPromptTelegram} {
		if strings.Contains(p, "busca") || strings.Contains(p, "Busca") || strings.Contains(p, "search") || strings.Contains(p, "Search") {
			t.Errorf("prompt %s menciona busca (decisão 10): %q", nome, p)
		}
	}
}
```

- [ ] **Step 2: Rodar e verificar que falham de compilação**

Run: `go test ./internal/brain/`
Expected: FAIL — `undefined: systemPromptVoz`, `undefined: systemPromptTelegram`, `undefined: systemPromptFor`.

- [ ] **Step 3: Implementar `internal/brain/prompt.go`** (constantes derivadas da §5 — o prompt.py não está no repo; sem menção a busca, decisão 10)

```go
package brain

// System prompts por canal (spec 06 §5), pt-BR. O prompt.py do projeto Python
// não está no repo — os textos derivam da §5, tradução fiel sem a frase sobre
// "resultados de busca" (decisão 10).
const (
	systemPromptVoz = `Você é o assistente da casa da família. Responda sempre em português do Brasil.

Você está falando por voz, e a sua resposta vira fala na Alexa. Siga estas regras:
- Responda em texto plano, falável: nada de Markdown, símbolos ou JSON.
- Use frases curtas: no máximo uma ou duas frases por resposta.
- Confirme automações em uma única frase (ex.: "Liguei a luz da sala.").
- Nunca invente clima: quando perguntarem, use a ferramenta get_weather.
- Se não souber, diga que não sabe.
- Seja breve.`

	systemPromptTelegram = `Você é o assistente da casa da família. Responda sempre em português do Brasil.

Você está conversando pelo Telegram. Siga estas regras:
- Markdown é permitido e as respostas podem ser um pouco mais longas, mas vá direto ao ponto.
- Nunca devolva JSON nem estruturas de dados.
- Nunca invente clima: quando perguntarem, use a ferramenta get_weather.
- Se não souber, diga que não sabe.`
)

// systemPromptFor roteia o system prompt pelo canal (§5): telegram → prompt de
// texto; qualquer outro source (vazio, "satellite", desconhecido) → voz. O
// source chega já normalizado pela API (spec 07) — comparação literal, sem
// trim nem lowercase aqui.
func systemPromptFor(source string) string {
	if source == "telegram" {
		return systemPromptTelegram
	}
	return systemPromptVoz
}
```

- [ ] **Step 4: Rodar e verificar passagem**

Run: `go test -race ./internal/brain/ -v`
Expected: PASS nos 3 testes.

- [ ] **Step 5: Gates + Commit**

Run: `gofmt -l . && go vet ./... && go test -race ./...`

```bash
git add internal/brain/prompt.go internal/brain/prompt_test.go
git commit -m "feat(brain): system prompts por canal (voz e telegram)

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

---

### Task 2: Catálogo completo — `Catalog(g, cli)` com get_weather + control_device (§6)

**Files:**
- Modify: `internal/tools/catalog.go`
- Modify: `internal/tools/catalog_test.go`

**Interfaces:**
- Consumes: `NewWeather(g) ai.ToolRef` (spec 04), `DefineControlDevice(g, cli) *ai.ToolAction[ControlDeviceInput, string]` (spec 05 — implementa `ai.ToolRef`), `ha.Client` (não alterar).
- Produces (spec 06/07 consomem): `func Catalog(g *genkit.Genkit, cli *ha.Client) []ai.ToolRef` — registra as DUAS tools no registry de `g` e devolve as refs na ordem `{get_weather, control_device}`.

- [ ] **Step 1: Escrever os testes que falham** — reescrever `TestCatalogRegistraGetWeather` em `internal/tools/catalog_test.go` como catálogo completo (§6):

```go
package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/firebase/genkit/go/genkit"

	"home-assistent-go/internal/config"
	"home-assistent-go/internal/ha"
)

// TestCatalogRegistraGetWeatherEControlDevice valida o catálogo completo
// (spec 06 §6): duas tools registradas, na ordem get_weather + control_device,
// ambas resolvíveis no registry.
func TestCatalogRegistraGetWeatherEControlDevice(t *testing.T) {
	var g gravada
	ts := novoServidor(t, &g, http.StatusOK, `[]`)
	cli := novoClient(ts)

	ctx := context.Background()
	gk := genkit.Init(ctx)

	cat := Catalog(gk, cli)
	if len(cat) != 2 {
		t.Fatalf("catálogo tem %d tools; want 2 (spec 06 §6)", len(cat))
	}
	if cat[0].Name() != "get_weather" {
		t.Errorf("catálogo[0].Name() = %q; want get_weather", cat[0].Name())
	}
	if cat[1].Name() != ControlDeviceName {
		t.Errorf("catálogo[1].Name() = %q; want %q", cat[1].Name(), ControlDeviceName)
	}

	// get_weather registrada (contrato da spec 04).
	tool := genkit.LookupTool(gk, "get_weather")
	if tool == nil {
		t.Fatal("get_weather não está registrada no registry do Genkit (§2)")
	}
	def := tool.Definition()
	if def.Name != "get_weather" || def.Description != descricaoWeather {
		t.Errorf("definição = %q / %q; want nome %q e descrição do §2", def.Name, def.Description, nomeWeather)
	}
	props, ok := def.InputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("inputSchema = %v; want objeto com properties (§2)", def.InputSchema)
	}
	if _, ok := props["location"]; !ok {
		t.Errorf("inputSchema.properties = %v; want propriedade location (§2)", props)
	}

	// control_device registrada (contrato da spec 05) e executável pelo
	// registry — o caminho que a spec 06 usa: ToolRequest → RunRaw.
	cd := genkit.LookupTool(gk, ControlDeviceName)
	if cd == nil {
		t.Fatalf("%s não está registrada no registry do Genkit (§2)", ControlDeviceName)
	}
	out, err := cd.RunRaw(ctx, map[string]any{"action": "on", "entity_id": "switch.tomada_sala"})
	if err != nil {
		t.Fatalf("control_device RunRaw: erro inesperado: %v", err)
	}
	if got, ok := out.(string); !ok || got != "Liguei o tomada da sala." {
		t.Errorf("control_device output = %v (%T); want \"Liguei o tomada da sala.\"", out, out)
	}
	if g.Method != http.MethodPost || g.Path != "/api/services/switch/turn_on" {
		t.Errorf("requisição: %s %s; want POST /api/services/switch/turn_on", g.Method, g.Path)
	}
}

// TestCatalogExecutaGetWeatherViaRegistry executa get_weather pelo registry
// (o caminho que a spec 06 usa: Generate devolve ToolRequest → tool executa)
// com URLs injetadas — valida map JSON → input tipado → frase.
func TestCatalogExecutaGetWeatherViaRegistry(t *testing.T) {
	var gGeo, gPrev gravadaMeteo
	tsGeo := servidorMeteo(t, &gGeo, http.StatusOK,
		`{"results":[{"latitude":-23.5505,"longitude":-46.6333}]}`)
	tsPrev := servidorMeteo(t, &gPrev, http.StatusOK,
		`{"current":{"weather_code":80},"daily":{"temperature_2m_max":[28.4],"temperature_2m_min":[19.2],"weather_code":[80]}}`)
	w := &weatherAPI{
		geocodingURL: tsGeo.URL,
		forecastURL:  tsPrev.URL,
		http:         &http.Client{Timeout: 2 * time.Second},
	}
	ctx := context.Background()
	gk := genkit.Init(ctx)
	_ = newWeatherTool(gk, w) // registra a tool com URLs de teste neste registry

	tool := genkit.LookupTool(gk, "get_weather")
	if tool == nil {
		t.Fatal("get_weather não registrada")
	}
	out, err := tool.RunRaw(ctx, map[string]any{"location": "São Paulo"})
	if err != nil {
		t.Fatalf("RunRaw: erro inesperado: %v", err)
	}
	got, ok := out.(string)
	if !ok {
		t.Fatalf("output = %T (%v); want string", out, out)
	}
	if want := "Máxima de 28, mínima de 19, pancadas de chuva"; got != want {
		t.Errorf("output = %q; want %q", got, want)
	}
	if gPrev.Query.Get("latitude") != "-23.5505" || gPrev.Query.Get("longitude") != "-46.6333" {
		t.Errorf("forecast: coordenadas = %v; want -23.5505/-46.6333 (input cru chegou à tool)", gPrev.Query)
	}
}
```

(Manter os helpers já existentes de `weather_test.go` — `servidorMeteo`, `gravadaMeteo`; os de device — `gravada`, `novoServidor`, `novoClient` — vêm de `device_test.go`. O json/io import só se ainda não usados: ajustar imports ao compilador.)

- [ ] **Step 2: Rodar e verificar que falham**

Run: `go test ./internal/tools/ -run TestCatalog -v`
Expected: FAIL de compilação — `Catalog(g, cli)` não aceita 2 argumentos (e/ou `len(cat) != 2`).

- [ ] **Step 3: Implementar `internal/tools/catalog.go`**

```go
package tools

import (
	"github.com/firebase/genkit/go/ai"
	"github.com/firebase/genkit/go/genkit"

	"home-assistent-go/internal/ha"
)

// Catalog é o catálogo único de tools do motor cognitivo (spec 06 §6) —
// paridade com ALL_TOOLS do projeto Python: única fonte de verdade vinculada
// ao passo chatbot via ai.WithTools(...). Registra as duas tools no registry
// de g e devolve as refs na ordem get_weather + control_device. O client HA
// entra injetado (decisão 7 da spec 05) — o control_device é consumidor dele.
func Catalog(g *genkit.Genkit, cli *ha.Client) []ai.ToolRef {
	return []ai.ToolRef{NewWeather(g), DefineControlDevice(g, cli)}
}
```

- [ ] **Step 4: Rodar e verificar passagem**

Run: `go test -race ./internal/tools/ -v`
Expected: PASS em todos (weather/device continuam verdes).

- [ ] **Step 5: Gates + Commit**

Run: `gofmt -l . && go vet ./... && go test -race ./...`

```bash
git add internal/tools/catalog.go internal/tools/catalog_test.go
git commit -m "feat(tools): catálogo único completo — get_weather + control_device

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

---

### Task 3: Flow `"brain"` — ChatInput/ChatOutput, loop com teto, roteamento e speak (§2–§4, §7, §8)

**Files:**
- Create: `internal/brain/flow.go`
- Create: `internal/brain/flow_test.go`

**Interfaces:**
- Consumes: `Motor` (brain.go, não alterar — `Genkit`/`ModelName`/`GenerationContext`), `systemPromptFor` (Task 1), `tools.Catalog(g, cli)` (Task 2), `ha.Client.Speak` (spec 03), `ai.*`/`genkit.*` v1.13.1.
- Produces (spec 07 consome):
  - `type ChatInput struct { Text string; Source string; SessionID string }`
  - `type ChatOutput struct { Reply string; Spoken bool; Error string; ToolsUsed []string }`
  - `func DefineBrain(m *Motor, cli *ha.Client) *core.Flow[ChatInput, ChatOutput, struct{}]`
  - nós nomeados: `chatbot(ctx, m, system, msgs, refs)` · `executarTools(ctx, g, resp, refs)` · `speak(ctx, cli, texto)`.

- [ ] **Step 1: Escrever os testes que falham** — criar `internal/brain/flow_test.go` com modelo fake scriptável via `genkit.DefineModel`, tools fake via `genkit.DefineTool` e HA httptest. Estrutura:

Helpers (package brain, mesmos padrões de tools/ha):

```go
package brain

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/firebase/genkit/go/ai"
	"github.com/firebase/genkit/go/genkit"

	"home-assistent-go/internal/config"
	"home-assistent-go/internal/ha"
)

// fakeModel modela respostas scriptadas: cada chamada gera consome o próximo
// item do roteiro; a resposta é texto ou tool request por nome/input.
type fakeModel struct {
	mu    sync.Mutex
	steps []any // string (texto) | ai.ToolRequest
}

func (f *fakeModel) add(step any) { f.steps = append(f.steps, step) }

func (f *fakeModel) handle(ctx context.Context, req *ai.ModelRequest, cb ai.ModelStreamCallback) (*ai.ModelResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.steps) == 0 {
		return nil, fmt.Errorf("fakeModel: roteiro esgotado (msgs=%d)", len(req.Messages))
	}
	switch step := f.steps[0].(type) {
	case string:
		if len(f.steps) > 1 {
			f.steps = f.steps[1:]
		}
		return &ai.ModelResponse{
			Message:      ai.NewModelTextMessage(step),
			FinishReason: ai.FinishReasonStop,
		}, nil
	default: // ai.ToolRequest
		if len(f.steps) > 1 {
			f.steps = f.steps[1:]
		}
		return &ai.ModelResponse{
			Message:      ai.NewModelMessage(ai.NewToolRequestPart(&step)),
			FinishReason: ai.FinishReasonStop,
		}, nil
	}
}

// …helpers novoServidorHA/novoClientHA (httptest do HA, espelho de tools/device_test.go)…

// registroFakeTools registra get_weather e control_device fake no registry:
// gravam invocações e devolve frase fixa; devolve as refs para DefineBrain.
```

Modelos fake capturam `req` (para asserções de system prompt/histórico) num campo `lastReq` protegido pelo mesmo mutex.

Os 6 testes de aceite (§8):

1. `TestFlowToolLoopCriterio1` — fake tool `get_weather` (mock gravando input/output); modelo: step 1 = ToolRequest{Name:"get_weather", Input:{location:"São Paulo"}}, step 2 = texto "A máxima é de 28 graus." → `Reply` igual ao texto; `ToolsUsed == ["get_weather"]`; `Spoken==true`; HA httptest recebeu POST `/api/services/notify/alexa_media` com `message` = Reply.
2. `TestFlowRespostaDiretaVozCriterio2` — modelo: só texto → `Spoken==true`, `Error==""`, `ToolsUsed` vazio (não nil).
3. `TestFlowRespostaDiretaTelegramCriterio3` — modelo: só texto; `ChatInput{Source:"telegram"}` → `Spoken==false`, `Error==""`, **zero requests** ao servidor HA (gravações vazias), `Reply` preservado.
4. `TestFlowTetoIteracoesCriterio4` — modelo sempre ToolRequest → `flow.Run` devolve erro contendo `limite de 8 iterações de ferramentas excedido`; modelo consumiu exatamente 9 respostas (8 execuções + a 9ª pendente), fake tool executada 8 vezes.
5. `TestFlowSpeakFalhaCriterio5` — HA httptest respondendo 500 → `Spoken==false`, `Error` preenchido (não vazio), `Reply` preservado, `err == nil` (flow não falha).
6. `TestFlowSessionIDNaoAlteraCriterio6` — mesmo cenário do critério 2 com `SessionID:"telegram_123"` → mesmo comportamento (mesma resposta, mesmo prompt de voz — comparar lastReq do modelo).

Testes de suporte: `TestFlowSystemPromptPorCanal` (fake model captura primeira mensagem: voice → contém substring do prompt de voz; telegram → do telegram) e `TestFlowToolsUsedOrdenado` (modelo pede 2 tools no mesmo turno — `control_device` e `get_weather` — ToolsUsed ordenado lexicograficamente).

- [ ] **Step 2: Rodar e verificar que falham de compilação**

Run: `go test ./internal/brain/ -run TestFlow -v`
Expected: FAIL — `undefined: ChatInput`, `undefined: ChatOutput`, `undefined: DefineBrain`.

- [ ] **Step 3: Implementar `internal/brain/flow.go`**

```go
package brain

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/firebase/genkit/go/ai"
	"github.com/firebase/genkit/go/core"
	"github.com/firebase/genkit/go/genkit"

	"home-assistent-go/internal/ha"
	"home-assistent-go/internal/tools"
)

// ChatInput é a entrada do flow (spec 06 §2). Source já chega normalizado
// pela API (spec 07) — o flow não normaliza. SessionID é carregado ao estado
// e NÃO consumido (§7, reservado para sessão futura).
type ChatInput struct {
	Text      string
	Source    string
	SessionID string
}

// ChatOutput é a saída do flow (spec 06 §2). Error só carrega falha do passo
// speak — erros de geração/roteamento encerram o flow com erro.
type ChatOutput struct {
	Reply     string
	Spoken    bool
	Error     string
	ToolsUsed []string
}

// maxIterTools é o teto de segurança do loop de tools (§3): 8 iterações; a
// 9ª rodada de tool requests encerra o turno com erro explícito.
const maxIterTools = 8

// DefineBrain define o flow "brain" (§2) sobre o motor e o client HA
// injetados: chatbot → (executarTools → chatbot)* → speak → fim. O catálogo
// de tools é o de produção (tools.Catalog — §6, única fonte de verdade).
func DefineBrain(m *Motor, cli *ha.Client) *core.Flow[ChatInput, ChatOutput, struct{}] {
	return defineBrain(m, cli, tools.Catalog(m.Genkit, cli))
}

// defineBrain é a variante injetável (decisão 12) — seam dos testes, que
// registram tools fake no mesmo registry.
func defineBrain(m *Motor, cli *ha.Client, refs []ai.ToolRef) *core.Flow[ChatInput, ChatOutput, struct{}] {
	return genkit.DefineFlow(m.Genkit, "brain", func(ctx context.Context, in ChatInput) (ChatOutput, error) {
		// Estado local do turno (§7): histórico + tools usadas.
		msgs := []*ai.Message{ai.NewUserTextMessage(in.Text)}
		used := []string{}

		system := systemPromptFor(in.Source)
		var resp *ai.ModelResponse
		for {
			var err error
			resp, err = chatbot(ctx, m, system, msgs, refs)
			if err != nil {
				return ChatOutput{}, err
			}
			reqs := resp.ToolRequests()
			if len(reqs) == 0 {
				break // sem tool calls: sai para o roteamento de canal
			}
			if len(used) >= maxIterTools { // 9ª rodada pendente → teto (§3)
				return ChatOutput{}, fmt.Errorf("limite de %d iterações de ferramentas excedido", maxIterTools)
			}
			toolMsg, err := executarTools(ctx, m.Genkit, resp, refs)
			if err != nil {
				return ChatOutput{}, err
			}
			// Nomes acumulam na ordem de execução (§3); output sai ordenado.
			for _, p := range reqs {
				used = append(used, p.ToolRequest.Name)
			}
			msgs = append(msgs, resp.Message, toolMsg)
		}

		out := ChatOutput{Reply: resp.Text()}
		if in.Source == "telegram" { // §4.2: telegram → fim direto, sem Alexa
			out.ToolsUsed = toolsOrdenadas(used)
			return out, nil
		}
		// §4.3: canal de voz → speak (passo terminal, ADR-0002).
		spoken, speakErr := speak(ctx, cli, resp.Text())
		out.Spoken = spoken
		out.Error = speakErr
		out.ToolsUsed = toolsOrdenadas(used)
		return out, nil
	})
}

// chatbot é o passo de geração (§3): system prompt do canal + histórico +
// catálogo de tools, cada chamada com o deadline de LLM_TIMEOUT_S (spec 02).
func chatbot(ctx context.Context, m *Motor, system string, msgs []*ai.Message, refs []ai.ToolRef) (*ai.ModelResponse, error) {
	gctx, cancel := m.GenerationContext(ctx)
	defer cancel()
	return genkit.Generate(gctx, m.Genkit,
		ai.WithModelName(m.ModelName),
		ai.WithSystem(system),
		ai.WithMessages(msgs...),
		ai.WithTools(refs...),
		ai.WithReturnToolRequests(true),
	)
}

// executarTools executa cada tool request da resposta (§3) e devolve a
// mensagem RoleTool com os resultados (mesma ordem dos requests). Falha de
// wiring (tool ausente no registry ou RunRaw com erro) encerra o turno com
// erro — as tools de produção são defensivas e não devolvem erro.
func executarTools(ctx context.Context, g *genkit.Genkit, resp *ai.ModelResponse, _ []ai.ToolRef) (*ai.Message, error) {
	toolMsg := &ai.Message{Role: ai.RoleTool}
	for _, p := range resp.ToolRequests() {
		req := p.ToolRequest
		tool := genkit.LookupTool(g, req.Name)
		if tool == nil {
			return nil, fmt.Errorf("brain: tool %q não registrada", req.Name)
		}
		out, err := tool.RunRaw(ctx, req.Input)
		if err != nil {
			return nil, fmt.Errorf("brain: tool %q falhou: %w", req.Name, err)
		}
		toolMsg.Content = append(toolMsg.Content, ai.NewToolResponsePart(&ai.ToolResponse{
			Name:   req.Name,
			Ref:    req.Ref,
			Output: out,
		}))
	}
	return toolMsg, nil
}

// speak é o passo terminal (§3, ADR-0002): TTS via client HA injetado.
// Nunca falha o flow (devolve false + erro como string); texto vazio →
// "sem conteúdo para falar" (§3).
func speak(ctx context.Context, cli *ha.Client, texto string) (bool, string) {
	if texto == "" {
		return false, "sem conteúdo para falar"
	}
	if cli == nil {
		return false, "brain: client Home Assistant ausente (wiring)"
	}
	res := cli.Speak(ctx, texto)
	return res.OK, res.Error
}

// toolsOrdenada copia a lista e devolve ordenada (§2: "ordenado"), nunca nil.
func toolsOrdenadas(used []string) []string {
	out := make([]string, len(used))
	copy(out, used)
	slices.Sort(out)
	return out
}
```

(Asserções do teste 4: com o modelo sempre pedindo tool, `used` enche 8× e a 9ª ToolRequests dispara o teto — conferir contagens: fake tool executada 8 vezes; modelo 9 chamadas.)

- [ ] **Step 4: Rodar e verificar passagem**

Run: `go test -race ./internal/brain/ -v`
Expected: PASS nos 8 testes (Task 1 e testes antigos do package continuam verdes).

- [ ] **Step 5: Gates + Commit**

Run: `gofmt -l . && go vet ./... && go test -race ./...`

```bash
git add internal/brain/flow.go internal/brain/flow_test.go
git commit -m "feat(brain): flow brain — chatbot, loop de tools com teto, roteamento e speak

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

---

### Task 4: Gates finais da branch

**Files:**
- Modify (apenas se algum gate acusar): qualquer arquivo de `internal/brain/`, `internal/tools/catalog.go`.

- [ ] **Step 1: Gates completos no módulo inteiro**

```bash
gofmt -l .
go vet ./...
go mod tidy && git diff --exit-code go.mod go.sum
go test -race ./... -count=1
go build ./...
```

Expected: gofmt sem saída; vet limpo; `go.mod`/`go.sum` estáveis pós-tidy; testes verdes; build ok.

- [ ] **Step 2: Auditoria da spec** — reler `internal/brain/flow.go` + `prompt.go` + `internal/tools/catalog.go`: (a) flow nomeado "brain" via DefineFlow; (b) roteamento §4 exato (telegram nunca chama Speak — conferir via teste com zero requests); (c) teto de 8 com mensagem exata; (d) speak nunca falha o flow (critério 5 verde); (e) prompts sem menção a busca; (f) zero secrets em logs/erros; (g) nenhum arquivo fora de `internal/brain/`, `internal/tools/catalog.go(+test)` e `docs/plans/` alterado.

- [ ] **Step 3: Commit apenas se algum gate exigiu correção**

```bash
git add -A && git commit -m "chore(brain): ajustes dos gates finais

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

(se nada mudou, nenhuma commit nesta task)
