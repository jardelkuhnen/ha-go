# Spec 03 — Integração Física: Home Assistant + Alexa TTS (F03)

Fonte Python: `src/services/ha_client.py` · Package Go: `internal/ha`

---

## 1. Objetivo

Client HTTP do Home Assistant para acionamento de serviços e a síntese de voz via
**Alexa Media Player** (`notify.alexa_media`). O `Speak` é o mecanismo de TTS do passo
terminal do fluxo (ADR-0002 preservado: TTS não é tool).

## 2. Client (`ha.Client`)

- `http.Client` com **timeout `HA_TIMEOUT_S` em toda chamada** (baseline item 2), criado
  uma vez no startup e **injetado** no flow e nas tools (decisão 7 — corrige a criação
  por-chamada do Python). Fechado no graceful shutdown (`CloseIdleConnections`).
- Headers: `Authorization: Bearer <HA_TOKEN>`, `Content-Type: application/json`.
- `baseURL`, `token`, `alexaMediaEntity` e o `http.Client` são **campos** — injetáveis
  nos testes (decisão 12).

## 3. Métodos

| Método | Endpoint HA | Comportamento |
|--------|-------------|---------------|
| `CallService(domain, service, serviceData)` | `POST /api/services/{domain}/{service}` | Corpo `{}` quando data nula; resposta JSON como `map`; status ≠ 2xx → erro |
| `Toggle(entityID)` | `homeassistant.toggle` | Delegação com `{"entity_id": …}` |
| `TurnOn(entityID)` / `TurnOff(entityID)` | `POST {domínio}/turn_on|turn_off` | Domínio extraído do `entity_id` (prefixo antes do primeiro `.`) |
| `GetState(entityID)` | `GET /api/states/{entity_id}` | Estado JSON do HA; status != 2xx → erro |
| `Speak(text)` | `POST notify/alexa_media` | **Defensivo — nunca propaga erro** |
| `Close()` | — | Libera conexões ociosas |

## 4. `Speak` — contrato do TTS

```json
{"message": "<texto>", "target": "<ALEXA_MEDIA_ENTITY>"}
```

- Usa `target` (campo padrão do serviço `notify`) — **não** `data.entity_id`, que o
  `alexa_media` rejeita com 500 (armadilha documentada no projeto Python).
- Em qualquer falha (rede, HTTP, timeout) retorna `SpeakResult{OK: false, Error: …}` —
  quem decide o que fazer é o passo `speak` do fluxo (spec 06), não o client.
- Em sucesso retorna `SpeakResult{OK: true}`.
- Sem `context` externo para cancelamento além do deadline do próprio client — TTS não
  deve ser abortado pela chamada HTTP do turno? **Deve**: `Speak` aceita `ctx` do turno e
  o deadline do fluxo vence.

## 5. Erros

- Erros retornam `*ha.Error` com status HTTP quando houver (útil para log/telemetria).
- Nenhum secret (token) aparece em mensagens de erro.

## 6. Critérios de aceite (testes unitários, `httptest`)

1. `CallService` monta URL/corpo/headers corretos; 200 com JSON → `map` devolvido.
2. `TurnOn("light.luz_sala")` chama `POST /api/services/light/turn_on`.
3. `Speak` sucesso → `{OK: true}`; 500 do HA → `{OK: false, Error}` sem panico;
   conexão recusada → `{OK: false, Error}` sem panico.
4. `GetState` 404 → erro com status 404.
5. Timeout do client respeitado (servidor que demora > `HA_TIMEOUT_S`).