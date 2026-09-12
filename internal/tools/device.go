// Package tools implementa as tools acionáveis pelo motor cognitivo
// (paridade com src/tools/ do projeto Python): hoje control_device (F06,
// spec 05) — ligar/desligar/alternar dispositivos do Home Assistant.
// Design defensivo (paridade F04): a tool nunca propaga erro ou panico ao
// motor — qualquer falha vira frase de fallback falável. TTS não vive aqui
// (ADR-0002): speak é passo do fluxo (spec 06).
package tools

import (
	"context"
	"log"
	"strings"

	"home-assistent-go/internal/ha"
)

// Ações aceitas pela tool control_device (§2) — estrito, sem normalização.
const (
	actionOn     = "on"
	actionOff    = "off"
	actionToggle = "toggle"
)

// fallbackControlDevice é a frase de fallback da tool (§5): acionamento não
// confirmado, entity inválido, ação inválida ou client ausente.
const fallbackControlDevice = "Não consegui acionar o dispositivo."

// deviceAliases é o mapa em memória v1 de apelidos amigáveis (§5) — paridade
// com o mapa hardcoded do Python. Entity fora do mapa → entity_id cru na
// frase (§5).
var deviceAliases = map[string]string{
	"switch.tomada_sala":      "tomada da sala",
	"switch.tomada_quarto":    "tomada do quarto",
	"light.luz_sala":          "luz da sala",
	"light.luz_quarto":        "luz do quarto",
	"media_player.alexa_sala": "Alexa da sala",
}

// validAction reporta se a ação é uma das aceitas (§2) — estrito, sem trim
// nem normalização: o LLM recebe o enum fixado no schema da tool.
func validAction(action string) bool {
	switch action {
	case actionOn, actionOff, actionToggle:
		return true
	}
	return false
}

// validEntityID valida o entity_id ANTES de qualquer chamada ao HA (§3):
// apenas os prefixos switch., light. e media_player., seguidos de sufixo não
// vazio. Baseline de segurança item 3.
func validEntityID(entityID string) bool {
	domain, suffix, ok := strings.Cut(entityID, ".")
	if !ok || suffix == "" {
		return false
	}
	switch domain {
	case "switch", "light", "media_player":
		return true
	}
	return false
}

// aliasOf devolve o apelido amigável do entity (§5); desconhecido → o
// entity_id cru.
func aliasOf(entityID string) string {
	if ap, ok := deviceAliases[entityID]; ok {
		return ap
	}
	return entityID
}

// confirmationPhrase monta a confirmação falável da ação (§5) — frases exatas
// da spec, sem correção gramatical (paridade). Ação desconhecida → fallback
// (defesa do package; controlDevice só a chama com ação válida).
func confirmationPhrase(action, alias string) string {
	switch action {
	case actionOn:
		return "Liguei o " + alias + "."
	case actionOff:
		return "Desliguei o " + alias + "."
	case actionToggle:
		return "Alternei o " + alias + "."
	}
	return fallbackControlDevice
}

// controlDevice executa a tool control_device (§4): valida ação e entity_id
// (nesta ordem, ANTES de tocar no HA — baseline item 3), aciona o client HA
// injetado e devolve confirmação falável com apelido (§5) — ou a frase de
// fallback em qualquer falha. Nunca devolve erro nem panico (design defensivo
// F05): rede/HTTP/timeout/deadline do ctx/client nulo → fallback.
func controlDevice(ctx context.Context, cli *ha.Client, action, entityID string) string {
	if !validAction(action) {
		log.Printf("tools: control_device: ação inválida %q", action)
		return fallbackControlDevice
	}
	if !validEntityID(entityID) {
		log.Printf("tools: control_device: entity_id inválido %q", entityID)
		return fallbackControlDevice
	}
	if cli == nil {
		log.Printf("tools: control_device: client HA ausente (wiring)")
		return fallbackControlDevice
	}
	var err error
	switch action {
	case actionOn:
		_, err = cli.TurnOn(ctx, entityID)
	case actionOff:
		_, err = cli.TurnOff(ctx, entityID)
	case actionToggle:
		_, err = cli.Toggle(ctx, entityID)
	}
	if err != nil {
		// O erro do client é sanitizado (nunca contém o token) e entity_id
		// não é secret — seguro para o log de diagnóstico.
		log.Printf("tools: control_device: falha do HA para %s: %v", entityID, err)
		return fallbackControlDevice
	}
	return confirmationPhrase(action, aliasOf(entityID))
}
