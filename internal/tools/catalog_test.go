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
// descrição e input schema. Cada teste usa seu próprio genkit.Init (o registry
// é por instância em v1.13.1; dois Init coexistem num binário de teste).
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
	props, ok := def.InputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("inputSchema = %v; want objeto com properties (§2)", def.InputSchema)
	}
	if _, ok := props["location"]; !ok {
		t.Errorf("inputSchema.properties = %v; want propriedade location (§2)", props)
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
