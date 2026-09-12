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
	if strings.Contains(got.Error, tokenTeste) {
		t.Errorf("erro vazou o token: %q", got.Error)
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
	if strings.Contains(got.Error, tokenTeste) {
		t.Errorf("erro vazou o token: %q", got.Error)
	}
}

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
