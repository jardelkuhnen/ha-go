package tools

import (
	"github.com/firebase/genkit/go/ai"
	"github.com/firebase/genkit/go/genkit"

	"home-assistent-go/internal/ha"
)

// Catalog é o catálogo único de tools do motor cognitivo (spec 06 §6) —
// paridade com ALL_TOOLS do projeto Python: única fonte de verdade vinculada
// ao passo chatbot via ai.WithTools(...). Registra as duas tools no registry
// de g e devolve as refs na ordem get_weather + control_device. O client HA
// entra injetado (decisão 7 da spec 05) — o control_device é consumidor dele.
func Catalog(g *genkit.Genkit, cli *ha.Client) []ai.ToolRef {
	return []ai.ToolRef{NewWeather(g), DefineControlDevice(g, cli)}
}

// CatalogWithWeather é a variante injetável exportada do catálogo (decisão
// 12) para testes integrados de outros packages (spec 07 §5): get_weather
// aponta para os endpoints Open-Meteo informados (httptest) em vez dos de
// produção; o control_device continua sobre o client HA injetado. Em
// produção use Catalog.
func CatalogWithWeather(g *genkit.Genkit, cli *ha.Client, geocodingURL, forecastURL string) []ai.ToolRef {
	return []ai.ToolRef{newWeatherTool(g, newWeatherAPIEm(geocodingURL, forecastURL)), DefineControlDevice(g, cli)}
}
