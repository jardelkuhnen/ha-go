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
