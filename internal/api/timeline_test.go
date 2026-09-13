package api

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"home-assistent-go/internal/agent"
)

// novoRouterComLog monta o router com logger capturado em buffer (handler de
// texto — formato da §3: msg="timeline" agent=… action=… details=…).
func novoRouterComLog(fr *fakeRunner) (*gin.Engine, *strings.Builder) {
	var buf strings.Builder
	r := NewRouter(Options{
		Runner:   fr,
		APIKey:   chaveTeste,
		Provider: "ollama",
		Model:    "llama3.2:3b",
		Logger:   slog.New(slog.NewTextHandler(&buf, nil)),
	})
	return r, &buf
}

// eventos devolve as linhas de slog do buffer.
func eventos(buf *strings.Builder) []string {
	s := strings.TrimRight(buf.String(), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// attr extrai o valor de um atributo no formato de texto do slog: o handler
// cita só valores que precisam ("Chatbot Agent"); valores simples saem crus
// (msg=timeline).
func attr(t *testing.T, linha, nome string) string {
	t.Helper()
	marca := nome + "="
	i := strings.Index(linha, marca)
	if i < 0 {
		t.Fatalf("linha sem %s: %q", nome, linha)
	}
	resto := linha[i+len(marca):]
	if strings.HasPrefix(resto, `"`) {
		fim := strings.Index(resto[1:], `"`)
		if fim < 0 {
			t.Fatalf("linha com %s malformado: %q", nome, linha)
		}
		return resto[1 : 1+fim]
	}
	fim := strings.IndexAny(resto, " \t")
	if fim < 0 {
		return resto
	}
	return resto[:fim]
}

// temAtributo reporta se a linha contém o atributo (com ou sem valor).
func temAtributo(linha, nome string) bool {
	return strings.Contains(linha, " "+nome+"=")
}

// ---------- §3: um evento por etapa, na ordem ----------

func TestTimelineTurnoComTool(t *testing.T) { // chatbot → tool → speak
	fr := &fakeRunner{out: agent.ChatOutput{Reply: "Hoje faz 28 graus.", Spoken: true,
		ToolsUsed: []string{"get_weather"}}}
	r, buf := novoRouterComLog(fr)
	if rec := requisicao(t, r, http.MethodPost, "/chat", chaveTeste, `{"text":"clima em São Paulo"}`); rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}
	ev := eventos(buf)
	if len(ev) != 3 {
		t.Fatalf("eventos = %d (%v); want 3 (chatbot, tool, speak)", len(ev), ev)
	}
	if got := attr(t, ev[0], "agent"); got != agentChatbot {
		t.Errorf("agent[0] = %q; want %q", got, agentChatbot)
	}
	if got := attr(t, ev[0], "action"); got != acaoRespostaGerada {
		t.Errorf("action[0] = %q; want %q", got, acaoRespostaGerada)
	}
	if got := attr(t, ev[0], "details"); got != "provider: ollama | model: llama3.2:3b" {
		t.Errorf("details[0] = %q; want provider/model ativos (§3)", got)
	}
	if got := attr(t, ev[1], "agent"); got != agentTools {
		t.Errorf("agent[1] = %q; want %q", got, agentTools)
	}
	if got := attr(t, ev[1], "action"); got != acaoToolExecutada {
		t.Errorf("action[1] = %q; want %q", got, acaoToolExecutada)
	}
	if got := attr(t, ev[1], "details"); got != "tool: get_weather" {
		t.Errorf("details[1] = %q; want tool: get_weather", got)
	}
	if got := attr(t, ev[2], "agent"); got != agentSpeak {
		t.Errorf("agent[2] = %q; want %q", got, agentSpeak)
	}
	if got := attr(t, ev[2], "action"); got != acaoRespostaEnviada {
		t.Errorf("action[2] = %q; want %q", got, acaoRespostaEnviada)
	}
	if temAtributo(ev[2], "details") {
		t.Errorf("sucesso de speak não deve ter details: %q", ev[2])
	}
	for _, ev := range ev {
		if got := attr(t, ev, "msg"); got != "timeline" {
			t.Errorf("msg = %q; want timeline", got)
		}
	}
}

func TestTimelineSpeakFalho(t *testing.T) { // falha de speak → action negativa + details
	fr := &fakeRunner{out: agent.ChatOutput{Reply: "Hoje faz 28 graus.", Spoken: false,
		Error: "ha: Speak: HTTP 500: Internal Server Error", ToolsUsed: []string{}}}
	r, buf := novoRouterComLog(fr)
	if rec := requisicao(t, r, http.MethodPost, "/chat", chaveTeste, `{"text":"clima"}`); rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 (paridade Python)", rec.Code)
	}
	ev := eventos(buf)
	if len(ev) != 2 {
		t.Fatalf("eventos = %d (%v); want 2 (chatbot, speak)", len(ev), ev)
	}
	if got := attr(t, ev[1], "agent"); got != agentSpeak {
		t.Errorf("agent[1] = %q; want %q", got, agentSpeak)
	}
	if got := attr(t, ev[1], "action"); got != acaoRespostaNaoEnviada {
		t.Errorf("action[1] = %q; want %q", got, acaoRespostaNaoEnviada)
	}
	if got := attr(t, ev[1], "details"); got != "ha: Speak: HTTP 500: Internal Server Error" {
		t.Errorf("details[1] = %q; want o erro do passo speak (§3)", got)
	}
}

func TestTimelineTelegramSemSpeak(t *testing.T) { // §3/§4.2: canal de texto não emite Speak Agent
	fr := &fakeRunner{out: agent.ChatOutput{Reply: "Markdown ok.", Spoken: false, Error: "", ToolsUsed: []string{}}}
	r, buf := novoRouterComLog(fr)
	if rec := requisicao(t, r, http.MethodPost, "/chat", chaveTeste, `{"text":"oi","metadata":{"source":"telegram"}}`); rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}
	ev := eventos(buf)
	if len(ev) != 1 {
		t.Fatalf("eventos = %d (%v); want 1 (só Chatbot Agent)", len(ev), ev)
	}
	if strings.Contains(ev[0], agentSpeak) {
		t.Errorf("telegram não deve emitir evento do Speak Agent: %q", ev[0])
	}
}

func TestTimelineVariasTools(t *testing.T) { // um evento Tool Agent por tool executada
	fr := &fakeRunner{out: agent.ChatOutput{Reply: "Feito.", Spoken: true,
		ToolsUsed: []string{"control_device", "get_weather"}}}
	r, buf := novoRouterComLog(fr)
	if rec := requisicao(t, r, http.MethodPost, "/chat", chaveTeste, `{"text":"ligue a tomada e diga o clima"}`); rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}
	ev := eventos(buf)
	if len(ev) != 4 {
		t.Fatalf("eventos = %d (%v); want 4 (chatbot, 2 tools, speak)", len(ev), ev)
	}
	if got := attr(t, ev[1], "details"); got != "tool: control_device" {
		t.Errorf("details[1] = %q; want tool: control_device", got)
	}
	if got := attr(t, ev[2], "details"); got != "tool: get_weather" {
		t.Errorf("details[2] = %q; want tool: get_weather", got)
	}
}

// ---------- §3: sem conteúdo nem argumentos do usuário ----------

func TestTimelineSemConteudoDoUsuario(t *testing.T) {
	fr := &fakeRunner{out: agent.ChatOutput{Reply: "Liguei a luz da sala.", Spoken: true,
		ToolsUsed: []string{"control_device"}}}
	r, buf := novoRouterComLog(fr)
	rec := requisicao(t, r, http.MethodPost, "/chat", chaveTeste,
		`{"text":"ligue a luz da sala","metadata":{"source":"satellite","session_id":"sat_9"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}
	log := buf.String()
	for _, proibido := range []string{
		"ligue a luz da sala", // texto do usuário
		"ligue a tomada e diga o clima",
		"Liguei a luz da sala", // reply é conteúdo do turno, não vai no log
		"session",
	} {
		if strings.Contains(log, proibido) {
			t.Errorf("timeline carrega %q (§3: sem conteúdo do usuário):\n%s", proibido, log)
		}
	}
}

func TestTimelineErroFlowSemTimeline(t *testing.T) { // turno abortado não emite timeline
	fr := &fakeRunner{err: errors.New("limite de 8 iterações de ferramentas excedido")}
	r, buf := novoRouterComLog(fr)
	rec := requisicao(t, r, http.MethodPost, "/chat", chaveTeste, `{"text":"clima"}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d; want 500", rec.Code)
	}
	for _, ev := range eventos(buf) {
		if strings.Contains(ev, "msg=timeline") || !strings.Contains(ev, "level=ERROR") {
			t.Errorf("turno abortado não deve emitir timeline: %q", ev)
		}
	}
}
