package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/firebase/genkit/go/genkit"
)

// TestDefineControlDevice valida o registro Genkit da tool (§2): nome,
// descrição, execução com input cru e passagem pelo client HA injetado.
// genkit.Init é por-processo (spec 02) — este package de teste o chama uma
// única vez por teste, cada um em seu processo `go test` próprio.
func TestDefineControlDevice(t *testing.T) {
	var g gravada
	ts := novoServidor(t, &g, http.StatusOK, `[]`)
	cli := novoClient(ts)

	ctx := context.Background()
	gk := genkit.Init(ctx)
	tool := DefineControlDevice(gk, cli)
	if tool == nil {
		t.Fatal("DefineControlDevice devolveu nil")
	}

	ref := genkit.LookupTool(gk, ControlDeviceName)
	if ref == nil {
		t.Fatalf("tool %q não registrada no Genkit", ControlDeviceName)
	}
	if ref.Name() != ControlDeviceName {
		t.Errorf("Name() = %q; want %q", ref.Name(), ControlDeviceName)
	}
	def := ref.Definition()
	if def == nil || def.Description != "Liga, desliga ou alterna um dispositivo do Home Assistant." {
		t.Errorf("descrição = %+v; want \"Liga, desliga ou alterna um dispositivo do Home Assistant.\"", def)
	}

	got, err := ref.RunRaw(ctx, map[string]any{"action": "on", "entity_id": "switch.tomada_sala"})
	if err != nil {
		t.Fatalf("RunRaw: erro inesperado: %v", err)
	}
	if got != "Liguei o tomada da sala." {
		t.Errorf("saída = %v; want \"Liguei o tomada da sala.\"", got)
	}
	if g.Method != http.MethodPost || g.Path != "/api/services/switch/turn_on" {
		t.Errorf("requisição: %s %s; want POST /api/services/switch/turn_on", g.Method, g.Path)
	}
}

// TestDefineControlDeviceRejeitaEntityForaDaCasa cobre a defesa pela
// superfície Genkit (issue #13): entity fora da casa é rejeitada no schema,
// antes do HA — igual ao enum do action. O fallback falável da camada de
// núcleo para entity desconhecida é coberto por TestEntityDesconhecidaNaoChamaHA.
func TestDefineControlDeviceRejeitaEntityForaDaCasa(t *testing.T) {
	var g gravada
	ts := novoServidor(t, &g, http.StatusOK, `[]`)
	cli := novoClient(ts)

	ctx := context.Background()
	gk := genkit.Init(ctx)
	DefineControlDevice(gk, cli)

	ref := genkit.LookupTool(gk, ControlDeviceName)
	got, err := ref.RunRaw(ctx, map[string]any{"action": "on", "entity_id": "camera.frente"})
	if err == nil {
		t.Fatalf("entity fora da casa devia ser rejeitada no schema; saída = %v", got)
	}
	if !strings.Contains(err.Error(), "entity_id") {
		t.Errorf("erro = %v; want menção ao entity_id", err)
	}
	if g.Method != "" {
		t.Errorf("requisição feita ao HA: %s %s; want nenhuma", g.Method, g.Path)
	}
}

// TestSchemaEntityEnumTravado valida o contrato do LLM (issue #13): o schema
// do entity_id que o modelo recebe fixa o enum dos dispositivos da casa e a
// descrição lista cada um — sem isso o modelo alucina entity (p. ex. tomada
// como light.*) e a tool dispara o serviço do domínio errado. O enum e o mapa
// deviceAliases não podem divergir (uma única fonte de verdade, dois lados).
func TestSchemaEntityEnumTravado(t *testing.T) {
	ctx := context.Background()
	gk := genkit.Init(ctx)
	DefineControlDevice(gk, nil)

	ref := genkit.LookupTool(gk, ControlDeviceName)
	def := ref.Definition()
	if def == nil {
		t.Fatal("definition nil")
	}
	b, err := json.Marshal(def.InputSchema)
	if err != nil {
		t.Fatalf("marshal do schema: %v", err)
	}
	var schema struct {
		Properties map[string]struct {
			Enum        []string `json:"enum"`
			Description string   `json:"description"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(b, &schema); err != nil {
		t.Fatalf("unmarshal do schema: %v", err)
	}
	entity, ok := schema.Properties["entity_id"]
	if !ok {
		t.Fatal("schema sem campo entity_id")
	}

	queros := make([]string, 0, len(deviceAliases))
	for e := range deviceAliases {
		queros = append(queros, e)
	}
	slices.Sort(queros)
	if len(entity.Enum) != len(queros) {
		t.Fatalf("enum do entity_id = %v; want %v", entity.Enum, queros)
	}
	gots := append([]string(nil), entity.Enum...)
	slices.Sort(gots)
	for i := range queros {
		if gots[i] != queros[i] {
			t.Fatalf("enum do entity_id = %v; want %v (mapa deviceAliases)", gots, queros)
		}
	}

	for _, e := range queros {
		if !strings.Contains(entity.Description, e) {
			t.Errorf("descrição do entity_id %q não lista o dispositivo %q", entity.Description, e)
		}
	}
}
