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

// newWeatherAPIEm é a variante injetável do client (decisão 12): endpoints
// informados em vez dos de produção — seam dos testes integrados da API
// (spec 07 §5), que apontam geocoding/forecast para httptest. Em produção
// use newWeatherAPI.
func newWeatherAPIEm(geocodingURL, forecastURL string) *weatherAPI {
	return &weatherAPI{
		geocodingURL: geocodingURL,
		forecastURL:  forecastURL,
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

// forecastResp é o subconjunto consumido da resposta do /v1/forecast. Os
// blocos e os códigos WMO são ponteiros — um código 0 real ("céu limpo") não
// deve ser confundido com campo ausente (§4).
type forecastResp struct {
	Current *currentBlock `json:"current"`
	Daily   *dailyBlock   `json:"daily"`
}

type currentBlock struct {
	WeatherCode *int `json:"weather_code"`
}

type dailyBlock struct {
	Max   []float64 `json:"temperature_2m_max"`
	Min   []float64 `json:"temperature_2m_min"`
	Codes []int     `json:"weather_code"`
}

// geolocalizacao é o ponto resolvido pelo geocoding.
type geolocalizacao struct {
	Latitude  float64
	Longitude float64
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

// forecast busca a previsão do dia para o ponto (§3.2): daily com max/min/
// weather_code, current com weather_code, forecast_days=1, timezone=auto.
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

// arredonda converte a temperatura para inteiro arredondado (§3.3).
func arredonda(t float64) int { return int(math.Round(t)) }

// formatFloat serializa a coordenada com todos os decimais — o Open-Meteo
// aceita o decimal completo.
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
				ctx = tctx.Context // context do turno: cancelamento propaga
			}
			return w.previsao(ctx, in.Location), nil
		})
}
