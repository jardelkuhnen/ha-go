package agent

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
		return &ai.ModelResponse{Request: req, Message: ai.NewModelTextMessage(s), FinishReason: ai.FinishReasonStop}, nil
	case ai.ToolRequest:
		tr := s
		return &ai.ModelResponse{Request: req, Message: ai.NewModelMessage(ai.NewToolRequestPart(&tr)), FinishReason: ai.FinishReasonStop}, nil
	case []ai.ToolRequest:
		parts := make([]*ai.Part, 0, len(s))
		for _, tr := range s {
			tr := tr
			parts = append(parts, ai.NewToolRequestPart(&tr))
		}
		return &ai.ModelResponse{Request: req, Message: ai.NewModelMessage(parts...), FinishReason: ai.FinishReasonStop}, nil
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
		{Name: "control_device", Input: map[string]any{"action": "on", "entity_id": "switch.indireta_cozinha"}},
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
