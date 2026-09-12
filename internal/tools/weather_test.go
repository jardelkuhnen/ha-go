package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// gravadaMeteo guarda o que o servidor de teste viu da última requisição.
type gravadaMeteo struct {
	Method string
	Query  url.Values // r.URL.Query()
}

// servidorMeteo cria um httptest.Server que grava a última requisição em g
// (pode ser nil) e responde com o status e corpo informados.
func servidorMeteo(t *testing.T, g *gravadaMeteo, status int, corpo string) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if g != nil {
			*g = gravadaMeteo{Method: r.Method, Query: r.URL.Query()}
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(corpo))
	}))
	t.Cleanup(ts.Close)
	return ts
}

// apiPara monta a weatherAPI apontando para os dois servidores de teste —
// valida o mecanismo de injeção de URLs (decisão 12).
func apiPara(geocoding, forecast *httptest.Server) *weatherAPI {
	return &weatherAPI{
		geocodingURL: geocoding.URL,
		forecastURL:  forecast.URL,
		http:         &http.Client{Timeout: 2 * time.Second},
	}
}

// servidorMorto devolve um "servidor" já fechado: toda conexão é recusada.
func servidorMorto(t *testing.T) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	ts.Close()
	return ts
}

func TestPrevisaoFluxoCompleto(t *testing.T) { // critério 1 + §3.1/§3.2
	var gGeo, gPrev gravadaMeteo
	tsGeo := servidorMeteo(t, &gGeo, http.StatusOK,
		`{"results":[{"latitude":-23.5505,"longitude":-46.6333}]}`)
	tsPrev := servidorMeteo(t, &gPrev, http.StatusOK,
		`{"current":{"weather_code":80},"daily":{"temperature_2m_max":[28.4],"temperature_2m_min":[19.2],"weather_code":[80]}}`)
	w := apiPara(tsGeo, tsPrev)

	got := w.previsao(context.Background(), "São Paulo")
	if want := "Máxima de 28, mínima de 19, pancadas de chuva"; got != want {
		t.Errorf("previsao = %q; want %q", got, want)
	}
	if gGeo.Method != http.MethodGet {
		t.Errorf("geocoding: método = %s; want GET", gGeo.Method)
	}
	if gGeo.Query.Get("name") != "São Paulo" || gGeo.Query.Get("count") != "1" || gGeo.Query.Get("language") != "pt" {
		t.Errorf("geocoding: query = %v; want name/count=1/language=pt (§3.1)", gGeo.Query)
	}
	if gPrev.Query.Get("latitude") != "-23.5505" || gPrev.Query.Get("longitude") != "-46.6333" {
		t.Errorf("forecast: coordenadas = %v; want -23.5505/-46.6333", gPrev.Query)
	}
	if gPrev.Query.Get("daily") != "temperature_2m_max,temperature_2m_min,weather_code" ||
		gPrev.Query.Get("current") != "weather_code" ||
		gPrev.Query.Get("forecast_days") != "1" || gPrev.Query.Get("timezone") != "auto" {
		t.Errorf("forecast: query = %v; want daily/current/forecast_days=1/timezone=auto (§3.2)", gPrev.Query)
	}
}

func TestPrevisaoCidadeNaoEncontrada(t *testing.T) { // critério 2: forecast nem é chamado
	tsGeo := servidorMeteo(t, nil, http.StatusOK, `{"results":[]}`)
	chamouForecast := false
	tsPrev := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chamouForecast = true
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(tsPrev.Close)
	w := apiPara(tsGeo, tsPrev)

	got := w.previsao(context.Background(), "Cidade Inexistente")
	if got != fraseCidadeNaoEncontrada {
		t.Errorf("previsao = %q; want %q (critério 2)", got, fraseCidadeNaoEncontrada)
	}
	if chamouForecast {
		t.Error("forecast não deve ser chamado quando o geocoding não tem resultados (critério 2)")
	}
}

func TestPrevisaoSemTemperaturas(t *testing.T) { // critério 3
	tsGeo := servidorMeteo(t, nil, http.StatusOK, `{"results":[{"latitude":1.0,"longitude":2.0}]}`)
	tsPrev := servidorMeteo(t, nil, http.StatusOK,
		`{"current":{"weather_code":3},"daily":{"weather_code":[3]}}`) // sem max/min
	w := apiPara(tsGeo, tsPrev)

	got := w.previsao(context.Background(), "São Paulo")
	if got != fraseFalhaClima {
		t.Errorf("previsao = %q; want %q (sem max/min → fallback de falha, critério 3)", got, fraseFalhaClima)
	}
}

func TestPrevisaoErroRedeNoGeocoding(t *testing.T) { // critério 4
	tsPrev := servidorMeteo(t, nil, http.StatusOK,
		`{"daily":{"temperature_2m_max":[28],"temperature_2m_min":[19]}}`)
	w := apiPara(servidorMorto(t), tsPrev)

	got := w.previsao(context.Background(), "São Paulo")
	if got != fraseFalhaClima {
		t.Errorf("previsao = %q; want %q (erro de rede → fallback de falha, NÃO \"Não encontrei essa cidade.\", critério 4)", got, fraseFalhaClima)
	}
}

func TestPrevisaoForecastHTTP500(t *testing.T) { // §5: falha HTTP → fallback
	tsGeo := servidorMeteo(t, nil, http.StatusOK, `{"results":[{"latitude":1.0,"longitude":2.0}]}`)
	tsPrev := servidorMeteo(t, nil, http.StatusInternalServerError, `{"message":"boom"}`)
	w := apiPara(tsGeo, tsPrev)

	got := w.previsao(context.Background(), "São Paulo")
	if got != fraseFalhaClima {
		t.Errorf("previsao = %q; want %q (HTTP 500 → fallback, §5)", got, fraseFalhaClima)
	}
}

func TestPrevisaoCorpoInvalido(t *testing.T) { // §5: resposta sem dados → fallback
	tsGeo := servidorMeteo(t, nil, http.StatusOK, `não é json`)
	tsPrev := servidorMeteo(t, nil, http.StatusOK, `também não`) // não deve ser chamado
	w := apiPara(tsGeo, tsPrev)

	got := w.previsao(context.Background(), "São Paulo")
	if got != fraseFalhaClima {
		t.Errorf("previsao = %q; want %q (corpo inválido → fallback, §5)", got, fraseFalhaClima)
	}
}

func TestPrevisaoCodigoCurrentVenceDaily(t *testing.T) { // critério 5a
	tsGeo := servidorMeteo(t, nil, http.StatusOK, `{"results":[{"latitude":1.0,"longitude":2.0}]}`)
	tsPrev := servidorMeteo(t, nil, http.StatusOK,
		`{"current":{"weather_code":3},"daily":{"temperature_2m_max":[28.4],"temperature_2m_min":[19.2],"weather_code":[80]}}`)
	w := apiPara(tsGeo, tsPrev)

	got := w.previsao(context.Background(), "São Paulo")
	if want := "Máxima de 28, mínima de 19, nublado"; got != want {
		t.Errorf("previsao = %q; want %q (current vence daily, critério 5)", got, want)
	}
}

func TestPrevisaoCodigoDoDiaSemCurrent(t *testing.T) { // §4: senão o primeiro código do dia
	tsGeo := servidorMeteo(t, nil, http.StatusOK, `{"results":[{"latitude":1.0,"longitude":2.0}]}`)
	tsPrev := servidorMeteo(t, nil, http.StatusOK,
		`{"daily":{"temperature_2m_max":[28.4],"temperature_2m_min":[19.2],"weather_code":[95]}}`)
	w := apiPara(tsGeo, tsPrev)

	got := w.previsao(context.Background(), "São Paulo")
	if want := "Máxima de 28, mínima de 19, tempestade"; got != want {
		t.Errorf("previsao = %q; want %q (sem current → primeiro código do dia)", got, want)
	}
}

func TestPrevisaoCodigoDesconhecidoSemCondicao(t *testing.T) { // critério 5b
	tsGeo := servidorMeteo(t, nil, http.StatusOK, `{"results":[{"latitude":1.0,"longitude":2.0}]}`)
	tsPrev := servidorMeteo(t, nil, http.StatusOK,
		`{"current":{"weather_code":42},"daily":{"temperature_2m_max":[28.4],"temperature_2m_min":[19.2],"weather_code":[80]}}`)
	w := apiPara(tsGeo, tsPrev)

	got := w.previsao(context.Background(), "São Paulo")
	if want := "Máxima de 28, mínima de 19"; got != want {
		t.Errorf("previsao = %q; want %q (código fora do mapa → sem condição, critério 5)", got, want)
	}
}

func TestPrevisaoTemperaturasNegativas(t *testing.T) { // §3.3: arredondamento
	tsGeo := servidorMeteo(t, nil, http.StatusOK, `{"results":[{"latitude":-25.0,"longitude":-50.0}]}`)
	tsPrev := servidorMeteo(t, nil, http.StatusOK,
		`{"current":{"weather_code":71},"daily":{"temperature_2m_max":[-2.6],"temperature_2m_min":[-12.4],"weather_code":[71]}}`)
	w := apiPara(tsGeo, tsPrev)

	got := w.previsao(context.Background(), "Curitiba")
	if want := "Máxima de -3, mínima de -12, neve fraca"; got != want {
		t.Errorf("previsao = %q; want %q (arredondamento, §3.3)", got, want)
	}
}

func TestPrevisaoTimeoutDoClient(t *testing.T) { // §5: timeout próprio por chamada
	tsGeo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond) // > timeout do client abaixo
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"results":[{"latitude":1.0,"longitude":2.0}]}`))
	}))
	t.Cleanup(tsGeo.Close)
	tsPrev := servidorMeteo(t, nil, http.StatusOK, `{}`)
	w := &weatherAPI{
		geocodingURL: tsGeo.URL,
		forecastURL:  tsPrev.URL,
		http:         &http.Client{Timeout: 50 * time.Millisecond},
	}

	got := w.previsao(context.Background(), "São Paulo")
	if got != fraseFalhaClima {
		t.Errorf("previsao = %q; want %q (timeout → fallback, §5)", got, fraseFalhaClima)
	}
}

func TestNewWeatherAPIPadroes(t *testing.T) { // §3 + §5: URLs de produção e timeout próprio de 5s
	w := newWeatherAPI()
	if w.geocodingURL != "https://geocoding-api.open-meteo.com/v1/search" {
		t.Errorf("geocodingURL = %q; want https://geocoding-api.open-meteo.com/v1/search (§3.1)", w.geocodingURL)
	}
	if w.forecastURL != "https://api.open-meteo.com/v1/forecast" {
		t.Errorf("forecastURL = %q; want https://api.open-meteo.com/v1/forecast (§3.2)", w.forecastURL)
	}
	if w.http == nil || w.http.Timeout != 5*time.Second {
		t.Errorf("timeout próprio = %v; want 5s (§5 — independente de LLM_TIMEOUT_S)", w.http)
	}
}

func TestWmoMapaParidade(t *testing.T) { // §4: mapa parcial, paridade exata com o Python
	casos := []struct {
		code int
		want string
	}{
		{0, "céu limpo"},
		{1, "predominantemente limpo"},
		{2, "parcialmente nublado"},
		{3, "nublado"},
		{45, "neblina"},
		{48, "neblina com geada"},
		{51, "garoa leve"},
		{53, "garoa moderada"},
		{55, "garoa intensa"},
		{61, "chuva leve"},
		{63, "chuva moderada"},
		{65, "chuva forte"},
		{71, "neve fraca"},
		{73, "neve moderada"},
		{75, "neve intensa"},
		{80, "pancadas de chuva"},
		{81, "pancadas de chuva moderadas"},
		{82, "pancadas de chuva fortes"},
		{95, "tempestade"},
		{96, "tempestade com granizo"},
		{99, "tempestade severa com granizo"},
	}
	if len(wmoClima) != len(casos) {
		t.Errorf("mapa WMO tem %d entradas; want %d (paridade exata com §4)", len(wmoClima), len(casos))
	}
	for _, tc := range casos {
		if got := wmoClima[tc.code]; got != tc.want {
			t.Errorf("wmoClima[%d] = %q; want %q", tc.code, got, tc.want)
		}
	}
}
