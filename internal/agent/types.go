// Package agent implementa o turno conversacional sobre a Agents API do Genkit
// (genkit/exp.DefineAgent). Substitui o loop de tools feito à mão do antigo
// internal/brain/flow.go: o framework roda o ciclo de tool-calls internamente,
// e o Runner mapeia a saída (AgentOutput) para o contrato do /chat.
//
// O agente é genérico: uma instância só (home_assistent), persona única de
// assistente, entrada = objetivo do usuário. O canal (voz/telegram) é
// decisão de entrega no Runner, não do agente.
package agent

// ChatInput é a entrada do turno (movida de internal/brain/flow.go). O Source
// chega normalizado pela API; SessionID é transportado e NÃO consumido
// (stateless — sem WithSessionStore).
type ChatInput struct {
	Text      string // fala/texto do usuário
	Source    string // "satellite" (default) | "telegram"
	SessionID string // transportado, não consumido
}

// ChatOutput é a saída do turno (movida de internal/brain/flow.go). Error só
// carrega falha do passo speak; falhas de geração (incl. teto de tools) e o
// teto de iterações encerram o turno com erro (→ 500). ToolsUsed sai ordenado.
type ChatOutput struct {
	Reply     string   // texto final do agente
	Spoken    bool     // TTS acionado com sucesso
	Error     string   // erro do passo speak, se houver
	ToolsUsed []string // nomes das tools executadas (ordenado)
}
