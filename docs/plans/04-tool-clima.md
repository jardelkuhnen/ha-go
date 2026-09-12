# Spec 04 — Tool clima: Open-Meteo (F04) — Plano de Implementação

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implementar a tool Genkit `get_weather` em `internal/tools` — previsão do tempo em frase curta falável via Open-Meteo (geocoding + forecast), com paridade com `src/tools/weather.py` do projeto Python e design defensivo (nunca propaga erro).

**Architecture:** Package `internal/tools` com dois arquivos de produção: `weather.go` (struct `weatherAPI` com URLs de geocoding/forecast injetáveis e `http.Client` de timeout próprio fixo de 5s; método `previsao(ctx, location) string` que resolve cidade → ponto → frase; mapa WMO parcial `wmoClima`; `NewWeather(g)` que registra a tool via `genkit.DefineTool` com input/output tipados) e `catalog.go` (catálogo único de tools — `Catalog(g) []ai.ToolRef`, paridade com `ALL_TOOLS`, que a spec 06 vincula ao chatbot e a spec 05 estende com `control_device`). A lógica HTTP+frase é testável sem Genkit (httptest sobre URLs injetadas); o registro Genkit é verificado com `genkit.Init` + `genkit.LookupTool` + `RunRaw` (registry do Genkit é por instância em v1.13.1 — dois `Init` coexistem num binário de teste).

**Tech Stack:** Go 1.25, stdlib (`net/http`, `encoding/json`, `context`, `math`, `net/url`, `strconv`), `github.com/firebase/genkit/go` v1.13.1 (já em `go.mod` — nenhuma dependência nova), `testing` + `net/http/httptest` (`-race`).

**Spec:** `docs/specs/04-tool-clima.md` (fonte da verdade — §2 contrato, §3 fluxo, §4 WMO, §5 design defensivo, §6 critérios de aceite).

## Global Constraints

- Package novo: `internal/tools` (`package tools`). Module: `home-assistent-go`. Go 1.25.
- Apenas dependências já presentes em `go.mod` (genkit v1.13.1) + stdlib — **nenhuma dependência nova**.
- **Nunca propagar erro** (§5): `previsao` retorna só `string` (toda falha vira frase de fallback); a função da tool devolve `(frase, nil)` sempre — o erro do Genkit nunca é acionado por falha de rede/HTTP/timeout/dados.
- Frases de fallback exatas: `"Não encontrei essa cidade."` (geocoding sem resultados) e `"Não consegui obter o clima agora."` (falha de rede/HTTP/timeout, corpo inválido, resposta sem max/min).
- **Timeout próprio fixo de 5s** por chamada HTTP (`timeoutClima = 5 * time.Second` em `newWeatherAPI`) — independente de `LLM_TIMEOUT_S`; nada de `config.Settings` entra aqui (a spec 01 não tem vars de clima; parsing não é duplicado).
- URLs de produção: geocoding `https://geocoding-api.open-meteo.com/v1/search`, forecast `https://api.open-meteo.com/v1/forecast` — **campos injetáveis** da struct `weatherAPI` (decisão 12) para os mocks `httptest`.
- Todo client HTTP com timeout explícito (`&http.Client{Timeout: …}`) — nenhum client sem timeout.
- Zero secrets: Open-Meteo não exige credencial; nenhum `log`/`fmt.Print*` no package; erros internos nunca chegam ao caller.
- Mapa WMO exato do §4 (21 códigos) — `0 céu limpo · 1 predominantemente limpo · 2 parcialmente nublado · 3 nublado · 45 neblina · 48 neblina com geada · 51/53/55 garoa leve/moderada/intensa · 61/63/65 chuva leve/moderada/forte · 71/73/75 neve fraca/moderada/intensa · 80/81/82 pancadas de chuva (80 sem modificador; moderadas=81; fortes=82) · 95 tempestade · 96 tempestade com granizo · 99 tempestade severa com granizo`. Código `current.weather_code` tem prioridade sobre o primeiro do `daily`; código fora do mapa → frase sem condição.
- Frase (§3.3): `"Máxima de {max}, mínima de {min}"` + `", {condição}"` quando houver; temperaturas arredondadas para inteiro (`math.Round`).
- NÃO tocar em `internal/brain`, `internal/ha`, `internal/config`, `cmd/`, `docs/specs/*`, `.env*`, `.loop-ledger.md`.
- Gates em toda task, antes do commit: `gofmt -l .` vazio; `go vet ./...` limpo; `go test -race ./...` verde (módulo inteiro, incluindo config/brain/ha).
- Commits: uma por task, `feat(tools): …` + linha final `Co-Authored-By: Claude Code <noreply@anthropic.com>`. Sempre no worktree `/Users/mac01/workspace/home-assistent-go/.worktrees/spec-04` (branch `spec/04-tool-clima`); commits locais — **NUNCA push, NUNCA merge**.
- Assinaturas públicas que as specs 05/06/07 consumirão — não mudar sem justificativa:

```go
func NewWeather(g *genkit.Genkit) ai.ToolRef // registra get_weather e devolve a referência
func Catalog(g *genkit.Genkit) []ai.ToolRef  // catálogo único (spec 06 §6); spec 05 acrescenta control_device
```

---

### Task 1: Núcleo do clima — `weatherAPI` (geocoding → forecast → frase) + mapa WMO

**Files:**
- Create: `internal/tools/weather.go`
- Create: `internal/tools/weather_test.go`

**Interfaces:**
- Consumes: nada de outros packages internos (autocontido — `config.Settings` não entra aqui por decisão do §5: timeout próprio, URLs injetáveis, sem vars de clima na config da spec 01).
- Produces (Task 2 e specs 05/06/07 dependem destes nomes exatos):
  - Constantes: `fraseCidadeNaoEncontrada = "Não encontrei essa cidade."`, `fraseFalhaClima = "Não consegui obter o clima agora."`, `timeoutClima = 5 * time.Second`, `nomeWeather = "get_weather"`, `descricaoWeather = "Retorna a previsão do tempo para uma cidade, em frase curta para voz."`.
  - `var wmoClima map[int]string` (21 entradas do §4).
  - `type weatherAPI struct` com campos não-exportados `geocodingURL string`, `forecastURL string`, `http *http.Client` (injetáveis pelos testes — decisão 12); construtor `func newWeatherAPI() *weatherAPI`.
  - `func (w *weatherAPI) previsao(ctx context.Context, location string) string` — o único ponto de saída: string puro, sem erro.
  - Tipos internos: `weatherInput{Location string \`json:"location"\`}`, `geocodingResp`, `forecastResp` (+ `currentBlock`/`dailyBlock`), sentinel `errSemResultados`; helpers `geocode`, `forecast`, `fraseClima`, `codigoClima`, `arredonda`, `getJSON`, `formatFloat`, `okStatus`, `drainClose`.
  - Helpers de teste em `weather_test.go`: `type gravadaMeteo struct`, `func servidorMeteo(t *testing.T, g *gravadaMeteo, status int, corpo string) *httptest.Server`, `func apiPara(geocoding, forecast *httptest.Server) *weatherAPI`, `func servidorMorto(t *testing.T) *httptest.Server`.

- [ ] **Step 1: Escrever os testes que falham** — criar `internal/tools/weather_test.go` completo:

```go
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

func TestPrevisaoTemperaturasNegativas(t *testing.T) { // §3: arredondamento de frías
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
```

- [ ] **Step 2: Rodar e verificar que falham de compilação**

Run: `go test ./internal/tools/`
Expected: FAIL de compilação — `undefined: weatherAPI`, `undefined: previsao`, `undefined: wmoClima` (nada implementado ainda).

- [ ] **Step 3: Implementar `internal/tools/weather.go`**

```go
// Package tools implementa as tools Genkit do motor cognitivo: clima
// (Open-Meteo, spec 04) e controle de dispositivos (Home Assistant, spec 05).
// O catálogo único (Catalog, spec 06 §6) é a única fonte de verdade vinculada
// ao passo chatbot — paridade com ALL_TOOLS do projeto Python.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/firebase/genkit/go/ai"
	"github.com/firebase/genkit/go/genkit"
)

// Frases de fallback (§5): o design defensivo transforma qualquer falha em
// frase falável — a tool nunca propaga erro ao motor.
const (
	fraseCidadeNaoEncontrada = "Não encontrei essa cidade."
	fraseFalhaClima          = "Não consegui obter o clima agora."
)

// nomeWeather e descricaoWeather formam o contrato da tool (§2): nome no
// registry e descrição em pt que o LLM vê.
const (
	nomeWeather      = "get_weather"
	descricaoWeather = "Retorna a previsão do tempo para uma cidade, em frase curta para voz."
)

// timeoutClima é o timeout próprio das APIs do Open-Meteo (§5) — por chamada
// HTTP e independente de LLM_TIMEOUT_S (spec 02), que é deadline do LLM.
const timeoutClima = 5 * time.Second

// errSemResultados marca geocoding bem-sucedido sem resultados (cidade
// desconhecida) — distinto de falha de rede/HTTP (§5).
var errSemResultados = errors.New("geocoding sem resultados")

// wmoClima traduz os códigos WMO do Open-Meteo para pt-BR (mapa parcial,
// paridade com o weather.py do projeto Python, §4). Código fora do mapa →
// frase sem condição.
var wmoClima = map[int]string{
	0:  "céu limpo",
	1:  "predominantemente limpo",
	2:  "parcialmente nublado",
	3:  "nublado",
	45: "neblina",
	48: "neblina com geada",
	51: "garoa leve",
	53: "garoa moderada",
	55: "garoa intensa",
	61: "chuva leve",
	63: "chuva moderada",
	65: "chuva forte",
	71: "neve fraca",
	73: "neve moderada",
	75: "neve intensa",
	80: "pancadas de chuva",
	81: "pancadas de chuva moderadas",
	82: "pancadas de chuva fortes",
	95: "tempestade",
	96: "tempestade com granizo",
	99: "tempestade severa com granizo",
}

// weatherAPI é o client das duas APIs públicas do Open-Meteo (geocoding +
// forecast). URLs injetáveis (decisão 12) para os mocks de teste; em produção
// construa com newWeatherAPI. Nenhum secret: as APIs são públicas e sem chave.
type weatherAPI struct {
	geocodingURL string // endpoint /v1/search, sem query
	forecastURL  string // endpoint /v1/forecast, sem query
	http         *http.Client
}

// newWeatherAPI cria o client de produção com os endpoints do §3 e o timeout
// próprio de 5s (§5).
func newWeatherAPI() *weatherAPI {
	return &weatherAPI{
		geocodingURL: "https://geocoding-api.open-meteo.com/v1/search",
		forecastURL:  "https://api.open-meteo.com/v1/forecast",
		http:         &http.Client{Timeout: timeoutClima},
	}
}

// weatherInput é a entrada da tool (§2): nome da cidade.
type weatherInput struct {
	Location string `json:"location"`
}

// geocodingResp é o subconjunto consumido da resposta do /v1/search.
type geocodingResp struct {
	Results []struct {
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
	} `json:"results"`
}

// forecastResp é o subconjunto consumido da resposta do /v1/forecast.
// Current/Daily são ponteiros e os códigos WMO também — um código 0 real
// ("céu limpo") não deve ser confundido com campo ausente (§4).
type forecastResp struct {
	Current *currentBlock `json:"current"`
	Daily   *dailyBlock   `json:"daily"`
}

type currentBlock struct {
	WeatherCode *int `json:"weather_code"`
}

type dailyBlock struct {
	Max    []float64 `json:"temperature_2m_max"`
	Min    []float64 `json:"temperature_2m_min"`
	Codes  []int     `json:"weather_code"`
}

// previsao devolve a frase falável do clima para a cidade (§3). Design
// defensivo (§5): retorna só string — toda falha (rede, HTTP, timeout, dados)
// vira frase de fallback; o erro nunca chega ao motor.
func (w *weatherAPI) previsao(ctx context.Context, location string) string {
	local, err := w.geocode(ctx, location)
	if err != nil {
		if errors.Is(err, errSemResultados) {
			return fraseCidadeNaoEncontrada
		}
		return fraseFalhaClima
	}
	f, err := w.forecast(ctx, local.Latitude, local.Longitude)
	if err != nil {
		return fraseFalhaClima
	}
	if f == nil || f.Daily == nil || len(f.Daily.Max) == 0 || len(f.Daily.Min) == 0 {
		return fraseFalhaClima // sem temperaturas → fallback (§3.3)
	}
	return fraseClima(f.Daily.Max[0], f.Daily.Min[0], codigoClima(f))
}

// geolocalizacao é o ponto resolvido pelo geocoding.
type geolocalizacao struct {
	Latitude  float64
	Longitude float64
}

// geocode resolve a cidade em latitude/longitude (§3.1):
// GET /v1/search?name={location}&count=1&language=pt → results[0].
// Devolve errSemResultados quando a busca responde sem resultados; outro erro
// para rede/HTTP/timeout/corpo inválido (§5).
func (w *weatherAPI) geocode(ctx context.Context, location string) (geolocalizacao, error) {
	q := url.Values{}
	q.Set("name", location)
	q.Set("count", "1")
	q.Set("language", "pt")
	var g geocodingResp
	if err := w.getJSON(ctx, "geocoding", w.geocodingURL, q, &g); err != nil {
		return geolocalizacao{}, err
	}
	if len(g.Results) == 0 {
		return geolocalizacao{}, errSemResultados
	}
	return geolocalizacao{Latitude: g.Results[0].Latitude, Longitude: g.Results[0].Longitude}, nil
}

// forecast busca a previsão do dia para o ponto (§3.2):
// daily=temperature_2m_max,temperature_2m_min,weather_code · current=weather_code
// · forecast_days=1 · timezone=auto.
func (w *weatherAPI) forecast(ctx context.Context, lat, lon float64) (*forecastResp, error) {
	q := url.Values{}
	q.Set("latitude", formatFloat(lat))
	q.Set("longitude", formatFloat(lon))
	q.Set("daily", "temperature_2m_max,temperature_2m_min,weather_code")
	q.Set("current", "weather_code")
	q.Set("forecast_days", "1")
	q.Set("timezone", "auto")
	var f forecastResp
	if err := w.getJSON(ctx, "forecast", w.forecastURL, q, &f); err != nil {
		return nil, err
	}
	return &f, nil
}

// fraseClima monta a frase (§3.3): "Máxima de X, mínima de Y" + ", condição"
// quando o código WMO está no mapa (§4). Temperaturas arredondadas.
func fraseClima(max, min float64, code *int) string {
	s := fmt.Sprintf("Máxima de %d, mínima de %d", arredonda(max), arredonda(min))
	if code != nil {
		if cond, ok := wmoClima[*code]; ok {
			s += ", " + cond
		}
	}
	return s
}

// codigoClima escolhe o código WMO da frase (§4): o código atual
// (current.weather_code) tem prioridade; senão o primeiro código do dia;
// nil quando não há código nenhum (frase sem condição).
func codigoClima(f *forecastResp) *int {
	if f.Current != nil && f.Current.WeatherCode != nil {
		return f.Current.WeatherCode
	}
	if f.Daily != nil && len(f.Daily.Codes) > 0 {
		return &f.Daily.Codes[0]
	}
	return nil
}

// arredonda converte a temperatura para inteiro arredondado (§3).
func arredonda(t float64) int { return int(math.Round(t)) }

// formatFloat serializa a coordenada sem casas fixas — o Open-Meteo aceita o
// decimal completo.
func formatFloat(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// getJSON faz o GET e decodifica o corpo JSON em v. Em falha de rede/timeout
// o erro preserva a cadeia original (%w); em status ≠ 2xx o erro carrega o
// código. O corpo é drenado e fechado em qualquer caminho.
func (w *weatherAPI) getJSON(ctx context.Context, op, endpoint string, q url.Values, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+q.Encode(), nil)
	if err != nil {
		return fmt.Errorf("clima: %s: %w", op, err)
	}
	resp, err := w.http.Do(req)
	if err != nil {
		return fmt.Errorf("clima: %s: %w", op, err)
	}
	defer drainClose(resp)
	if !okStatus(resp.StatusCode) {
		return fmt.Errorf("clima: %s: HTTP %d", op, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

// okStatus reporta sucesso 2xx.
func okStatus(status int) bool { return status >= 200 && status <= 299 }

// drainClose drena o corpo (limitado, para reuso da conexão keep-alive) e o
// fecha.
func drainClose(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
}

// NewWeather registra a tool get_weather no Genkit (§2) e devolve a referência
// para o catálogo (spec 06 §6). A função da tool nunca devolve erro: a
// previsão defensiva (§5) transforma toda falha em frase de fallback.
func NewWeather(g *genkit.Genkit) ai.ToolRef {
	return newWeatherTool(g, newWeatherAPI())
}

// newWeatherTool define a tool sobre uma weatherAPI injetável (decisão 12) —
// seam dos testes, que registram a tool apontando para servidores httptest.
func newWeatherTool(g *genkit.Genkit, w *weatherAPI) ai.ToolRef {
	return genkit.DefineTool(g, nomeWeather, descricaoWeather,
		func(tctx *ai.ToolContext, in weatherInput) (string, error) {
			ctx := context.Background()
			if tctx != nil {
				ctx = tctx.Context // context do turno (cancelamento propaga)
			}
			return w.previsao(ctx, in.Location), nil
		})
}
```

- [ ] **Step 4: Rodar os testes e verificar que passam**

Run: `go test -race ./internal/tools/ -v`
Expected: PASS nos 13 testes (fluxo completo, cidade não encontrada, sem temperaturas, erro de rede, HTTP 500, corpo inválido, current vence daily, código do dia, desconhecido, negativas, timeout, padrões da API, mapa WMO).

- [ ] **Step 5: Gates**

Run: `gofmt -l . && go vet ./... && go test -race ./...`
Expected: gofmt sem saída; vet limpo; testes verdes (config, brain, ha e tools).

- [ ] **Step 6: Commit**

```bash
git add internal/tools/
git commit -m "feat(tools): núcleo do clima — Open-Meteo geocoding + forecast com mapa WMO

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

---

### Task 2: Registro Genkit — `NewWeather` e catálogo `Catalog`

**Files:**
- Modify: `internal/tools/weather.go` (só o seam `newWeatherTool` já criado na Task 1 — nada a modificar se a Task 1 entregou o package completo; esta task acrescenta o arquivo do catálogo e os testes de registro)
- Create: `internal/tools/catalog.go`
- Test: `internal/tools/catalog_test.go`

**Interfaces:**
- Consumes: `weatherAPI`, `newWeatherTool`, `newWeatherAPI`, `nomeWeather`, `descricaoWeather`, `fraseFalhaClima`, helpers de teste `gravadaMeteo`/`servidorMeteo`/`apiPara` (Task 1); `genkit.Init`/`genkit.LookupTool` e `ai.ToolRef`/`ai.Tool` do genkit v1.13.1.
- Produces (specs 05/06/07 dependem destes nomes exatos):
  - `func Catalog(g *genkit.Genkit) []ai.ToolRef` — catálogo único (spec 06 §6, paridade com `ALL_TOOLS`); na spec 04 devolve exatamente `[]ai.ToolRef{get_weather}`; a spec 05 acrescenta `control_device`.
  - `func NewWeather(g *genkit.Genkit) ai.ToolRef` (já existente na Task 1) — a tool `get_weather` registrada com `genkit.DefineTool`, input `{ "location": string }` e output `string`.

- [ ] **Step 1: Escrever os testes que falham** — criar `internal/tools/catalog_test.go`:

```go
package tools

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/firebase/genkit/go/genkit"
)

// TestCatalogRegistraGetWeather valida o registro Genkit (§2): o catálogo
// (spec 06 §6) expõe get_weather e o registry a resolve com o contrato — nome,
// descrição e input schema. genkit.Init é por-instância em v1.13.1 (dois Init
// coexistem num binário de teste), mas cada teste usa o próprio.
func TestCatalogRegistraGetWeather(t *testing.T) {
	g := genkit.Init(context.Background())

	cat := Catalog(g)
	if len(cat) != 1 {
		t.Fatalf("catálogo tem %d tools; want 1 (spec 04 entrega só get_weather; spec 05 acrescenta control_device)", len(cat))
	}
	if cat[0].Name() != "get_weather" {
		t.Errorf("catálogo[0].Name() = %q; want get_weather", cat[0].Name())
	}

	tool := genkit.LookupTool(g, "get_weather")
	if tool == nil {
		t.Fatal("get_weather não está registrada no registry do Genkit (§2)")
	}
	def := tool.Definition()
	if def.Name != "get_weather" || def.Description != descricaoWeather {
		t.Errorf("definição = %q / %q; want nome %q e descrição do §2", def.Name, def.Description, nomeWeather)
	}
	if _, ok := def.InputSchema["location"]; !ok {
		t.Errorf("inputSchema = %v; want propriedade location (§2)", def.InputSchema)
	}
}

// TestToolRunViaGenkit executa a tool pelo registry com input cru (o caminho
// que a spec 06 usa: Generate devolve ToolRequest → tool executa) com URLs
// injetadas — valida map JSON → input tipado → frase.
func TestToolRunViaGenkit(t *testing.T) {
	g := genkit.Init(context.Background())

	var gGeo, gPrev gravadaMeteo
	tsGeo := servidorMeteo(t, &gGeo, http.StatusOK,
		`{"results":[{"latitude":-23.5505,"longitude":-46.6333}]}`)
	tsPrev := servidorMeteo(t, &gPrev, http.StatusOK,
		`{"current":{"weather_code":80},"daily":{"temperature_2m_max":[28.4],"temperature_2m_min":[19.2],"weather_code":[80]}}`)
	w := &weatherAPI{
		geocodingURL: tsGeo.URL,
		forecastURL:  tsPrev.URL,
		http:         &http.Client{Timeout: 2 * time.Second},
	}
	_ = newWeatherTool(g, w) // registra a tool com URLs de teste neste registry

	tool := genkit.LookupTool(g, "get_weather")
	if tool == nil {
		t.Fatal("get_weather não registrada")
	}
	out, err := tool.RunRaw(context.Background(), map[string]any{"location": "São Paulo"})
	if err != nil {
		t.Fatalf("RunRaw: erro inesperado: %v", err)
	}
	got, ok := out.(string)
	if !ok {
		t.Fatalf("output = %T (%v); want string", out, out)
	}
	if want := "Máxima de 28, mínima de 19, pancadas de chuva"; got != want {
		t.Errorf("output = %q; want %q", got, want)
	}
	if gPrev.Query.Get("latitude") != "-23.5505" || gPrev.Query.Get("longitude") != "-46.6333" {
		t.Errorf("forecast: coordenadas = %v; want -23.5505/-46.6333 (input cru chegou à tool)", gPrev.Query)
	}
}
```

- [ ] **Step 2: Rodar e verificar que falham de compilação**

Run: `go test ./internal/tools/ -run 'TestCatalogRegistraGetWeather|TestToolRunViaGenkit'`
Expected: FAIL de compilação — `undefined: Catalog` (o catálogo ainda não existe).

- [ ] **Step 3: Implementar `internal/tools/catalog.go`**

```go
package tools

import (
	"github.com/firebase/genkit/go/ai"
	"github.com/firebase/genkit/go/genkit"
)

// Catalog é o catálogo único de tools do motor cognitivo (spec 06 §6) —
// paridade com ALL_TOOLS do projeto Python: única fonte de verdade vinculada
// ao passo chatbot via ai.WithTools(...). A spec 04 entrega get_weather; a
// spec 05 acrescenta control_device.
func Catalog(g *genkit.Genkit) []ai.ToolRef {
	return []ai.ToolRef{NewWeather(g)}
}
```

- [ ] **Step 4: Rodar os testes e verificar que passam**

Run: `go test -race ./internal/tools/ -v`
Expected: PASS em todos (os 13 da Task 1 + os 2 de registro).

- [ ] **Step 5: Gates**

Run: `gofmt -l . && go vet ./... && go test -race ./...`
Expected: gofmt sem saída; vet limpo; testes verdes.

- [ ] **Step 6: Commit**

```bash
git add internal/tools/
git commit -m "feat(tools): tool get_weather registrada no Genkit e catálogo Catalog

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

---

### Task 3: Gates finais da branch

**Files:**
- Modify (apenas se algum gate acusar): qualquer arquivo de `internal/tools/` ou `docs/plans/`.

**Interfaces:**
- Consumes: branch completa (Tasks 1–2).
- Produces: branch pronta para a integração `integracao/onda-3` (merge com spec 05).

- [ ] **Step 1: Gates completos no módulo inteiro**

```bash
gofmt -l .
go vet ./...
go mod tidy && git diff --exit-code go.mod go.sum
go test -race ./... -count=1
go build ./...
```

Expected: gofmt sem saída; vet limpo; `go.mod`/`go.sum` estáveis pós-tidy (nenhuma dependência nova); testes verdes; build ok.

- [ ] **Step 2: Auditoria de handoffs** — reler `internal/tools/`: (a) nenhum secret (as APIs do Open-Meteo são públicas, sem credencial); (b) nenhum `log`/`fmt.Print` no package; (c) `previsao` retorna só string — nenhum caminho devolve erro ao caller e a função da tool devolve `(frase, nil)`; (d) o único `http.Client` nasce em `newWeatherAPI` com `Timeout: 5s` (ou é injetado nos testes com timeout explícito) — nenhuma chamada HTTP fora dele; (e) nada de `internal/config` aqui (timeout próprio, §5).

- [ ] **Step 3: Commit apenas se algum gate exigiu correção**

```bash
git add -A && git commit -m "chore(tools): ajustes dos gates finais

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

(se nada mudou, nenhuma commit nesta task)