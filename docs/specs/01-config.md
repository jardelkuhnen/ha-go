# Spec 01 — Configuração tipada e estrita (F01)

Fonte Python: `src/config.py` · Package Go: `internal/config`

---

## 1. Objetivo

Leitura única e validada das variáveis de ambiente do Cérebro, com **rejeição de variáveis de
aplicação desconhecidas** (paridade com `extra="forbid"` do pydantic-settings) e acesso
singleton. Motor de leitura: **Viper** (`.env` + ambiente).

## 2. Variáveis reconhecidas

| Var | Tipo | Default | Obrigatoria |
|-----|------|---------|-------------|
| `LLM_PROVIDER` | enum `gemini\|openai\|ollama` | — | ✅ |
| `LLM_MODEL` | string | default por provedor (spec 02) | — |
| `GEMINI_API_KEY` | secret | `""` | quando `gemini` |
| `OPENAI_API_KEY` | secret | `""` | quando `openai` |
| `OPENAI_API_BASE` | string | `""` (endpoint oficial) | — |
| `OLLAMA_BASE_URL` | string | `http://127.0.0.1:11434` | — |
| `OLLAMA_MODEL` | string | `llama3.2:3b` | — |
| `LLM_TIMEOUT_S` | duration | `30s` | — |
| `HA_URL` | URL | — | ✅ |
| `HA_TOKEN` | secret | — | ✅ |
| `HA_TIMEOUT_S` | duration | `5s` | — |
| `ALEXA_MEDIA_ENTITY` | string | — | ✅ |
| `BRAIN_API_KEY` | secret | — | ✅ |
| `BRAIN_PORT` | int | `8000` | — |

Precedência do modelo (provider ollama): `LLM_MODEL` > `OLLAMA_MODEL` > default do provedor.
Para `gemini`/`openai`: `LLM_MODEL` > default do provedor (spec 02).

## 3. Checagem estrita (decisão 5)

No `Load`, além de ler as chaves acima, varre `os.Environ()` e **falha com erro** se existir
variável cujo prefixo pertence aos namespaces da aplicação e cujo nome não é reconhecido:

- Namespaces: `LLM_`, `GEMINI_`, `OPENAI_`, `OLLAMA_`, `HA_`, `ALEXA_`, `BRAIN_`,
  `TAVILY_`, `TELEGRAM_`, `WHISPER_`.
- Exceção — **conhecidas-ignoradas** (convivência com o `.env` do projeto Python durante a
  migração; são lidas como conhecidas mas não consumidas): `TAVILY_API_KEY`,
  `TELEGRAM_BOT_TOKEN`, `WHISPER_MODEL`, `ALLOWED_USERS`, `BRAIN_URL`, `BRAIN_TIMEOUT_S`.
- Variáveis fora dos namespaces (sistema: `PATH`, `SHELL`, `USER`…) são ignoradas.

Exemplos: `HA_TIMEOT_S` → erro no startup; `TAVILY_API_KEY` presente → ok (ignorada);
`BRAIN_URL` presente → ok (ignorada).

## 4. Comportamento

- `config.Load() (Settings, error)` — lê `.env` do diretório corrente + env do processo
  (env do processo vence sobre `.env`), valida tudo e devolve a struct.
- `config.MustLoad()` — variante de conveniência para o startup (loga e `os.Exit(1)` em erro).
- Singleton: `sync.Once` + variável de pacote; o erro de carga é memorizado.
- Erros devem citar a **variável** e o **motivo** (ex.: `config: variável desconhecida do app: HA_TIMEOT_S`,
  `config: HA_TOKEN é obrigatória`, `config: LLM_PROVIDER inválido: "foo"`).
- Timeouts: parsing com `time.ParseDuration` **e** fallback numérico em segundos
  (`"30"` ⇒ 30s; `"30.0"` ok) — o `.env` do Python usa `30.0`; valores ≤ 0 são rejeitados.
- `Secrets` ficam como `string` em campos privados-capitalizados; **nunca** entram em log.

## 5. Critérios de aceite (testes unitários)

1. `.env` com var desconhecida de namespace do app → erro citando a var.
2. Var conhecida-ignorada presente → carrega sem erro.
3. Defaults corretos quando `.env` ausente (exceto obrigatórias → erro).
4. Precedência de `LLM_MODEL`/`OLLAMA_MODEL` por provedor.
5. Parsing de timeout aceita `"30"`, `"30.0"`, `"90s"`; rejeita `0`/negativo/garbage.
6. `LLM_PROVIDER` fora do enum → erro; env do processo vence sobre `.env`.