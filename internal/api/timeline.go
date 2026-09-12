package api

import (
	"fmt"

	"home-assistent-go/internal/brain"
)

// Agentes da timeline (§3) — paridade de nomes com o projeto Python.
const (
	agentChatbot = "Chatbot Agent"
	agentTools   = "Tool Agent"
	agentSpeak   = "Speak Agent"
)

// Actions da timeline (§3) — strings exatas do contrato.
const (
	acaoRespostaGerada     = "Resposta gerada pelo motor cognitivo"
	acaoToolExecutada      = "Tool executada"
	acaoRespostaEnviada    = "Resposta enviada para a Alexa"
	acaoRespostaNaoEnviada = "Resposta não enviada para a Alexa"
)

// emitirTimeline emite um evento slog por etapa do turno (§3, decisão 8),
// na ordem: Chatbot Agent → Tool Agent (um por tool executada) → Speak
// Agent. Nenhum atributo carrega conteúdo nem argumentos do usuário — só
// nomes de agent/tool e provider/model. Canal de texto (telegram) não emite
// evento de Speak: o passo não roda (spec 06 §4.2). Turno abortado (erro do
// flow) não passa por aqui — não há timeline parcial.
func (s *server) emitirTimeline(out brain.ChatOutput) {
	s.logger.Info("timeline",
		"agent", agentChatbot,
		"action", acaoRespostaGerada,
		"details", fmt.Sprintf("provider: %s | model: %s", s.provider, s.model),
	)
	for _, nome := range out.ToolsUsed {
		s.logger.Info("timeline",
			"agent", agentTools,
			"action", acaoToolExecutada,
			"details", "tool: "+nome,
		)
	}
	if out.Spoken {
		s.logger.Info("timeline", "agent", agentSpeak, "action", acaoRespostaEnviada)
	} else if out.Error != "" {
		s.logger.Info("timeline",
			"agent", agentSpeak,
			"action", acaoRespostaNaoEnviada,
			"details", out.Error,
		)
	}
}
