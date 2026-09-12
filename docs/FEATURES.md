# Inventário de Features — `home-assistent-brain`

Documento-fonte para a re-implementação do projeto **home-assistent-brain**
(Python/FastAPI/LangGraph) em **Go**, usando **[Genkit](https://github.com/genkit-ai/genkit)**
como motor de orquestração dos fluxos (no lugar do LangGraph).

> Origem: leitura completa de `README.md`, `docs/ROADMAP.md`, `docs/adr/*`,
> `AGENT.md`, `CONTEXT.md` e do código-fonte de
> `/Users/mac01/workspace/home-assistent-brain`.

---

## 1. Arquitetura atual (resumo)

| Peça | Tecnologia (hoje) | Re-implementação em Go |
|------|-------------------|------------------------|
| **Cérebro** | FastAPI + LangGraph + LangChain | Gin para framework Servidor HTTP + **Genkit Go** (`genkit.DefineFlow`, `genkit.DefineTool`) |
| **Motor cognitivo** | Gemini / OpenAI-compatível / Ollama, atrás de factory (ADR-0001) | Plugins Genkit: `googlegenai` / OpenAI-compatível / `ollama`, seleção por config |
| **Satélite** | `faster-whisper` (CPU/int8) + `sounddevice` + `pynput` | STT 100% offline, push-to-talk (Shift) |
| **Telegram** | `python-telegram-bot` (long polling) | Bot de texto, canal isolado da Alexa |
| **Integração Física** | Home Assistant REST + Alexa Media Player (TTS) | Client REST HA + `notify.alexa_media` |
| **Tools** | Open-Meteo, Tavily, Home Assistant (`@tool`) | `genkit.DefineTool` com schemas tipados |

Fluxo em uma frase: usuário fala → Satélite transcreve offline → `POST /chat`
ao Cérebro → fluxo raciocina, aciona tools se preciso → resposta textual é
**falada** pela Alexa via Home Assistant.

---

## 2. Features (inventário)

### F01 — Configuração tipada e estrita
Fonte: `src/config.py`
- `Settings` lido de `.env`; **rejeita variáveis desconhecidas** (`extra="forbid"` — proteção contra typos); chaves case-insensitive.
- Acesso com cache (`get_settings()` singleton).
- Campos: provedor LLM e credenciais (Gemini/OpenAI/Ollama), `HA_URL`, `HA_TOKEN`, `ALEXA_MEDIA_ENTITY`, `TAVILY_API_KEY`, `BRAIN_API_KEY`, `BRAIN_URL`, `BRAIN_TIMEOUT_S` (90s), `WHISPER_MODEL`, `TELEGRAM_BOT_TOKEN`, `ALLOWED_USERS`, timeouts `HA_TIMEOUT_S` (5s) e `LLM_TIMEOUT_S` (30s).
- `ALLOWED_USERS` chega como string `"11111,22222"` e é convertido para lista de ints.
- Secrets nunca expostos (tipo Secret); paridade 1:1 com `.env.example`.

### F02 — Motor cognitivo trocável (ADR-0001)
Fonte: `src/config.py` (`get_llm`, Protocol `CognitiveMotor`)
- LLM nunca instanciado diretamente no fluxo; factory seleciona backend por `LLM_PROVIDER`: `gemini` (gemini-3.5-flash), `openai` (gpt-4o-mini, base URL opcional) ou `ollama` (default `llama3.2:3b`).
- Timeout explícito (`LLM_TIMEOUT_S`) em todo backend.
- Contrato tipado (`Protocol`) desacopla o fluxo do backend concreto — trocar motor é só configuração.
- **Em Genkit:** a abstração de *models/plugins* do Genkit subsume o ADR-0001; provedores registrados como plugins, endereçados por nome de model.

### F03 — Integração Física: Home Assistant + Alexa (TTS)
Fonte: `src/services/ha_client.py`
- Client HTTP assíncrono do HA: `base_url`, `Authorization: Bearer <token>`, timeout `HA_TIMEOUT_S` em toda chamada.
- `call_service(domain, service, service_data)` — POST `/api/services/{domain}/{service}`.
- `toggle` (`homeassistant.toggle`), `turn_on` / `turn_off` (domínio extraído do `entity_id`), `get_state` (GET `/api/states/{entity_id}`).
- `speak(text)` — `notify.alexa_media` com `target` (campo padrão do serviço notify; usar `data.entity_id` dá 500). **Defensivo: não propaga falha** — captura erro de rede/HTTP e retorna `{ok: false, error}` para o fluxo decidir.
- `close()` no desligamento do servidor.

### F04 — Tool: clima (Open-Meteo)
Fonte: `src/tools/weather.py` → `get_weather(location)`
- Geocoding (`/v1/search?name=...&count=1&language=pt`) → lat/lon; depois forecast (`/v1/forecast`, daily max/min + weather_code, `forecast_days=1`, `timezone=auto`).
- Weather codes WMO traduzidos parcialmente para linguagem falável ("céu limpo", "pancadas de chuva", "tempestade"...).
- Saída: frase curta — "Máxima de 28, mínima de 19, pancadas de chuva".
- Design defensivo: cidade não encontrada → "Não encontrei essa cidade."; falha de rede → "Não consegui obter o clima agora." — **nunca propaga exceção ao motor**.
- Timeout próprio (5s).

### F05 — Tool: busca web (Tavily)
Fonte: `src/tools/search.py` → `web_search(query, max_results=3)`
- Usa `answer` da Tavily quando presente; senão junta `content` dos resultados truncado a 300 chars.
- `max_results` validado 1..10.
- Defensivo: qualquer falha → "Não encontrei nada sobre isso."

### F06 — Tool: controle de dispositivos (Home Assistant)
Fonte: `src/tools/home.py` → `control_device(action, entity_id)`
- `action` ∈ {on, off, toggle}; delega ao client HA (`turn_on`/`turn_off`/`toggle`).
- **Validação estrita de `entity_id`** (baseline de segurança item 3): lista fechada dos dispositivos da casa — as entidades do mapa de apelidos, fixadas também no enum do schema da tool. Entity livre deixava o modelo alucinar o domínio (p. ex. tomada como `light.*`) e disparar `/api/services/light/` em vez de `/api/services/switch/` (issue #13) — inválido é rejeitado antes de chegar ao HA.
- Apelidos amigáveis em memória (`switch.tomada_sala` → "tomada da sala"...) para confirmação falável: "Liguei o tomada da sala." / "Desliguei o ..." / "Alternei o ...".
- Defensivo: falha → "Não consegui acionar o dispositivo."
- Não faz TTS (TTS é nó terminal do fluxo — ADR-0002).

### F07 — Orquestração dos fluxos (Cérebro)
Fonte: `src/graph/{state,nodes,workflow,prompt}.py`
- Estado do fluxo: `messages` (histórico acumulado), `spoken`, `error`, `source`, `session_id` (aceito e carregado, não consumido — reservado para memória/sessão futura).
- Topologia: `chatbot → (tools → chatbot)* → speak → END`.
  - `chatbot`: invoca motor cognitivo com tools vinculadas + system prompt.
  - Roteamento condicional pós-chatbot: há tool calls pendentes → `tools` (loop); sem tool calls + `source=="telegram"` → END direto (**pula a Alexa**); sem tool calls + canal de voz → `speak`.
- **TTS como nó terminal obrigatório (ADR-0002)** — não é tool que o LLM "pode esquecer" de chamar: toda resposta de voz é sempre falada.
- **System prompt por canal**: voz (`SYSTEM_PROMPT`) — texto plano, sem Markdown/símbolos/JSON, frases curtas, confirmação de automação em uma frase; Telegram (`SYSTEM_PROMPT_TELEGRAM`) — Markdown permitido, respostas mais longas, direto.
- `content_to_text`: desempacota `content` do LLM que vem como lista de blocos (Gemini devolve `[{"type": "text", "text": ...}]` — serializar direto vazaria assinaturas do Google na fala).
- Catálogo único de tools (`ALL_TOOLS`) exportado para o fluxo.

### F08 — API do Cérebro (HTTP)
Fonte: `api.py`
- `POST /chat` com auth `X-API-Key` (401 se inválida) — baseline item 4.
  - Request: `{text, metadata?: {source?, session_id?}}`.
  - `source` default `"satellite"`, normalizado (trim + lowercase); `"telegram"` pula a Alexa; demais valores = caminho de voz (regressão zero).
  - Response: `{reply, spoken, source, metadata: {tools_used}, error?}` — `tools_used` = nomes das tools executadas no turno (ordenado).
- `GET /health` sem auth → `{"status": "ok"}`.
- **Timeline de auditoria** no log: eventos por nó (Chatbot Agent / Tool Agent / Speak Agent) com timestamp e detalhes — **sem conteúdo nem argumentos do usuário**.
- Lifespan: HA client + fluxo compilados no start; client fechado no shutdown.
- Log de inicialização com provider/model ativos.

### F09 — Canal Telegram
Fonte: `telegram_bot.py`
- Bot de texto em **long polling**; mensagem de texto → `POST /chat` com `metadata.source="telegram"` e `session_id="telegram_<chat_id>"` + header `X-API-Key`.
- **Whitelist** `ALLOWED_USERS`: mensagens de outros usuários são ignoradas com log `WARNING unauthorized user_id=...`.
- Indicador "digitando..." reenviado a cada ~4s durante o turno (Telegram oculta após ~5s); falha do indicador é silenciada (cosmético).
- **Resiliência — o bot nunca cai**: qualquer erro (timeout, HTTP, inesperado) → mensagem genérica "Algo deu errado, tente de novo." ao usuário + log de erro; reply vazio também vira fallback.
- `TELEGRAM_BOT_TOKEN` ausente → encerra com instrução clara; `ALLOWED_USERS` vazio → warning no start (ninguém poderia conversar).

### F10 — Satélite: voz offline (STT)
Fonte: `satelite.py`
- Push-to-talk: grava enquanto **Shift** pressionado (detecção via pynput), teto de segurança de 30s, PCM mono 16-bit a 16 kHz. Requer permissão de Accessibility no macOS.
- `capture_audio()` isolada — ponto de extensão documentado para trocar por VAD sem reescrever o pipeline.
- Transcrição `faster-whisper`, CPU/int8, `language="pt"`, modelo por `WHISPER_MODEL` (default `small`; baixado na 1ª execução).
- `POST /chat` **sem metadata** (comportamento de voz) + `X-API-Key`, timeout `BRAIN_TIMEOUT_S` (90s — um turno com tools demora mais que o do HA).
- Spinner no terminal durante gravação/transcrição/consulta; erros defensivos ("O Cérebro demorou demais...", "Não consegui falar com o Cérebro..."); "Nada transcrito." se vazio.

---

## 3. O que não é feature de código (registro)

- **`ha/` (brain: `home-assistent/`) — stack de infraestrutura local**: docker-compose com Home Assistant, Mosquitto, Zigbee2MQTT, Grafana, InfluxDB, MariaDB, Node-RED. É ambiente de execução, não feature do Cérebro — não será re-implementada em Go (pode ser movida/copiada como está).
  - ⚠️ Nota: `ha/config/homeassistant/secrets.yaml` está dentro do diretório do projeto — manter fora do controle de versão quando o repo virar git.
- **Governança Python** (pre-commit ruff/mypy/gitleaks, Makefile, pytest) — equivalentes Go: `golangci-lint`/`go vet`, `gitleaks`, Makefile, `go test`.
- **Baseline de segurança (7 itens)** — carrega para a re-implementação:
  1. Zero secrets no código (só `.env` + `.env.example`).
  2. Timeout explícito em todo cliente HTTP.
  3. Validação de input em toda tool (ex.: `entity_id` restrito a `switch|light|media_player`).
  4. Auth `X-API-Key` no `POST /chat`.
  5. HA token de longa duração com permissão mínima (documentado no `.env.example`).
  6. `gitleaks` no pre-commit.
  7. `.gitignore` cobre `.env`, caches e modelos Whisper baixados.

---

## 4. Mapeamento para specs (proposto)

Specs em `docs/specs/`, uma por feature, com template /to-spec:

| Spec | Feature | Arquivo |
|------|---------|---------|
| 01 | Configuração tipada e estrita | `docs/specs/01-config.md` |
| 02 | Motor cognitivo trocável (Genkit plugins) | `docs/specs/02-motor-cognitivo.md` |
| 03 | Integração Física: Home Assistant + Alexa | `docs/specs/03-home-assistant.md` |
| 04 | Tool clima (Open-Meteo) | `docs/specs/04-tool-clima.md` |
| 05 | Tool busca web (Tavily) | `docs/specs/05-tool-busca-web.md` |
| 06 | Tool controle de dispositivos | `docs/specs/06-tool-dispositivos.md` |
| 07 | Orquestração dos fluxos (Genkit: chatbot/tools/speak) | `docs/specs/07-orquestracao.md` |
| 08 | API do Cérebro (/chat, /health) | `docs/specs/08-api-cerebro.md` |
| 09 | Canal Telegram | `docs/specs/09-telegram.md` |
| 10 | Satélite (STT offline) | `docs/specs/10-satelite.md` |

Ordem de implementação sugerida (dependency order): 01 → 02/03 → 04/05/06 → 07 → 08 → 09/10.