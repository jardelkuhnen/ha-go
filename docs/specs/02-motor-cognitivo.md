# Spec 02 — Motor cognitivo trocável (F02, subsume ADR-0001)

Fonte Python: `src/config.py` (`get_llm`, Protocol `CognitiveMotor`) · Package Go: `internal/brain` (setup Genkit)

---

## 1. Objetivo

O fluxo nunca instancia o LLM diretamente: o motor é selecionado por configuração e
endereçado por **nome de modelo no registry do Genkit**. A abstração de plugins/modelos do
Genkit substitui o Protocol `CognitiveMotor` do Python (FEATURES.md §F02).

## 2. Provedores (plugins Genkit Go)

| Provider | Plugin (import) | Endereçamento | Config mínima |
|----------|-----------------|---------------|---------------|
| `gemini` | `github.com/firebase/genkit/go/plugins/googlegenai` → `&googlegenai.GoogleAI{APIKey: …}` | `googleai/<LLM_MODEL>` | `GEMINI_API_KEY` |
| `openai` | `github.com/firebase/genkit/go/plugins/openai` (OpenAI-compatível genérico) → `&openai.OpenAI{APIKey: …, BaseURL: …}` | `openai/<LLM_MODEL>` | `OPENAI_API_KEY`; `OPENAI_API_BASE` opcional |
| `ollama` | `github.com/firebase/genkit/go/plugins/ollama` → `&ollama.Ollama{ServerAddress: …, Timeout: …}` | `ollama/<LLM_MODEL>` | `OLLAMA_BASE_URL` |

**Registro condicional (decisão 4):** só o plugin do `LLM_PROVIDER` escolhido é registrado —
não exige credencial dos outros no startup. Falha rápida com mensagem clara se faltar a
credencial do provedor escolhido (ex.: `gemini` sem `GEMINI_API_KEY`).

## 3. Modelo ativo

- Defaults por provedor (paridade com o Python): `gemini → gemini-3.5-flash`,
  `openai → gpt-4o-mini`, `ollama → llama3.2:3b`.
- Resolução: `LLM_MODEL` (se setada) → para ollama, `OLLAMA_MODEL` como fallback histórico →
  default do provedor. Resolvida **uma vez no startup** e logada (`provider` + `model`).
- Ollama plugin detecta automaticamente tool support (`/api/show`); o modelo escolhido
  precisa suportar tools (ex.: `llama3.2:3b` suporta).

## 4. Timeout e chamadas

- `LLM_TIMEOUT_S` vira `context.WithTimeout` em **toda** chamada de geração (baseline item 2).
- Tools são vinculadas via opção de geração (`ai.WithTools(...)`) — nunca hardcoded no motor.
- O plugin ollama recebe `Timeout` de `LLM_TIMEOUT_S`; para os demais, o deadline do context
  é o mecanismo.

## 5. Troca de motor

Trocar motor = editar `.env` (`LLM_PROVIDER` + credencial, opcionalmente `LLM_MODEL`) e
reiniciar. Zero mudança de código ou de binding do fluxo — o fluxo resolve o modelo por
nome no registry.

## 6. Critérios de aceite (manuais, com `.env` real)

1. `LLM_PROVIDER=ollama` com Ollama local → startup loga `provider=ollama model=…` e responde.
2. `LLM_PROVIDER=gemini` sem `GEMINI_API_KEY` → erro de startup claro (não panico).
3. `LLM_MODEL=xpto` aparece no log e é usado na chamada.
4. `LLM_TIMEOUT_S` curto + Ollama lento → timeout respeitado com erro limpo.