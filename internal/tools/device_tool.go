package tools

import (
	"github.com/firebase/genkit/go/ai"
	"github.com/firebase/genkit/go/genkit"

	"home-assistent-go/internal/ha"
)

// ControlDeviceName é o nome registrado da tool (§2) — é o nome que o LLM
// invoca e o que aparece em ToolsUsed (spec 06).
const ControlDeviceName = "control_device"

// ControlDeviceInput é a entrada tipada da tool (§2). Os enums de Action e
// EntityID ficam fixados no JSON Schema via tags jsonschema — o LLM só pode
// pedir on|off|toggle sobre os dispositivos da casa (issue #13: entity livre
// deixava o modelo alucinar o domínio — tomada virava light.* — e a tool
// disparava /api/services/light/ em vez de /api/services/switch/). Desvios do
// schema são rejeitados pelo Genkit no limite da tool; a checagem do núcleo
// (validAction/validEntityID) repete a validação como defesa — baseline de
// segurança item 3.
type ControlDeviceInput struct {
	Action   string `json:"action" jsonschema:"enum=on,enum=off,enum=toggle" jsonschema_description:"Ação a executar no dispositivo: on (ligar), off (desligar) ou toggle (alternar)"`
	EntityID string `json:"entity_id" jsonschema:"enum=switch.indireta_cozinha,enum=switch.principal_cozinha,enum=switch.spot_cozinha,enum=media_player.alexa_sala" jsonschema_description:"Entity ID do dispositivo no Home Assistant. Dispositivos da casa: switch.indireta_cozinha (indireta cozinha), switch.principal_cozinha (principal cozinha), switch.spot_cozinha (spot cozinha) ou media_player.alexa_sala (Alexa da sala)"`
}

// DefineControlDevice registra a tool control_device no registry do Genkit
// (§2) fechando sobre o client HA injetado (decisão 7). A função registrada
// nunca devolve erro (design defensivo F05): o que chega ao núcleo fora do
// esperado — ação/entity inválidos, rede, HA, timeout — vira frase de
// fallback. Desvios do schema da tool (enums de action e entity_id) são
// rejeitados pelo Genkit antes de chegar ao núcleo. O ToolAction devolvido
// implementa ai.ToolRef e entra no catálogo de tools do chatbot (spec 06 §6).
func DefineControlDevice(g *genkit.Genkit, cli *ha.Client) *ai.ToolAction[ControlDeviceInput, string] {
	return genkit.DefineTool(g, ControlDeviceName, "Liga, desliga ou alterna um dispositivo do Home Assistant.",
		func(tctx *ai.ToolContext, in ControlDeviceInput) (string, error) {
			// tctx carrega o context do turno (deadline do LLM propaga para o HA).
			return controlDevice(tctx, cli, in.Action, in.EntityID), nil
		})
}
