# Spec 03 — Integração Física: Home Assistant + Alexa TTS (F03) — Plano de Implementação

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implementar `internal/ha` — client HTTP do Home Assistant (acionamento de serviços, toggle, turn_on/turn_off, leitura de estado) e a síntese de voz via Alexa Media Player (`notify.alexa_media`), com paridade com `src/services/ha_client.py` do projeto Python.

**Architecture:** Package `internal/ha` com dois arquivos: `client.go` (tipo `Error`, struct `Client` com `baseURL`/`token`/`alexaMediaEntity`/`http` injetáveis, `NewClient(settings)`, `Close`, e a maquinaria compartilhada de requisição `do`/`statusError`/`drainClose`/`sanitize` + `CallService`, `Toggle`, `TurnOn`, `TurnOff`, `GetState`) e `speak.go` (`SpeakResult` e `Speak` defensivo). Um único `http.Client` com timeout `HA_TIMEOUT_S` criado no `NewClient` vale para toda chamada (corrige a criação por-chamada do Python, decisão 7). Testes em `client_test.go`/`speak_test.go` com `httptest` (mesmo package, para poderem injetar os campos não-exportados — decisão 12).

**Tech Stack:** Go 1.25, apenas stdlib (`net/http`, `encoding/json`, `context`, `net/url`), `testing` + `net/http/httptest` (`-race`).

**Spec:** `docs/specs/03-home-assistant.md` (fonte da verdade — §2 client, §3 métodos, §4 contrato do Speak, §5 erros, §6 critérios de aceite).

## Global Constraints

- Package novo: `internal/ha`. Module: `home-assistent-go` (import de config: `home-assistent-go/internal/config`). Go 1.25.
- **Apenas stdlib** no client HTTP (`net/http`) — nenhuma dependência nova em `go.mod`.
- Um único `http.Client` com `Timeout: settings.HATimeout`, criado uma vez no `NewClient` — **timeout vale em toda chamada** (baseline de segurança item 2, decisão 7). Nada cria `http.Client` fora do `NewClient` (os testes podem injetar o campo).
- Headers em toda requisição: `Authorization: Bearer <HA_TOKEN>` e `Content-Type: application/json` (§2).
- Campos da struct `Client`: `baseURL`, `token`, `alexaMediaEntity`, `http` — **não-exportados, injetáveis pelos testes** (mesmo package, decisão 12). Construtor de produção: `func NewClient(settings config.Settings) *Client` (wiring da spec 07 consome exatamente este nome).
- `Speak`: corpo `{"message": <texto>, "target": <ALEXA_MEDIA_ENTITY>}` — usa **`target`**, NUNCA `data.entity_id` (o `alexa_media` rejeita com 500 — armadilha documentada no projeto Python). Defensivo: qualquer falha → `SpeakResult{OK: false, Error: …}`, nunca propaga pânico/erro; sucesso → `SpeakResult{OK: true}`. `Speak` aceita `ctx` do turno (o deadline do fluxo vence).
- Erros de chamada retornam `*ha.Error` com `Status` HTTP quando houver (0 em falha de rede/timeout) e **preservam a cadeia original** (`%w`) — quem consome pode usar `errors.Is(err, context.DeadlineExceeded)`/`errors.As(err, &net.Error)`. **Nenhum secret (token) aparece em mensagens de erro**: o token só existe no header (nunca ecoado pelo net/http) e a extração de string no `Speak` passa por `sanitize`.
- Todos os métodos HTTP recebem `context.Context` como primeiro parâmetro (idioma Go para o cancelamento do turno; a spec exige isso explicitamente para `Speak` §4).
- Gates em toda tarefa, antes do commit: `gofmt -l .` vazio; `go vet ./...` limpo; `go test -race ./...` verde (módulo inteiro, incluindo `internal/config`).
- NÃO criar nem editar `.env`/`.env.example` (bloqueados). NÃO tocar em `internal/config`, `cmd/`, nem em `docs/specs/*`.
- Commits: mensagem conventional curta + linha final `Co-Authored-By: Claude Code <noreply@anthropic.com>`. Trabalhar sempre no worktree atual (`/Users/mac01/workspace/home-assistent-go/.worktrees/spec-03`); commits locais, sem push/merge.
- Assinaturas públicas que as specs 05 (tools) e 06 (flow) consumirão — não mudar sem justificativa:

```go
func NewClient(settings config.Settings) *Client
func (c *Client) CallService(ctx context.Context, domain, service string, serviceData map[string]any) (map[string]any, error)
func (c *Client) Toggle(ctx context.Context, entityID string) (map[string]any, error)
func (c *Client) TurnOn(ctx context.Context, entityID string) (map[string]any, error)
func (c *Client) TurnOff(ctx context.Context, entityID string) (map[string]any, error)
func (c *Client) GetState(ctx context.Context, entityID string) (map[string]any, error)
func (c *Client) Speak(ctx context.Context, text string) SpeakResult
func (c *Client) Close()
type Error struct{ Status int; Op string; Err error } // implementa error + Unwrap
type SpeakResult struct{ OK bool; Error string }
```

---

### Task 1: Esqueleto do package — `Error`, `Client`, `NewClient`, `Close`, `CallService`

**Files:**
- Create: `internal/ha/client.go`
- Create: `internal/ha/client_test.go`

**Interfaces:**
- Consumes: `config.Settings` de `home-assistent-go/internal/config` (campos `HAURL`, `HAToken`, `HATimeout`, `AlexaMediaEntity` — já implementado na spec 01).
- Produces (tasks 2–3 e specs 05/06/07 dependem destes nomes exatos):
  - `type Error struct { Status int; Op string; Err error }` com `func (e *Error) Error() string` e `func (e *Error) Unwrap() error`.
  - `type Client struct` com campos não-exportados `baseURL string`, `token string`, `alexaMediaEntity string`, `http *http.Client`.
  - `func NewClient(settings config.Settings) *Client`; `func (c *Client) Close()`.
  - `func (c *Client) CallService(ctx context.Context, domain, service string, serviceData map[string]any) (map[string]any, error)`.
  - Helpers internos (mesmo package): `func (c *Client) do(ctx context.Context, op, method, path string, payload any) (*http.Response, error)`, `func (c *Client) url(path string) string`, `func (c *Client) sanitize(msg string) string`, `func okStatus(status int) bool`, `func statusError(op string, resp *http.Response) *Error`, `func drainClose(resp *http.Response)`, `func servicePath(domain, service string) string`, `func decodeState(r io.Reader) (map[string]any, error)`.
  - Helpers de teste em `client_test.go` (tasks 2–3 reutilizam): `type gravada struct`, `func novoServidor(t *testing.T, g *gravada, status int, corpo string) *httptest.Server`, `func novoClient(ts *httptest.Server) *Client`, `func corpoDifere(body []byte, want map[string]any) string`, constante `tokenTeste = "token-de-teste"`.

- [ ] **Step 1: Escrever os testes que falham** — criar `internal/ha/client_test.go` completo:

```go
package ha

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"home-assistent-go/internal/config"
)

// tokenTeste é o token fake usado nos testes — as asserções de vazamento
// garantem que ele nunca aparece em mensagens de erro (§5).
const tokenTeste = "token-de-teste"

// gravada guarda o que o servidor de teste viu da última requisição.
type gravada struct {
	Method      string
	Path        string
	Auth        string
	ContentType string
	Body        []byte
}

// novoServidor cria um httptest.Server que grava a requisição em g e responde
// com o status e corpo informados.
func novoServidor(t *testing.T, g *gravada, status int, corpo string) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		*g = gravada{
			Method:      r.Method,
			Path:        r.URL.Path,
			Auth:        r.Header.Get("Authorization"),
			ContentType: r.Header.Get("Content-Type"),
			Body:        body,
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(corpo))
	}))
	t.Cleanup(ts.Close)
	return ts
}

// novoClient cria o Client de produção (NewClient) apontando para o servidor
// de teste — valida o wiring que a spec 07 fará com o Settings real.
func novoClient(ts *httptest.Server) *Client {
	return NewClient(config.Settings{
		HAURL:            ts.URL,
		HAToken:          tokenTeste,
		HATimeout:        5 * time.Second,
		AlexaMediaEntity: "media_player.alexa",
	})
}

// corpoDifere compara o JSON gravado com o objeto esperado; "" quando iguais.
func corpoDifere(body []byte, want map[string]any) string {
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		return fmt.Sprintf("corpo não é JSON de objeto: %q (%v)", body, err)
	}
	if !maps.Equal(got, want) {
		return fmt.Sprintf("corpo = %v; want %v", got, want)
	}
	return ""
}

func TestCallServiceMontaRequisicao(t *testing.T) { // critério 1
	var g gravada
	ts := novoServidor(t, &g, http.StatusOK, `{"state":"on"}`)
	c := novoClient(ts)

	got, err := c.CallService(context.Background(), "light", "turn_on", map[string]any{"entity_id": "light.luz_sala"})
	if err != nil {
		t.Fatalf("CallService: erro inesperado: %v", err)
	}
	if g.Method != http.MethodPost || g.Path != "/api/services/light/turn_on" {
		t.Errorf("requisição: %s %s; want POST /api/services/light/turn_on", g.Method, g.Path)
	}
	if g.Auth != "Bearer "+tokenTeste {
		t.Errorf("Authorization = %q; want Bearer %s", g.Auth, tokenTeste)
	}
	if g.ContentType != "application/json" {
		t.Errorf("Content-Type = %q; want application/json", g.ContentType)
	}
	if diff := corpoDifere(g.Body, map[string]any{"entity_id": "light.luz_sala"}); diff != "" {
		t.Errorf("%s", diff)
	}
	if got["state"] != "on" {
		t.Errorf("resposta = %v; want map com state=on", got)
	}
}

func TestCallServiceCorpoVazioQuandoDataNula(t *testing.T) { // §3
	var g gravada
	ts := novoServidor(t, &g, http.StatusOK, `[]`)
	c := novoClient(ts)

	got, err := c.CallService(context.Background(), "homeassistant", "update_entity", nil)
	if err != nil {
		t.Fatalf("CallService: erro inesperado: %v", err)
	}
	if string(g.Body) != "{}" {
		t.Errorf("corpo = %q; want {} (data nula vira corpo vazio)", g.Body)
	}
	// Resposta do HA que não é objeto (lista de estados modificados) vem
	// embrulhada — paridade com ha_client.py.
	if _, ok := got["result"]; !ok {
		t.Errorf("resposta = %v; want embrulhada em {\"result\": …}", got)
	}
}

func TestCallServiceErroStatus(t *testing.T) { // §5
	var g gravada
	ts := novoServidor(t, &g, http.StatusInternalServerError, `{"message":"boom"}`)
	c := novoClient(ts)

	_, err := c.CallService(context.Background(), "light", "turn_on", nil)
	var haErr *Error
	if !errors.As(err, &haErr) {
		t.Fatalf("want *ha.Error, veio %T: %v", err, err)
	}
	if haErr.Status != http.StatusInternalServerError {
		t.Errorf("Status = %d; want 500", haErr.Status)
	}
	if strings.Contains(err.Error(), tokenTeste) {
		t.Errorf("mensagem de erro vazou o token: %v", err)
	}
}

func TestTimeoutDoClientRespeitado(t *testing.T) { // critério 5
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond) // > HA_TIMEOUT_S do client abaixo
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ts.Close)
	c := NewClient(config.Settings{
		HAURL:     ts.URL,
		HAToken:   tokenTeste,
		HATimeout: 50 * time.Millisecond,
	})

	_, err := c.CallService(context.Background(), "light", "turn_on", nil)
	var ne net.Error
	if !errors.As(err, &ne) || !ne.Timeout() {
		t.Fatalf("want erro de timeout (net.Error.Timeout), veio %T: %v", err, err)
	}
	var haErr *Error
	if !errors.As(err, &haErr) || haErr.Status != 0 {
		t.Errorf("falha de rede deve ser *ha.Error com Status 0, veio: %v", err)
	}
}

func TestCamposInjetaveis(t *testing.T) { // decisão 12
	var g gravada
	ts := novoServidor(t, &g, http.StatusOK, `{"state":"off"}`)
	c := &Client{
		baseURL:          ts.URL,
		token:            "outro-token",
		alexaMediaEntity: "media_player.outro",
		http:             &http.Client{Timeout: time.Second},
	}
	got, err := c.CallService(context.Background(), "light", "turn_on", nil)
	if err != nil {
		t.Fatalf("CallService: erro inesperado: %v", err)
	}
	if g.Auth != "Bearer outro-token" {
		t.Errorf("Authorization = %q; want Bearer outro-token (campos injetáveis)", g.Auth)
	}
	if got["state"] != "off" {
		t.Errorf("estado = %v; want off", got)
	}
}

func TestCloseNaoPanica(t *testing.T) {
	var g gravada
	ts := novoServidor(t, &g, http.StatusOK, `{}`)
	c := novoClient(ts)
	c.Close() // graceful shutdown: libera conexões ociosas, sem panico
}
```

- [ ] **Step 2: Rodar e verificar que falham de compilação**

Run: `go test ./internal/ha/`
Expected: FAIL de compilação — `undefined: NewClient`, `undefined: Client`, `undefined: Error` (nada implementado ainda).

- [ ] **Step 3: Implementar `internal/ha/client.go`**

```go
// Package ha implementa o client HTTP do Home Assistant (paridade com
// src/services/ha_client.py do projeto Python): acionamento de serviços,
// toggle, turn_on/turn_off, leitura de estados e a síntese de voz via Alexa
// Media Player (notify.alexa_media). O Speak é o mecanismo de TTS do passo
// terminal do fluxo (ADR-0002): TTS não é tool e nunca propaga erro.
package ha

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"home-assistent-go/internal/config"
)

// Error é o erro retornado pelo client em falhas de chamada ao HA (§5).
// Status é o código HTTP quando houve resposta; 0 em falha de rede, timeout
// ou cancelamento do ctx. A mensagem nunca contém o token (sanitize).
type Error struct {
	Status int
	Op     string // operação: "CallService", "GetState", "Speak", …
	Err    error
}

func (e *Error) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("ha: %s: HTTP %d: %v", e.Op, e.Status, e.Err)
	}
	return fmt.Sprintf("ha: %s: %v", e.Op, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

// Client é o client HTTP do Home Assistant. Os campos são injetáveis nos
// testes (decisão 12); em produção construa com NewClient.
type Client struct {
	baseURL          string // ex.: "http://home.local:8123", sem barra final
	token            string // secret — nunca em log nem em mensagem de erro
	alexaMediaEntity string // media_player alvo do notify.alexa_media
	http             *http.Client
}

// NewClient cria o client a partir das configurações carregadas (spec 01):
// um único http.Client com timeout HA_TIMEOUT_S, que vale para toda chamada
// (baseline item 2) — corrige a criação por-chamada do Python (decisão 7).
func NewClient(settings config.Settings) *Client {
	return &Client{
		baseURL:          strings.TrimRight(settings.HAURL, "/"),
		token:            settings.HAToken,
		alexaMediaEntity: settings.AlexaMediaEntity,
		http:             &http.Client{Timeout: settings.HATimeout},
	}
}

// Close libera as conexões ociosas do pool (graceful shutdown, §2).
func (c *Client) Close() { c.http.CloseIdleConnections() }

// CallService faz o POST /api/services/{domain}/{service} (§3). Corpo é {}
// quando serviceData é nil. A resposta JSON do HA vem como map: quando o
// corpo não é um objeto (p. ex. a lista de estados modificados), vem
// embrulhado como {"result": …} — paridade com ha_client.py.
func (c *Client) CallService(ctx context.Context, domain, service string, serviceData map[string]any) (map[string]any, error) {
	data := serviceData
	if data == nil {
		data = map[string]any{}
	}
	resp, err := c.do(ctx, "CallService", http.MethodPost, servicePath(domain, service), data)
	if err != nil {
		return nil, err
	}
	defer drainClose(resp)
	if !okStatus(resp.StatusCode) {
		return nil, statusError("CallService", resp)
	}
	m, err := decodeState(resp.Body)
	if err != nil {
		return nil, &Error{Op: "CallService", Err: err}
	}
	return m, nil
}

// do monta a requisição com os headers padrão (§2) e a envia. Quem chama é
// responsável por drainClose(resp). Erros preservam a cadeia original (%w) —
// quem consome pode usar errors.Is(err, context.DeadlineExceeded) e
// errors.As(err, &net.Error); o token não entra em nenhuma mensagem porque só
// existe no header (nunca ecoado) e a extração de string no Speak passa por
// sanitize.
func (c *Client) do(ctx context.Context, op, method, path string, payload any) (*http.Response, error) {
	var body io.Reader
	if payload != nil {
		buf, err := json.Marshal(payload)
		if err != nil {
			return nil, &Error{Op: op, Err: fmt.Errorf("serializando corpo: %w", err)}
		}
		body = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.url(path), body)
	if err != nil {
		return nil, &Error{Op: op, Err: err}
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, &Error{Op: op, Err: fmt.Errorf("%s %s: %w", method, path, err)}
	}
	return resp, nil
}

// url junta baseURL e path.
func (c *Client) url(path string) string { return c.baseURL + path }

// sanitize remove o token de qualquer mensagem de erro (§5) — defensivo:
// nenhum caminho de erro pode ecoar o secret.
func (c *Client) sanitize(msg string) string {
	if c.token == "" {
		return msg
	}
	return strings.ReplaceAll(msg, c.token, "[token]")
}

// okStatus reporta sucesso 2xx.
func okStatus(status int) bool { return status >= 200 && status <= 299 }

// statusError constrói o *Error para um status ≠ 2xx. A mensagem carrega
// apenas status e motivo — nunca o corpo nem o token (§5).
func statusError(op string, resp *http.Response) *Error {
	return &Error{Status: resp.StatusCode, Op: op, Err: errors.New(http.StatusText(resp.StatusCode))}
}

// drainClose drena o corpo (limitado, para reuso da conexão keep-alive) e o
// fecha.
func drainClose(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
}

// servicePath monta o caminho de /api/services com escape de path.
func servicePath(domain, service string) string {
	return "/api/services/" + url.PathEscape(domain) + "/" + url.PathEscape(service)
}

// decodeState decodifica o corpo JSON como map; quando não é um objeto,
// embrulha em {"result": …} — paridade com ha_client.py.
func decodeState(r io.Reader) (map[string]any, error) {
	var raw any
	if err := json.NewDecoder(r).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decodificando resposta JSON: %w", err)
	}
	if m, ok := raw.(map[string]any); ok {
		return m, nil
	}
	return map[string]any{"result": raw}, nil
}
```

(Import block de `client.go`: `bytes`, `context`, `encoding/json`, `errors`, `fmt`, `io`, `net/http`, `net/url`, `strings` e `home-assistent-go/internal/config` — `time` não é usado neste arquivo; o timeout vem pronto como `time.Duration` em `settings.HATimeout`.)

- [ ] **Step 4: Rodar os testes e verificar que passam**

Run: `go test -race ./internal/ha/ -v`
Expected: PASS nos 6 testes da Step 1 (montagem da requisição, corpo vazio, erro de status, timeout, campos injetáveis, close). Se `TestTimeoutDoClientRespeitado` falhar, conferir se o `http.Client` do `NewClient` carrega `Timeout: settings.HATimeout`.

- [ ] **Step 5: Gates**

Run: `gofmt -l . && go vet ./... && go test -race ./...`
Expected: gofmt sem saída; vet limpo; testes verdes (incluindo `internal/config`).

- [ ] **Step 6: Commit**

```bash
git add internal/ha/
git commit -m "feat(ha): client HTTP com CallService e Error tipada

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

---

### Task 2: `Toggle`, `TurnOn`/`TurnOff` (domínio do entity) e `GetState`

**Files:**
- Modify: `internal/ha/client.go`
- Test: `internal/ha/client_test.go`

**Interfaces:**
- Consumes: `CallService`, `do`, `statusError`, `decodeState`, helpers de teste `gravada`/`novoServidor`/`novoClient`/`corpoDifere`/`tokenTeste` (Task 1).
- Produces: `func (c *Client) Toggle(ctx context.Context, entityID string) (map[string]any, error)`, `func (c *Client) TurnOn(ctx context.Context, entityID string) (map[string]any, error)`, `func (c *Client) TurnOff(ctx context.Context, entityID string) (map[string]any, error)`, `func (c *Client) GetState(ctx context.Context, entityID string) (map[string]any, error)`, helper `func domainOf(entityID string) string`.

- [ ] **Step 1: Escrever os testes que falham** — acrescentar a `internal/ha/client_test.go`:

```go
func TestToggleChamaHomeassistant(t *testing.T) { // §3
	var g gravada
	ts := novoServidor(t, &g, http.StatusOK, `[]`)
	c := novoClient(ts)

	if _, err := c.Toggle(context.Background(), "switch.tomada"); err != nil {
		t.Fatalf("Toggle: erro inesperado: %v", err)
	}
	if g.Method != http.MethodPost || g.Path != "/api/services/homeassistant/toggle" {
		t.Errorf("requisição: %s %s; want POST /api/services/homeassistant/toggle", g.Method, g.Path)
	}
	if diff := corpoDifere(g.Body, map[string]any{"entity_id": "switch.tomada"}); diff != "" {
		t.Errorf("%s", diff)
	}
}

func TestTurnOnExtraiDominioDoEntity(t *testing.T) { // critério 2
	var g gravada
	ts := novoServidor(t, &g, http.StatusOK, `[]`)
	c := novoClient(ts)

	if _, err := c.TurnOn(context.Background(), "light.luz_sala"); err != nil {
		t.Fatalf("TurnOn: erro inesperado: %v", err)
	}
	if g.Path != "/api/services/light/turn_on" {
		t.Errorf("path = %q; want /api/services/light/turn_on (domínio do prefixo)", g.Path)
	}
	if diff := corpoDifere(g.Body, map[string]any{"entity_id": "light.luz_sala"}); diff != "" {
		t.Errorf("%s", diff)
	}
}

func TestTurnOffExtraiDominioDoEntity(t *testing.T) {
	var g gravada
	ts := novoServidor(t, &g, http.StatusOK, `[]`)
	c := novoClient(ts)

	if _, err := c.TurnOff(context.Background(), "switch.tomada_cozinha"); err != nil {
		t.Fatalf("TurnOff: erro inesperado: %v", err)
	}
	if g.Path != "/api/services/switch/turn_off" {
		t.Errorf("path = %q; want /api/services/switch/turn_off", g.Path)
	}
}

func TestDomainOf(t *testing.T) { // paridade: entity_id.split(".", 1)[0]
	casos := []struct{ entity, want string }{
		{"light.luz_sala", "light"},
		{"switch.tomada_cozinha", "switch"},
		{"semDomingo", "semDomingo"}, // sem ".": o próprio entity (paridade)
	}
	for _, tc := range casos {
		if got := domainOf(tc.entity); got != tc.want {
			t.Errorf("domainOf(%q) = %q; want %q", tc.entity, got, tc.want)
		}
	}
}

func TestGetState(t *testing.T) { // §3
	var g gravada
	ts := novoServidor(t, &g, http.StatusOK, `{"entity_id":"light.luz_sala","state":"on"}`)
	c := novoClient(ts)

	got, err := c.GetState(context.Background(), "light.luz_sala")
	if err != nil {
		t.Fatalf("GetState: erro inesperado: %v", err)
	}
	if g.Method != http.MethodGet || g.Path != "/api/states/light.luz_sala" {
		t.Errorf("requisição: %s %s; want GET /api/states/light.luz_sala", g.Method, g.Path)
	}
	if got["state"] != "on" {
		t.Errorf("estado = %v; want on", got)
	}
}

func TestGetState404ComStatus(t *testing.T) { // critério 4
	var g gravada
	ts := novoServidor(t, &g, http.StatusNotFound, `{"message":"Entity not found."}`)
	c := novoClient(ts)

	_, err := c.GetState(context.Background(), "light.inexistente")
	var haErr *Error
	if !errors.As(err, &haErr) {
		t.Fatalf("want *ha.Error, veio %T: %v", err, err)
	}
	if haErr.Status != http.StatusNotFound {
		t.Errorf("Status = %d; want 404", haErr.Status)
	}
	if strings.Contains(err.Error(), tokenTeste) {
		t.Errorf("mensagem de erro vazou o token: %v", err)
	}
}
```

- [ ] **Step 2: Rodar e verificar que falham de compilação**

Run: `go test ./internal/ha/ -run 'TestToggle|TestTurn|TestDomainOf|TestGetState' -v`
Expected: FAIL — `c.Toggle undefined`, `c.TurnOn undefined`, `c.GetState undefined`, `domainOf undefined`.

- [ ] **Step 3: Implementar em `internal/ha/client.go`** (acrescentar após `CallService`):

```go
// Toggle aciona homeassistant.toggle para o entity (§3).
func (c *Client) Toggle(ctx context.Context, entityID string) (map[string]any, error) {
	return c.CallService(ctx, "homeassistant", "toggle", map[string]any{"entity_id": entityID})
}

// TurnOn liga o entity, com o domínio extraído do prefixo antes do primeiro
// "." (§3).
func (c *Client) TurnOn(ctx context.Context, entityID string) (map[string]any, error) {
	return c.CallService(ctx, domainOf(entityID), "turn_on", map[string]any{"entity_id": entityID})
}

// TurnOff desliga o entity, com o domínio extraído do prefixo antes do
// primeiro "." (§3).
func (c *Client) TurnOff(ctx context.Context, entityID string) (map[string]any, error) {
	return c.CallService(ctx, domainOf(entityID), "turn_off", map[string]any{"entity_id": entityID})
}

// domainOf devolve o prefixo do entity_id antes do primeiro "." — paridade
// com entity_id.split(".", 1)[0] do Python (sem ".", devolve o próprio entity).
func domainOf(entityID string) string {
	domain, _, _ := strings.Cut(entityID, ".")
	return domain
}

// GetState faz o GET /api/states/{entity_id} (§3).
func (c *Client) GetState(ctx context.Context, entityID string) (map[string]any, error) {
	resp, err := c.do(ctx, "GetState", http.MethodGet, "/api/states/"+url.PathEscape(entityID), nil)
	if err != nil {
		return nil, err
	}
	defer drainClose(resp)
	if !okStatus(resp.StatusCode) {
		return nil, statusError("GetState", resp)
	}
	m, err := decodeState(resp.Body)
	if err != nil {
		return nil, &Error{Op: "GetState", Err: err}
	}
	return m, nil
}
```

- [ ] **Step 4: Rodar e verificar passagem**

Run: `go test -race ./internal/ha/ -v`
Expected: PASS em todos os testes (Task 1 continua verde).

- [ ] **Step 5: Gates + Commit**

Run: `gofmt -l . && go vet ./... && go test -race ./...`

```bash
git add internal/ha/
git commit -m "feat(ha): toggle, turn_on/turn_off por domínio e GetState

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

---

### Task 3: `Speak` — TTS defensivo via `notify.alexa_media`

**Files:**
- Create: `internal/ha/speak.go`
- Test: `internal/ha/speak_test.go`

**Interfaces:**
- Consumes: `Client` (campos `token`, `alexaMediaEntity`), `do`, `okStatus`, `statusError`, `drainClose`, `c.url`, helpers de teste `gravada`/`novoServidor`/`novoClient`/`tokenTeste` (Tasks 1–2).
- Produces: `type SpeakResult struct { OK bool; Error string }` e `func (c *Client) Speak(ctx context.Context, text string) SpeakResult`.

- [ ] **Step 1: Escrever os testes que falham** — criar `internal/ha/speak_test.go`:

```go
package ha

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSpeakSucesso(t *testing.T) { // critério 3a + contrato §4
	var g gravada
	ts := novoServidor(t, &g, http.StatusOK, `[]`)
	c := novoClient(ts)

	got := c.Speak(context.Background(), "Luz da sala ligada")
	if !got.OK || got.Error != "" {
		t.Fatalf("Speak = %+v; want {OK: true, Error: \"\"}", got)
	}
	var corpo map[string]any
	if err := json.Unmarshal(g.Body, &corpo); err != nil {
		t.Fatalf("corpo não é JSON: %q (%v)", g.Body, err)
	}
	if corpo["message"] != "Luz da sala ligada" {
		t.Errorf("message = %v; want o texto falado", corpo["message"])
	}
	if corpo["target"] != "media_player.alexa" {
		t.Errorf("target = %v; want ALEXA_MEDIA_ENTITY (campo target, §4)", corpo["target"])
	}
	if _, temData := corpo["data"]; temData {
		t.Errorf("corpo não deve usar data.entity_id (alexa_media rejeita com 500): %v", corpo)
	}
	if g.Method != http.MethodPost || g.Path != "/api/services/notify/alexa_media" {
		t.Errorf("requisição: %s %s; want POST /api/services/notify/alexa_media", g.Method, g.Path)
	}
	if g.Auth != "Bearer "+tokenTeste || g.ContentType != "application/json" {
		t.Errorf("headers = %q / %q; want Bearer + application/json", g.Auth, g.ContentType)
	}
}

func TestSpeakErro500SemPanico(t *testing.T) { // critério 3b
	var g gravada
	ts := novoServidor(t, &g, http.StatusInternalServerError, `{"message":"erro"}`)
	c := novoClient(ts)

	got := c.Speak(context.Background(), "olá")
	if got.OK {
		t.Fatal("Speak deve reportar falha em 500")
	}
	if got.Error == "" {
		t.Error("Speak deve informar o erro em Error")
	}
	if strings.Contains(got.Error, tokenTeste) {
		t.Errorf("erro vazou o token: %q", got.Error)
	}
}

func TestSpeakConexaoRecusadaSemPanico(t *testing.T) { // critério 3c
	ts := httptestNovoServidorMorto(t)
	c := novoClient(ts)

	got := c.Speak(context.Background(), "olá")
	if got.OK || got.Error == "" {
		t.Fatalf("Speak = %+v; want {OK: false, Error: não-vazio}", got)
	}
}

func TestSpeakContextoDoTurnoVence(t *testing.T) { // §4: ctx do turno vence
	ts := httptestNovoServidorLento(t, 200*time.Millisecond)
	c := novoClient(ts)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	got := c.Speak(ctx, "olá")
	if got.OK || got.Error == "" {
		t.Fatalf("Speak = %+v; want falha pelo deadline do ctx do turno", got)
	}
}
```

E os dois helpers auxiliares (também em `speak_test.go` — servidores sem gravação, para os casos em que a requisição não importa):

```go
// httptestNovoServidorMorto devolve um "servidor" já fechado: toda conexão é
// recusada.
func httptestNovoServidorMorto(t *testing.T) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	ts.Close()
	return ts
}

// httptestNovoServidorLento responde 200 após a espera informada.
func httptestNovoServidorLento(t *testing.T, espera time.Duration) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(espera)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ts.Close)
	return ts
}
```

- [ ] **Step 2: Rodar e verificar que falham de compilação**

Run: `go test ./internal/ha/ -run TestSpeak -v`
Expected: FAIL — `c.Speak undefined`.

- [ ] **Step 3: Implementar `internal/ha/speak.go`**

```go
package ha

import (
	"context"
	"net/http"
)

// SpeakResult é o resultado defensivo do TTS (§4): quem decide o que fazer em
// falha de fala é o passo speak do fluxo (spec 06), não o client.
type SpeakResult struct {
	OK    bool
	Error string
}

// Speak sintetiza voz via notify.alexa_media (§4) com o contrato
// {"message": <texto>, "target": <ALEXA_MEDIA_ENTITY>} — usa target (campo
// padrão do serviço notify), NUNCA data.entity_id, que o alexa_media rejeita
// com 500 (armadilha documentada no projeto Python). Defensivo: em qualquer
// falha (rede, HTTP, timeout, deadline do ctx do turno) devolve
// SpeakResult{OK: false, Error: …} e nunca propaga erro; em sucesso devolve
// SpeakResult{OK: true}.
func (c *Client) Speak(ctx context.Context, text string) SpeakResult {
	payload := map[string]string{"message": text, "target": c.alexaMediaEntity}
	resp, err := c.do(ctx, "Speak", http.MethodPost, "/api/services/notify/alexa_media", payload)
	if err != nil {
		return SpeakResult{OK: false, Error: c.sanitize(err.Error())}
	}
	defer drainClose(resp)
	if !okStatus(resp.StatusCode) {
		return SpeakResult{OK: false, Error: c.sanitize(statusError("Speak", resp).Error())}
	}
	return SpeakResult{OK: true}
}
```

- [ ] **Step 4: Rodar e verificar passagem**

Run: `go test -race ./internal/ha/ -v`
Expected: PASS em todos os testes (Tasks 1–2 continuam verdes).

- [ ] **Step 5: Gates + Commit**

Run: `gofmt -l . && go vet ./... && go test -race ./...`

```bash
git add internal/ha/
git commit -m "feat(ha): Speak defensivo via notify.alexa_media

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

---

### Task 4: Gates finais da branch

**Files:**
- Modify (apenas se algum gate acusar): qualquer arquivo de `internal/ha/`.

**Interfaces:**
- Consumes: branch completa (Tasks 1–3).
- Produces: branch pronta para a revisão final do subagent-driven-development e para o code-review.

- [ ] **Step 1: Gates completos no módulo inteiro**

```bash
gofmt -l .
go vet ./...
go mod tidy && git diff --exit-code go.mod go.sum
go test -race ./... -count=1
go build ./...
```

Expected: gofmt sem saída; vet limpo; `go.mod`/`go.sum` estáveis pós-tidy (nenhuma dependência nova); testes verdes; build ok.

- [ ] **Step 2: Auditoria de secrets e timeout** — reler `internal/ha/`: (a) nenhum `log`/`fmt.Print` no package; (b) o token nunca entra em mensagem de erro — só existe no header `Authorization` (nunca ecoado pelo `net/http`) e toda extração de string visível no `Speak` passa por `sanitize`; erros preservam a cadeia via `%w`; (c) o único `http.Client` nasce em `NewClient` com `Timeout: settings.HATimeout` — nenhuma chamada HTTP fora dele.

- [ ] **Step 3: Commit apenas se algum gate exigiu correção**

```bash
git add -A && git commit -m "chore(ha): ajustes dos gates finais

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

(se nada mudou, nenhuma commit nesta task)
