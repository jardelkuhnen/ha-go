# Spec 06 — Orquestração dos fluxos com Genkit (F07)

Fonte Python: `src/graph/{state,nodes,workflow,prompt}.py` · Package Go: `internal/brain`

---

## 1. Objetivo

Um único flow Genkit imperativo (decisão 3) que realiza o turno:
**chatbot → (executarTools → chatbot)\* → speak → fim**, com roteamento por canal.
Genkit Go não tem grafo explícito: os nós do LangGraph viram funções nomeadas dentro do
flow, com o mesmo comportamento observável.

## 2. Flow

```go
genkit.DefineFlow(g, "brain", func(ctx, in ChatInput) (ChatOutput, error) { … })
```

```go
type ChatInput struct {
    Text      string  // fala/texto do usuário
    Source    string  // "satellite" (default) | "telegram"
    SessionID string  // carregado ao estado, NÃO consumido (reservado p/ sessão futura)
}

type ChatOutput struct {
    Reply     string   // texto final do motor cognitivo
    Spoken    bool     // TTS acionado com sucesso
    Error     string   // erro do passo speak, se houver
    ToolsUsed []string // nomes das tools executadas (ordenado)
}
```

## 3. Passos (nós)

### chatbot
- `Generate` com: system prompt do canal (voz vs telegram, §5), histórico de mensagens e
  `ai.WithTools(get_weather, control_device)`.
- Deadline do contexto = `LLM_TIMEOUT_S` (spec 02).

### executarTools (loop)
- Enquanto a resposta contiver `ToolRequests`: executa cada tool e anexa os resultados ao
  histórico; nova chamada `Generate` (de volta ao passo chatbot).
- **Teto de segurança: 8 iterações** — excedido → erro explícito
  "limite de 8 iterações de ferramentas excedido" (o turno termina com erro; não arrasta
  até o timeout do LLM).
- Nomes executados acumulam em `ToolsUsed`.

### speak (passo terminal — ADR-0002)
- Texto = `resp.Text()` da última resposta (em Genkit o texto já vem limpo — o
  `content_to_text` do Python, que desempacotava blocos do Gemini com assinaturas, **não
  é necessário**).
- `ha.Speak(texto)` via client injetado (spec 03). Sucesso → `Spoken=true`; falha →
  `Spoken=false`, `Error=…` — **nunca falha o flow por erro de TTS**.
- Texto vazio → `Spoken=false`, `Error="sem conteúdo para falar"`.

## 4. Roteamento (paridade com `route_tools`)

1. Há tool calls pendentes (qualquer canal) → `executarTools` → volta ao chatbot.
2. Sem tool calls e `source == "telegram"` → **fim direto** (não aciona a Alexa).
3. Sem tool calls e canal de voz (`source` vazio/`"satellite"`/outros) → `speak` → fim.

`source` chega já normalizado pela API (trim + lowercase, default `"satellite"`).

## 5. System prompts por canal (pt-BR, v1 sem menção a busca — decisão 10)

- **Voz** (`source` ≠ telegram): texto plano falável — sem Markdown/símbolos/JSON, frases
  curtas (1–2 frases), confirmação de automação em uma frase, nunca inventar clima (usar a
  ferramenta), não sabe → diz que não sabe, breve.
- **Telegram**: Markdown permitido, respostas um pouco mais longas, direto; sem JSON/estruturas;
  mesma regra de ferramenta de clima e de honestidade.
- Textos completos fixados em `internal/brain/prompt.go` como constantes (tradução fiel do
  `prompt.py` **removendo a frase sobre "resultados de busca"** — decisão 10).

## 6. Catálogo de tools

`[]ai.ToolRef{get_weather, control_device}` exportado do `internal/tools` — única fonte
de verdade vinculada ao chatbot (paridade com `ALL_TOOLS`).

## 7. Estado

- Mensagens acumuladas no escopo do flow (histórico local do turno — sem persistência,
  paridade com o Python que não usa checkpointer).
- `SessionID` apenas transportado no input/output para uso futuro.

## 8. Critérios de aceite (testes com modelo fake `DefineModel`)

1. Modelo fake devolve tool call → tool executa (mock) → 2ª volta texto → `Reply` correto,
   `ToolsUsed` preenchido, `Spoken=true` (client HA mockado com sucesso).
2. Resposta direta sem tool + canal voz → `Spoken=true`.
3. Resposta direta sem tool + `source=telegram` → `Spoken=false`, `Error` vazio, `Speak` nunca chamado.
4. Loop com modelo fake que sempre pede tool → erro de teto após 8 iterações.
5. `Speak` falha → `Spoken=false`, `Error` preenchido, `Reply` preservado (flow não falha).
6. `SessionID` chega ao estado e não altera o comportamento.