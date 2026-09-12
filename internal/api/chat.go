package api

import (
	"net/http"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"

	"home-assistent-go/internal/brain"
)

// sourceDefault é o canal de voz (§2): metadata ausente, source vazio ou só
// whitespace → "satellite".
const sourceDefault = "satellite"

// chatRequest é o corpo do POST /chat (§2). Text é ponteiro para distinguir
// campo AUSENTE (→ 400; §2: só metadata é opcional) de campo PRESENTE vazio
// (segue ao flow — o speak trata vazio; paridade pydantic, que aceita "").
// Campos desconhecidos do corpo são ignorados.
type chatRequest struct {
	Text     *string          `json:"text"`
	Metadata *requestMetadata `json:"metadata"`
}

// requestMetadata são os campos opcionais de metadata (§2). session_id é
// transportado ao flow e não consumido (spec 06 §7).
type requestMetadata struct {
	Source    string `json:"source"`
	SessionID string `json:"session_id"`
}

// chatResponse é a resposta do turno (§2) — SEMPRE 200 no fim do turno,
// mesmo com falha de TTS (paridade Python). Error é null quando não há
// falha; preenchido quando o passo speak falhou (spoken=false). tools_used é
// ordenado e nunca null.
type chatResponse struct {
	Reply    string           `json:"reply"`
	Spoken   bool             `json:"spoken"`
	Source   string           `json:"source"`
	Metadata responseMetadata `json:"metadata"`
	Error    *string          `json:"error"`
}

type responseMetadata struct {
	ToolsUsed []string `json:"tools_used"`
}

// chat roda o turno (§2): corpo validado → source normalizado pela API (o
// flow NÃO normaliza) → flow "brain" → resposta. Erro do flow (ex.: teto de
// tools) → 500; fim do turno → sempre 200.
func (s *server) chat(c *gin.Context) {
	var req chatRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Text == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	in := brain.ChatInput{Text: *req.Text, Source: sourceNormalizado(req.Metadata)}
	if req.Metadata != nil {
		in.SessionID = req.Metadata.SessionID
	}
	out, err := s.runner.Run(c.Request.Context(), in)
	if err != nil {
		// §3: a timeline registra etapas de turnos concluídos; falha do flow
		// é log à parte — sem conteúdo nem argumentos do usuário.
		s.logger.Error("chat: turno falhou", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// (a timeline §3 entra na Task 2, junto dos testes dela)
	c.JSON(http.StatusOK, chatResponse{
		Reply:    out.Reply,
		Spoken:   out.Spoken,
		Source:   in.Source,
		Metadata: responseMetadata{ToolsUsed: toolsUsadas(out.ToolsUsed)},
		Error:    erroOuNulo(out.Error),
	})
}

// sourceNormalizado aplica a normalização da API (§2): trim + lowercase;
// vazio → "satellite". Valores desconhecidos passam normalizados — o flow os
// roteia pelo caminho de voz (regressão zero).
func sourceNormalizado(m *requestMetadata) string {
	if m == nil {
		return sourceDefault
	}
	s := strings.ToLower(strings.TrimSpace(m.Source))
	if s == "" {
		return sourceDefault
	}
	return s
}

// toolsUsadas devolve cópia ordenada, nunca nil — o JSON da API ganha array
// vazio, não null (§2). Ordena de novo na fronteira: o contrato da resposta
// é da API, seja quem for o runner.
func toolsUsadas(used []string) []string {
	out := slices.Clone(used)
	slices.Sort(out)
	if out == nil {
		out = []string{}
	}
	return out
}

// erroOuNulo devolve nil quando não há falha de speak (§2: error: null) e o
// erro quando o passo speak falhou.
func erroOuNulo(erro string) *string {
	if erro == "" {
		return nil
	}
	return &erro
}
