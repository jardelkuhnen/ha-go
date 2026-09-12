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

// ---------- modelo fake (§8: genkit.DefineModel) ----------

// fakeModel modela um motor cognitivo scriptado: cada chamada de Generate
// consome o próximo passo do roteiro; um passo único nunca esgota (modelo
// "sempre tool" do critério 4). Passos: string (texto), ai.ToolRequest (uma
// tool call) ou []ai.ToolRequest (várias tool calls na mesma resposta).
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

func (f *fakeModel) handle(ctx context.Context, req *ai.ModelRequest, cb ai.ModelStreamCallback) (*ai.ModelResponse, error) {
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

// novoMotorFake monta o Genkit com o modelo fake endereçado por m.ModelName e
// o Motor à mão (paridade com brain_test.go — timeout direto, sem Setup).
func novoMotorFake(t *testing.T, fm *fakeModel) *Motor {
	t.Helper()
	ctx := context.Background()
	g := genkit.Init(ctx)
	opts := &ai.ModelOptions{
		Label: "motor fake dos testes (spec 06 §8)",
		Supports: &ai.ModelSupports{
			Tools:      true,
			ToolChoice: true,
			Multiturn:  true,
			SystemRole: true,
		},
	}
	if genkit.DefineModel(g, nomeModeloFake, opts, fm.handle) == nil {
		t.Fatal("DefineModel devolveu nil")
	}
	return &Motor{Genkit: g, Provider: "fake", ModelName: nomeModeloFake, timeout: 2 * time.Second}
}

const nomeModeloFake = "fake/motor"

// ---------- tools fake (§8: tool executa — mock) ----------

// fakeTools grava invocações das tools fake na ordem.
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

// registrarFakeTools registra get_weather e control_device fake no registry de
// g e devolve as refs — os mocks do critério 1, na mesma forma de wiring que
// o DefineBrain de produção recebe de tools.Catalog.
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
			return "Liguei o tomada da sala.", nil
		})
	return []ai.ToolRef{weather, control}
}

// ---------- mock do Home Assistant (§8: padrão httptest) ----------

// gravadaHA guarda a última requisição que o servidor HA de teste viu.
type gravadaHA struct {
	Method string
	Path   string
	Body   []byte
}

func novoServidorHA(t *testing.T, g *gravadaHA, status int) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		*g = gravadaHA{Method: r.Method, Path: r.URL.Path, Body: body}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(ts.Close)
	return ts
}

// novoClientHA cria o client de produção (ha.NewClient) apontando para o
// servidor de teste — o flow nunca monta HTTP próprio (spec 03, decisão 7).
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

// rodarTurno define o flow (com as refs informadas) e executa um turno.
func rodarTurno(t *testing.T, m *Motor, cli *ha.Client, refs []ai.ToolRef, in ChatInput) (ChatOutput, error) {
	t.Helper()
	flow := defineBrain(m, cli, refs)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return flow.Run(ctx, in)
}

// ---------- critérios de aceite (§8) ----------

func TestFlowToolLoopCriterio1(t *testing.T) { // critério 1: tool → executa → 2ª volta texto
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
	if len(out.ToolsUsed) != 1 || out.ToolsUsed[0] != "get_weather" {
		t.Errorf("ToolsUsed = %v; want [get_weather]", out.ToolsUsed)
	}
	if !out.Spoken {
		t.Errorf("Spoken = false; want true (client HA mockado com sucesso)")
	}
	if out.Error != "" {
		t.Errorf("Error = %q; want vazio", out.Error)
	}
	if gHA.Method != http.MethodPost || gHA.Path != "/api/services/notify/alexa_media" {
		t.Fatalf("speak: requisição %s %s; want POST /api/services/notify/alexa_media", gHA.Method, gHA.Path)
	}
	corpo := corpoSpeak(t, gHA.Body)
	if corpo["message"] != out.Reply {
		t.Errorf("speak: message = %v; want %q", corpo["message"], out.Reply)
	}
	if corpo["target"] != "media_player.alexa_sala" {
		t.Errorf("speak: target = %v; want media_player.alexa_sala", corpo["target"])
	}

	// O histórico da 2ª geração trouxe os resultados da tool (§3): system +
	// user + model (tool request) + tool (resposta).
	last := fm.lastReq
	if got := len(last.Messages); got != 4 {
		t.Fatalf("histórico da 2ª geração = %d mensagens; want 4", got)
	}
	if last.Messages[2].Role != ai.RoleModel || last.Messages[2].Content[0].ToolRequest == nil {
		t.Fatalf("mensagens[2] = %v; want model com tool request", last.Messages[2])
	}
	if last.Messages[3].Role != ai.RoleTool {
		t.Fatalf("mensagens[3].Role = %q; want tool", last.Messages[3].Role)
	}
	if got := last.Messages[3].Content[0].ToolResponse.Output; got != "Máxima de 28, mínima de 19." {
		t.Errorf("output da tool no histórico = %v; want frase do mock", got)
	}
}

func TestFlowRespostaDiretaVozCriterio2(t *testing.T) { // critério 2
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
	if gHA.Path != "/api/services/notify/alexa_media" {
		t.Errorf("speak não acionou a Alexa: %s %s", gHA.Method, gHA.Path)
	}
}

func TestFlowRespostaDiretaTelegramCriterio3(t *testing.T) { // critério 3
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
		t.Errorf("Error = %q; want vazio (fim direto não é erro)", out.Error)
	}
	if gHA.Method != "" || gHA.Path != "" {
		t.Errorf("Speak foi chamado: %s %s; want nenhuma requisição", gHA.Method, gHA.Path)
	}
}

func TestFlowTetoIteracoesCriterio4(t *testing.T) { // critério 4: sempre tool → teto
	fm := &fakeModel{}
	fm.add(ai.ToolRequest{Name: "get_weather", Input: map[string]any{"location": "São Paulo"}}) // passo único: sempre tool
	m := novoMotorFake(t, fm)
	ft := &fakeTools{}
	refs := registrarFakeTools(m.Genkit, ft)

	var gHA gravadaHA
	ts := novoServidorHA(t, &gHA, http.StatusOK)
	cli := novoClientHA(ts)

	out, err := rodarTurno(t, m, cli, refs, ChatInput{Text: "que horas vão bater?"})
	if err == nil {
		t.Fatalf("teto de %d iterações não disparou; out = %+v", maxIterTools, out)
	}
	if !strings.Contains(err.Error(), "limite de 8 iterações de ferramentas excedido") {
		t.Errorf("erro = %v; want contendo \"limite de 8 iterações de ferramentas excedido\"", err)
	}
	if got := fm.chamadas; got != 9 { // 8 execuções + a 9ª resposta pendente
		t.Errorf("chamadas ao modelo = %d; want 9", got)
	}
	if got := ft.total(); got != 8 {
		t.Errorf("tools executadas = %d; want 8", got)
	}
}

func TestFlowSpeakFalhaCriterio5(t *testing.T) { // critério 5: TTS falha, flow não falha
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
		t.Fatalf("flow falhou por erro de TTS: %v", err)
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

func TestFlowSessionIDNaoAlteraCriterio6(t *testing.T) { // critério 6
	fm := &fakeModel{}
	fm.add("Já liguei a luz da sala.")
	m := novoMotorFake(t, fm)
	ft := &fakeTools{}
	refs := registrarFakeTools(m.Genkit, ft)

	var gHA gravadaHA
	ts := novoServidorHA(t, &gHA, http.StatusOK)
	cli := novoClientHA(ts)

	out, err := rodarTurno(t, m, cli, refs, ChatInput{
		Text:      "ligue a luz da sala",
		SessionID: "telegram_123",
	})
	if err != nil {
		t.Fatalf("turno: erro inesperado: %v", err)
	}
	if out.Reply != "Já liguei a luz da sala." || !out.Spoken || out.Error != "" {
		t.Errorf("out = %+v; want mesmo comportamento do critério 2", out)
	}
	// Prompt segue o canal de voz mesmo com SessionID de telegram.
	if got := systemPromptFor(""); got != systemPromptVoz || !strings.Contains(fm.lastReq.Messages[0].Content[0].Text, "voz") {
		t.Errorf("system prompt = %q; want prompt de voz", fm.lastReq.Messages[0].Content[0].Text)
	}
}

// ---------- suporte: prompt por canal e ToolsUsed ordenado ----------

func TestFlowSystemPromptPorCanal(t *testing.T) { // §5: system prompt do canal no request
	casos := []struct {
		source     string
		substring  string
		wantPrompt string
	}{
		{"", "voz", systemPromptVoz},
		{"satellite", "voz", systemPromptVoz},
		{"telegram", "Telegram", systemPromptTelegram},
	}
	for _, tc := range casos {
		fm := &fakeModel{}
		fm.add("Ok.")
		m := novoMotorFake(t, fm)
		refs := registrarFakeTools(m.Genkit, &fakeTools{})

		var gHA gravadaHA
		cli := novoClientHA(novoServidorHA(t, &gHA, http.StatusOK))

		if _, err := rodarTurno(t, m, cli, refs, ChatInput{Text: "oi", Source: tc.source}); err != nil {
			t.Fatalf("source %q: turno: %v", tc.source, err)
		}
		sys := fm.lastReq.Messages[0]
		if sys.Role != ai.RoleSystem {
			t.Errorf("source %q: mensagens[0].Role = %q; want system", tc.source, sys.Role)
		}
		got := sys.Content[0].Text
		if got != tc.wantPrompt {
			t.Errorf("source %q: system prompt = %q; want o prompt do canal", tc.source, got)
		}
		if !strings.Contains(got, tc.substring) {
			t.Errorf("source %q: prompt não menciona %q", tc.source, tc.substring)
		}
	}
}

func TestFlowTextoVazioSemConteudo(t *testing.T) { // §3: texto vazio → "sem conteúdo para falar"
	fm := &fakeModel{}
	fm.add("") // resposta do modelo sem texto algum
	m := novoMotorFake(t, fm)
	refs := registrarFakeTools(m.Genkit, &fakeTools{})

	var gHA gravadaHA
	ts := novoServidorHA(t, &gHA, http.StatusOK)
	cli := novoClientHA(ts)

	out, err := rodarTurno(t, m, cli, refs, ChatInput{Text: "…"})
	if err != nil {
		t.Fatalf("flow falhou por texto vazio: %v", err)
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
	if gHA.Method != "" || gHA.Path != "" {
		t.Errorf("speak acionado sem conteúdo: %s %s; want nenhuma requisição", gHA.Method, gHA.Path)
	}
}

func TestDefineBrainVinculaCatalogo(t *testing.T) { // §6: catálogo de produção é a única fonte
	fm := &fakeModel{}
	fm.add("Já liguei.")
	m := novoMotorFake(t, fm)
	// Wiring de produção: DefineBrain vincula tools.Catalog — as duas tools
	// do §6 ficam registradas e o flow fica endereçável como "brain".
	flow := DefineBrain(m, nil)
	if flow == nil {
		t.Fatal("DefineBrain devolveu nil")
	}
	if genkit.LookupTool(m.Genkit, "get_weather") == nil {
		t.Error("get_weather não registrada pelo catálogo de produção")
	}
	if genkit.LookupTool(m.Genkit, "control_device") == nil {
		t.Error("control_device não registrada pelo catálogo de produção")
	}
	if flows := genkit.ListFlows(m.Genkit); len(flows) != 1 {
		t.Errorf("flows registrados = %d; want 1 (brain)", len(flows))
	}

	// Turno direto pelo próprio flow de produção (telegram: fim direto —
	// client nil nunca é tocado e nenhuma tool é executada).
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := flow.Run(ctx, ChatInput{Text: "oi", Source: "telegram"})
	if err != nil {
		t.Fatalf("turno: erro inesperado: %v", err)
	}
	if out.Reply != "Já liguei." || out.Spoken || out.Error != "" || len(out.ToolsUsed) != 0 {
		t.Errorf("out = %+v; want fim direto no telegram", out)
	}
}

func TestFlowToolsUsedOrdenado(t *testing.T) { // §2: ToolsUsed ordenado
	fm := &fakeModel{}
	fm.add([]ai.ToolRequest{ // duas tools no mesmo turno
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

// ---------- seams exportados para o teste integrado da API (spec 07 §5) ----------

// TestNewMotorEDefineBrainWithRefs valida os seams exportados usados pelo
// teste integrado de internal/api: NewMotor monta o Motor sobre o Genkit com
// timeout definido (sem ele o context de geração nasce expirado — falha
// fechada) e DefineBrainWithRefs passa as refs injetadas — o turno se comporta
// como o defineBrain deste package.
func TestNewMotorEDefineBrainWithRefs(t *testing.T) {
	fm := &fakeModel{}
	fm.add(ai.ToolRequest{Name: "get_weather", Input: map[string]any{"location": "São Paulo"}})
	fm.add("A máxima é de 28 graus.")
	mFake := novoMotorFake(t, fm) // genkit com o modelo fake registrado
	m := NewMotor(mFake.Genkit, mFake.Provider, mFake.ModelName, 2*time.Second)

	ft := &fakeTools{}
	refs := registrarFakeTools(m.Genkit, ft)

	var gHA gravadaHA
	cli := novoClientHA(novoServidorHA(t, &gHA, http.StatusOK))

	flow := DefineBrainWithRefs(m, cli, refs)
	out, err := flow.Run(context.Background(), ChatInput{Text: "clima em São Paulo", Source: "satellite"})
	if err != nil {
		t.Fatalf("turno: erro inesperado: %v", err)
	}
	if out.Reply != "A máxima é de 28 graus." {
		t.Errorf("Reply = %q; want o texto do script", out.Reply)
	}
	if fmt.Sprint(out.ToolsUsed) != "[get_weather]" {
		t.Errorf("ToolsUsed = %v; want [get_weather]", out.ToolsUsed)
	}
	if !out.Spoken || out.Error != "" {
		t.Errorf("Spoken/Error = %v/%q; want true/\"\" (speak no canal de voz)", out.Spoken, out.Error)
	}
	if got := ft.total(); got != 1 {
		t.Errorf("tools executadas = %d; want 1 (refs passadas ao flow)", got)
	}
	if gHA.Method != http.MethodPost || gHA.Path != "/api/services/notify/alexa_media" {
		t.Errorf("speak: %s %s; want POST /api/services/notify/alexa_media", gHA.Method, gHA.Path)
	}
}
