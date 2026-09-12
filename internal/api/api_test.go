package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"home-assistent-go/internal/brain"
)

// ---------- runner fake (o contrato do package: TurnRunner) ----------

// fakeRunner grava a entrada recebida e devolve saída/erro scriptados.
type fakeRunner struct {
	in  brain.ChatInput
	out brain.ChatOutput
	err error
}

func (f *fakeRunner) Run(_ context.Context, in brain.ChatInput) (brain.ChatOutput, error) {
	f.in = in
	return f.out, f.err
}

// ---------- helpers ----------

const chaveTeste = "chave-cerebro"

// novoRouter monta o router com o runner fake e a chave de teste.
func novoRouter(fr *fakeRunner) *gin.Engine {
	return NewRouter(Options{
		Runner:   fr,
		APIKey:   chaveTeste,
		Provider: "ollama",
		Model:    "llama3.2:3b",
	})
}

// requisição roda method/path com o corpo JSON e o header X-API-Key (key ""
// = header ausente) contra o router — sem rede, sem porta.
func requisicao(t *testing.T, r *gin.Engine, method, path, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rd *strings.Reader
	if body == "" {
		rd = strings.NewReader("")
	} else {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set(headerAPIKey, key)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// decodifica o corpo da resposta como objeto JSON genérico.
func corpo(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("corpo não é JSON: %q (%v)", rec.Body.String(), err)
	}
	return m
}

// ---------- §2: GET /health (sem auth) ----------

func TestHealthSemAuth(t *testing.T) { // critério 4: health → 200 sem auth
	r := novoRouter(&fakeRunner{})
	rec := requisicao(t, r, http.MethodGet, "/health", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}
	if got := corpo(t, rec); got["status"] != "ok" {
		t.Errorf("corpo = %v; want {\"status\": \"ok\"}", got)
	}
}

// ---------- §2: auth X-API-Key (baseline item 4) ----------

func TestChat401SemHeader(t *testing.T) { // header ausente → 401 exato
	r := novoRouter(&fakeRunner{})
	rec := requisicao(t, r, http.MethodPost, "/chat", "", `{"text":"oi"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d; want 401", rec.Code)
	}
	if got := corpo(t, rec); got["error"] != "invalid api key" {
		t.Errorf("corpo = %v; want {\"error\": \"invalid api key\"}", got)
	}
}

func TestChat401HeaderErrado(t *testing.T) { // header incorreto → 401 exato
	r := novoRouter(&fakeRunner{})
	for _, key := range []string{"errada", "", "chave-cerebro ", "CHAVE-CEREBRO"} {
		rec := requisicao(t, r, http.MethodPost, "/chat", key, `{"text":"oi"}`)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("key %q: status = %d; want 401", key, rec.Code)
		}
		if got := corpo(t, rec); got["error"] != "invalid api key" {
			t.Errorf("key %q: corpo = %v; want erro exato da §2", key, got)
		}
	}
}

func TestChatHeaderCorretoPassa(t *testing.T) { // key certa chega ao runner
	fr := &fakeRunner{out: brain.ChatOutput{Reply: "oi", Spoken: false, ToolsUsed: []string{}}}
	r := novoRouter(fr)
	rec := requisicao(t, r, http.MethodPost, "/chat", chaveTeste, `{"text":"oi"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}
	if fr.in.Text != "oi" {
		t.Errorf("runner recebeu text=%q; want %q", fr.in.Text, "oi")
	}
}

// ---------- §2: request — normalização de source é da API ----------

func TestChatSourceNormalizado(t *testing.T) { // trim + lowercase + default
	fr := &fakeRunner{out: brain.ChatOutput{Reply: "ok", ToolsUsed: []string{}}}
	r := novoRouter(fr)
	casos := []struct{ metadata, want string }{
		{`{"source":"  Satellite  ","session_id":"sat_1"}`, "satellite"},
		{`{"source":"TELEGRAM"}`, "telegram"},
		{`{}`, "satellite"},                   // metadata sem source → default
		{`{"source":"   "}`, "satellite"},     // só whitespace → default
		{`{"source":"WhatsAPP"}`, "whatsapp"}, // desconhecido passa normalizado
	}
	for _, tc := range casos {
		fr.in = brain.ChatInput{}
		body := `{"text":"oi","metadata":` + tc.metadata + `}`
		if rec := requisicao(t, r, http.MethodPost, "/chat", chaveTeste, body); rec.Code != http.StatusOK {
			t.Fatalf("metadata %s: status = %d; want 200", tc.metadata, rec.Code)
		}
		if fr.in.Source != tc.want {
			t.Errorf("metadata %s: runner recebeu source=%q; want %q (normalização é da API)", tc.metadata, fr.in.Source, tc.want)
		}
	}
}

func TestChatMetadataOpcional(t *testing.T) { // §2: metadata e campos opcionais
	fr := &fakeRunner{out: brain.ChatOutput{Reply: "ok", ToolsUsed: []string{}}}
	r := novoRouter(fr)

	// sem metadata: source default "satellite", session vazio.
	if rec := requisicao(t, r, http.MethodPost, "/chat", chaveTeste, `{"text":"oi"}`); rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}
	if fr.in.Source != sourceDefault || fr.in.SessionID != "" {
		t.Errorf("sem metadata: source=%q session=%q; want %q e vazio", fr.in.Source, fr.in.SessionID, sourceDefault)
	}

	// com metadata: session_id transporta ao flow (§7 — reservado, não consumido).
	if rec := requisicao(t, r, http.MethodPost, "/chat", chaveTeste, `{"text":"oi","metadata":{"session_id":"telegram_123"}}`); rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}
	if fr.in.SessionID != "telegram_123" {
		t.Errorf("session_id não transportou: %q", fr.in.SessionID)
	}
}

// ---------- §2: response — 200 sempre no fim do turno ----------

func TestChatRespostaExata(t *testing.T) { // critério 2: contrato §2 exato
	fr := &fakeRunner{out: brain.ChatOutput{Reply: "Liguei a luz da sala.", Spoken: true, ToolsUsed: []string{"get_weather"}}}
	r := novoRouter(fr)
	rec := requisicao(t, r, http.MethodPost, "/chat", chaveTeste, `{"text":"ligue a luz da sala"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}
	c := corpo(t, rec)
	if c["reply"] != "Liguei a luz da sala." || c["spoken"] != true || c["source"] != sourceDefault {
		t.Errorf("campos = %v; want reply/spoken/source da §2", c)
	}
	meta, ok := c["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("metadata ausente: %v", c)
	}
	if tu, ok := meta["tools_used"].([]any); !ok || len(tu) != 1 || tu[0] != "get_weather" {
		t.Errorf("tools_used = %v; want [get_weather]", meta["tools_used"])
	}
	if e, ok := c["error"]; !ok || e != nil {
		t.Errorf("error = %v (%T); want null", c["error"], c["error"])
	}
}

func TestChatSpeakFalho200(t *testing.T) { // falha de TTS → 200 com error preenchido
	fr := &fakeRunner{out: brain.ChatOutput{Reply: "A máxima é de 28 graus.", Spoken: false,
		Error: "ha: Speak: HTTP 500: Internal Server Error", ToolsUsed: []string{}}}
	r := novoRouter(fr)
	rec := requisicao(t, r, http.MethodPost, "/chat", chaveTeste, `{"text":"clima"}`)
	if rec.Code != http.StatusOK { // paridade Python: fim de turno é sempre 200
		t.Fatalf("status = %d; want 200 mesmo com speak falho", rec.Code)
	}
	c := corpo(t, rec)
	if c["spoken"] != false {
		t.Errorf("spoken = %v; want false", c["spoken"])
	}
	if c["error"] != "ha: Speak: HTTP 500: Internal Server Error" {
		t.Errorf("error = %v; want o erro do passo speak", c["error"])
	}
	if c["reply"] != "A máxima é de 28 graus." {
		t.Errorf("reply = %v; want preservado", c["reply"])
	}
}

func TestChatErroFlow500(t *testing.T) { // teto de tools → 500 {"error": …}
	fr := &fakeRunner{err: errors.New("limite de 8 iterações de ferramentas excedido")}
	r := novoRouter(fr)
	rec := requisicao(t, r, http.MethodPost, "/chat", chaveTeste, `{"text":"clima"}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d; want 500", rec.Code)
	}
	if got := corpo(t, rec); got["error"] != "limite de 8 iterações de ferramentas excedido" {
		t.Errorf("corpo = %v; want o erro do flow", got)
	}
}

func TestChatToolsUsedNuncaNull(t *testing.T) { // §2: array vazio, não null
	fr := &fakeRunner{out: brain.ChatOutput{Reply: "oi", ToolsUsed: []string{}}}
	// runner fake devolve nil para exercitar a defesa do package.
	fr.out.ToolsUsed = nil
	r := novoRouter(fr)
	rec := requisicao(t, r, http.MethodPost, "/chat", chaveTeste, `{"text":"oi"}`)
	var resp struct {
		Metadata struct {
			ToolsUsed []string `json:"tools_used"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("corpo não é JSON: %v", err)
	}
	if resp.Metadata.ToolsUsed == nil {
		t.Errorf("tools_used saiu null; want [] (§2)")
	}
}

// ---------- §2: corpo inválido → 400 ----------

func TestChatCorpoInvalido400(t *testing.T) { // critério 5
	r := novoRouter(&fakeRunner{})
	for _, body := range []string{
		`{"text":`,             // JSON malformado
		`não é json`,           // lixo
		`{"metadata":{}}`,      // text ausente (§2: só metadata é opcional)
		`{"text": null}`,       // text null
		``,                     // corpo vazio
		`{"reply":"spoofing"}`, // objeto sem text
	} {
		rec := requisicao(t, r, http.MethodPost, "/chat", chaveTeste, body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("corpo %q: status = %d; want 400", body, rec.Code)
		}
	}
}

func TestChatTextVazioPassaAoFlow(t *testing.T) { // text presente e vazio → flow decide
	fr := &fakeRunner{out: brain.ChatOutput{Reply: "", Spoken: false, Error: "sem conteúdo para falar", ToolsUsed: []string{}}}
	r := novoRouter(fr)
	rec := requisicao(t, r, http.MethodPost, "/chat", chaveTeste, `{"text":""}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 (text vazio é decisão do flow/speak)", rec.Code)
	}
	if fr.in.Text != "" {
		t.Errorf("runner recebeu text=%q; want vazio", fr.in.Text)
	}
}

// ---------- rotas/methods ----------

func TestRotasNaoDefinidas(t *testing.T) { // /chat não responde GET; /health não responde POST
	r := novoRouter(&fakeRunner{})
	if rec := requisicao(t, r, http.MethodGet, "/chat", chaveTeste, ""); rec.Code != http.StatusNotFound {
		t.Errorf("GET /chat = %d; want 404", rec.Code)
	}
	if rec := requisicao(t, r, http.MethodPost, "/health", "", ""); rec.Code != http.StatusNotFound {
		t.Errorf("POST /health = %d; want 404", rec.Code)
	}
}

// ---------- garantias do contrato (tempo constante não é observável; a
// comparação correta para chaves de comprimentos distintos é) ----------

func TestAPIKeyValidaComprimentosDistintos(t *testing.T) {
	if !apiKeyValida(chaveTeste, chaveTeste) {
		t.Error("key correta rejeitada")
	}
	if apiKeyValida("chave-cerebro-mais-longa", chaveTeste) {
		t.Error("key mais longa aceita")
	}
	if apiKeyValida("x", chaveTeste) {
		t.Error("key curta aceita")
	}
	if apiKeyValida(chaveTeste, "") {
		t.Error("wiring sem chave aceitou request (deve falhar fechado)")
	}
}
