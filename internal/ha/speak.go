package ha

import (
	"context"
	"net/http"
)

// SpeakResult é o resultado defensivo do TTS (§4): quem decide o que fazer em
// falha de fala é o passo speak do fluxo (spec 06), não o client.
type SpeakResult struct {
	OK    bool
	Error string
}

// Speak sintetiza voz via notify.alexa_media (§4) com o contrato
// {"message": <texto>, "target": <ALEXA_MEDIA_ENTITY>} — usa target (campo
// padrão do serviço notify), NUNCA data.entity_id, que o alexa_media rejeita
// com 500 (armadilha documentada no projeto Python). Defensivo: em qualquer
// falha (rede, HTTP, timeout, deadline do ctx do turno) devolve
// SpeakResult{OK: false, Error: …} e nunca propaga erro; em sucesso devolve
// SpeakResult{OK: true}.
func (c *Client) Speak(ctx context.Context, text string) SpeakResult {
	payload := map[string]string{"message": text, "target": c.alexaMediaEntity}
	resp, err := c.do(ctx, "Speak", http.MethodPost, "/api/services/notify/alexa_media", payload)
	if err != nil {
		return SpeakResult{OK: false, Error: c.sanitize(err.Error())}
	}
	defer drainClose(resp)
	if !okStatus(resp.StatusCode) {
		return SpeakResult{OK: false, Error: c.sanitize(statusError("Speak", resp).Error())}
	}
	return SpeakResult{OK: true}
}
