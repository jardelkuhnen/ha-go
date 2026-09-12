package tools

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/firebase/genkit/go/genkit"
)

// TestCatalogRegistraGetWeatherEControlDevice valida o catálogo completo
// (spec 06 §6): duas tools registradas, na ordem get_weather + control_device,
// ambas resolvíveis no registry e executáveis pelo caminho cru que a spec 06
// usa (Generate devolve ToolRequest → LookupTool → RunRaw). Cada teste usa o
// próprio genkit.Init (o registry é por instância em v1.13.1).
func TestCatalogRegistraGetWeatherEControlDevice(t *testing.T) {
	var g gravada
	ts := novoServidor(t, &g, http.StatusOK, `[]`)
	cli := novoClient(ts)

	ctx := context.Background()
	gk := genkit.Init(ctx)

	cat := Catalog(gk, cli)
	if len(cat) != 2 {
		t.Fatalf("catálogo tem %d tools; want 2 (spec 06 §6)", len(cat))
	}
	if cat[0].Name() != "get_weather" {
		t.Errorf("catálogo[0].Name() = %q; want get_weather", cat[0].Name())
	}
	if cat[1].Name() != ControlDeviceName {
		t.Errorf("catálogo[1].Name() = %q; want %q", cat[1].Name(), ControlDeviceName)
	}

	// get_weather: contrato da spec 04 (nome, descrição, input schema).
	tool := genkit.LookupTool(gk, "get_weather")
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

	// control_device: contrato da spec 05 e executável pelo registry.
	cd := genkit.LookupTool(gk, ControlDeviceName)
	if cd == nil {
		t.Fatalf("%s não está registrada no registry do Genkit (§2)", ControlDeviceName)
	}
	out, err := cd.RunRaw(ctx, map[string]any{"action": "on", "entity_id": "switch.tomada_sala"})
	if err != nil {
		t.Fatalf("control_device RunRaw: erro inesperado: %v", err)
	}
	if got, ok := out.(string); !ok || got != "Liguei o tomada da sala." {
		t.Errorf("control_device output = %v (%T); want \"Liguei o tomada da sala.\"", out, out)
	}
	if g.Method != http.MethodPost || g.Path != "/api/services/switch/turn_on" {
		t.Errorf("requisição: %s %s; want POST /api/services/switch/turn_on", g.Method, g.Path)
	}
}

// TestCatalogExecutaGetWeatherViaRegistry executa get_weather pelo registry
// com URLs injetadas — valida map JSON → input tipado → frase.
func TestCatalogExecutaGetWeatherViaRegistry(t *testing.T) {
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
	ctx := context.Background()
	gk := genkit.Init(ctx)
	_ = newWeatherTool(gk, w) // registra a tool com URLs de teste neste registry

	tool := genkit.LookupTool(gk, "get_weather")
	if tool == nil {
		t.Fatal("get_weather não registrada")
	}
	out, err := tool.RunRaw(ctx, map[string]any{"location": "São Paulo"})
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
