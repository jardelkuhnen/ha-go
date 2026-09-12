# Spec 05 — Tool controle de dispositivos (F06) — Plano de Implementação

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implementar `control_device` em `internal/tools` — ligar/desligar/alternar um dispositivo do Home Assistant com validação estrita de `entity_id` ANTES de tocar no HA e confirmação falável via apelidos (paridade com `src/tools/home.py` do projeto Python).

**Architecture:** Package `internal/tools` com dois arquivos de produção: `device.go` (núcleo puro — validação de ação/entity_id, mapa de apelidos em memória v1, frases de confirmação e a função `controlDevice` que consome o `*ha.Client` injetado) e `device_tool.go` (registro Genkit via `genkit.DefineTool`, input tipado com enum de ação). Design defensivo: a tool NUNCA devolve erro/panico ao motor — qualquer falha vira frase de fallback falável. Testes em `device_test.go`/`device_tool_test.go` com `httptest` (mesmo package) e `ha.NewClient(config.Settings{...})` apontando para o servidor de teste — a tool nunca monta HTTP própria.

**Tech Stack:** Go 1.25, stdlib (`context`, `log`, `strings`, `net/http`, `net/http/httptest`, `encoding/json`) + `github.com/firebase/genkit/go` (dependência direta já em `go.mod` — nenhuma dependência nova). Testes com `-race`.

**Spec:** `docs/specs/05-tool-dispositivos.md` (fonte da verdade — §2 contrato, §3 validação, §4 execução, §5 apelidos/frases, §6 critérios de aceite).

## Global Constraints

- Go 1.25; module `home-assistent-go`; package novo `internal/tools` (este worktree é o primeiro a criá-lo — o spec-04 paralelo usará nomes de arquivo `weather*`; manter arquivos `device*` distintos).
- **Consumir o `*ha.Client` injetado** (`internal/ha`: `TurnOn`/`TurnOff`/`Toggle`/`CallService`) — NUNCA duplicar o client nem montar HTTP próprio. Em testes, o client nasce de `ha.NewClient(config.Settings{HAURL: ts.URL, ...})`.
- **Validação de `entity_id` antes de qualquer chamada ao HA** (§3): apenas prefixos `switch.`/`light.`/`media_player.` seguidos de sufixo não vazio. Inválido → warning no log + frase de fallback, **zero requests ao HA**. Ação fora de `on|off|toggle` → mesmo tratamento.
- **Frases exatas da spec (paridade, §5/§6)** — não corrigir gramática: `on` → `"Liguei o {apelido}."` · `off` → `"Desliguei o {apelido}."` · `toggle` → `"Alternei o {apelido}."` · fallback → `"Não consegui acionar o dispositivo."` Entity desconhecido → `entity_id` cru na frase.
- Mapa de apelidos **em memória, v1** (§5) — hardcoded em `internal/tools`; NÃO criar parsing/env novo (nada em `config.Settings` para apelidos na v1; não tocar em `internal/config`).
- Design defensivo (paridade F04/F05): a tool **nunca propaga erro nem panico** — rede/HTTP/timeout/ctx do turno/client nulo → fallback. O retorno da tool Genkit é sempre `(string, nil)`.
- Zero secrets: token do HA nunca em logs/erros (ele só existe no header do client; o package tools não loga nada além de entity_id/ação/erro do client, já sanitizado).
- O registro Genkit (`device_tool.go`) é camada fina sobre o núcleo: `genkit.DefineTool(g, …)` com input tipado por struct + tags `jsonschema:"enum=…"`.
- Gates antes de CADA commit: `gofmt -l .` vazio; `go vet ./...` limpo; `go test -race ./...` verde (módulo inteiro).
- NÃO tocar em `internal/brain`, `internal/ha`, `internal/config`, `cmd/`, `docs/specs/*`, `.env`/`.env.example`. Nenhuma dependência nova em `go.mod`.
- Commits locais apenas — NUNCA push, NUNCA merge. Worktree: `/Users/mac01/workspace/home-assistent-go/.worktrees/spec-05`, branch `spec/05-tool-dispositivos`. Mensagens no padrão `feat(tools): …` + linha final `Co-Authored-By: Claude Code <noreply@anthropic.com>`.
- Assinaturas públicas que a spec 06 (orquestração, §6) consumirá — não mudar sem justificativa:

```go
const ControlDeviceName = "control_device"
type ControlDeviceInput struct {
	Action   string `json:"action"   jsonschema:"enum=on,enum=off,enum=toggle"`
	EntityID string `json:"entity_id"`
}
func DefineControlDevice(g *genkit.Genkit, cli *ha.Client) *ai.ToolAction[ControlDeviceInput, string]
```

- **Desvio deliberado (coordinación spec-04):** o catálogo `[]ai.ToolRef{get_weather, control_device}` (spec 06 §6) é montado na integração — `get_weather` vive em worktree paralelo (spec 04) e não existe nesta branch. `DefineControlDevice` devolve `*ai.ToolAction[...]`, que implementa `ai.ToolRef`, pronto para entrar no catálogo.

---

### Task 1: Núcleo puro — validação, apelidos e frases (sem HTTP)

**Files:**
- Create: `internal/tools/device.go`
- Create: `internal/tools/device_test.go`

**Interfaces:**
- Consumes: nada (stdlib apenas).
- Produces (Tasks 2–3 e spec 06 dependem destes nomes):
  - `const fallbackControlDevice = "Não consegui acionar o dispositivo."`
  - `const actionOn = "on"`, `const actionOff = "off"`, `const actionToggle = "toggle"`
  - `var deviceAliases = map[string]string{"switch.tomada_sala": "tomada da sala", "switch.tomada_quarto": "tomada do quarto", "light.luz_sala": "luz da sala", "light.luz_quarto": "luz do quarto", "media_player.alexa_sala": "Alexa da sala"}`
  - `func validAction(action string) bool` — só `on|off|toggle` (estrito, sem trim/normalização).
  - `func validEntityID(entityID string) bool` — prefixo `switch|light|media_player` + sufixo não vazio.
  - `func aliasOf(entityID string) string` — apelido do mapa; desconhecido → `entity_id` cru.
  - `func confirmationPhrase(action, alias string) string` — frase fixa por ação; ação desconhecida → `fallbackControlDevice`.

- [ ] **Step 1: Escrever os testes que falham** — criar `internal/tools/device_test.go`:

```go
package tools

import (
	"testing"
)

func TestValidEntityID(t *testing.T) { // §3: apenas switch/light/media_player + sufixo não vazio
	casos := []struct {
		entity string
		want   bool
	}{
		{"switch.tomada_sala", true},
		{"light.luz_sala", true},
		{"media_player.alexa_sala", true},
		{"switch.tomada.sala", true}, // sufixo não vazio mesmo com ponto extra
		{"camera.frente", false},     // prefixo fora da lista (critério 4)
		{"fan.quarto", false},
		{"tomada", false}, // sem ponto (critério 5)
		{"", false},       // vazio (critério 5)
		{"switch.", false}, // sufixo vazio
		{".tomada", false}, // domínio vazio
	}
	for _, tc := range casos {
		if got := validEntityID(tc.entity); got != tc.want {
			t.Errorf("validEntityID(%q) = %v; want %v", tc.entity, got, tc.want)
		}
	}
}

func TestValidAction(t *testing.T) { // §2: enum estrito on|off|toggle
	casos := []struct {
		acao string
		want bool
	}{
		{"on", true},
		{"off", true},
		{"toggle", true},
		{"ON", false},   // sem normalização
		{"on ", false},  // sem trim
		{"ligar", false},
		{"", false},
	}
	for _, tc := range casos {
		if got := validAction(tc.acao); got != tc.want {
			t.Errorf("validAction(%q) = %v; want %v", tc.acao, got, tc.want)
		}
	}
}

func TestAliasOf(t *testing.T) { // §5: mapa em memória; desconhecido → entity cru
	casos := []struct{ entity, want string }{
		{"switch.tomada_sala", "tomada da sala"},
		{"switch.tomada_quarto", "tomada do quarto"},
		{"light.luz_sala", "luz da sala"},
		{"light.luz_quarto", "luz do quarto"},
		{"media_player.alexa_sala", "Alexa da sala"},
		{"switch.ventilador", "switch.ventilador"}, // desconhecido → entity_id cru
	}
	for _, tc := range casos {
		if got := aliasOf(tc.entity); got != tc.want {
			t.Errorf("aliasOf(%q) = %q; want %q", tc.entity, got, tc.want)
		}
	}
}

func TestConfirmationPhrase(t *testing.T) { // §5: frases fixas por ação
	casos := []struct {
		acao, apelido, want string
	}{
		{"on", "tomada da sala", "Liguei o tomada da sala."},
		{"off", "luz da sala", "Desliguei o luz da sala."},
		{"toggle", "switch.ventilador", "Alternei o switch.ventilador."},
		{"reboot", "tomada da sala", fallbackControlDevice}, // defesa: ação inválida nunca chega aqui
	}
	for _, tc := range casos {
		if got := confirmationPhrase(tc.acao, tc.apelido); got != tc.want {
			t.Errorf("confirmationPhrase(%q, %q) = %q; want %q", tc.acao, tc.apelido, got, tc.want)
		}
	}
}
```

- [ ] **Step 2: Rodar e verificar que falham de compilação**

Run: `go test ./internal/tools/`
Expected: FAIL de compilação — `undefined: validEntityID`, `undefined: validAction`, `undefined: aliasOf`, `undefined: confirmationPhrase`, `undefined: fallbackControlDevice` (nada implementado ainda).

- [ ] **Step 3: Implementar `internal/tools/device.go`**

```go
// Package tools implementa as tools acionáveis pelo motor cognitivo
// (paridade com src/tools/ do projeto Python): hoje control_device (F06,
// spec 05) — ligar/desligar/alternar dispositivos do Home Assistant.
// Design defensivo (paridade F04): a tool nunca propaga erro ou panico ao
// motor — qualquer falha vira frase de fallback falável. TTS não vive aqui
// (ADR-0002): speak é passo do fluxo (spec 06).
package tools

import (
	"strings"
)

// Ações aceitas pela tool control_device (§2) — estrito, sem normalização.
const (
	actionOn     = "on"
	actionOff    = "off"
	actionToggle = "toggle"
)

// fallbackControlDevice é a frase de fallback da tool (§5): acionamento não
// confirmado, entity inválido, ação inválida ou client ausente.
const fallbackControlDevice = "Não consegui acionar o dispositivo."

// deviceAliases é o mapa em memória v1 de apelidos amigáveis (§5) — paridade
// com o mapa hardcoded do Python. Entity fora do mapa → entity_id cru na
// frase (§5).
var deviceAliases = map[string]string{
	"switch.tomada_sala":      "tomada da sala",
	"switch.tomada_quarto":    "tomada do quarto",
	"light.luz_sala":          "luz da sala",
	"light.luz_quarto":        "luz do quarto",
	"media_player.alexa_sala": "Alexa da sala",
}

// validAction reporta se a ação é uma das aceitas (§2) — estrito, sem trim
// nem normalização: o LLM recebe o enum fixado no schema da tool.
func validAction(action string) bool {
	switch action {
	case actionOn, actionOff, actionToggle:
		return true
	}
	return false
}

// validEntityID valida o entity_id ANTES de qualquer chamada ao HA (§3):
// apenas os prefixos switch., light. e media_player., seguidos de sufixo não
// vazio. Baseline de segurança item 3.
func validEntityID(entityID string) bool {
	domain, suffix, ok := strings.Cut(entityID, ".")
	if !ok || suffix == "" {
		return false
	}
	switch domain {
	case "switch", "light", "media_player":
		return true
	}
	return false
}

// aliasOf devolve o apelido amigável do entity (§5); desconhecido → o
// entity_id cru.
func aliasOf(entityID string) string {
	if ap, ok := deviceAliases[entityID]; ok {
		return ap
	}
	return entityID
}

// confirmationPhrase monta a confirmação falável da ação (§5) — frases exatas
// da spec, sem correção gramatical (paridade). Ação desconhecida → fallback
// (defesa do package; controlDevice só a chama com ação válida).
func confirmationPhrase(action, alias string) string {
	switch action {
	case actionOn:
		return "Liguei o " + alias + "."
	case actionOff:
		return "Desliguei o " + alias + "."
	case actionToggle:
		return "Alternei o " + alias + "."
	}
	return fallbackControlDevice
}
```

- [ ] **Step 4: Rodar e verificar passagem**

Run: `go test -race ./internal/tools/ -v`
Expected: PASS nos 4 testes da Step 1.

- [ ] **Step 5: Gates + Commit**

Run: `gofmt -l . && go vet ./... && go test -race ./...`
Expected: gofmt sem saída; vet limpo; testes verdes (incluindo `internal/config`, `internal/ha`, `internal/brain`).

```bash
git add internal/tools/
git commit -m "feat(tools): núcleo puro do control_device — validação, apelidos e frases

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

---

### Task 2: Execução via `*ha.Client` injetado — `controlDevice` (httptest)

**Files:**
- Modify: `internal/tools/device.go`
- Test: `internal/tools/device_test.go`

**Interfaces:**
- Consumes: `ha.Client` de `home-assistent-go/internal/ha` — `func (c *Client) TurnOn(ctx context.Context, entityID string) (map[string]any, error)`, `func (c *Client) TurnOff(...)`, `func (c *Client) Toggle(...)` (spec 03, não alterar); helpers da Task 1 (`validAction`, `validEntityID`, `aliasOf`, `confirmationPhrase`, `fallbackControlDevice`, `actionOn/Off/Toggle`).
- Produces: `func controlDevice(ctx context.Context, cli *ha.Client, action, entityID string) string` — núcleo completo consumido pelo wrapper Genkit da Task 3. Helper de teste `func novoClient(ts *httptest.Server) *ha.Client` (Task 3 reutiliza).

- [ ] **Step 1: Escrever os testes que falham** — acrescentar a `internal/tools/device_test.go` (imports de `context`, `encoding/json`, `io`, `net/http`, `net/http/httptest`, `time`, `home-assistent-go/internal/config`, `home-assistent-go/internal/ha`):

```go
// gravada guarda o que o servidor de teste viu da última requisição.
type gravada struct {
	Method string
	Path   string
	Body   []byte
}

// novoServidor cria um httptest.Server que grava a última requisição em g e
// responde com o status e corpo informados.
func novoServidor(t *testing.T, g *gravada, status int, corpo string) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		*g = gravada{Method: r.Method, Path: r.URL.Path, Body: body}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(corpo))
	}))
	t.Cleanup(ts.Close)
	return ts
}

// novoClient cria o client de produção (ha.NewClient) apontando para o
// servidor de teste — a tool nunca monta HTTP própria (decisão 7 da spec 03).
func novoClient(ts *httptest.Server) *ha.Client {
	return ha.NewClient(config.Settings{
		HAURL:     ts.URL,
		HAToken:   "token-de-teste",
		HATimeout: 5 * time.Second,
	})
}

// corpoComEntity assegura que o JSON gravado carrega o entity_id esperado.
func corpoComEntity(t *testing.T, body []byte, want string) {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("corpo não é JSON de objeto: %q (%v)", body, err)
	}
	if got["entity_id"] != want {
		t.Errorf("entity_id = %v; want %q", got["entity_id"], want)
	}
}

func TestOnNoSwitchComApelido(t *testing.T) { // critério 1
	var g gravada
	ts := novoServidor(t, &g, http.StatusOK, `[]`)
	cli := novoClient(ts)

	got := controlDevice(context.Background(), cli, "on", "switch.tomada_sala")
	if got != "Liguei o tomada da sala." {
		t.Errorf("frase = %q; want %q", got, "Liguei o tomada da sala.")
	}
	if g.Method != http.MethodPost || g.Path != "/api/services/switch/turn_on" {
		t.Errorf("requisição: %s %s; want POST /api/services/switch/turn_on", g.Method, g.Path)
	}
	corpoComEntity(t, g.Body, "switch.tomada_sala")
}

func TestOffNaLuzComApelido(t *testing.T) { // critério 2
	var g gravada
	ts := novoServidor(t, &g, http.StatusOK, `[]`)
	cli := novoClient(ts)

	got := controlDevice(context.Background(), cli, "off", "light.luz_sala")
	if got != "Desliguei o luz da sala." {
		t.Errorf("frase = %q; want %q", got, "Desliguei o luz da sala.")
	}
	if g.Method != http.MethodPost || g.Path != "/api/services/light/turn_off" {
		t.Errorf("requisição: %s %s; want POST /api/services/light/turn_off", g.Method, g.Path)
	}
	corpoComEntity(t, g.Body, "light.luz_sala")
}

func TestToggleSemApelido(t *testing.T) { // critério 3: homeassistant/toggle + entity cru
	var g gravada
	ts := novoServidor(t, &g, http.StatusOK, `[]`)
	cli := novoClient(ts)

	got := controlDevice(context.Background(), cli, "toggle", "switch.ventilador")
	if got != "Alternei o switch.ventilador." {
		t.Errorf("frase = %q; want %q", got, "Alternei o switch.ventilador.")
	}
	if g.Method != http.MethodPost || g.Path != "/api/services/homeassistant/toggle" {
		t.Errorf("requisição: %s %s; want POST /api/services/homeassistant/toggle", g.Method, g.Path)
	}
	corpoComEntity(t, g.Body, "switch.ventilador")
}

func TestToggleComApelido(t *testing.T) { // §5: toggle de entity conhecido usa o apelido
	var g gravada
	ts := novoServidor(t, &g, http.StatusOK, `[]`)
	cli := novoClient(ts)

	got := controlDevice(context.Background(), cli, "toggle", "media_player.alexa_sala")
	if got != "Alternei o Alexa da sala." {
		t.Errorf("frase = %q; want %q", got, "Alternei o Alexa da sala.")
	}
}

func TestEntityInvalidoNaoChamaHA(t *testing.T) { // critério 4: zero requests
	var g gravada
	ts := novoServidor(t, &g, http.StatusOK, `[]`)
	cli := novoClient(ts)

	got := controlDevice(context.Background(), cli, "on", "camera.frente")
	if got != fallbackControlDevice {
		t.Errorf("frase = %q; want fallback %q", got, fallbackControlDevice)
	}
	if g.Method != "" || g.Path != "" {
		t.Errorf("requisição feita ao HA: %s %s; want nenhuma", g.Method, g.Path)
	}
}

func TestEntitySemPontoOuVazio(t *testing.T) { // critério 5
	var g gravada
	ts := novoServidor(t, &g, http.StatusOK, `[]`)
	cli := novoClient(ts)

	for _, entity := range []string{"tomada", "", "switch."} {
		if got := controlDevice(context.Background(), cli, "on", entity); got != fallbackControlDevice {
			t.Errorf("entity %q: frase = %q; want fallback", entity, got)
		}
	}
	if g.Method != "" {
		t.Errorf("requisição feita ao HA: %s %s; want nenhuma", g.Method, g.Path)
	}
}

func TestHA500FallbackSemPanico(t *testing.T) { // critério 6
	var g gravada
	ts := novoServidor(t, &g, http.StatusInternalServerError, `{"message":"boom"}`)
	cli := novoClient(ts)

	got := controlDevice(context.Background(), cli, "on", "switch.tomada_sala")
	if got != fallbackControlDevice {
		t.Errorf("frase = %q; want fallback", got)
	}
}

func TestAcaoInvalidaNaoChamaHA(t *testing.T) { // §2: action fora do enum
	var g gravada
	ts := novoServidor(t, &g, http.StatusOK, `[]`)
	cli := novoClient(ts)

	if got := controlDevice(context.Background(), cli, "reboot", "switch.tomada_sala"); got != fallbackControlDevice {
		t.Errorf("frase = %q; want fallback", got)
	}
	if g.Method != "" {
		t.Errorf("requisição feita ao HA: %s %s; want nenhuma", g.Method, g.Path)
	}
}

func TestClientNuloFallbackSemPanico(t *testing.T) { // defensivo: wiring errado não panica
	got := controlDevice(context.Background(), nil, "on", "switch.tomada_sala")
	if got != fallbackControlDevice {
		t.Errorf("frase = %q; want fallback", got)
	}
}

func TestContextoDoTurnoVence(t *testing.T) { // §4: deadline do ctx propaga → fallback
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ts.Close)
	cli := novoClient(ts)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	if got := controlDevice(ctx, cli, "on", "switch.tomada_sala"); got != fallbackControlDevice {
		t.Errorf("frase = %q; want fallback (deadline do ctx)", got)
	}
}
```

- [ ] **Step 2: Rodar e verificar que falham de compilação**

Run: `go test ./internal/tools/ -run TestOn -v`
Expected: FAIL — `undefined: controlDevice`.

- [ ] **Step 3: Implementar `controlDevice` em `internal/tools/device.go`** (acrescentar imports `context`, `log` e `home-assistent-go/internal/ha`; função ao final do arquivo):

```go
// controlDevice executa a tool control_device (§4): valida ação e entity_id
// (nesta ordem, ANTES de tocar no HA — baseline item 3), aciona o client HA
// injetado e devolve confirmação falável com apelido (§5) — ou a frase de
// fallback em qualquer falha. Nunca devolve erro nem panico (design defensivo
// F05): rede/HTTP/timeout/deadline do ctx/client nulo → fallback.
func controlDevice(ctx context.Context, cli *ha.Client, action, entityID string) string {
	if !validAction(action) {
		log.Printf("tools: control_device: ação inválida %q", action)
		return fallbackControlDevice
	}
	if !validEntityID(entityID) {
		log.Printf("tools: control_device: entity_id inválido %q", entityID)
		return fallbackControlDevice
	}
	if cli == nil {
		log.Printf("tools: control_device: client HA ausente (wiring)")
		return fallbackControlDevice
	}
	var err error
	switch action {
	case actionOn:
		_, err = cli.TurnOn(ctx, entityID)
	case actionOff:
		_, err = cli.TurnOff(ctx, entityID)
	case actionToggle:
		_, err = cli.Toggle(ctx, entityID)
	}
	if err != nil {
		// O erro do client é sanitizado (nunca contém o token) e entity_id
		// não é secret — seguro para o log de diagnóstico.
		log.Printf("tools: control_device: falha do HA para %s: %v", entityID, err)
		return fallbackControlDevice
	}
	return confirmationPhrase(action, aliasOf(entityID))
}
```

- [ ] **Step 4: Rodar e verificar passagem**

Run: `go test -race ./internal/tools/ -v`
Expected: PASS em todos os testes (Task 1 continua verde). Warnings de `log` nos caminhos de erro são esperados (ruído de stderr, não falha).

- [ ] **Step 5: Gates + Commit**

Run: `gofmt -l . && go vet ./... && go test -race ./...`

```bash
git add internal/tools/
git commit -m "feat(tools): controlDevice executa via ha.Client com fallback defensivo

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

---

### Task 3: Registro Genkit — `DefineControlDevice` (input tipado com enum)

**Files:**
- Create: `internal/tools/device_tool.go`
- Test: `internal/tools/device_tool_test.go`

**Interfaces:**
- Consumes: `controlDevice` (Task 2), helpers de teste `gravada`/`novoServidor`/`novoClient` (Task 2); `genkit.DefineTool` e `ai.ToolContext` de `github.com/firebase/genkit/go` (dependência direta já em `go.mod`, usada por `internal/brain`).
- Produces (spec 06 §6 — catálogo de tools):
  - `const ControlDeviceName = "control_device"`.
  - `type ControlDeviceInput struct { Action string; EntityID string }` com tags `json:"action"`/`json:"entity_id"` e `jsonschema:"enum=on,enum=off,enum=toggle"` em Action.
  - `func DefineControlDevice(g *genkit.Genkit, cli *ha.Client) *ai.ToolAction[ControlDeviceInput, string]` — devolve o ToolAction (implementa `ai.ToolRef`), pronto para `ai.WithTools(...)`.

- [ ] **Step 1: Escrever o teste que falha** — criar `internal/tools/device_tool_test.go`:

```go
package tools

import (
	"context"
	"net/http"
	"testing"

	"github.com/firebase/genkit/go/genkit"
)

// TestDefineControlDevice valida o registro Genkit da tool (§2): nome,
// descrição, execução com input cru e passagem pelo client HA injetado.
// genkit.Init é por-processo (spec 02) — este package de teste o chama uma
// única vez, neste teste.
func TestDefineControlDevice(t *testing.T) {
	var g gravada
	ts := novoServidor(t, &g, http.StatusOK, `[]`)
	cli := novoClient(ts)

	ctx := context.Background()
	gk := genkit.Init(ctx)
	tool := DefineControlDevice(gk, cli)
	if tool == nil {
		t.Fatal("DefineControlDevice devolveu nil")
	}

	ref := genkit.LookupTool(gk, ControlDeviceName)
	if ref == nil {
		t.Fatalf("tool %q não registrada no Genkit", ControlDeviceName)
	}
	if ref.Name() != ControlDeviceName {
		t.Errorf("Name() = %q; want %q", ref.Name(), ControlDeviceName)
	}
	def := ref.Definition()
	if def == nil || def.Description != "Liga, desliga ou alterna um dispositivo do Home Assistant." {
		t.Errorf("descrição = %+v; want \"Liga, desliga ou alterna um dispositivo do Home Assistant.\"", def)
	}

	got, err := ref.RunRaw(ctx, map[string]any{"action": "on", "entity_id": "switch.tomada_sala"})
	if err != nil {
		t.Fatalf("RunRaw: erro inesperado: %v", err)
	}
	if got != "Liguei o tomada da sala." {
		t.Errorf("saída = %v; want \"Liguei o tomada da sala.\"", got)
	}
	if g.Method != http.MethodPost || g.Path != "/api/services/switch/turn_on" {
		t.Errorf("requisição: %s %s; want POST /api/services/switch/turn_on", g.Method, g.Path)
	}
}

// TestDefineControlDeviceFallbackViaTool cobre o caminho defensivo pela
// superfície Genkit: entity inválido vira frase de fallback, nunca erro.
func TestDefineControlDeviceFallbackViaTool(t *testing.T) {
	var g gravada
	ts := novoServidor(t, &g, http.StatusOK, `[]`)
	cli := novoClient(ts)

	ctx := context.Background()
	gk := genkit.Init(ctx)
	DefineControlDevice(gk, cli)

	ref := genkit.LookupTool(gk, ControlDeviceName)
	got, err := ref.RunRaw(ctx, map[string]any{"action": "on", "entity_id": "camera.frente"})
	if err != nil {
		t.Fatalf("tool nunca devolve erro: %v", err)
	}
	if got != fallbackControlDevice {
		t.Errorf("saída = %v; want fallback %q", got, fallbackControlDevice)
	}
	if g.Method != "" {
		t.Errorf("requisição feita ao HA: %s %s; want nenhuma", g.Method, g.Path)
	}
}
```

- [ ] **Step 2: Rodar e verificar que falha de compilação**

Run: `go test ./internal/tools/ -run TestDefineControlDevice -v`
Expected: FAIL — `undefined: DefineControlDevice`, `undefined: ControlDeviceName`.

- [ ] **Step 3: Implementar `internal/tools/device_tool.go`**

```go
package tools

import (
	"github.com/firebase/genkit/go/ai"
	"github.com/firebase/genkit/go/genkit"

	"home-assistent-go/internal/ha"
)

// ControlDeviceName é o nome registrado da tool (§2) — é o nome que o LLM
// invoca e o que aparece em ToolsUsed (spec 06).
const ControlDeviceName = "control_device"

// ControlDeviceInput é a entrada tipada da tool (§2). O enum de Action fica
// fixado no JSON Schema via tags jsonschema — o LLM só pode pedir
// on|off|toggle; qualquer desvio cai no fallback do núcleo.
type ControlDeviceInput struct {
	Action   string `json:"action" jsonschema:"enum=on,enum=off,enum=toggle" jsonschema_description:"Ação a executar no dispositivo: on (ligar), off (desligar) ou toggle (alternar)"`
	EntityID string `json:"entity_id" jsonschema_description:"Entity ID do dispositivo no Home Assistant, como switch.tomada_sala, light.luz_sala ou media_player.alexa_sala"`
}

// DefineControlDevice registra a tool control_device no registry do Genkit
// (§2) fechando sobre o client HA injetado (decisão 7). A saída é sempre uma
// frase falável e o retorno nunca é erro (design defensivo F05) — falha de
// validação, de rede ou do HA vira fallback dentro do próprio núcleo. O
// ToolAction devolvido implementa ai.ToolRef e entra no catálogo de tools do
// chatbot (spec 06 §6).
func DefineControlDevice(g *genkit.Genkit, cli *ha.Client) *ai.ToolAction[ControlDeviceInput, string] {
	return genkit.DefineTool(g, ControlDeviceName, "Liga, desliga ou alterna um dispositivo do Home Assistant.",
		func(tctx *ai.ToolContext, in ControlDeviceInput) (string, error) {
			// tctx carrega o context do turno (deadline do LLM propaga para o HA).
			return controlDevice(tctx, cli, in.Action, in.EntityID), nil
		})
}
```

- [ ] **Step 4: Rodar e verificar passagem**

Run: `go test -race ./internal/tools/ -v`
Expected: PASS em todos os testes (Tasks 1–2 continuam verdes). Se `genkit.Init` falhar no ambiente de teste por motivo infraestrutural (não esperado: `genkit.Init(ctx)` sem plugins não precisa de credencial nem de rede), documentar o desvio e reduzir os testes deste arquivo a uma asserção de compilação — `var _ ai.ToolRef = (*ai.ToolAction[ControlDeviceInput, string])(nil)` — mantendo o contrato assinado.

- [ ] **Step 5: Gates + Commit**

Run: `gofmt -l . && go vet ./... && go test -race ./...`

```bash
git add internal/tools/
git commit -m "feat(tools): registra control_device como tool Genkit com input tipado

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

---

### Task 4: Gates finais da branch

**Files:**
- Modify (apenas se algum gate acusar): qualquer arquivo de `internal/tools/`.

**Interfaces:**
- Consumes: branch completa (Tasks 1–3).
- Produces: branch pronta para a integração (onda 2) — catálogo de tools (spec 06 §6) montado na integração com `get_weather`.

- [ ] **Step 1: Gates completos no módulo inteiro**

```bash
gofmt -l .
go vet ./...
go mod tidy && git diff --exit-code go.mod go.sum
go test -race ./... -count=1
go build ./...
```

Expected: gofmt sem saída; vet limpo; `go.mod`/`go.sum` estáveis pós-tidy (nenhuma dependência nova); testes verdes; build ok.

- [ ] **Step 2: Auditoria de secrets e contratos** — reler `internal/tools/`: (a) nenhum secret (token) aparece em código/log/erro — o package só loga ação/entity_id/erro do client (sanitizado); (b) nenhuma chamada HTTP fora do `*ha.Client` injetado (nenhum `http.Client`/`http.Get` no package); (c) validação de entity_id precede toda chamada ao HA; (d) assinaturas da spec 06 intactas (`ControlDeviceName`, `ControlDeviceInput`, `DefineControlDevice`); (e) nenhum arquivo fora de `internal/tools/` e `docs/plans/` alterado.

- [ ] **Step 3: Commit apenas se algum gate exigiu correção**

```bash
git add -A && git commit -m "chore(tools): ajustes dos gates finais

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

(se nada mudou, nenhuma commit nesta task)