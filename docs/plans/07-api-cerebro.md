# Spec 07 — API do Cérebro (F08) — Plano de Implementação

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implementar o servidor **Gin** da API do Cérebro em `internal/api` (`GET /health` sem auth; `POST /chat` com auth `X-API-Key` em tempo constante, contrato idêntico ao Python, sempre 200 no fim do turno), a timeline de auditoria `slog` (§3) emitida pelo package api, o wiring completo em `cmd/brain/main.go` (config → motor → client HA → catálogo → flow → router; shutdown grace) e o teste integrado ponta a ponta da §5 (modelo fake, HA e Open-Meteo via httptest).

**Architecture:** Package `internal/api` sobre Gin. `api.go` carrega `TurnRunner` (interface com `Run(ctx, brain.ChatInput) (brain.ChatOutput, error)` — `*core.Flow[brain.ChatInput, brain.ChatOutput, struct{}]` satisfaz), `Options` (runner, API key, provider/model p/ timeline, logger) e `NewRouter(o)`. `chat.go` carrega o handler: bind do corpo (`text` obrigatório presente; `metadata` e campos opcionais), normalização de `source` (trim+lowercase, default `"satellite"` — a API normaliza, o flow NÃO), chamada ao runner com o context da request e mapeamento de status: 401 (auth, middleware), 400 (corpo inválido), 500 (erro do flow), 200 sempre no fim do turno (mesmo com speak falho — `error` preenchido, `spoken=false`). `auth.go` carrega o middleware: `sha256` + `subtle.ConstantTimeCompare` (compare em tempo constante, baseline item 4; hash elimina vazamento de tamanho). `timeline.go` emite um evento `slog` por etapa do turno (Chatbot/Tool/Speak Agent, action strings exatas da §3) a partir do `ChatOutput` — sem conteúdo nem argumentos do usuário; o flow do brain não sabe da timeline. `cmd/brain/main.go` substitui o stub: `config.MustLoad()` → `brain.Setup` → `ha.NewClient` → `tools.Catalog(m.Genkit, cli)` → `brain.DefineBrain(m, cli)` → `api.NewRouter` → `http.Server` em `:BRAIN_PORT`; SIGINT/SIGTERM → `srv.Shutdown(ctx)` → `ha.Close()`. Testes: unitários com runner fake (`api_test.go`, `timeline_test.go`), integrado com modelo `genkit.DefineModel` + tools reais apontadas para httptest (`integration_test.go`).

**Tech Stack:** Go 1.25, stdlib (`net/http`, `log/slog`, `crypto/subtle`, `crypto/sha256`, `os/signal`) + `github.com/firebase/genkit/go` v1.13.1 + `github.com/gin-gonic/gin` v1.12.0 (DEPENDÊNCIA NOVA — única desta spec, exigida pela §1).

**Spec:** `docs/specs/07-api-cerebro.md` (fonte da verdade — §2 endpoints, §3 timeline, §4 ciclo de vida, §5 teste integrado, §6 critérios de aceite).

## Global Constraints

- Go 1.25; module `home-assistent-go`; arquivos de produção em `internal/api` (`api.go`, `auth.go`, `chat.go`, `timeline.go`) + `cmd/brain/main.go` (substitui o stub). Testes ao lado (`api_test.go`, `timeline_test.go`, `integration_test.go`). Seam mínimo em packages anteriores: `internal/tools/catalog.go` (variante com Open-Meteo injetável) e `internal/brain/flow.go`/`brain.go` (variante exportada do flow + construtor de Motor p/ testes). NÃO tocar em `internal/config`, `internal/ha`, `docs/specs/*`, `.env`/`.env.example`.
- **Única dependência nova:** `github.com/gin-gonic/gin` v1.12.0 (go.mod/go.sum). Nada mais.
- **Contrato HTTP (§2, exato):**
  - `GET /health` — SEM auth → `200 {"status": "ok"}`.
  - `POST /chat` — auth header `X-API-Key`; ausente/incorreto → `401 {"error":"invalid api key"}`. Comparação em TEMPO CONSTANTE (`subtle.ConstantTimeCompare` sobre sha256 das duas chaves — sem vazamento de comprimento/timing; baseline de segurança).
  - Request: `{"text": …, "metadata": {"source": …, "session_id": …}}` — `metadata` e campos OPCIONAIS; corpo não-JSON, JSON inválido ou sem `text` presente → `400 {"error":"invalid request"}` (spec §2: só metadata é opcional — text é obrigatório). `text` presente porém `""` → segue ao flow (paridade pydantic: `""` é str válida; o flow trata vazio no speak).
  - `source`: normalização é da API — trim + lowercase; vazio → `"satellite"`; valor desconhecido passa normalizado e o flow o roteia pelo caminho de voz (regressão zero). O flow NÃO normaliza.
  - Response do turno SEMPRE `200`, mesmo com falha de TTS: `{"reply","spoken","source","metadata":{"tools_used":[ordenado]},"error"}`. `source` = valor normalizado; `tools_used` ordenado e NUNCA null (array vazio); `error` = `null` quando não há falha, preenchido com o erro do speak quando `spoken=false`. Falha de TTS é 200 (paridade Python); ERRO do flow (ex.: teto de tools) → `500 {"error": <msg>}`.
- **Framework:** Gin (`gin.New()`, SEM o Logger default — a timeline slog é o log do turno; com `Recovery` p/ panics virarem 500 em vez de crash). `gin.SetMode(gin.ReleaseMode)`.
- **Timeline (§3, decisão 8):** emitida pelo package api APENAS no caminho 200 (fim de turno), com `slog.Info("timeline", "agent", …, "action", …, "details", …)`. Agentes/actions EXATOS: `Chatbot Agent` → `"Resposta gerada pelo motor cognitivo"` com `details="provider: <provider> | model: <model>"` (model BARE, p.ex. `llama3.2:3b`); um evento `Tool Agent` → `"Tool executada"` com `details="tool: <nome>"` por tool executada; `Speak Agent` → `"Resposta enviada para a Alexa"` no sucesso e `"Resposta não enviada para a Alexa"` com `details=<erro>` na falha. Ordem: chatbot → tools → speak. Canal telegram não emite evento de Speak (passo não roda). SEM conteúdo nem argumentos do usuário em NENHUM atributo (nem texto, nem argumentos de tool — só NOMES). Erro do flow (500) não emite timeline (turno não completou) — loga `slog.Error` sem conteúdo.
- **Wiring de produção (§4):** `DefineBrain` de produção com `tools.Catalog` (fonte única de verdade) — os seams `CatalogWithWeather`/`NewMotor`/`DefineBrainWithRefs` existem APENAS para o teste integrado (decisão 12, paridade com `defineBrain`/`newWeatherTool`).
- **`cmd/brain/main.go` (§4):** ordem `config.MustLoad()` → `brain.Setup(ctx, cfg)` (genkit.Init + plugin, spec 02) → `ha.NewClient(cfg)` (spec 03) → `tools.Catalog(m.Genkit, cli)` (specs 04/05) → `brain.DefineBrain(m, cli)` (spec 06) → `api.NewRouter(...)` → `http.Server` em `:BRAIN_PORT`. Erros de startup → log claro + exit 1 (`log.Fatalf`). `http.Server` com `ReadHeaderTimeout` (baseline de segurança, slowloris) e SEM WriteTimeout que mate turnos longos. Shutdown SIGINT/SIGTERM (`signal.NotifyContext`): `srv.Shutdown(ctx com teto)` → `ha.Close()` — requests completam antes. **NUNCA logar `Settings` com `%+v`/`%v`** (secrets em string) — logar campos individuais não-sensíveis (porta, provider, model — provider/model já saem no log do `brain.Setup`).
- **Teste integrado (§5, decisão 9):** servidor completo (`api.NewRouter` com o flow REAL) via `httptest.NewServer` (zero porta fixa); modelo fake `genkit.DefineModel` endereçado por `m.ModelName` (script: 1ª volta tool call, 2ª volta texto); client de produção `ha.NewClient` apontado para httptest (Speak); `tools.CatalogWithWeather` apontando geocoding/forecast para httptest — NADA de serviço externo. 5 cenários: (1) sem key/key errada → 401; (2) turno com tool → 200, `tools_used=["get_weather"]`, `reply` do script, `spoken=true`, `source="satellite"`; (3) `metadata.source="telegram"` → `spoken=false`, `error` vazio/null; (4) health sem auth → 200; (5) corpo inválido → 400. Verde em `go test -race ./...` determinístico.
- **`main.go` sem teste automatizado** (wiring puro, paridade com o stub anterior): validado por `go build ./...` e por pieces cobertas no teste integrado (mesma montagem router+auth+flow). Desvio consciente, sem lógica de negócio no arquivo.
- Gates antes de CADA commit: `gofmt -l .` vazio; `go vet ./...` limpo; `go test -race ./...` verde (módulo inteiro).
- Commits locais apenas — NUNCA push, NUNCA merge. Worktree: `/Users/mac01/workspace/home-assistent-go/.worktrees/spec-07`, branch `spec/07-api-cerebro`. Mensagens no padrão `feat(api): …`/`feat(brain): …`/`feat(tools): …`/`test(api): …`/`docs(plans): …` + linha final `Co-Authored-By: Claude Code <noreply@anthropic.com>`.
- Sem revisão (política do usuário): sem reviewer, sem fix loop, sem `/code-review`.

---

### Task 0: Plano

**Files:**
- Create: `docs/plans/07-api-cerebro.md` (este arquivo).

- [x] **Step 1: Escrever o plano** e commitar.

```bash
git add docs/plans/07-api-cerebro.md
git commit -m "docs(plans): plano da spec 07

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

---

### Task 1: Contrato HTTP — `internal/api` (§2)

**Files:**
- Create: `internal/api/api.go`, `internal/api/auth.go`, `internal/api/chat.go`
- Create: `internal/api/api_test.go`
- Modify: `go.mod`/`go.sum` (gin v1.12.0)

**Interfaces:**
- Consumes: `brain.ChatInput`/`brain.ChatOutput` (spec 06 — tipos, não flow), `*core.Flow[brain.ChatInput, brain.ChatOutput, struct{}].Run` (satisfaz `TurnRunner`).
- Produces (Tasks 2/6 e `main.go` consomem): `type TurnRunner interface { Run(ctx, brain.ChatInput) (brain.ChatOutput, error) }`; `type Options struct { Runner TurnRunner; APIKey string; Provider string; Model string; Logger *slog.Logger }`; `func NewRouter(o Options) *gin.Engine`.

- [ ] **Step 0:** `go get github.com/gin-gonic/gin@v1.12.0` (única dependência nova da spec).

- [ ] **Step 1: Escrever os testes que falham** — `internal/api/api_test.go` (runner fake; router direto via `router.ServeHTTP`): health sem auth 200 `{"status":"ok"}`; 401 sem header/header errado com corpo exato; header correto passa e runner recebe `ChatInput` com source normalizado (`"  Satellite "` → `"satellite"`), session_id transportado, sem metadata → `"satellite"`; source desconhecido (`"WhatsAPP"` → `"whatsapp"`) passa sem ser forçado a satellite; 200 no fim do turno com corpo exato (`error` null, `tools_used` do output); speak falho → 200 com `spoken=false` e `error` preenchido; erro do runner → 500; corpo inválido (JSON malformado, sem `text`) → 400; `tools_used` nunca null.

- [ ] **Step 2: Rodar e verificar que falham de compilação** (`undefined: NewRouter` etc).

- [ ] **Step 3: Implementar** `api.go` (package doc + TurnRunner + Options + NewRouter com ReleaseMode/Recovery/rotas), `auth.go` (middleware + `sha256`+`subtle.ConstantTimeCompare`), `chat.go` (tipos do corpo, `normalizeSource`, handler chat com mapeamento de status e resposta).

- [ ] **Step 4: Rodar e verificar passagem** (`go test -race ./internal/api/ -v`).

- [ ] **Step 5: Gates + Commit**

```bash
go get github.com/gin-gonic/gin@v1.12.0 && go mod tidy
gofmt -l . && go vet ./... && go test -race ./...
git add internal/api/ go.mod go.sum
git commit -m "feat(api): contrato HTTP do /chat — health, auth constante, normalização e status"
```

---

### Task 2: Timeline de auditoria — `timeline.go` (§3)

**Files:**
- Create: `internal/api/timeline.go`
- Create: `internal/api/timeline_test.go`

**Interfaces:**
- Consumes: `brain.ChatOutput` (ToolsUsed/Spoken/Error), provider/model de `Options`.
- Produces: eventos `msg="timeline"` na ordem chatbot → tool(s) → speak (§3), constantes dos agentes/actions.

- [ ] **Step 1: Escrever os testes que falham** — `timeline_test.go` com `slog.NewTextHandler(&buf)`: turno com tool + speak ok → 3 linhas na ordem com `agent`/`action`/`details` exatos (incl. `details="provider: ollama | model: llama3.2:3b"`); speak falho → `"Resposta não enviada para a Alexa"` + `details=<erro>`; telegram → sem linha do Speak Agent; texto do usuário nunca aparece no buffer; erro do flow → sem linha `timeline` (só log de erro).

- [ ] **Step 2: Rodar e verificar que falham** (`undefined: emitirTimeline`/constantes).

- [ ] **Step 3: Implementar** `timeline.go` — emissão no handler (chamada da Task 1 ajustada: timeline só no caminho 200) com as constantes exatas da §3.

- [ ] **Step 4: Rodar e verificar passagem.**

- [ ] **Step 5: Gates + Commit** — `feat(api): timeline de auditoria do turno — slog por etapa`.

---

### Task 3: Seam em `internal/tools` — catálogo com Open-Meteo injetável

**Files:**
- Modify: `internal/tools/catalog.go`, `internal/tools/weather.go`
- Modify: `internal/tools/catalog_test.go`

**Interfaces:**
- Produces (Task 5 consome): `func CatalogWithWeather(g *genkit.Genkit, cli *ha.Client, geocodingURL, forecastURL string) []ai.ToolRef` — get_weather apontando para os endpoints informados (httptest); `Catalog` de produção inalterado.

- [ ] **Step 1:** teste em `catalog_test.go`: `CatalogWithWeather` registra as 2 tools e o get_weather executado via registry consome os servidores httptest (frase correta, requisições chegaram).
- [ ] **Step 2:** falha de compilação (`undefined: CatalogWithWeather`).
- [ ] **Step 3:** implementar (`newWeatherAPIEm` em weather.go + `CatalogWithWeather` em catalog.go).
- [ ] **Step 4:** passagem.
- [ ] **Step 5:** Gates + Commit — `feat(tools): catálogo com Open-Meteo injetável (seam do teste integrado)`.

---

### Task 4: Seams em `internal/brain` — `NewMotor` + `DefineBrainWithRefs`

**Files:**
- Modify: `internal/brain/brain.go` (NewMotor), `internal/brain/flow.go` (DefineBrainWithRefs)
- Modify: `internal/brain/flow_test.go`

**Interfaces:**
- Produces (Task 5 consome): `func NewMotor(g *genkit.Genkit, provider, modelName string, timeout time.Duration) *Motor` — montagem à mão exportada (timeout é campo privado; sem ele o context de geração nasce expirado); `func DefineBrainWithRefs(m *Motor, cli *ha.Client, refs []ai.ToolRef) *core.Flow[ChatInput, ChatOutput, struct{}]` — variante injetável exportada de `defineBrain` (decisão 12).

- [ ] **Step 1:** testes em `flow_test.go` (mesmos helpers do package): `DefineBrainWithRefs` com refs fake comporta-se como `defineBrain` (tool call → texto, speak no canal de voz); `NewMotor` monta Motor endereçando o modelo fake.
- [ ] **Step 2:** falha de compilação.
- [ ] **Step 3:** implementar (delegações de 2 linhas com doc de seam).
- [ ] **Step 4:** passagem.
- [ ] **Step 5:** Gates + Commit — `feat(brain): NewMotor e DefineBrainWithRefs — seams exportados p/ o teste integrado`.

---

### Task 5: Teste integrado ponta a ponta (§5)

**Files:**
- Create: `internal/api/integration_test.go`

- [ ] **Step 1:** servidor completo (`api.NewRouter` com o flow REAL `brain.DefineBrainWithRefs(brain.NewMotor(genkit fake model), cli, tools.CatalogWithWeather(...))`) + 5 cenários da spec: 401 sem/erro de key; turno "clima em São Paulo" → 200 `tools_used=["get_weather"]`, `reply` do script, `spoken=true` (HA httptest recebeu notify.alexa_media), `source="satellite"`; telegram → `spoken=false`, `error` null; health → 200; corpo inválido → 400. Modelo fake local (script 2 voltas), `httptest.NewServer` (zero porta fixa), zero serviço externo, verde sob `-race`.
- [ ] **Step 2:** rodar e verificar passagem (testes primeiro já escrevem contra as seams das Tasks 3/4 — se algo falhar, corrigir antes do commit).
- [ ] **Step 3:** Gates + Commit — `test(api): teste integrado ponta a ponta — 5 cenários da spec 07`.

---

### Task 6: `cmd/brain/main.go` — ciclo de vida completo (§4)

**Files:**
- Modify: `cmd/brain/main.go` (substitui o stub)

- [ ] **Step 1:** implementar: `config.MustLoad()` → `brain.Setup` → `ha.NewClient` → `tools.Catalog(m.Genkit, cli)` → `brain.DefineBrain(m, cli)` → `api.NewRouter` (provider/model p/ timeline; logger slog text) → `http.Server{Addr: :BRAIN_PORT, ReadHeaderTimeout}`; `signal.NotifyContext(SIGINT/SIGTERM)` → `srv.Shutdown(ctx)` → `ha.Close()`; erros de startup → log claro + exit 1; NUNCA logar Settings inteiro; log de startup com porta (não-sensível).
- [ ] **Step 2:** `go build ./...` + Gates completos.
- [ ] **Step 3:** Commit — `feat(brain): wiring do main — startup completo e shutdown grace`.

---

### Task 7: Gates finais da branch

**Files:**
- Modify (apenas se algum gate acusar): qualquer arquivo da spec.

- [ ] **Step 1:** `gofmt -l .` vazio; `go vet ./...` limpo; `go test -race ./... -count=1` verde; `go build ./...`; `go mod tidy` estável.
- [ ] **Step 2:** auditoria da spec §2–§5 (corpos exatos 401/400/200/500; timeline sem conteúdo; ordem de shutdown; sem log de secrets).
- [ ] **Step 3:** commit apenas se correção foi necessária.