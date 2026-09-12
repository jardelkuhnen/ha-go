# Plano de Implementação — `home-assistent-go`

Re-implementação do Cérebro **home-assistent-brain** (Python/FastAPI/LangGraph) em **Go**,
com **Genkit** (orquestração dos flows), **Viper** (config `.env`) e **Gin** (HTTP).

Fonte do inventário: [`docs/FEATURES.md`](FEATURES.md) (leitura completa do projeto Python em
`/Users/mac01/workspace/home-assistent-brain`).

---

## 1. Escopo v1

| Feature | Spec | Status no escopo |
|---------|------|------------------|
| Estrutura do projeto | — | ✅ F0 |
| F01 — Configuração tipada e estrita | [`docs/specs/01-config.md`](specs/01-config.md) | ✅ |
| F02 — Motor cognitivo trocável | [`docs/specs/02-motor-cognitivo.md`](specs/02-motor-cognitivo.md) | ✅ |
| F03 — Integração Física (HA + Alexa TTS) | [`docs/specs/03-home-assistant.md`](specs/03-home-assistant.md) | ✅ |
| F04 — Tool clima (Open-Meteo) | [`docs/specs/04-tool-clima.md`](specs/04-tool-clima.md) | ✅ |
| F06 — Tool controle de dispositivos | [`docs/specs/05-tool-dispositivos.md`](specs/05-tool-dispositivos.md) | ✅ |
| F07 — Orquestração dos fluxos | [`docs/specs/06-orquestracao.md`](specs/06-orquestracao.md) | ✅ |
| F08 — API do Cérebro | [`docs/specs/07-api-cerebro.md`](specs/07-api-cerebro.md) | ✅ |
| F05 — Tool busca web (Tavily) | — | ❌ fora do escopo v1 |
| F09 — Canal Telegram | — | ❌ fora do escopo v1 |
| F10 — Satélite (STT offline) | — | ❌ fora do escopo v1 |

`ha/` (stack de infraestrutura: docker-compose, config, dados) já está copiada no repo e
**não é re-implementada** — é ambiente de execução.

---

## 2. Registro de decisões (sessão de alinhamento — 2026-09-11)

| # | Decisão | Escolha |
|---|---------|---------|
| 1 | Estrutura | Layout Go idiomático — `cmd/brain/` + `internal/{config, brain, ha, tools, api}`, module `home-assistent-go` |
| 2 | Git | `git init` imediato + `.gitignore` (.env, `secrets.yaml`, `ha/data/`, `ha/whisper-data/`, logs, dbs) + commit inicial |
| 3 | Orquestração | Flow **único imperativo** Genkit (`DefineFlow`); nós do LangGraph = funções nomeadas; loop de tools com **teto de 8 iterações** e erro explícito; `speak` como passo terminal dentro do flow |
| 4 | Motor trocável | Plugin registrado **condicionalmente** por `LLM_PROVIDER` (googlegenai / openai genérico / ollama); modelo endereçado por nome no registry (`googleai/…`, `openai/…`, `ollama/…`); nova var `LLM_MODEL` com default por provedor; **sem interface própria** (registry do Genkit subsume o ADR-0001) |
| 5 | Config estrita | Vars de app desconhecidas → **erro no startup** (proteção contra typos, paridade com `extra="forbid"`); vars do sistema ignoradas; lista de chaves conhecidas-ignoradas para convivência com o `.env` do projeto Python |
| 6 | Vars | Só o que o brain usa + `BRAIN_PORT` (default 8000); `BRAIN_URL`/`BRAIN_TIMEOUT_S` saem do código (são do cliente satélite), comentadas no `.env.example` |
| 7 | Client HA | **Único, injetado** no flow e nas tools (corrige a criação por-chamada do Python); fechado no graceful shutdown |
| 8 | Timeline | **slog estruturado**, um evento por etapa (agent/action/details), sem conteúdo nem argumentos do usuário |
| 9 | Testes | **Unitários por pacote** (config, ha, tools, flow) + **1 teste integrado** do `/chat` com mocks ponta a ponta (modelo Genkit fake via `DefineModel`, HA e Open-Meteo via `httptest`) — determinístico, roda com `go test ./...` |
| 10 | Prompt | Remover a menção a "resultados de busca" dos 2 system prompts (Tavily fora do escopo; evita tool call órfã) |
| 11 | Artefatos | 7 specs em `docs/specs/` + este plano |
| 12 | Base URLs | URLs do HA e do Open-Meteo **injetáveis** (campos do client) — requisito dos mocks de teste; paridade de comportamento mantida em produção |

---

## 3. Fases (ordem de dependência)

### F0 — Fundações ✅ primeira entrega
- `git init` + `.gitignore` + commit inicial (docs + esqueleto)
- `go.mod` (module `home-assistent-go`)
- `Makefile` — alvos: `run`, `build`, `fmt`, `vet`, `test` (`go test -race ./...`), `tidy`
- `.env.example`
- Stub `cmd/brain/main.go` (o wiring real entra na F6)

### F1 — F01 Configuração (spec 01)
- `internal/config`: `Settings` via Viper (`.env` + env vars), defaults, validação de enum/requeridos,
  parsing de timeouts, checagem estrita, singleton `sync.Once`.
- **Testes**: estrita (erro em var desconhecida de app), parsing (`LLM_TIMEOUT_S`), defaults,
  `LLM_MODEL` > `OLLAMA_MODEL` > default por provedor.

### F2 — F03 Client Home Assistant (spec 03)
- `internal/ha`: `Client` (http.Client com timeout `HA_TIMEOUT_S`, base URL e token injetáveis),
  `CallService`/`Toggle`/`TurnOn`/`TurnOff`/`GetState`, `Speak` defensivo (não propaga), `Close`.
- **Testes**: `httptest` mock do HA (sucesso, 401/500, erro de rede, timeout).

### F3 — F04 + F06 Tools (specs 04 e 05)
- `internal/tools`: `get_weather` (geocoding → forecast, mapa WMO, frase falável, defensivo) e
  `control_device` (validação de prefixo `switch|light|media_player`, aliases, confirmações faláveis),
  ambos via `genkit.DefineTool`, recebendo `*ha.Client`; URLs do Open-Meteo injetáveis.
- **Testes**: mocks `httptest` para Open-Meteo e HA (cidade não encontrada, falha de rede,
  entity_id inválido, caminhos on/off/toggle).

### F4 — F02 Motor cognitivo (spec 02)
- Registro condicional do plugin por `LLM_PROVIDER`; resolução `LLM_MODEL`;
  timeout `LLM_TIMEOUT_S` via `context.WithTimeout` em toda chamada de modelo;
  log de startup com provider/model.

### F5 — F07 Orquestração (spec 06)
- `internal/brain`: `DefineFlow("chat")` — `chatbot` → (`executarTools` → `chatbot`)* → `speak`;
  roteamento por `source` (`telegram` pula speak); `session_id` carregado, não consumido;
  system prompts por canal (v1 sem menção a busca).
- **Testes**: modelo fake scriptado (`DefineModel`: 1ª volta tool call, 2ª volta texto) —
  valida loop, teto de 8, roteamento e estado.

### F6 — F08 API do Cérebro (spec 07)
- `internal/api`: Gin — `GET /health` (sem auth), `POST /chat` com middleware `X-API-Key` (401),
  contrato idêntico (`reply/spoken/source/metadata.tools_used/error`),
  timeline slog por etapa, `BRAIN_PORT` (default 8000), graceful shutdown, wiring em `main.go`.

### F7 — Teste integrado + validação manual
- **Integrado** (`go test ./...`): servidor Gin completo + auth + modelo fake + HA/Open-Meteo mockados;
  valida ponta a ponta: 401 → flow → loop de tools → speak → contrato da resposta;
  inclui caminho `source=telegram` (pula speak).
- **Manual** (`.env` real): Ollama local → clima → dispositivo → Alexa TTS → auth inválida.

---

## 4. Riscos e notas

1. **Superfície da API Genkit Go** — prefixos de modelo (`googleai/`, `openai/`, `ollama/`) e o
   formato do loop sobre `ToolRequests` são confirmados contra a versão fixada no `go get`
   (docs: genkit.dev/go). Qualquer desvio é código de adaptação isolado em `internal/brain`.
2. **Modelo Gemini default** — mantido `gemini-3.5-flash` (paridade com o Python); se o plugin
   rejeitar, ajuste é só config (`LLM_MODEL`).
3. **Convivência de `.env`** — o `.env.example` do Go documenta as vars dos componentes fora do
   escopo (Tavily, Telegram, Whisper) como conhecidas-ignoradas, para o mesmo `.env` servir aos
   dois projetos durante a migração.
4. Sem cobertura de lint tooling em v1 além de `go vet` + `gofmt` (golangci-lint/gitleaks ficam
   para quando o repo amadurecer).

---

## 5. Baseline de segurança (carregado do projeto Python)

1. Zero secrets no código (só `.env` + `.env.example`).
2. Timeout explícito em todo cliente HTTP (HA, Open-Meteo, LLM).
3. Validação de input em toda tool (`entity_id` restrito a `switch|light|media_player`).
4. Auth `X-API-Key` no `POST /chat`.
5. HA token de longa duração com permissão mínima (documentado no `.env.example`).
6. `.gitignore` cobre `.env`, `secrets.yaml`, caches e dados de runtime.