# Spec 02 — Motor cognitivo trocável (F02) — Plano de Implementação

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implementar `internal/brain` — setup do motor cognitivo Genkit: registro condicional do plugin do `LLM_PROVIDER` escolhido, resolução do modelo ativo (endereçado por nome no registry), deadline de `LLM_TIMEOUT_S` para as chamadas de geração e log de startup `provider` + `model`.

**Architecture:** Três arquivos no package `internal/brain`: `model.go` (funções puras: `ActiveModel` — defaults por provedor sobre `config.ResolvedModel()` — e `modelName` — endereçamento `provider/model` no registry), `plugin.go` (função pura `pluginFor` — monta **só** o plugin do provider escolhido e pré-valida a credencial mínima, pois os plugins fazem **panic** no `Init` sem credencial e o startup precisa de erro claro) e `brain.go` (`Motor` + `Setup` — orquestra e chama `genkit.Init` **uma única vez por processo** — e `GenerationContext`, que deriva o deadline de `LLM_TIMEOUT_S`).

**Tech Stack:** Go 1.25, `github.com/firebase/genkit/go` **v1.13.1** (módulo único que cobre `genkit`, `ai`, `core/api` e os plugins) com plugins `googlegenai`, `compat_oai/openai` e `ollama`; `github.com/openai/openai-go/option` para o `BaseURL`; `testing` padrão (`-race`).

**Spec:** `docs/specs/02-motor-cognitivo.md` (fonte da verdade — §2 provedores, §3 modelo ativo, §4 timeout, §5 troca de motor, §6 critérios manuais). Referência Python: `src/config.py::get_llm`.

## Global Constraints

- Package novo: `internal/brain`. Module: `home-assistent-go`. Go 1.25.
- Genkit **v1.13.1** fixado: `go get github.com/firebase/genkit/go@v1.13.1`. É um módulo único — cobre `genkit`, `ai`, `core/api` e todos os plugins; **não existe** `go get github.com/firebase/genkit/go/plugins/<x>` separado.
- Caminho real do plugin OpenAI: `github.com/firebase/genkit/go/plugins/compat_oai/openai` (a spec §2 cita `plugins/openai`, caminho que não existe no v1.13.1 — adaptação isolada em `internal/brain`). O struct é `openai.OpenAI{APIKey, Opts}` — **não tem campo `BaseURL`**; entra via `Opts: []option.RequestOption{option.WithBaseURL(...)}` (SDK `github.com/openai/openai-go/option`).
- APIs verificadas no v1.13.1: `genkit.Init(ctx, ...genkit.GenkitOption) *genkit.Genkit` (devolve sem `error`; **panica** em falha interna) · `genkit.WithPlugins(...api.Plugin)` · `genkit.WithDefaultModel(name)` · `genkit.LookupModel(g, name) ai.Model` (`nil` se não resolver; para ollama é cache-only, sem rede) · interface `api.Plugin`: `Name() string; Init(ctx) []api.Action` · `ollama.Ollama{ServerAddress string; Timeout int // segundos}` (panica se `ServerAddress` vazio) · `googlegenai.GoogleAI{APIKey string}` (panica no `Init` sem chave) · `openai.OpenAI{APIKey string}` (panica no `Init` sem chave). Prefixos do registry = `Name()` de cada plugin: `googleai`, `openai`, `ollama`.
- **Registro condicional (decisão 4, §2):** só o plugin do `LLM_PROVIDER` é montado/registrado; os outros provedores não precisam de credencial no startup. Falha rápida com erro claro (sempre **nome de variável + motivo**, nunca panico) se faltar a credencial do provider escolhido — a pré-validação em `pluginFor` cobre exatamente os casos em que o plugin panicaria no `Init`. Panics restantes do `genkit.Init` são bugs de biblioteca/ambiente e devem aparecer como panic de startup (não capturar com `recover`).
- `genkit.Init` é **por-processo**: `Setup` carrega guard de unicidade (`atomic.Bool`); segunda chamada → erro claro. Guard liberado quando a falha acontece **antes** do `Init` (permite retry); após `Init` bem-sucedido permanece ocupado.
- Timeout (§4): `cfg.LLMTimeout` (validado `> 0` pela config) vira deadline via `Motor.GenerationContext` (`context.WithTimeout`) em toda geração; o plugin ollama **também** recebe `Timeout` em segundos inteiros (`int` no plugin) com piso de 1s — `0` faria o plugin aplicar silenciosamente o default dele (30s).
- Secrets (`GEMINI_API_KEY`, `OPENAI_API_KEY`, …) nunca em logs nem em mensagens de erro.
- NÃO criar nem editar `.env`/`.env.example` (bloqueados). NÃO tocar em `cmd/brain/main.go` (wiring é spec 07), NÃO implementar flow (spec 06) nem tools (specs 04/05).
- Testes unitários exercitam **apenas o determinístico, sem serviços reais**: nada de `genkit.Init` no caminho feliz dos testes (os critérios §6 são manuais, com `.env` real/Ollama local → deferidos). Caminhos testáveis: montagem/seleção de plugin, strings de endereçamento, resolução de modelo, deadline do context, mensagens de erro sem credencial, guard de unicidade.
- Sem `t.Parallel()` (testes manipulam estado do package, ex. `setupDone`).
- Gates em toda tarefa, antes do commit: `gofmt -l .` vazio; `go vet ./...` limpo; `go test -race ./...` verde; `go mod tidy` sem diff residual.
- Commits: mensagem conventional curta + linha final `Co-Authored-By: Claude Code <noreply@anthropic.com>`. Trabalhar sempre em `/Users/mac01/workspace/home-assistent-go/.worktrees/spec-02` (branch `spec/02-motor-cognitivo`); commits locais — sem push, sem merge.
- NÃO editar `.loop-ledger.md` nem `docs/specs/*`.

---

### Task 1: Resolução do modelo ativo — `ActiveModel` e `modelName`

**Files:**
- Create: `internal/brain/model.go`
- Create: `internal/brain/model_test.go`
- Modify: `internal/config/config.go:44-47` (apenas o comentário de `ResolvedModel`, que promete o default "na spec 02" — apontar para onde ele foi)

**Interfaces:**
- Consumes: `config.Settings` (campos `LLMProvider`, `LLMModel`, `OllamaModel`) e seu método `ResolvedModel()` (`internal/config/config.go:47`) — **usar o método existente, sem duplicar a precedência**.
- Produces (tasks 3 depende destes nomes exatos):
  - `func ActiveModel(cfg config.Settings) string` — modelo ativo (§3).
  - `func modelName(provider, model string) string` — endereçamento no registry (§2): `"googleai/<model>"`, `"openai/<model>"`, `"ollama/<model>"`.
  - Consts não exportadas `defaultGeminiModel = "gemini-3.5-flash"`, `defaultOpenAIModel = "gpt-4o-mini"`, `defaultOllamaModel = "llama3.2:3b"`.

- [ ] **Step 1: Escrever os testes que falham**

`internal/brain/model_test.go`:

```go
package brain

import (
	"testing"
	"time"

	"home-assistent-go/internal/config"
)

func TestActiveModel(t *testing.T) { // §3: LLM_MODEL > OLLAMA_MODEL (ollama) > default
	casos := []struct {
		nome        string
		provider    string
		llmModel    string
		ollamaModel string
		want        string
	}{
		{"LLM_MODEL vence (gemini)", "gemini", "xpto", "", "xpto"},
		{"LLM_MODEL vence (ollama)", "ollama", "xpto", "llama-velho", "xpto"},
		{"OLLAMA_MODEL é fallback histórico (ollama)", "ollama", "", "llama-2", "llama-2"},
		{"default gemini (§3)", "gemini", "", "", "gemini-3.5-flash"},
		{"default openai (§3)", "openai", "", "", "gpt-4o-mini"},
		{"default ollama (§3)", "ollama", "", "", "llama3.2:3b"},
		{"provider desconhecido devolve vazio (Setup valida antes)", "vertex", "", "", ""},
	}
	for _, tc := range casos {
		cfg := config.Settings{
			LLMProvider:   tc.provider,
			LLMModel:      tc.llmModel,
			OllamaModel:   tc.ollamaModel,
			OllamaBaseURL: "http://127.0.0.1:11434",
			LLMTimeout:    30 * time.Second,
		}
		if got := ActiveModel(cfg); got != tc.want {
			t.Errorf("%s: ActiveModel() = %q; want %q", tc.nome, got, tc.want)
		}
	}
}

func TestModelName(t *testing.T) { // §2: endereçamento por nome no registry
	casos := []struct{ provider, model, want string }{
		{"gemini", "gemini-3.5-flash", "googleai/gemini-3.5-flash"},
		{"openai", "gpt-4o-mini", "openai/gpt-4o-mini"},
		{"ollama", "llama3.2:3b", "ollama/llama3.2:3b"},
		{"gemini com LLM_MODEL=xpto (critério 3)", "gemini", "xpto", "googleai/xpto"},
	}
	for _, tc := range casos {
		if got := modelName(tc.provider, tc.model); got != tc.want {
			t.Errorf("modelName(%q, %q) = %q; want %q", tc.provider, tc.model, got, tc.want)
		}
	}
}
```

- [ ] **Step 2: Rodar os testes para verificar que falham**

Run: `go test ./internal/brain/ -v`
Expected: FAIL — `undefined: ActiveModel` (e `undefined: modelName`).

- [ ] **Step 3: Implementar o mínimo para passar**

`internal/brain/model.go`:

```go
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
```

Em `internal/config/config.go`, trocar **só o comentário** de `ResolvedModel` (nenhuma mudança de código — os testes da spec 01 fixam `""` para gemini/openai):

```go
// ResolvedModel devolve o modelo efetivo (§2): LLM_MODEL > OLLAMA_MODEL
// (provider ollama) > default do provedor. Os defaults de gemini/openai são
// aplicados por brain.ActiveModel (spec 02) — aqui devolve "".
```

- [ ] **Step 4: Rodar os testes para verificar que passam**

Run: `go test ./internal/brain/ -v && go test ./internal/config/ -v`
Expected: PASS em ambos (comportamento da config intacto).

- [ ] **Step 5: Gates e commit**

```bash
gofmt -l . && go vet ./... && go test -race ./... && go mod tidy && git diff --quiet go.mod go.sum \
  && git add internal/brain/model.go internal/brain/model_test.go internal/config/config.go \
  && git commit -m "feat(brain): ActiveModel e modelName — defaults por provedor e endereçamento no registry

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

Expected: gofmt sem saída, vet limpo, testes verdes, tidy sem diff em `go.mod`/`go.sum` — qualquer gate vermelho **bloqueia** o commit (corrija antes de seguir).

---

### Task 2: Registro condicional — `pluginFor` (+ dependências Genkit fixadas)

**Files:**
- Create: `internal/brain/plugin.go`
- Create: `internal/brain/plugin_test.go`
- Modify: `go.mod` / `go.sum` (via `go get` + `go mod tidy`)

**Interfaces:**
- Consumes: `config.Settings` (campos `LLMProvider`, `GeminiAPIKey`, `OpenAIAPIKey`, `OpenAIAPIBase`, `OllamaBaseURL`, `LLMTimeout`).
- Produces (task 3 depende destes nomes exatos):
  - `func pluginFor(cfg config.Settings) (api.Plugin, error)` — monta só o plugin do provider escolhido (registro condicional §2); erro claro citando a variável faltante; provider desconhecido → erro.
  - `func timeoutSeconds(d time.Duration) int` — Timeout do plugin ollama em segundos inteiros, piso 1.
  - `api.Plugin` = `github.com/firebase/genkit/go/core/api`.Plugin (`Name() string; Init(ctx) []api.Action`).

- [ ] **Step 1: Fixar a dependência Genkit v1.13.1**

```bash
go get github.com/firebase/genkit/go@v1.13.1
```

(É um módulo único: `genkit`, `ai`, `core/api` e os plugins `googlegenai`, `compat_oai/openai`, `ollama` vêm todos daqui. O `go mod tidy` depois da implementação promove os imports diretos — ex. `github.com/openai/openai-go` — e fixa as versões.)

- [ ] **Step 2: Escrever os testes que falham**

`internal/brain/plugin_test.go`:

```go
package brain

import (
	"strings"
	"testing"
	"time"

	"github.com/firebase/genkit/go/plugins/googlegenai"
	"github.com/firebase/genkit/go/plugins/ollama"

	openai "github.com/firebase/genkit/go/plugins/compat_oai/openai"

	"home-assistent-go/internal/config"
)

func cfgBrain(provider string) config.Settings {
	return config.Settings{
		LLMProvider:   provider,
		GeminiAPIKey:  "gk-test",
		OpenAIAPIKey:  "sk-test",
		OpenAIAPIBase: "https://proxy.example.com/v1",
		OllamaBaseURL: "http://127.0.0.1:11434",
		LLMTimeout:    30 * time.Second,
	}
}

func TestPluginForMontaSoOPluginEscolhido(t *testing.T) { // registro condicional (§2)
	// gemini
	p, err := pluginFor(cfgBrain("gemini"))
	if err != nil {
		t.Fatalf("gemini: pluginFor: erro inesperado: %v", err)
	}
	if got := p.Name(); got != "googleai" {
		t.Errorf("gemini: Name() = %q; want googleai", got)
	}
	g, ok := p.(*googlegenai.GoogleAI)
	if !ok {
		t.Fatalf("gemini: tipo = %T; want *googlegenai.GoogleAI", p)
	}
	if g.APIKey != "gk-test" {
		t.Errorf("gemini: APIKey não propagada")
	}

	// openai (caminho real v1.13.1: compat_oai/openai; BaseURL via Opts)
	p, err = pluginFor(cfgBrain("openai"))
	if err != nil {
		t.Fatalf("openai: pluginFor: erro inesperado: %v", err)
	}
	if got := p.Name(); got != "openai" {
		t.Errorf("openai: Name() = %q; want openai", got)
	}
	o, ok := p.(*openai.OpenAI)
	if !ok {
		t.Fatalf("openai: tipo = %T; want *openai.OpenAI", p)
	}
	if o.APIKey != "sk-test" {
		t.Errorf("openai: APIKey não propagada")
	}
	if len(o.Opts) != 1 { // option.WithBaseURL(OPENAI_API_BASE); o valor é opaco ao SDK
		t.Errorf("openai: Opts = %d itens; want 1 (WithBaseURL)", len(o.Opts))
	}

	// ollama
	p, err = pluginFor(cfgBrain("ollama"))
	if err != nil {
		t.Fatalf("ollama: pluginFor: erro inesperado: %v", err)
	}
	if got := p.Name(); got != "ollama" {
		t.Errorf("ollama: Name() = %q; want ollama", got)
	}
	ol, ok := p.(*ollama.Ollama)
	if !ok {
		t.Fatalf("ollama: tipo = %T; want *ollama.Ollama", p)
	}
	if ol.ServerAddress != "http://127.0.0.1:11434" {
		t.Errorf("ollama: ServerAddress não propagado")
	}
	if ol.Timeout != 30 { // Timeout é int em segundos
		t.Errorf("ollama: Timeout = %d; want 30", ol.Timeout)
	}
}

func TestPluginForOpenAISemBaseURL(t *testing.T) { // OPENAI_API_BASE é opcional (§2)
	cfg := cfgBrain("openai")
	cfg.OpenAIAPIBase = ""
	p, err := pluginFor(cfg)
	if err != nil {
		t.Fatalf("pluginFor: erro inesperado: %v", err)
	}
	if o := p.(*openai.OpenAI); len(o.Opts) != 0 {
		t.Errorf("Opts = %d itens; want 0 sem OPENAI_API_BASE", len(o.Opts))
	}
}

func TestPluginForFalhaRapidaSemCredencial(t *testing.T) { // §2 critério 2: erro claro, não panico
	casos := []struct {
		nome string
		cfg  func() config.Settings
		want string
	}{
		{"gemini sem GEMINI_API_KEY", func() config.Settings { c := cfgBrain("gemini"); c.GeminiAPIKey = ""; return c },
			"brain: GEMINI_API_KEY é obrigatória quando LLM_PROVIDER=gemini"},
		{"openai sem OPENAI_API_KEY", func() config.Settings { c := cfgBrain("openai"); c.OpenAIAPIKey = ""; return c },
			"brain: OPENAI_API_KEY é obrigatória quando LLM_PROVIDER=openai"},
		{"ollama sem OLLAMA_BASE_URL", func() config.Settings { c := cfgBrain("ollama"); c.OllamaBaseURL = ""; return c },
			"brain: OLLAMA_BASE_URL é obrigatória quando LLM_PROVIDER=ollama"},
		{"provider desconhecido", func() config.Settings { return cfgBrain("vertex") },
			`brain: LLM_PROVIDER inválido: "vertex"`},
	}
	for _, tc := range casos {
		p, err := pluginFor(tc.cfg())
		if p != nil || err == nil || err.Error() != tc.want {
			t.Errorf("%s: pluginFor = (%v, %v); want (nil, %q)", tc.nome, p, err, tc.want)
		}
		if err != nil && (strings.Contains(err.Error(), "gk-test") || strings.Contains(err.Error(), "sk-test")) {
			t.Errorf("%s: erro vazou secret: %v", tc.nome, err)
		}
	}
}

func TestTimeoutSeconds(t *testing.T) { // §4: Timeout do plugin ollama em segundos inteiros
	casos := []struct {
		nome string
		d    time.Duration
		want int
	}{
		{"30s → 30", 30 * time.Second, 30},
		{"90.5s trunca para 90", 90500 * time.Millisecond, 90},
		{"sub-segundo → piso 1 (0 faria o plugin aplicar o default 30s dele)", 500 * time.Millisecond, 1},
		{"zero → piso 1", 0, 1},
	}
	for _, tc := range casos {
		if got := timeoutSeconds(tc.d); got != tc.want {
			t.Errorf("%s: timeoutSeconds(%v) = %d; want %d", tc.nome, tc.d, got, tc.want)
		}
	}
}
```

- [ ] **Step 3: Rodar os testes para verificar que falham**

Run: `go test ./internal/brain/ -v`
Expected: FAIL — `undefined: pluginFor` (e `undefined: timeoutSeconds`).

- [ ] **Step 4: Implementar o mínimo para passar**

`internal/brain/plugin.go`:

```go
package brain

import (
	"fmt"
	"time"

	"github.com/firebase/genkit/go/core/api"
	"github.com/firebase/genkit/go/plugins/googlegenai"
	"github.com/firebase/genkit/go/plugins/ollama"

	openai "github.com/firebase/genkit/go/plugins/compat_oai/openai"

	"github.com/openai/openai-go/option"

	"home-assistent-go/internal/config"
)

// pluginFor monta o plugin do provider escolhido — registro condicional
// (decisão 4, §2): só o plugin do LLM_PROVIDER é construído, e os outros
// provedores não precisam de credencial no startup. Antes de montar, valida
// a credencial mínima: os plugins fazem panic no Init sem credencial, e o
// startup precisa de erro claro, não panico (critério 2). Erros citam
// variável + motivo, nunca o valor dos secrets.
func pluginFor(cfg config.Settings) (api.Plugin, error) {
	switch cfg.LLMProvider {
	case "gemini":
		if cfg.GeminiAPIKey == "" {
			return nil, fmt.Errorf("brain: GEMINI_API_KEY é obrigatória quando LLM_PROVIDER=gemini")
		}
		return &googlegenai.GoogleAI{APIKey: cfg.GeminiAPIKey}, nil
	case "openai":
		if cfg.OpenAIAPIKey == "" {
			return nil, fmt.Errorf("brain: OPENAI_API_KEY é obrigatória quando LLM_PROVIDER=openai")
		}
		var opts []option.RequestOption
		if cfg.OpenAIAPIBase != "" {
			// Adaptação isolada (Global Constraints): o plugin v1.13.1 não tem
			// campo BaseURL — o endpoint entra como opção do SDK OpenAI.
			opts = append(opts, option.WithBaseURL(cfg.OpenAIAPIBase))
		}
		return &openai.OpenAI{APIKey: cfg.OpenAIAPIKey, Opts: opts}, nil
	case "ollama":
		if cfg.OllamaBaseURL == "" {
			return nil, fmt.Errorf("brain: OLLAMA_BASE_URL é obrigatória quando LLM_PROVIDER=ollama")
		}
		return &ollama.Ollama{
			ServerAddress: cfg.OllamaBaseURL,
			Timeout:       timeoutSeconds(cfg.LLMTimeout),
		}, nil
	default:
		// Config.Load já rejeita; aqui é a defesa do package (Setup é a
		// única porta de entrada do motor).
		return nil, fmt.Errorf("brain: LLM_PROVIDER inválido: %q", cfg.LLMProvider)
	}
}

// timeoutSeconds converte o deadline da config para o Timeout do plugin
// ollama, que é em segundos inteiros. Piso de 1s: truncar para 0 faria o
// plugin ignorar o valor e aplicar silenciosamente o default dele (30s). O
// mecanismo primário segue sendo o deadline do context (§4) — o Timeout do
// plugin é redonde de segurança do cliente HTTP.
func timeoutSeconds(d time.Duration) int {
	if s := int(d.Seconds()); s >= 1 {
		return s
	}
	return 1
}
```

- [ ] **Step 5: Rodar os testes para verificar que passam**

Run: `go mod tidy && go test ./internal/brain/ -v`
Expected: PASS. `go.mod` agora lista `github.com/firebase/genkit/go v1.13.1` como direct (e `github.com/openai/openai-go` promovido a direct pelo uso de `option`).

- [ ] **Step 6: Gates e commit**

```bash
gofmt -l . && go vet ./... && go test -race ./...
git add go.mod go.sum internal/brain/plugin.go internal/brain/plugin_test.go
git commit -m "feat(brain): pluginFor — registro condicional do provider e pré-validação de credencial

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

---

### Task 3: `Motor`, `GenerationContext` e `Setup` (unicidade + integração)

**Files:**
- Create: `internal/brain/brain.go`
- Create: `internal/brain/brain_test.go`

**Interfaces:**
- Consumes: `pluginFor(cfg) (api.Plugin, error)`, `ActiveModel(cfg) string`, `modelName(provider, model) string` (tasks 1–2); `config.Settings` (campo `LLMTimeout`); `genkit.Init/WithPlugins/WithDefaultModel/LookupModel`.
- Produces (spec 06 e spec 07 consomem exatamente isto):
  - `func Setup(ctx context.Context, cfg config.Settings) (*Motor, error)` — chamar **uma única vez** por processo.
  - `type Motor struct { Genkit *genkit.Genkit; Provider string; ModelName string }` (+ campo privado `timeout`).
  - `func (m *Motor) GenerationContext(parent context.Context) (context.Context, context.CancelFunc)` — deadline de `LLM_TIMEOUT_S` para cada geração (§4).

- [ ] **Step 1: Escrever os testes que falham**

`internal/brain/brain_test.go`:

```go
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
```

- [ ] **Step 2: Rodar os testes para verificar que falham**

Run: `go test ./internal/brain/ -v`
Expected: FAIL — `undefined: Setup` (e `undefined: setupDone`, `undefined: Motor`).

- [ ] **Step 3: Implementar o mínimo para passar**

`internal/brain/brain.go`:

```go
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
```

- [ ] **Step 4: Rodar os testes para verificar que passam**

Run: `go test ./internal/brain/ -v && go build ./...`
Expected: PASS; build limpo (nenhum wiring em `cmd/`).

- [ ] **Step 5: Gates e commit**

```bash
gofmt -l . && go vet ./... && go test -race ./... && go mod tidy && git diff --quiet go.mod go.sum \
  && git add internal/brain/brain.go internal/brain/brain_test.go \
  && git commit -m "feat(brain): Setup com registro condicional, modelo default e deadline de geração

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

---

### Task 4: Fechamento — `go mod tidy` final, gates na branch inteira e conformidade

**Files:**
- Modify: `go.mod` / `go.sum` (somente se o tidy final encontrar resíduo)
- Nenhuma outra mudança de código esperada.

**Interfaces:**
- Consumes: branch inteira (tasks 1–3).
- Produces: branch pronta para revisão final — nada novo.

- [ ] **Step 1: Tidy final e verificação de versões fixadas**

```bash
go mod tidy
go list -m github.com/firebase/genkit/go github.com/openai/openai-go
git diff --quiet go.mod go.sum || git diff go.mod go.sum
```

Expected: `github.com/firebase/genkit/go v1.13.1`; `openai-go` na versão que o genkit v1.13.1 exige (registrada no relatório). Diff em `go.mod`/`go.sum` só se houver resíduo — se houver, commitar como `chore(deps): tidy final`.

- [ ] **Step 2: Gates completos na branch inteira**

```bash
gofmt -l .
go vet ./...
go build ./...
go test -race ./...
git log --oneline ea948c1..HEAD
```

Expected: gofmt sem saída; vet limpo; build OK; testes verdes; histórico só com os commits do plano.

- [ ] **Step 3: Conformidade com a spec (checklist manual de leitura)**

- §2 registro condicional: `pluginFor` monta um único plugin; erros citam variável + motivo, sem panico e sem secret.
- §3 modelo ativo: `ActiveModel` cobre os três defaults (`gemini-3.5-flash`, `gpt-4o-mini`, `llama3.2:3b`) e a precedência via `config.ResolvedModel()`; `Setup` loga `provider` + `model` e fixa `WithDefaultModel`.
- §4 timeout: `GenerationContext` (context deadline) + `ollama.Ollama.Timeout` (segundos inteiros, piso 1).
- §5 troca de motor: nenhum provider fora de `.env` no código (só os defaults de modelo, que são paridade do Python).
- Escopo: nada em `cmd/`, nada de flow/tools.

- [ ] **Step 4: Commit de resíduo, se houver**

```bash
git add go.mod go.sum   # apenas se o Step 1 alterou algo
git commit -m "chore(deps): go mod tidy final da spec 02

Co-Authored-By: Claude Code <noreply@anthropic.com>" || echo "nada a commitar"
```

---

## Deferido — validação manual (§6, exige `.env` real / Ollama local)

1. `LLM_PROVIDER=ollama` com Ollama local → startup loga `provider=ollama model=…` e responde.
2. `LLM_PROVIDER=gemini` sem `GEMINI_API_KEY` → erro de startup claro (não panico).
3. `LLM_MODEL=xpto` aparece no log e é usado na chamada.
4. `LLM_TIMEOUT_S` curto + Ollama lento → timeout respeitado com erro limpo.

Estes critérios dependem de serviço externo (API de LLM / Ollama local) e do wiring do main (spec 07). O caminho de erro do critério 2 e a mecânica de deadline do critério 4 estão cobertos por testes unitários (Tasks 2 e 3); a validação ponta a ponta fica para após a spec 07.
