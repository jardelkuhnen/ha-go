package agent

import (
	"github.com/firebase/genkit/go/ai"
	aix "github.com/firebase/genkit/go/ai/exp"
	genkitx "github.com/firebase/genkit/go/genkit/exp"

	"home-assistent-go/internal/brain"
	"home-assistent-go/internal/ha"
	"home-assistent-go/internal/tools"
)

// maxTurns é o teto de voltas do loop de tools do agente (paridade com o
// maxIterTools=8 do antigo flow.go). Em sincronia com turnDeadlineMultiplier
// (internal/brain): o deadline de turno cobre maxTurns+1 gerações.
const maxTurns = 8

// DefineHomeAssistent define o agente "home_assistent" sobre o motor e o
// client HA injetados — catálogo único de tools (tools.Catalog) e prompt
// unificado estático. Sem WithSessionStore: stateless, client-managed.
// Em produção use esta. O Runner (NewRunner) envelopa o agente para a API.
func DefineHomeAssistent(m *brain.Motor, cli *ha.Client) *aix.Agent[struct{}] {
	return DefineHomeAssistentWithRefs(m, tools.Catalog(m.Genkit, cli))
}

// DefineHomeAssistentWithRefs é a variante injetável (seam de testes): mesmo
// agente com refs informadas — os testes integrados montam o catálogo com
// endpoints Open-Meteo apontados a httptest. Em produção use DefineHomeAssistent.
func DefineHomeAssistentWithRefs(m *brain.Motor, refs []ai.ToolRef) *aix.Agent[struct{}] {
	prompt := aix.InlinePrompt{
		ai.WithModelName(m.ModelName),
		ai.WithSystem(systemPrompt),
		ai.WithTools(refs...),
		ai.WithMaxTurns(maxTurns),
	}
	return genkitx.DefineAgent[struct{}](m.Genkit, "home_assistent", prompt)
}
