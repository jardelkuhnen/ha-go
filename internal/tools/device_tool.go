package tools

import (
	"github.com/firebase/genkit/go/ai"
	"github.com/firebase/genkit/go/genkit"

	"home-assistent-go/internal/ha"
)

// ControlDeviceName é o nome registrado da tool (§2) — é o nome que o LLM
// invoca e o que aparece em ToolsUsed (spec 06).
const ControlDeviceName = "control_device"

// ControlDeviceInput é a entrada tipada da tool (§2). O enum de Action fica
// fixado no JSON Schema via tags jsonschema — o LLM só pode pedir
// on|off|toggle; qualquer desvio cai no fallback do núcleo.
type ControlDeviceInput struct {
	Action   string `json:"action" jsonschema:"enum=on,enum=off,enum=toggle" jsonschema_description:"Ação a executar no dispositivo: on (ligar), off (desligar) ou toggle (alternar)"`
	EntityID string `json:"entity_id" jsonschema_description:"Entity ID do dispositivo no Home Assistant, como switch.tomada_sala, light.luz_sala ou media_player.alexa_sala"`
}

// DefineControlDevice registra a tool control_device no registry do Genkit
// (§2) fechando sobre o client HA injetado (decisão 7). A saída é sempre uma
// frase falável e o retorno nunca é erro (design defensivo F05) — falha de
// validação, de rede ou do HA vira fallback dentro do próprio núcleo. O
// ToolAction devolvido implementa ai.ToolRef e entra no catálogo de tools do
// chatbot (spec 06 §6).
func DefineControlDevice(g *genkit.Genkit, cli *ha.Client) *ai.ToolAction[ControlDeviceInput, string] {
	return genkit.DefineTool(g, ControlDeviceName, "Liga, desliga ou alterna um dispositivo do Home Assistant.",
		func(tctx *ai.ToolContext, in ControlDeviceInput) (string, error) {
			// tctx carrega o context do turno (deadline do LLM propaga para o HA).
			return controlDevice(tctx, cli, in.Action, in.EntityID), nil
		})
}
