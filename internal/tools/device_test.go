package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"home-assistent-go/internal/config"
	"home-assistent-go/internal/ha"
)

func TestValidEntityID(t *testing.T) { // §3: apenas os dispositivos da casa (mapa deviceAliases)
	casos := []struct {
		entity string
		want   bool
	}{
		{"switch.tomada_sala", true},
		{"switch.tomada_quarto", true},
		{"media_player.alexa_sala", true},
		{"light.tomada_sala", false},  // tomada alucinada no domínio light (issue #13)
		{"switch.tomada.sala", false}, // sufixo válido, mas fora do mapa da casa
		{"camera.frente", false},      // critério 4
		{"fan.quarto", false},
		{"tomada", false},  // sem ponto (critério 5)
		{"", false},        // vazio (critério 5)
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
		{"ON", false},  // sem normalização
		{"on ", false}, // sem trim
		{"ligar", false},
		{"", false},
	}
	for _, tc := range casos {
		if got := validAction(tc.acao); got != tc.want {
			t.Errorf("validAction(%q) = %v; want %v", tc.acao, got, tc.want)
		}
	}
}

func TestAliasOf(t *testing.T) { // §5: mapa em memória — só entidades validadas (§3) chegam aqui
	casos := []struct{ entity, want string }{
		{"switch.tomada_sala", "tomada da sala"},
		{"switch.tomada_quarto", "tomada do quarto"},
		{"media_player.alexa_sala", "Alexa da sala"},
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
		{"toggle", "tomada do quarto", "Alternei o tomada do quarto."},
		{"reboot", "tomada da sala", fallbackControlDevice}, // defesa: ação inválida nunca chega aqui
	}
	for _, tc := range casos {
		if got := confirmationPhrase(tc.acao, tc.apelido); got != tc.want {
			t.Errorf("confirmationPhrase(%q, %q) = %q; want %q", tc.acao, tc.apelido, got, tc.want)
		}
	}
}

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

func TestToggleEmEntityConhecida(t *testing.T) { // critério 3: toggle de entity conhecida → homeassistant/toggle + apelido
	var g gravada
	ts := novoServidor(t, &g, http.StatusOK, `[]`)
	cli := novoClient(ts)

	got := controlDevice(context.Background(), cli, "toggle", "switch.tomada_quarto")
	if got != "Alternei o tomada do quarto." {
		t.Errorf("frase = %q; want %q", got, "Alternei o tomada do quarto.")
	}
	if g.Method != http.MethodPost || g.Path != "/api/services/homeassistant/toggle" {
		t.Errorf("requisição: %s %s; want POST /api/services/homeassistant/toggle", g.Method, g.Path)
	}
	corpoComEntity(t, g.Body, "switch.tomada_quarto")
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

func TestEntityDesconhecidaNaoChamaHA(t *testing.T) { // issue #13: prefixo válido, mas fora do mapa da casa
	// O domínio do serviço vem do prefixo do entity_id: uma tomada alucinada
	// como light.tomada_sala dispararia POST /api/services/light/turn_on —
	// que o HA responde com 200 mesmo sem o entity. Sem lista fixa, o erro
	// passa silencioso. A defesa é rejeitar a entity ANTES de tocar no HA.
	var g gravada
	ts := novoServidor(t, &g, http.StatusOK, `[]`)
	cli := novoClient(ts)

	got := controlDevice(context.Background(), cli, "on", "light.tomada_sala")
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
