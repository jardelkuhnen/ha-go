package api

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

	"home-assistent-go/internal/brain"
	"home-assistent-go/internal/config"
	"home-assistent-go/internal/ha"
	"home-assistent-go/internal/tools"
)

// ---------- teste integrado (spec 07 §5, decisão 9 — mocks ponta a ponta) ----------
// Sobe o servidor COMPLETO (auth + flow real do brain com as tools reais de
// produção, todas as fronteiras externas mockadas): modelo Genkit fake via
// genkit.DefineModelAction (script: 1ª volta tool call, 2ª volta texto), Home
// Assistant e Open-Meteo via httptest. Determinístico: httptest.NewServer
// (zero porta fixa), zero serviço externo, verde sob -race.

// nomeModeloFake é o endereçamento do modelo scriptado no registry.
const nomeModeloFake = "fake/motor"

// modeloFake é o motor cognitivo scriptado (§5): cada chamada de Generate
// consome o próximo passo do roteiro; passo único nunca esgota.
type modeloFake struct {
	mu    sync.Mutex
	steps []any
	calls int
}

func (m *modeloFake) handle(_ context.Context, _ *ai.ModelRequest, _ any, _ ai.ModelStreamCallback) (*ai.ModelResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	if len(m.steps) == 0 {
		return nil, fmt.Errorf("modeloFake: roteiro esgotado (chamada %d)", m.calls)
	}
	step := m.steps[0]
	if len(m.steps) > 1 {
		m.steps = m.steps[1:]
	}
	switch s := step.(type) {
	case string:
		return &ai.ModelResponse{Message: ai.NewModelTextMessage(s), FinishReason: ai.FinishReasonStop}, nil
	case ai.ToolRequest:
		req := s
		return &ai.ModelResponse{Message: ai.NewModelMessage(ai.NewToolRequestPart(&req)), FinishReason: ai.FinishReasonStop}, nil
	default:
		return nil, fmt.Errorf("modeloFake: passo inesperado: %T", step)
	}
}

// haMock grava cada requisição que o Home Assistant de teste recebeu.
type haMock struct {
	mu   sync.Mutex
	reqs []string // "METHOD path corpo"
}

func (h *haMock) linhas() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.reqs...)
}

// meteoMock grava a última query recebida por um endpoint do Open-Meteo.
type meteoMock struct {
	mu    sync.Mutex
	query string
}

func (mm *meteoMock) ultima() string {
	mm.mu.Lock()
	defer mm.mu.Unlock()
	return mm.query
}

// stack é o servidor completo da §5: modelo fake + HA httptest + Open-Meteo
// httptest + flow real (tools de produção apontadas aos mocks) + router com
// auth. Cada teste monta a própria (registry do Genkit é por instância).
type stack struct {
	ts   *httptest.Server // API (auth + flow real)
	ha   *haMock
	geo  *meteoMock
	prev *meteoMock
}

// montaStack sobe a stack inteira da §5. Wiring de produção (§4) com só as
// fronteiras externas trocadas: client HA de produção (ha.NewClient),
// catálogo real com os endpoints do Open-Meteo apontados aos mocks e flow
// real (DefineBrainWithRefs — catálogo injetável, única diferença do main).
func montaStack(t *testing.T, steps []any) *stack {
	t.Helper()
	ctx := context.Background()

	g := genkit.Init(ctx)
	modelo := &modeloFake{steps: steps}
	opts := &ai.ModelOptions{
		Label: "modelo fake do teste integrado (spec 07 §5)",
		Supports: &ai.ModelSupports{
			Tools:      true,
			ToolChoice: true,
			Multiturn:  true,
			SystemRole: true,
		},
	}
	if genkit.DefineModelAction(g, nomeModeloFake, opts, modelo.handle) == nil {
		t.Fatal("genkit.DefineModelAction devolveu nil")
	}

	haMock := &haMock{}
	tsHA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		haMock.mu.Lock()
		haMock.reqs = append(haMock.reqs, r.Method+" "+r.URL.Path+" "+string(body))
		haMock.mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(tsHA.Close)

	geo := &meteoMock{}
	tsGeo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		geo.mu.Lock()
		geo.query = r.URL.RawQuery
		geo.mu.Unlock()
		_, _ = w.Write([]byte(`{"results":[{"latitude":-23.5505,"longitude":-46.6333}]}`))
	}))
	t.Cleanup(tsGeo.Close)

	prev := &meteoMock{}
	tsPrev := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		prev.mu.Lock()
		prev.query = r.URL.RawQuery
		prev.mu.Unlock()
		_, _ = w.Write([]byte(`{"current":{"weather_code":80},"daily":{"temperature_2m_max":[28.4],"temperature_2m_min":[19.2],"weather_code":[80]}}`))
	}))
	t.Cleanup(tsPrev.Close)

	cli := ha.NewClient(config.Settings{
		HAURL:            tsHA.URL,
		HAToken:          "token-de-teste",
		HATimeout:        5 * time.Second,
		AlexaMediaEntity: "media_player.alexa_sala",
	})
	m := brain.NewMotor(g, "ollama", nomeModeloFake, 5*time.Second)
	refs := tools.CatalogWithWeather(g, cli, tsGeo.URL, tsPrev.URL)
	flow := brain.DefineBrainWithRefs(m, cli, refs)

	router := NewRouter(Options{
		Runner:   flow,
		APIKey:   chaveTeste,
		Provider: "ollama",
		Model:    "llama3.2:3b",
	})
	ts := httptest.NewServer(router)
	t.Cleanup(ts.Close)

	return &stack{ts: ts, ha: haMock, geo: geo, prev: prev}
}

// chatPost faz o POST /chat no servidor da stack com a key informada ("" =
// sem header).
func chatPost(t *testing.T, s *stack, key, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, s.ts.URL+"/chat", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set(headerAPIKey, key)
	}
	resp, err := s.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("POST /chat: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// corpoJSON lê e decodifica o corpo da resposta.
func corpoJSON(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatalf("corpo não é JSON: %v", err)
	}
	return m
}

// toolsUsadasDo extrai metadata.tools_used decodificado.
func toolsUsadasDo(t *testing.T, c map[string]any) []any {
	t.Helper()
	meta, ok := c["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("metadata ausente ou não é objeto: %v", c)
	}
	tu, ok := meta["tools_used"].([]any)
	if !ok {
		t.Fatalf("tools_used ausente ou não é array: %v", meta)
	}
	return tu
}

// ---------- cenário 1: sem API key / key errada → 401 ----------

func TestIntegrado401(t *testing.T) { // §5.1
	s := montaStack(t, nil)
	for _, key := range []string{"", "key-errada"} {
		resp := chatPost(t, s, key, `{"text":"clima em São Paulo"}`)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("key=%q: status = %d; want 401", key, resp.StatusCode)
		}
		c := corpoJSON(t, resp)
		if c["error"] != "invalid api key" {
			t.Errorf("corpo = %v; want {\"error\": \"invalid api key\"}", c)
		}
	}
	// O turno nunca começou: zero requests ao HA.
	if got := s.ha.linhas(); len(got) != 0 {
		t.Errorf("401 não deve tocar o flow; HA recebeu %v", got)
	}
}

// ---------- cenário 2: turno com tool → 200, tools_used=[get_weather] ----------

func TestIntegradoTurnoComTool(t *testing.T) { // §5.2
	steps := []any{
		ai.ToolRequest{Name: "get_weather", Input: map[string]any{"location": "São Paulo"}},
		"A máxima é de 28 graus, com pancadas de chuva.",
	}
	s := montaStack(t, steps)
	resp := chatPost(t, s, chaveTeste, `{"text":"clima em São Paulo"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; want 200", resp.StatusCode)
	}
	c := corpoJSON(t, resp)
	if c["reply"] != "A máxima é de 28 graus, com pancadas de chuva." {
		t.Errorf("reply = %v; want o texto da 2ª volta do script", c["reply"])
	}
	if c["spoken"] != true || c["error"] != nil {
		t.Errorf("spoken/error = %v/%v; want true/null (HA mockado)", c["spoken"], c["error"])
	}
	if c["source"] != sourceDefault {
		t.Errorf("source = %v; want %q", c["source"], sourceDefault)
	}
	if tu := toolsUsadasDo(t, c); len(tu) != 1 || tu[0] != "get_weather" {
		t.Errorf("tools_used = %v; want [get_weather]", tu)
	}

	// O caminho real completo: get_weather de produção rodou contra os mocks
	// do Open-Meteo (geocoding resolveu São Paulo; forecast recebeu o ponto).
	if got := s.geo.ultima(); !strings.Contains(got, "name=S%C3%A3o+Paulo") {
		t.Errorf("geocoding: query = %q; want name=São Paulo codificada", got)
	}
	if got := s.prev.ultima(); !strings.Contains(got, "latitude=-23.5505") || !strings.Contains(got, "longitude=-46.6333") {
		t.Errorf("forecast: query = %q; want coordenadas de São Paulo", got)
	}

	// TTS no HA mockado: notify.alexa_media com o reply como message (§4).
	linhas := s.ha.linhas()
	if len(linhas) != 1 {
		t.Fatalf("HA recebeu %d requests (%v); want 1 (speak)", len(linhas), linhas)
	}
	if !strings.HasPrefix(linhas[0], http.MethodPost+" /api/services/notify/alexa_media ") {
		t.Errorf("speak: %q; want POST /api/services/notify/alexa_media", linhas[0])
	}
	if !strings.Contains(linhas[0], `"message":"A máxima é de 28 graus, com pancadas de chuva."`) {
		t.Errorf("speak sem o reply como message: %q", linhas[0])
	}
	if !strings.Contains(linhas[0], `"target":"media_player.alexa_sala"`) {
		t.Errorf("speak sem o target da config: %q", linhas[0])
	}
}

// ---------- cenário 3: metadata.source="telegram" → spoken=false ----------

func TestIntegradoTelegram(t *testing.T) { // §5.3
	steps := []any{
		ai.ToolRequest{Name: "get_weather", Input: map[string]any{"location": "São Paulo"}},
		"Em São Paulo, máxima de 28 e pancadas de chuva.",
	}
	s := montaStack(t, steps)
	resp := chatPost(t, s, chaveTeste,
		`{"text":"clima em São Paulo","metadata":{"source":"telegram","session_id":"telegram_123"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; want 200", resp.StatusCode)
	}
	c := corpoJSON(t, resp)
	if c["spoken"] != false || c["error"] != nil {
		t.Errorf("spoken/error = %v/%v; want false/null (telegram não fala)", c["spoken"], c["error"])
	}
	if c["source"] != "telegram" {
		t.Errorf("source = %v; want telegram (normalizado e ecoado)", c["source"])
	}
	if tu := toolsUsadasDo(t, c); len(tu) != 1 || tu[0] != "get_weather" {
		t.Errorf("tools_used = %v; want [get_weather]", tu)
	}
	if got := s.ha.linhas(); len(got) != 0 {
		t.Errorf("telegram não deve acionar a Alexa; HA recebeu %v", got)
	}
}

// ---------- cenário 4: health sem auth ----------

func TestIntegradoHealth(t *testing.T) { // §5.4
	s := montaStack(t, nil)
	resp, err := s.ts.Client().Get(s.ts.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; want 200 sem auth", resp.StatusCode)
	}
	if c := corpoJSON(t, resp); c["status"] != "ok" {
		t.Errorf("corpo = %v; want {\"status\": \"ok\"}", c)
	}
}

// ---------- cenário 5: corpo inválido → 400 ----------

func TestIntegradoCorpoInvalido(t *testing.T) { // §5.5
	s := montaStack(t, nil)
	resp := chatPost(t, s, chaveTeste, `{"text":`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400", resp.StatusCode)
	}
	c := corpoJSON(t, resp)
	if _, ok := c["error"]; !ok {
		t.Errorf("corpo sem \"error\": %v", c)
	}
}
