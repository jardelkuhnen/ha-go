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
