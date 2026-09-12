# Spec 07 — API do Cérebro (F08)

Fonte Python: `api.py` · Package Go: `internal/api` + `cmd/brain/main.go`

---

## 1. Objetivo

Servidor **Gin** expondo o turno conversacional, com auth por API key, contrato idêntico ao
do projeto Python e ciclo de vida completo (wiring no `main.go` + graceful shutdown).

## 2. Endpoints

### `GET /health` (sem auth)

```json
{"status": "ok"}
```

### `POST /chat` (auth `X-API-Key` — baseline item 4)

Request:

```json
{
  "text": "ligue a luz da sala",
  "metadata": {"source": "telegram", "session_id": "telegram_123"}
}
```

- `metadata` e seus campos são opcionais; `source` é normalizado (trim + lowercase) e
  default `"satellite"`; valores desconhecidos seguem o caminho de voz (regressão zero).
- Header ausente/incorreto → `401 {"error": "invalid api key"}`.

Response 200 (sempre 200 no fim do turno, mesmo com falha de TTS — paridade Python):

```json
{
  "reply": "Liguei a luz da sala.",
  "spoken": true,
  "source": "satellite",
  "metadata": {"tools_used": []},
  "error": null
}
```

- `tools_used` = nomes das tools executadas no turno, **ordenado**; `error` preenchido
  quando o passo speak falhou (`spoken=false`).
- Erro no flow (ex.: teto de tools excedido) → 500 com `{"error": …}`.

## 3. Timeline de auditoria (decisão 8 — slog estruturado)

Um evento `slog` por etapa do turno, sem conteúdo nem argumentos do usuário:

```
msg="timeline" agent="Chatbot Agent" action="Resposta gerada pelo motor cognitivo" details="provider: ollama | model: llama3.2:3b"
msg="timeline" agent="Tool Agent" action="Tool executada" details="tool: get_weather"
msg="timeline" agent="Speak Agent" action="Resposta enviada para a Alexa"
```

- `agent` ∈ `Chatbot Agent | Tool Agent | Speak Agent` (paridade de nomes com o Python).
- Falha de speak → `agent="Speak Agent"`, `action="Resposta não enviada para a Alexa"`, `details=<erro>`.
- Log de startup: `provider` e `model` ativos (spec 02).

## 4. Ciclo de vida (`cmd/brain/main.go`)

Ordem de montagem (erros de startup → log claro + exit 1):

1. `config.MustLoad()` — Settings (checagem estrita, spec 01).
2. `genkit.Init` + registro do plugin do provider (spec 02).
3. `ha.NewClient(settings)` — client único (spec 03).
4. Tools registradas com `*ha.Client` injetado (specs 04/05).
5. Flow `chat` definido com client + tools (spec 06).
6. Router Gin + middleware auth; `http.Server` em `:BRAIN_PORT` (default 8000).

Shutdown (`SIGINT`/`SIGTERM`): `http.Server.Shutdown(ctx)` → `ha.Close()` — sem perda de
turno em andamento (requests completam antes).

## 5. Teste integrado (decisão 9 — mocks ponta a ponta)

Sobe o servidor completo (auth + flow real) com: modelo Genkit fake via `DefineModel`
(script: 1ª volta tool call, 2ª volta texto), HA e Open-Meteo via `httptest`.

Cenários:

1. Sem API key / key errada → 401.
2. Turno com tool (ex.: "clima em São Paulo") → 200, `tools_used=["get_weather"]`,
   `reply` do script, `spoken=true` (HA mockado), `source="satellite"`.
3. `metadata.source="telegram"` → `spoken=false`, `error` vazio.
4. Health → 200 sem auth.
5. Corpo inválido → 400.

Determinístico: roda em `go test -race ./...` sem serviços externos.

## 6. Critérios de aceite (manuais, com `.env` real)

1. `curl -H "X-API-Key: …" -d '{"text":"clima em São Paulo"}' :8000/chat` → resposta com `tools_used=["get_weather"]` e Alexa falando.
2. Key errada → 401.
3. `POST /chat` com `"ligue a tomada da sala"` → Alexa confirma e `tools_used=["control_device"]`.
4. Ctrl-C encerra limpo (sem goroutines órfãs).