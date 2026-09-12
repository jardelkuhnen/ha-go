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
