package api

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"

	"github.com/gin-gonic/gin"
)

// headerAPIKey é o header de auth do POST /chat (§2, baseline item 4).
const headerAPIKey = "X-API-Key"

// authMiddleware valida o X-API-Key antes do handler (§2): header
// ausente/incorreto → 401 {"error": "invalid api key"}.
func (s *server) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !apiKeyValida(c.GetHeader(headerAPIKey), s.apiKey) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid api key"})
			return
		}
		c.Next()
	}
}

// apiKeyValida compara a chave recebida com a esperada em TEMPO CONSTANTE
// (baseline de segurança). A comparação rola sobre sha256 de comprimento
// fixo: além do compare constante, o custo não revela o comprimento da chave
// correta (subtle.ConstantTimeCompare direto vaza-o quando os tamanhos
// diferem). Wiring sem chave (expected "") falha fechado.
func apiKeyValida(provided, expected string) bool {
	if expected == "" {
		return false
	}
	p := sha256.Sum256([]byte(provided))
	e := sha256.Sum256([]byte(expected))
	return subtle.ConstantTimeCompare(p[:], e[:]) == 1
}
