# Design: Agente `home_assistent` (Genkit Agents API)

Data: 2026-09-13
Branch: `feat/agente-genkit`
Base: `d174e16` (branch `fix/lista-dispositivos`)

## Contexto e motivação

O projeto hoje orquestra o turno conversacional com um **loop de tools feito à mão**
em `internal/brain/flow.go`: `chatbot → executarTools → chatbot* → speak`, sobre
`genkit.DefineFlow` + `genkit.Generate(WithReturnToolRequests(true))`, com um teto
manual de 8 iterações e dois system prompts por canal (voz / telegram).

O Genkit (v1.13.1, já no `go.mod`) oferece a **Agents API** (`genkit/exp.DefineAgent`
+ `ai/exp`), que substitui esse loop: o framework roda o ciclo de tool-calls
internamente, com `WithTools`, `WithMaxTurns`, e devolve `AgentOutput` com a mensagem
final e o estado da conversa. A reestruturação cria um **agente genérico** chamado
`home_assistent` sobre essa API, eliminando o loop manual e o `DefineFlow`.

## Decisões (aprovadas)

1. **Um agente genérico.** Instância única `DefineAgent[struct{}]`, persona de
   assistente, entrada = objetivo do usuário. Canal não é um agente diferente.
2. **Prompt unificado estático.** Um `WithSystem` só, com as regras de voz (texto
   plano, breve, pt-BR, clima só via `get_weather`, dispositivos via `control_device`,
   honestidade, sem Markdown/JSON) aplicadas a **ambos** os canais. Telegram perde
   Markdown — mudança menor aceita.
3. **Voz igual a hoje; telegram fora do escopo.** `speak` (TTS via HA) continua como
   pós-processamento da API no canal de voz, **não vira tool**. `send_telegram` é
   evolução futura (precisa bot token, chat id, client) — não implementado aqui.
4. **Stateless.** Sem `WithSessionStore` → estado client-managed, sem persistência.
   `SessionID` continua transportado, não consumido (paridade com o flow atual).

## Arquitetura

```
cmd/brain/main.go
  cfg := config.MustLoad()
  motor := brain.Setup(ctx, cfg)            // motor cognitivo (genkit.Init + plugin + model)
  cli := ha.NewClient(cfg)
  ag := agent.DefineHomeAssistent(motor, cli)   // NOVO: DefineAgent sobre o motor
  runner := agent.NewRunner(motor, ag, cli)     // NOVO: adapter api.TurnRunner
  api.NewRouter({Runner: runner, ...})          // /chat inalterado
```

- **`internal/brain/`** mantém o **motor cognitivo**: `brain.go` (Motor/Setup/NewMotor),
  `model.go`, `plugin.go`. O motor é ortogonal ao loop agêntico.
- **`internal/brain/flow.go` e `internal/brain/prompt.go` são removidos.**
- **Novo `internal/agent/`** é dono do turno conversacional.

## Layout de pacotes — `internal/agent/`

### `agent.go`
```go
func DefineHomeAssistent(m *brain.Motor, cli *ha.Client) *aix.Agent[struct{}]
```
Monta `aix.InlinePrompt`:
- `ai.WithModelName(m.ModelName)`
- `ai.WithSystem(systemPrompt)` (constante em `prompt.go`)
- `ai.WithTools(tools.Catalog(m.Genkit, cli)...)` — catálogo único reutilizado
- `ai.WithMaxTurns(maxTurns)` com `maxTurns = 8` (paridade com o teto atual)

Devolve `genkitx.DefineAgent[struct{}](m.Genkit, "home_assistent", prompt)` — sem
`WithSessionStore` (stateless). State type `struct{}` (sem estado custom).

### `prompt.go`
Constante `systemPrompt` — a regra unificada (derivação do `systemPromptVoz` atual,
aplicada aos dois canais, com menção explícita às duas tools).

### `runner.go`
```go
type Runner struct { ... }
func NewRunner(m *brain.Motor, ag *aix.Agent[struct{}], cli *ha.Client) *Runner
func (r *Runner) Run(ctx context.Context, in ChatInput) (ChatOutput, error)
```
`Runner` implementa `api.TurnRunner` (interface existente, inalterada). `Run`:
1. Deadline de turno: `gctx, cancel := m.TurnDeadline(ctx)` — ver §Deadline.
2. `out, err := ag.RunText(gctx, in.Text)`. Em erro (incl.
   `ai.ErrMaxTurnsExceeded` / `AgentFinishReasonAborted`) → `ChatOutput{}, err`
   (a API devolve 500, como hoje).
3. `reply := out.Message.Text()`.
4. `tools := extractToolNames(out.State.Messages)` — percorre as mensagens do estado,
   coleta `part.IsToolRequest()` → `part.ToolRequest.Name`, ordenado, nunca nil.
5. Roteamento por canal:
   - `in.Source == "telegram"` → `ChatOutput{Reply: reply, ToolsUsed: tools}` (sem speak).
   - demais (voz) → `spoken, speakErr := speak(ctx, cli, reply)` →
     `ChatOutput{Reply, Spoken: spoken, Error: speakErr, ToolsUsed: tools}`.

### `ChatInput` / `ChatOutput`
Movidos de `internal/brain/flow.go` para `internal/agent/` (o agent é o novo dono do
contrato do turno). Mesma forma atual:
```go
type ChatInput struct { Text, Source, SessionID string }
type ChatOutput struct {
    Reply     string
    Spoken    bool
    Error     string
    ToolsUsed []string
}
```
`api` passa a importar `agent` por esses tipos. `api.TurnRunner` e o handler `/chat`
não mudam de forma.

### `speak` / helpers
`speak` (TTS via HA, passo terminal defensivo) e `toolsOrdenadas` mudam de `flow.go`
para `internal/agent/runner.go` (ou `speak.go` no mesmo package). Mesma lógica:
texto vazio → "sem conteúdo para falar"; `cli == nil` → erro de wiring; `cli.Speak`
devolve `{OK, Error}`.

## Deadline de turno (adaptação)

Hoje cada geração do `chatbot` recebe `LLM_TIMEOUT_S` (default 30s) como deadline
próprio, então um turno de 8 tools pode rodar ~240s. A Agents API não expõe timeout
por geração — o `ctx` de `RunText` limita o **turno inteiro**.

Para preservar o orçamento multi-tool, o adapter usa um **deadline de turno** =
`m.timeout × (maxTurns + 1)` (ex.: 30s × 9 = 270s). Um único `LLM_TIMEOUT_S` cortaria
indevidamente turnos com várias tools. Adiciona-se `Motor.TurnDeadline(ctx)` (acessor
— o campo `timeout` é privado hoje) devolvendo `(context.Context, context.CancelFunc)`.

## Fluxo de dados (um turno)

1. `POST /chat` → handler valida body, normaliza `source` (`trim+lower`; vazio →
   `"satellite"`), monta `agent.ChatInput{Text, Source, SessionID}`.
2. `runner.Run(ctx, in)` — roda o agente (passo 2 acima), mapeia saída, roteia speak.
3. Handler monta `chatResponse` (200) e emite a timeline slog (Chatbot/Tool/Speak),
   inalterada. Em erro do runner → 500.

## Tratamento de erros

- Erro do agente (provider, `ErrMaxTurnsExceeded`) → 500, igual ao erro do flow hoje.
- Falha de tool: tools seguem defensivas (strings de fallback, nunca devolvem erro) —
  inalterado.
- Falha de speak: nunca derruba o turno → `Spoken=false, Error=<msg>` → 200 com
  campo error (paridade).
- Reply vazio: o guard do speak devolve "sem conteúdo para falar" (paridade).

## Testes

- `agent/runner_test.go` — mapeamento do adapter: extração de reply, extração de nomes
  de tools de `State.Messages`, speak chamado na voz / pulado no telegram, caminho de
  erro → 500. Usa um agente stub (`DefineCustomAgent` ou modelo fake registrado sob
  `m.ModelName`) para rodar sem LLM real.
- Testes do motor em `brain/` permanecem; `flow_test.go` e `prompt_test.go` são
  removidos com os arquivos.
- Testes de `api/` inalterados (rodam contra um `TurnRunner` fake).
- Timeline: nomes de tools extraídos do estado batem com os eventos emitidos.

## Arquivos afetados

| Ação | Arquivo |
|------|---------|
| novo | `internal/agent/agent.go` |
| novo | `internal/agent/prompt.go` |
| novo | `internal/agent/runner.go` ( Runner, ChatInput, ChatOutput, speak, helpers) |
| novo | `internal/agent/runner_test.go` (+ testes do package) |
| remove | `internal/brain/flow.go` |
| remove | `internal/brain/prompt.go` |
| remove | `internal/brain/flow_test.go` |
| remove | `internal/brain/prompt_test.go` |
| edit | `internal/brain/brain.go` — adiciona `Motor.TurnDeadline` |
| edit | `cmd/brain/main.go` — wiring do agent + runner |
| edit | `internal/api/api.go` — import `agent` por `ChatInput`/`ChatOutput` |
| edit | `internal/api/chat.go` — import `agent` por `ChatInput` |

## Fora de escopo

- `send_telegram` tool (precisa bot token, chat id, client HTTP).
- Persistência de sessão (`WithSessionStore`) / memória multi-turno.
- Stream de resposta (`Connect` / SSE).
