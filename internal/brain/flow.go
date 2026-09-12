package brain

import (
	"context"
	"fmt"
	"slices"

	"github.com/firebase/genkit/go/ai"
	"github.com/firebase/genkit/go/core"
	"github.com/firebase/genkit/go/genkit"

	"home-assistent-go/internal/ha"
	"home-assistent-go/internal/tools"
)

// ChatInput é a entrada do flow "brain" (spec 06 §2). O Source já chega
// normalizado pela API (spec 07) — o flow não normaliza. O SessionID é
// carregado ao estado local do turno e NÃO consumido (§7 — reservado para a
// sessão/memória futura).
type ChatInput struct {
	Text      string // fala/texto do usuário
	Source    string // "satellite" (default) | "telegram"
	SessionID string // transportado, não consumido
}

// ChatOutput é a saída do flow "brain" (spec 06 §2). Error só carrega falha
// do passo speak — falhas de geração (incl. timeout do LLM) e o teto de tools
// encerram o flow com erro. ToolsUsed sai ordenado (§2).
type ChatOutput struct {
	Reply     string   // texto final do motor cognitivo
	Spoken    bool     // TTS acionado com sucesso
	Error     string   // erro do passo speak, se houver
	ToolsUsed []string // nomes das tools executadas (ordenado)
}

// maxIterTools é o teto de segurança do loop de tools (§3): 8 iterações de
// execução; a 9ª rodada de tool requests encerra o turno com erro explícito —
// não arrasta até o timeout do LLM. Fonte única do número na mensagem de erro.
const maxIterTools = 8

// DefineBrain define o flow "brain" (spec 06 §2) sobre o motor e o client HA
// injetados — chatbot → (executarTools → chatbot)* → speak → fim — e vincula
// o catálogo de produção (tools.Catalog, §6: única fonte de verdade). Nós do
// LangGraph viram as funções nomeadas abaixo (decisão 3): Genkit Go não tem
// grafo explícito, mas o comportamento observável é o mesmo.
func DefineBrain(m *Motor, cli *ha.Client) *core.Flow[ChatInput, ChatOutput, struct{}] {
	return defineBrain(m, cli, tools.Catalog(m.Genkit, cli))
}

// DefineBrainWithRefs é a variante injetável exportada de defineBrain
// (decisão 12): mesmo flow "brain" com um catálogo informado — seam dos
// testes integrados da API (spec 07 §5), que montam o catálogo com httptest.
// Em produção use DefineBrain.
func DefineBrainWithRefs(m *Motor, cli *ha.Client, refs []ai.ToolRef) *core.Flow[ChatInput, ChatOutput, struct{}] {
	return defineBrain(m, cli, refs)
}

// defineBrain é a variante injetável (decisão 12) — seam dos testes deste
// package, que registram tools fake no mesmo registry do Genkit.
func defineBrain(m *Motor, cli *ha.Client, refs []ai.ToolRef) *core.Flow[ChatInput, ChatOutput, struct{}] {
	return genkit.DefineFlow(m.Genkit, "brain", func(ctx context.Context, in ChatInput) (ChatOutput, error) {
		// Estado local do turno (§7): histórico acumulado sem persistência
		// (paridade: o Python não usa checkpointer) e tools executadas.
		msgs := []*ai.Message{ai.NewUserTextMessage(in.Text)}
		system := systemPromptFor(in.Source)
		used := []string{}

		var resp *ai.ModelResponse
		for {
			var err error
			resp, err = chatbot(ctx, m, system, msgs, refs)
			if err != nil {
				return ChatOutput{}, err
			}
			reqs := resp.ToolRequests()
			if len(reqs) == 0 {
				break // sem tool calls pendentes → roteamento por canal (§4)
			}
			if len(used) >= maxIterTools {
				// Teto de segurança (§3): a resposta ainda pede tools depois
				// de 8 iterações — termina o turno com erro agora.
				return ChatOutput{}, fmt.Errorf("limite de %d iterações de ferramentas excedido", maxIterTools)
			}
			toolMsg, err := executarTools(ctx, m.Genkit, resp)
			if err != nil {
				return ChatOutput{}, err
			}
			// Nomes executados acumulam em ToolsUsed (§3).
			for _, p := range reqs {
				used = append(used, p.ToolRequest.Name)
			}
			// Anexa o pedido do modelo e as respostas ao histórico e volta ao
			// chatbot (§3).
			msgs = append(msgs, resp.Message, toolMsg)
		}

		out := ChatOutput{Reply: resp.Text()}
		out.ToolsUsed = toolsOrdenadas(used)
		if in.Source == "telegram" {
			// §4.2: sem tool calls e canal de texto → fim direto, não aciona
			// a Alexa (Speak NUNCA é chamado).
			return out, nil
		}
		// §4.3: canal de voz → speak, passo terminal (ADR-0002).
		out.Spoken, out.Error = speak(ctx, cli, resp.Text())
		return out, nil
	})
}

// chatbot é o passo de geração (§3): system prompt do canal, histórico do
// turno e catálogo de tools; cada chamada recebe o deadline de LLM_TIMEOUT_S
// via Motor.GenerationContext (spec 02).
func chatbot(ctx context.Context, m *Motor, system string, msgs []*ai.Message, refs []ai.ToolRef) (*ai.ModelResponse, error) {
	gctx, cancel := m.GenerationContext(ctx)
	defer cancel()
	return genkit.Generate(gctx, m.Genkit,
		ai.WithModelName(m.ModelName),
		ai.WithSystem(system),
		ai.WithMessages(msgs...),
		ai.WithTools(refs...),
		ai.WithReturnToolRequests(true),
	)
}

// executarTools executa cada tool request pendente na resposta (§3) e devolve
// a mensagem RoleTool com os resultados, na ordem dos pedidos. Falha de
// wiring (tool alucinada não registrada, ou RunRaw com erro) encerra o turno
// com erro — as tools de produção são defensivas e não devolvem erro.
func executarTools(ctx context.Context, g *genkit.Genkit, resp *ai.ModelResponse) (*ai.Message, error) {
	toolMsg := &ai.Message{Role: ai.RoleTool}
	for _, p := range resp.ToolRequests() {
		req := p.ToolRequest
		tool := genkit.LookupTool(g, req.Name)
		if tool == nil {
			return nil, fmt.Errorf("brain: tool %q não registrada", req.Name)
		}
		// Context do turno (não o de geração): o deadline de LLM_TIMEOUT_S é
		// da geração; as tools têm timeout próprio (specs 03/04).
		out, err := tool.RunRaw(ctx, req.Input)
		if err != nil {
			return nil, fmt.Errorf("brain: tool %q falhou: %w", req.Name, err)
		}
		toolMsg.Content = append(toolMsg.Content, ai.NewToolResponsePart(&ai.ToolResponse{
			Name:   req.Name,
			Ref:    req.Ref,
			Output: out,
		}))
	}
	return toolMsg, nil
}

// speak é o passo terminal (§3, ADR-0002): TTS via client HA injetado, sempre
// acionado no canal de voz. Nunca falha o flow — qualquer falha devolve
// Spoken=false com o erro como string; texto vazio → "sem conteúdo para
// falar" (§3).
func speak(ctx context.Context, cli *ha.Client, texto string) (bool, string) {
	if texto == "" {
		return false, "sem conteúdo para falar"
	}
	if cli == nil {
		return false, "brain: client Home Assistant ausente (wiring)"
	}
	res := cli.Speak(ctx, texto)
	return res.OK, res.Error
}

// toolsOrdenadas devolve uma cópia ordenada dos nomes executados (§2:
// "ordenado"), nunca nil — o JSON da API ganha array vazio, não null.
func toolsOrdenadas(used []string) []string {
	out := make([]string, len(used))
	copy(out, used)
	slices.Sort(out)
	return out
}
