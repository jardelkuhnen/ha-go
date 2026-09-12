package tools

import (
	"context"
	"net/http"
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

// TestDefineControlDeviceFallbackViaTool cobre o caminho defensivo pela
// superfície Genkit: entity inválido vira frase de fallback, nunca erro.
func TestDefineControlDeviceFallbackViaTool(t *testing.T) {
	var g gravada
	ts := novoServidor(t, &g, http.StatusOK, `[]`)
	cli := novoClient(ts)

	ctx := context.Background()
	gk := genkit.Init(ctx)
	DefineControlDevice(gk, cli)

	ref := genkit.LookupTool(gk, ControlDeviceName)
	got, err := ref.RunRaw(ctx, map[string]any{"action": "on", "entity_id": "camera.frente"})
	if err != nil {
		t.Fatalf("tool nunca devolve erro: %v", err)
	}
	if got != fallbackControlDevice {
		t.Errorf("saída = %v; want fallback %q", got, fallbackControlDevice)
	}
	if g.Method != "" {
		t.Errorf("requisição feita ao HA: %s %s; want nenhuma", g.Method, g.Path)
	}
}
