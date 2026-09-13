package agent

import (
	"context"
	"slices"

	aix "github.com/firebase/genkit/go/ai/exp"

	"home-assistent-go/internal/brain"
	"home-assistent-go/internal/ha"
)

// Runner adapta o agente home_assistent à interface api.TurnRunner: roda o
// turno via agent.RunText, extrai reply + tools do AgentOutput, e roteia o
// passo speak no canal de voz. Stateless — o SessionID do ChatInput é
// transportado e não consumido (sem WithSessionStore).
type Runner struct {
	motor *brain.Motor
	ag    *aix.Agent[struct{}]
	cli   *ha.Client
}

// NewRunner monta o Runner sobre o motor, o agente e o client HA injetados.
func NewRunner(m *brain.Motor, ag *aix.Agent[struct{}], cli *ha.Client) *Runner {
	return &Runner{motor: m, ag: ag, cli: cli}
}

// Run executa um turno conversacional: deadline de turno (Motor.TurnDeadline)
// → agent.RunText → mapeamento da saída → speak (canal de voz). Erro do agente
// (provider, ErrMaxTurnsExceeded) vira erro (→ 500 na API); falha de speak
// nunca derruba o turno (Spoken=false, Error=<msg> → 200).
func (r *Runner) Run(ctx context.Context, in ChatInput) (ChatOutput, error) {
	gctx, cancel := r.motor.TurnDeadline(ctx)
	defer cancel()

	out, err := r.ag.RunText(gctx, in.Text)
	if err != nil {
		// Connect/Send falhou, ou o context do turno foi cancelado (deadline).
		return ChatOutput{}, err
	}
	// ErrMaxTurnsExceeded (e falhas de provider que resolveram graciosamente):
	// a Agents API devolve (out, nil) com out.Error populado e FinishReason
	// Aborted — não (nil, err). Convertemos em erro para a API devolver 500.
	if out.Error != nil {
		return ChatOutput{}, out.Error
	}

	reply := ""
	if out.Message != nil {
		reply = out.Message.Text()
	}
	tools := extractToolNames(out.State)

	if in.Source == "telegram" {
		// Canal de texto: fim direto, speak NUNCA é chamado.
		return ChatOutput{Reply: reply, ToolsUsed: tools}, nil
	}
	spoken, speakErr := speak(ctx, r.cli, reply)
	return ChatOutput{Reply: reply, Spoken: spoken, Error: speakErr, ToolsUsed: tools}, nil
}

// extractToolNames percorre as mensagens do estado da conversa e devolve os
// nomes das tools chamadas pelo modelo, ordenados e nunca nil. O State é
// populado mesmo sem session store (client-managed). Nil-safe: State nil → [].
func extractToolNames(state *aix.SessionState[struct{}]) []string {
	used := []string{}
	if state == nil {
		return used
	}
	for _, msg := range state.Messages {
		if msg == nil {
			continue
		}
		for _, part := range msg.Content {
			if part.IsToolRequest() && part.ToolRequest != nil {
				used = append(used, part.ToolRequest.Name)
			}
		}
	}
	slices.Sort(used)
	return used
}

// speak é o passo terminal defensivo (canal de voz): TTS via client HA. Nunca
// falha o turno — texto vazio → "sem conteúdo para falar"; cli nil → erro de
// wiring; qualquer falha do HA vira Spoken=false com o erro como string.
func speak(ctx context.Context, cli *ha.Client, texto string) (bool, string) {
	if texto == "" {
		return false, "sem conteúdo para falar"
	}
	if cli == nil {
		return false, "agent: client Home Assistant ausente (wiring)"
	}
	res := cli.Speak(ctx, texto)
	return res.OK, res.Error
}
