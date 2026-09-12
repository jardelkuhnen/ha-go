# Spec 01 — Configuração tipada e estrita (F01) — Plano de Implementação

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implementar `internal/config` — leitura única e validada das variáveis de ambiente do Cérebro via Viper (`.env` + ambiente), com rejeição de variáveis desconhecidas dos namespaces do app, acesso singleton e mensagens de erro citando variável e motivo.

**Architecture:** Dois arquivos no package `internal/config`: `config.go` (struct `Settings`, singleton `sync.Once`, `Load`/`MustLoad`, wiring do Viper) e `validate.go` (varredura estrita de namespaces, validações de valor: enum, obrigatórias, timeouts, URL, porta). Testes em `config_test.go` (caminho de `Load`, critérios §5) e `validate_test.go` (funções puras). O Viper dá a precedência env-do-processo > `.env` de graça (`AutomaticEnv` vence sobre config file).

**Tech Stack:** Go 1.25, `github.com/spf13/viper`, `testing` padrão (`t.Setenv`, `t.Chdir`, `-race`).

**Spec:** `docs/specs/01-config.md` (fonte da verdade — §2 variáveis, §3 checagem estrita, §4 comportamento, §5 critérios de aceite).

## Global Constraints

- Package novo: `internal/config`. Module: `home-assistent-go`. Go 1.25.
- Motor de leitura: `github.com/spf13/viper` — `.env` do diretório corrente + ambiente do processo; **env do processo vence sobre `.env`**.
- A spec `docs/specs/01-config.md` é a fonte da verdade; nada além dela. Formatos de erro exatos (§4): `config: variável desconhecida do app: HA_TIMEOT_S`, `config: HA_TOKEN é obrigatória`, `config: LLM_PROVIDER inválido: "foo"`. Extensões do mesmo formato citam sempre **variável + motivo**.
- Secrets (`GEMINI_API_KEY`, `OPENAI_API_KEY`, `HA_TOKEN`, `BRAIN_API_KEY`) ficam como `string` em campos exportados da struct; **nunca** aparecem em logs nem em mensagens de erro — erros citam apenas nome de variável (ou valor não-secreto, como o enum inválido).
- Gates em toda tarefa, antes do commit: `gofmt -l .` vazio; `go vet ./...` limpo; `go test -race ./...` verde.
- Testes mutam ambiente/cwd/singleton → **proibido `t.Parallel()`**; cada teste chama `carrega`/`reset` para isolar o singleton.
- NÃO criar nem editar `.env.example` ou `.env` (bloqueados; `.env` já está no `.gitignore`).
- Commits: mensagem conventional curta + linha final `Co-Authored-By: Claude Code <noreply@anthropic.com>`. Trabalhar sempre no worktree atual; commits locais, sem push/merge.
- Variáveis reconhecidas (§2): `LLM_PROVIDER`, `LLM_MODEL`, `GEMINI_API_KEY`, `OPENAI_API_KEY`, `OPENAI_API_BASE`, `OLLAMA_BASE_URL`, `OLLAMA_MODEL`, `LLM_TIMEOUT_S`, `HA_URL`, `HA_TOKEN`, `HA_TIMEOUT_S`, `ALEXA_MEDIA_ENTITY`, `BRAIN_API_KEY`, `BRAIN_PORT`.
- Conhecidas-ignoradas (§3): `TAVILY_API_KEY`, `TELEGRAM_BOT_TOKEN`, `WHISPER_MODEL`, `ALLOWED_USERS`, `BRAIN_URL`, `BRAIN_TIMEOUT_S` — reconhecidas (não causam erro) mas não consumidas.
- Namespaces (§3): `LLM_`, `GEMINI_`, `OPENAI_`, `OLLAMA_`, `HA_`, `ALEXA_`, `BRAIN_`, `TAVILY_`, `TELEGRAM_`, `WHISPER_`.
- Decisões fechadas (não reinventar): porta validada só com `strconv.Atoi` (a spec não exige faixa); `HA_URL` exige scheme `http`/`https` (paridade com `AnyHttpUrl`); `ResolvedModel()` devolve `""` para gemini/openai sem `LLM_MODEL` (default do provedor entra na spec 02).

---

### Task 1: Esqueleto do package — Settings, Viper, defaults, obrigatórias, singleton

**Files:**
- Create: `internal/config/config.go`
- Create: `internal/config/validate.go`
- Create: `internal/config/config_test.go`
- Create: `internal/config/validate_test.go`
- Modify: `go.mod` / `go.sum` (via `go get`)

**Interfaces:**
- Consumes: nada (primeira tarefa).
- Produces (tasks seguintes dependem destes nomes exatos):
  - `type Settings struct` com campos: `LLMProvider string`, `LLMModel string`, `OllamaModel string`, `GeminiAPIKey string`, `OpenAIAPIKey string`, `OpenAIAPIBase string`, `OllamaBaseURL string`, `LLMTimeout time.Duration`, `HAURL string`, `HAToken string`, `HATimeout time.Duration`, `AlexaMediaEntity string`, `BrainAPIKey string`, `BrainPort int`.
  - `func Load() (Settings, error)`, `func MustLoad() Settings`.
  - `func validate(s *Settings, v *viper.Viper) error` — ponto único de validação em `validate.go`; tasks 2–4 adicionam checagens dentro dela.
  - Helpers de teste (mesmo package, `config_test.go`): `reset()`, `clearAppEnv(t)`, `withDotenv(t, content)`, `carrega(t, dotenv, env)`, `dotenvDe(vars)`, `envObrigatorias(provider)`, constante `dotenvBase`.

- [ ] **Step 1: Adicionar a dependência Viper**

```bash
cd /Users/mac01/workspace/home-assistent-go/.worktrees/spec-01
go get github.com/spf13/viper
go mod tidy
```

Expected: `go.mod` ganha `github.com/spf13/viper` (e deps transitivas em `go.sum`), sem erro.

- [ ] **Step 2: Escrever os testes que falham** — `internal/config/config_test.go` completo (o `validate_test.go` desta task fica com um teste mínimo de marcador que a Task 3 substitui; sem ele o package de teste ainda compila, então este passo opcionalmente só cria o arquivo vazio com `package config`).

```go
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// reset limpa o singleton para isolar cada teste. Só existe nos testes.
func reset() {
	once = sync.Once{}
	singleton = Settings{}
	loadErr = nil
}

// clearAppEnv remove do ambiente do processo (e restaura no fim do teste, via
// t.Cleanup) toda variável que caia num namespace do app ou que seja
// conhecida-ignorada — para o teste controlar completamente o ambiente.
func clearAppEnv(t *testing.T) {
	t.Helper()
	var restore []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		up := strings.ToUpper(name)
		matched := knownIgnored[up]
		if !matched {
			for _, ns := range appNamespaces {
				if strings.HasPrefix(up, ns) {
					matched = true
					break
				}
			}
		}
		if matched {
			os.Unsetenv(name)
			restore = append(restore, kv)
		}
	}
	t.Cleanup(func() {
		for _, kv := range restore {
			name, val, _ := strings.Cut(kv, "=")
			os.Setenv(name, val)
		}
	})
}

// withDotenv entra num dir temporário (t.Chdir) e, se content != "", escreve
// um `.env` nele. Dir vazio = caso ".env ausente".
func withDotenv(t *testing.T, content string) {
	t.Helper()
	t.Chdir(t.TempDir())
	if content == "" {
		return
	}
	if err := os.WriteFile(".env", []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// carrega roda Load com ambiente controlado: reseta o singleton, limpa vars de
// app do processo, escreve o .env informado e seta env por cima (env vence).
func carrega(t *testing.T, dotenv string, env map[string]string) (Settings, error) {
	t.Helper()
	reset()
	clearAppEnv(t)
	withDotenv(t, dotenv)
	for k, v := range env {
		t.Setenv(k, v)
	}
	return Load()
}

// dotenvDe monta o conteúdo de um .env a partir de um mapa.
func dotenvDe(vars map[string]string) string {
	var b strings.Builder
	for k, v := range vars {
		fmt.Fprintf(&b, "%s=%s\n", k, v)
	}
	return b.String()
}

// envObrigatorias devolve as 5 obrigatórias da §2 (provider parametrizado).
func envObrigatorias(provider string) map[string]string {
	return map[string]string{
		"LLM_PROVIDER":       provider,
		"HA_URL":             "http://home.local:8123",
		"HA_TOKEN":           "token-ha",
		"ALEXA_MEDIA_ENTITY": "media_player.alexa",
		"BRAIN_API_KEY":      "chave-do-cerebro",
	}
}

// dotenvBase cobre as obrigatórias com provider ollama (não exige API key).
const dotenvBase = `LLM_PROVIDER=ollama
HA_URL=http://home.local:8123
HA_TOKEN=token-secreto
ALEXA_MEDIA_ENTITY=media_player.alexa
BRAIN_API_KEY=chave-cerebro
`

func TestLoadLeDotenv(t *testing.T) {
	s, err := carrega(t, dotenvBase, nil)
	if err != nil {
		t.Fatalf("Load: erro inesperado: %v", err)
	}
	if s.LLMProvider != "ollama" || s.HAURL != "http://home.local:8123" ||
		s.HAToken != "token-secreto" || s.AlexaMediaEntity != "media_player.alexa" ||
		s.BrainAPIKey != "chave-cerebro" {
		t.Errorf("valores do .env não chegaram: %+v", s)
	}
}

func TestLoadDefaultsQuandoDotenvAusente(t *testing.T) { // critério 3
	s, err := carrega(t, "", envObrigatorias("ollama"))
	if err != nil {
		t.Fatalf("Load: erro inesperado: %v", err)
	}
	if s.OllamaBaseURL != "http://127.0.0.1:11434" {
		t.Errorf("OllamaBaseURL = %q; want default da §2", s.OllamaBaseURL)
	}
	if s.OllamaModel != "llama3.2:3b" {
		t.Errorf("OllamaModel = %q; want llama3.2:3b", s.OllamaModel)
	}
	if s.OpenAIAPIBase != "" || s.LLMModel != "" || s.GeminiAPIKey != "" || s.OpenAIAPIKey != "" {
		t.Errorf("defaults vazios incorretos: %+v", s)
	}
}

func TestLoadObrigatoriasFaltando(t *testing.T) { // critério 3 (exceto)
	for _, req := range []string{"LLM_PROVIDER", "HA_URL", "HA_TOKEN", "ALEXA_MEDIA_ENTITY", "BRAIN_API_KEY"} {
		vars := map[string]string{}
		for k, v := range envObrigatorias("ollama") {
			vars[k] = v
		}
		delete(vars, req)
		_, err := carrega(t, dotenvDe(vars), nil)
		if err == nil || !strings.Contains(err.Error(), req+" é obrigatória") {
			t.Errorf("faltando %s: want erro %q, veio %v", req, "config: "+req+" é obrigatória", err)
		}
	}
}

func TestLoadEnvVenceSobreDotenv(t *testing.T) { // critério 6 (parte)
	s, err := carrega(t, dotenvBase, map[string]string{"HA_URL": "http://outro.local:8123"})
	if err != nil {
		t.Fatalf("Load: erro inesperado: %v", err)
	}
	if s.HAURL != "http://outro.local:8123" {
		t.Errorf("HAURL = %q; want valor do ambiente (vence sobre .env)", s.HAURL)
	}
}

func TestLoadSingleton(t *testing.T) {
	s1, err := carrega(t, dotenvBase, nil)
	if err != nil {
		t.Fatalf("Load: erro inesperado: %v", err)
	}
	s2, err2 := Load()
	if err2 != nil || s1 != s2 {
		t.Errorf("Load subsequente deve devolver o mesmo Settings memorizado: %v / %v", s2, err2)
	}
}

func TestLoadConcorrente(t *testing.T) {
	if _, err := carrega(t, dotenvBase, nil); err != nil {
		t.Fatalf("Load: erro inesperado: %v", err)
	}
	const n = 8
	results := make([]Settings, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = Load()
		}(i)
	}
	wg.Wait()
	for i := 1; i < n; i++ {
		if errs[i] != nil || results[i] != results[0] {
			t.Errorf("goroutine %d: resultado divergente: %v / %v", i, results[i], errs[i])
		}
	}
}

func TestLoadErroMemorizado(t *testing.T) {
	vars := envObrigatorias("ollama")
	delete(vars, "HA_TOKEN")
	delete(vars, "BRAIN_API_KEY")
	_, err1 := carrega(t, dotenvDe(vars), nil)
	if err1 == nil {
		t.Fatal("Load: want erro de obrigatória faltando")
	}
	// Depois do erro, tornar o ambiente válido: o erro continua memorizado.
	t.Setenv("HA_TOKEN", "agora-tem")
	t.Setenv("BRAIN_API_KEY", "agora-tem")
	_, err2 := Load()
	if err2 != err1 {
		t.Fatalf("erro de carga deve ser memorizado; err1=%v err2=%v", err1, err2)
	}
}

func TestMustLoadLogaEAborta(t *testing.T) {
	if _, err := carrega(t, "", nil); err == nil {
		t.Fatal("carrega: want erro (nenhuma obrigatória setada)")
	}
	var msg string
	var called bool
	orig := fatalf
	fatalf = func(format string, args ...any) {
		called = true
		msg = fmt.Sprintf(format, args...)
	}
	t.Cleanup(func() { fatalf = orig })
	_ = MustLoad()
	if !called {
		t.Fatal("MustLoad deve invocar fatalf em erro")
	}
	if !strings.HasPrefix(msg, "config: ") {
		t.Errorf("mensagem de erro deve manter o prefixo config: %q", msg)
	}
}
```

- [ ] **Step 3: Rodar os testes e verificar que falham de compilação**

Run: `go test ./internal/config/`
Expected: FAIL — `undefined: once` / `undefined: Load` / `undefined: fatalf` etc. (nada implementado ainda).

- [ ] **Step 4: Implementar `internal/config/config.go`**

```go
// Package config faz a leitura única e validada das variáveis de ambiente do
// Cérebro (paridade com src/config.py do projeto Python): lê `.env` do
// diretório corrente + ambiente do processo, rejeita variáveis desconhecidas
// dos namespaces da aplicação (paridade com extra="forbid") e expõe acesso
// singleton. Motor de leitura: Viper.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/spf13/viper"
)

// Settings é a configuração tipada do Cérebro. Campos de secret ficam como
// string e nunca entram em log nem em mensagens de erro — os erros deste
// package citam apenas o nome da variável.
type Settings struct {
	// Motor cognitivo (§2).
	LLMProvider   string // "gemini" | "openai" | "ollama"
	LLMModel      string // LLM_MODEL cru; pode ser vazio
	OllamaModel   string // OLLAMA_MODEL
	GeminiAPIKey  string // secret
	OpenAIAPIKey  string // secret
	OpenAIAPIBase string
	OllamaBaseURL string
	LLMTimeout    time.Duration

	// Home Assistant / Alexa (§2).
	HAURL            string
	HAToken          string // secret
	HATimeout        time.Duration
	AlexaMediaEntity string

	// API do Cérebro (auth, §2).
	BrainAPIKey string // secret
	BrainPort   int
}

var (
	once      sync.Once
	singleton Settings
	loadErr   error
)

// Load lê e valida a configuração uma única vez (singleton): a primeira
// chamada lê `.env` do diretório corrente + o ambiente do processo (o ambiente
// vence sobre `.env`), valida tudo e memoriza o resultado — inclusive o erro.
// Chamadas seguintes devolvem o valor memorizado sem recarregar.
func Load() (Settings, error) {
	once.Do(func() { singleton, loadErr = load() })
	return singleton, loadErr
}

// fatalf existe para os testes substituírem o aborto do MustLoad.
var fatalf = log.Fatalf

// MustLoad é a variante de conveniência para o startup: em erro, loga e
// encerra o processo com código 1 (log.Fatalf).
func MustLoad() Settings {
	s, err := Load()
	if err != nil {
		fatalf("%v", err)
	}
	return s
}

func load() (Settings, error) {
	v := viper.New()
	v.SetConfigFile(".env")
	v.SetConfigType("env")
	v.AutomaticEnv()

	// Defaults da §2. Timeouts/porta ficam como string e são parseados em
	// validate (parseTimeout aceita "30", "30.0" e "30s").
	v.SetDefault("LLM_MODEL", "")
	v.SetDefault("GEMINI_API_KEY", "")
	v.SetDefault("OPENAI_API_KEY", "")
	v.SetDefault("OPENAI_API_BASE", "")
	v.SetDefault("OLLAMA_BASE_URL", "http://127.0.0.1:11434")
	v.SetDefault("OLLAMA_MODEL", "llama3.2:3b")
	v.SetDefault("LLM_TIMEOUT_S", "30s")
	v.SetDefault("HA_TIMEOUT_S", "5s")
	v.SetDefault("BRAIN_PORT", 8000)

	// .env ausente é válido (defaults + ambiente bastam).
	if err := v.ReadInConfig(); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Settings{}, fmt.Errorf("config: lendo .env: %w", err)
	}

	s := Settings{
		LLMProvider:      v.GetString("LLM_PROVIDER"),
		LLMModel:         v.GetString("LLM_MODEL"),
		OllamaModel:      v.GetString("OLLAMA_MODEL"),
		GeminiAPIKey:     v.GetString("GEMINI_API_KEY"),
		OpenAIAPIKey:     v.GetString("OPENAI_API_KEY"),
		OpenAIAPIBase:    v.GetString("OPENAI_API_BASE"),
		OllamaBaseURL:    v.GetString("OLLAMA_BASE_URL"),
		HAURL:            v.GetString("HA_URL"),
		HAToken:          v.GetString("HA_TOKEN"),
		AlexaMediaEntity: v.GetString("ALEXA_MEDIA_ENTITY"),
		BrainAPIKey:      v.GetString("BRAIN_API_KEY"),
	}
	if err := validate(&s, v); err != nil {
		return Settings{}, err
	}
	return s, nil
}
```

E `internal/config/validate.go` (mínimo desta task — cresce nas tasks 2–5). Os dados de namespace (§3) já nascem aqui porque `clearAppEnv` (testes) precisa deles:

```go
package config

import (
	"fmt"

	"github.com/spf13/viper"
)

// appNamespaces são os prefixos da aplicação (§3): variável com um desses
// prefixos precisa ser reconhecida, senão o startup falha.
var appNamespaces = []string{
	"LLM_", "GEMINI_", "OPENAI_", "OLLAMA_", "HA_",
	"ALEXA_", "BRAIN_", "TAVILY_", "TELEGRAM_", "WHISPER_",
}

// knownIgnored: reconhecidas mas não consumidas — convivência com o .env do
// projeto Python durante a migração (§3).
var knownIgnored = map[string]bool{
	"TAVILY_API_KEY":     true,
	"TELEGRAM_BOT_TOKEN": true,
	"WHISPER_MODEL":      true,
	"ALLOWED_USERS":      true,
	"BRAIN_URL":          true,
	"BRAIN_TIMEOUT_S":    true,
}

// validate concentra todas as checagens do Load (§3 e §4). Cada task seguinte
// adiciona suas regras aqui, sempre citando variável e motivo no erro.
func validate(s *Settings, v *viper.Viper) error {
	if s.LLMProvider == "" {
		return fmt.Errorf("config: LLM_PROVIDER é obrigatória")
	}
	if s.HAURL == "" {
		return fmt.Errorf("config: HA_URL é obrigatória")
	}
	if s.HAToken == "" {
		return fmt.Errorf("config: HA_TOKEN é obrigatória")
	}
	if s.AlexaMediaEntity == "" {
		return fmt.Errorf("config: ALEXA_MEDIA_ENTITY é obrigatória")
	}
	if s.BrainAPIKey == "" {
		return fmt.Errorf("config: BRAIN_API_KEY é obrigatória")
	}
	return nil
}
```

E `internal/config/validate_test.go` (marcador; a Task 3 o substitui):

```go
package config
```

- [ ] **Step 5: Rodar os testes e verificar que passam**

Run: `go test -race ./internal/config/ -v`
Expected: PASS em todos os testes da Step 2 (incluindo singleton, concorrência e erro memorizado). Se algum teste de obrigatória falhar, conferir a ordem e o formato exato `config: %s é obrigatória`.

- [ ] **Step 6: Gates**

Run: `gofmt -l . && go vet ./... && go test -race ./...`
Expected: sem saída do gofmt; vet limpo; testes verdes em todo o módulo.

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum internal/config/
git commit -m "feat(config): leitura tipada com viper, defaults e obrigatórias

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

---

### Task 2: Enum de `LLM_PROVIDER` e chaves condicionais

**Files:**
- Modify: `internal/config/validate.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Consumes: `Settings`, `carrega`, `envObrigatorias`, `dotenvDe` (Task 1); `validate(s *Settings, v *viper.Viper) error`.
- Produces: `validate` passa a rejeitar provider fora do enum (§2) com formato exato `config: LLM_PROVIDER inválido: "foo"` e a exigir `GEMINI_API_KEY`/`OPENAI_API_KEY` condicionalmente.

- [ ] **Step 1: Escrever os testes que falham** (acrescentar a `config_test.go`)

```go
func TestLoadProviderForaDoEnum(t *testing.T) { // critério 6
	vars := envObrigatorias("foo")
	_, err := carrega(t, dotenvDe(vars), nil)
	if err == nil || !strings.Contains(err.Error(), `config: LLM_PROVIDER inválido: "foo"`) {
		t.Fatalf("want erro de enum no formato da §4, veio: %v", err)
	}
}

func TestLoadChaveCondicionalGemini(t *testing.T) {
	vars := envObrigatorias("gemini")
	_, err := carrega(t, dotenvDe(vars), nil)
	if err == nil || !strings.Contains(err.Error(), "config: GEMINI_API_KEY é obrigatória") {
		t.Fatalf("gemini sem GEMINI_API_KEY: want erro, veio: %v", err)
	}
	vars["GEMINI_API_KEY"] = "gk-123"
	if _, err := carrega(t, dotenvDe(vars), nil); err != nil {
		t.Fatalf("gemini com GEMINI_API_KEY: erro inesperado: %v", err)
	}
}

func TestLoadChaveCondicionalOpenAI(t *testing.T) {
	vars := envObrigatorias("openai")
	_, err := carrega(t, dotenvDe(vars), nil)
	if err == nil || !strings.Contains(err.Error(), "config: OPENAI_API_KEY é obrigatória") {
		t.Fatalf("openai sem OPENAI_API_KEY: want erro, veio: %v", err)
	}
	vars["OPENAI_API_KEY"] = "sk-123"
	if _, err := carrega(t, dotenvDe(vars), nil); err != nil {
		t.Fatalf("openai com OPENAI_API_KEY: erro inesperado: %v", err)
	}
}
```

- [ ] **Step 2: Rodar e verificar falha**

Run: `go test ./internal/config/ -run 'ProviderForaDoEnum|ChaveCondicional' -v`
Expected: `TestLoadProviderForaDoEnum` FAIL (Task 1 aceita provider qualquer não-vazio); condicionais FAIL (não exigem chave ainda).

- [ ] **Step 3: Estender `validate` em `internal/config/validate.go`** — substituir o bloco do provider por enum + condicionais (as demais checagens ficam como estão):

```go
	switch s.LLMProvider {
	case "gemini":
		if s.GeminiAPIKey == "" {
			return fmt.Errorf("config: GEMINI_API_KEY é obrigatória quando LLM_PROVIDER=gemini")
		}
	case "openai":
		if s.OpenAIAPIKey == "" {
			return fmt.Errorf("config: OPENAI_API_KEY é obrigatória quando LLM_PROVIDER=openai")
		}
	case "ollama":
		// Motor local: não exige chave.
	default:
		if s.LLMProvider == "" {
			return fmt.Errorf("config: LLM_PROVIDER é obrigatória")
		}
		return fmt.Errorf("config: LLM_PROVIDER inválido: %q", s.LLMProvider)
	}
```

- [ ] **Step 4: Rodar e verificar passagem**

Run: `go test -race ./internal/config/ -v`
Expected: PASS (novos testes e os da Task 1 — inclusive `TestLoadObrigatoriasFaltando`, que segue citando `LLM_PROVIDER é obrigatória`).

- [ ] **Step 5: Gates + Commit**

Run: `gofmt -l . && go vet ./... && go test -race ./...`

```bash
git add internal/config/
git commit -m "feat(config): enum de LLM_PROVIDER e chaves condicionais

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

---

### Task 3: Timeouts — `time.ParseDuration` com fallback em segundos

**Files:**
- Modify: `internal/config/validate.go`
- Replace: `internal/config/validate_test.go`

**Interfaces:**
- Consumes: `validate(s *Settings, v *viper.Viper) error`, defaults `LLM_TIMEOUT_S=30s` / `HA_TIMEOUT_S=5s` (Task 1).
- Produces: `func parseTimeout(key, raw string) (time.Duration, error)` — usada por `validate`; `Settings.LLMTimeout`/`HATimeout` passam a vir preenchidos; `Settings.HAURL` continua a string crua desta task.

- [ ] **Step 1: Substituir `internal/config/validate_test.go`** pelo conteúdo real (a Task 1 o criou como marcador `package config`):

```go
package config

import (
	"strings"
	"testing"
	"time"
)

func TestParseTimeoutAceitaFormatos(t *testing.T) { // critério 5
	casos := []struct {
		raw  string
		want time.Duration
	}{
		{"30", 30 * time.Second},        // fallback numérico em segundos
		{"30.0", 30 * time.Second},      // formato do .env do Python
		{"90s", 90 * time.Second},       // time.ParseDuration
		{"1m30s", 90 * time.Second},     // ParseDuration composto
		{" 5s ", 5 * time.Second},       // tolera espaços
	}
	for _, tc := range casos {
		d, err := parseTimeout("LLM_TIMEOUT_S", tc.raw)
		if err != nil {
			t.Fatalf("parseTimeout(%q): erro inesperado: %v", tc.raw, err)
		}
		if d != tc.want {
			t.Errorf("parseTimeout(%q) = %v; want %v", tc.raw, d, tc.want)
		}
	}
}

func TestParseTimeoutRejeitaInvalidos(t *testing.T) { // critério 5
	for _, raw := range []string{"0", "-5", "-5s", "0s", "abc", ""} {
		d, err := parseTimeout("LLM_TIMEOUT_S", raw)
		if err == nil {
			t.Errorf("parseTimeout(%q) = %v; want erro", raw, d)
			continue
		}
		if !strings.Contains(err.Error(), "LLM_TIMEOUT_S") {
			t.Errorf("erro não cita a variável: %v", err)
		}
	}
}
```

- [ ] **Step 2: Rodar e verificar falha de compilação**

Run: `go test ./internal/config/ -run TestParseTimeout -v`
Expected: FAIL — `undefined: parseTimeout`.

- [ ] **Step 3: Implementar `parseTimeout` e plugar em `validate`** (acrescentar a `internal/config/validate.go`)

```go
// parseTimeout aceita duração do Go ("90s", "1m30s") e, em fallback, número
// puro em segundos ("30" ⇒ 30s; "30.0" ok — formato do .env do Python).
// Valores ≤ 0 ou lixo viram erro citando a variável (§4).
func parseTimeout(key, raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if d, err := time.ParseDuration(raw); err == nil {
		if d <= 0 {
			return 0, fmt.Errorf("config: %s deve ser > 0: %q", key, raw)
		}
		return d, nil
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("config: %s inválido: %q", key, raw)
	}
	if f <= 0 {
		return 0, fmt.Errorf("config: %s deve ser > 0: %q", key, raw)
	}
	return time.Duration(f * float64(time.Second)), nil
}
```

E, dentro de `validate` (depois das checagens de obrigatórias, antes do `return nil`):

```go
	var err error
	if s.LLMTimeout, err = parseTimeout("LLM_TIMEOUT_S", v.GetString("LLM_TIMEOUT_S")); err != nil {
		return err
	}
	if s.HATimeout, err = parseTimeout("HA_TIMEOUT_S", v.GetString("HA_TIMEOUT_S")); err != nil {
		return err
	}
```

(imports de `validate.go` passam a incluir `strconv`, `time`.)

- [ ] **Step 4: Rodar e verificar passagem + wiring no Load**

Run: `go test -race ./internal/config/ -v`
Expected: PASS. Acrescente também (em `config_test.go`) o teste de wiring abaixo antes de rodar — sem ele a task não valida o caminho end-to-end:

```go
func TestLoadTimeouts(t *testing.T) { // critério 5 no caminho do Load
	vars := envObrigatorias("ollama")
	vars["LLM_TIMEOUT_S"] = "90"
	vars["HA_TIMEOUT_S"] = "2.5"
	s, err := carrega(t, dotenvDe(vars), nil)
	if err != nil {
		t.Fatalf("Load: erro inesperado: %v", err)
	}
	if s.LLMTimeout != 90*time.Second {
		t.Errorf("LLMTimeout = %v; want 90s (\"90\" ⇒ 90s)", s.LLMTimeout)
	}
	if s.HATimeout != 2500*time.Millisecond {
		t.Errorf("HATimeout = %v; want 2.5s", s.HATimeout)
	}
}
```

(`config_test.go` precisa de `"time"` no import block.)

- [ ] **Step 5: Gates + Commit**

Run: `gofmt -l . && go vet ./... && go test -race ./...`

```bash
git add internal/config/
git commit -m "feat(config): parsing de timeouts com fallback em segundos

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

---

### Task 4: Validação de `HA_URL` e `BRAIN_PORT` (+ segredo sem vazamento)

**Files:**
- Modify: `internal/config/validate.go`
- Test: `internal/config/validate_test.go`, `internal/config/config_test.go`

**Interfaces:**
- Consumes: `validate`, `parseTimeout` (Task 3).
- Produces: `func parseURL(key, raw string) (string, error)`; `Settings.HAURL` validada (http/https), `Settings.BrainPort` parseado com `strconv.Atoi`.

- [ ] **Step 1: Escrever os testes que falham** — acrescentar a `validate_test.go`:

```go
func TestParseURL(t *testing.T) {
	if _, err := parseURL("HA_URL", "http://home.local:8123"); err != nil {
		t.Errorf("http válido rejeitado: %v", err)
	}
	if _, err := parseURL("HA_URL", "https://ha.example.com"); err != nil {
		t.Errorf("https válido rejeitado: %v", err)
	}
	for _, raw := range []string{"notaurl", "home.local:8123", "ftp://home.local"} {
		if _, err := parseURL("HA_URL", raw); err == nil {
			t.Errorf("parseURL(%q): want erro, veio nil", raw)
		}
	}
}
```

E a `config_test.go`:

```go
func TestLoadPortaDefaultEInvalida(t *testing.T) {
	s, err := carrega(t, "", envObrigatorias("ollama"))
	if err != nil {
		t.Fatalf("Load: erro inesperado: %v", err)
	}
	if s.BrainPort != 8000 {
		t.Errorf("BrainPort = %d; want default 8000", s.BrainPort)
	}
	vars := envObrigatorias("ollama")
	vars["BRAIN_PORT"] = "abc"
	if _, err := carrega(t, dotenvDe(vars), nil); err == nil ||
		!strings.Contains(err.Error(), "config: BRAIN_PORT inválido: \"abc\"") {
		t.Fatalf("BRAIN_PORT=abc: want erro no formato da §4, veio: %v", err)
	}
}

func TestLoadErroNaoVazaSecret(t *testing.T) {
	vars := envObrigatorias("ollama")
	vars["HA_URL"] = "notaurl"
	_, err := carrega(t, dotenvDe(vars), nil)
	if err == nil {
		t.Fatal("want erro de HA_URL inválida")
	}
	if strings.Contains(err.Error(), "token-ha") {
		t.Errorf("mensagem de erro vazou o secret: %v", err)
	}
}
```

- [ ] **Step 2: Rodar e verificar falha**

Run: `go test ./internal/config/ -run 'TestParseURL|TestLoadPorta|TestLoadErroNaoVazaSecret' -v`
Expected: FAIL — `undefined: parseURL`; porta: Load ainda não valida/rejeita `BRAIN_PORT=abc` (cast silencioso) e `TestLoadPortaDefaultEInvalida` falha na parte do erro.

- [ ] **Step 3: Implementar em `internal/config/validate.go`**

```go
// parseURL valida URL http(s) (paridade com AnyHttpUrl do pydantic);
// erro cita a variável (§4) e nunca o valor de secrets.
func parseURL(key, raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("config: %s inválida: %q", key, raw)
	}
	return raw, nil
}
```

E dentro de `validate`, no fim (antes de `return nil`):

```go
	if s.HAURL, err = parseURL("HA_URL", s.HAURL); err != nil {
		return err
	}
	port, err := strconv.Atoi(v.GetString("BRAIN_PORT"))
	if err != nil {
		return fmt.Errorf("config: BRAIN_PORT inválido: %q", v.GetString("BRAIN_PORT"))
	}
	s.BrainPort = port
```

(Reorganizar `validate` para declarar `err` uma única vez; importar `net/url`. Nota deliberada: a spec valida ≤ 0 só para timeouts — `BRAIN_PORT` usa apenas `Atoi`, sem faixa adicional.)

- [ ] **Step 4: Rodar e verificar passagem**

Run: `go test -race ./internal/config/ -v`
Expected: PASS em tudo.

- [ ] **Step 5: Gates + Commit**

Run: `gofmt -l . && go vet ./... && go test -race ./...`

```bash
git add internal/config/
git commit -m "feat(config): validação de HA_URL e BRAIN_PORT

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

---

### Task 5: Varredura estrita de namespaces e conhecidas-ignoradas (§3)

**Files:**
- Modify: `internal/config/validate.go`
- Modify: `internal/config/config.go` (chamada da varredura em `load`)
- Test: `internal/config/config_test.go`

**Interfaces:**
- Consumes: `load()` (Task 1); `appNamespaces` e `knownIgnored` (declarados em `validate.go` desde a Task 1).
- Produces: `var recognizedKeys map[string]bool`, `func unknownAppVar(v *viper.Viper) (string, bool)`.

- [ ] **Step 1: Escrever os testes que falham** (acrescentar a `config_test.go`)

```go
func TestLoadRejeitaDesconhecidaNoDotenv(t *testing.T) { // critério 1
	_, err := carrega(t, dotenvBase+"HA_TIMEOT_S=5s\n", nil)
	if err == nil {
		t.Fatal("Load: want erro para var desconhecida no .env")
	}
	if !strings.Contains(err.Error(), "config: variável desconhecida do app: HA_TIMEOT_S") {
		t.Errorf("erro deve citar a variável no formato da §4: %v", err)
	}
}

func TestLoadRejeitaDesconhecidaNoAmbiente(t *testing.T) {
	vars := envObrigatorias("ollama")
	vars["TAVILY_SECRET"] = "x"
	_, err := carrega(t, "", vars)
	if err == nil || !strings.Contains(err.Error(), "config: variável desconhecida do app: TAVILY_SECRET") {
		t.Fatalf("want erro citando TAVILY_SECRET, veio: %v", err)
	}
}

func TestLoadAceitaConhecidasIgnoradas(t *testing.T) { // critério 2
	dotenv := dotenvBase + `TAVILY_API_KEY=tvly-xxx
TELEGRAM_BOT_TOKEN=123:abc
WHISPER_MODEL=small
ALLOWED_USERS=11111,22222
BRAIN_URL=http://localhost:8000
BRAIN_TIMEOUT_S=90.0
`
	s, err := carrega(t, dotenv, nil)
	if err != nil {
		t.Fatalf("conhecidas-ignoradas não devem causar erro: %v", err)
	}
	if s.HAToken != "token-secreto" {
		t.Errorf("Load básico quebrou: %+v", s)
	}
}

func TestLoadIgnoraForaDosNamespaces(t *testing.T) { // §3: sistema não bloqueia
	vars := envObrigatorias("ollama")
	vars["PATH"] = os.Getenv("PATH") // já presente, mas explícito
	vars["LLMXX_SEM_PREFIXO"] = "x"  // não casa com LLM_ (prefixo exato)
	if _, err := carrega(t, "", vars); err != nil {
		t.Fatalf("vars fora dos namespaces não devem bloquear: %v", err)
	}
}
```

- [ ] **Step 2: Rodar e verificar falha**

Run: `go test ./internal/config/ -run 'Desconhecida|ConhecidasIgnoradas|ForaDosNamespaces' -v`
Expected: `TestLoadRejeitaDesconhecidaNoDotenv`/`...NoAmbiente` FAIL (nada rejeita ainda); `TestLoadAceitaConhecidasIgnoradas` e `TestLoadIgnoraForaDosNamespaces` tendem a PASSAR acidentalmente — a checagem real de regressão deles é a Task 6 de revisão; o FAIL dos dois primeiros é o sinal de TDD desta task.

- [ ] **Step 3: Implementar a varredura** — `appNamespaces` e `knownIgnored` já existem desde a Task 1; acrescentar a `internal/config/validate.go` apenas:

```go
// recognizedKeys são as chaves que o Load consome (§2).
var recognizedKeys = map[string]bool{
	"LLM_PROVIDER": true, "LLM_MODEL": true,
	"GEMINI_API_KEY": true, "OPENAI_API_KEY": true, "OPENAI_API_BASE": true,
	"OLLAMA_BASE_URL": true, "OLLAMA_MODEL": true, "LLM_TIMEOUT_S": true,
	"HA_URL": true, "HA_TOKEN": true, "HA_TIMEOUT_S": true,
	"ALEXA_MEDIA_ENTITY": true, "BRAIN_API_KEY": true, "BRAIN_PORT": true,
}

// unknownAppVar varre o ambiente do processo e as chaves do `.env` (viper) e
// devolve a primeira variável cujo prefixo pertence a um namespace do app e
// cujo nome não é reconhecido (§3). Matching case-insensitive, em paridade
// com case_sensitive=False do pydantic-settings.
func unknownAppVar(v *viper.Viper) (string, bool) {
	check := func(name string) (string, bool) {
		name = strings.ToUpper(name)
		if recognizedKeys[name] || knownIgnored[name] {
			return "", false
		}
		for _, ns := range appNamespaces {
			if strings.HasPrefix(name, ns) {
				return name, true
			}
		}
		return "", false
	}
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if u, bad := check(name); bad {
			return u, true
		}
	}
	// AllKeys cobre o `.env` (viper guarda as chaves em minúsculas; AllKeys
	// também inclui os defaults, todos reconhecidos).
	for _, key := range v.AllKeys() {
		if u, bad := check(key); bad {
			return u, true
		}
	}
	return "", false
}
```

E em `load()` (`internal/config/config.go`), logo após a leitura do `.env` (antes de montar `s`):

```go
	// Varredura estrita (§3): var de namespace do app não reconhecida → erro.
	if unknown, ok := unknownAppVar(v); ok {
		return Settings{}, fmt.Errorf("config: variável desconhecida do app: %s", unknown)
	}
```

(`validate.go` importa `os`.)

- [ ] **Step 4: Rodar e verificar passagem**

Run: `go test -race ./internal/config/ -v`
Expected: PASS em tudo (os testes das tasks anteriores continuam verdes: `clearAppEnv` já limpa os namespaces, então nenhum teste herda var de app do processo do `go test`).

- [ ] **Step 5: Gates + Commit**

Run: `gofmt -l . && go vet ./... && go test -race ./...`

```bash
git add internal/config/
git commit -m "feat(config): varredura estrita de namespaces e conhecidas-ignoradas

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

---

### Task 6: Precedência do modelo — `ResolvedModel`

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Consumes: `Settings` (campos `LLMProvider`, `LLMModel`, `OllamaModel` com default `llama3.2:3b`).
- Produces: `func (s Settings) ResolvedModel() string` — `LLM_MODEL` > `OLLAMA_MODEL` (só para ollama) > `""` (default de gemini/openai entra na spec 02).

- [ ] **Step 1: Escrever os testes que falham** (acrescentar a `config_test.go`)

```go
func TestLoadPrecedenciaModelo(t *testing.T) { // critério 4
	casos := []struct {
		nome string
		env  map[string]string
		want string
	}{
		{"LLM_MODEL vence sobre OLLAMA_MODEL (ollama)",
			comModelo("ollama", "gpt-x", "llama-velho"), "gpt-x"},
		{"sem LLM_MODEL, OLLAMA_MODEL vale (ollama)", comModelo("ollama", "", "llama-2"), "llama-2"},
		{"sem ambos, default da §2 (ollama)", comModelo("ollama", "", ""), "llama3.2:3b"},
		{"gemini sem LLM_MODEL → \"\" (default do provedor é da spec 02)", comModelo("gemini", "", ""), ""},
		{"gemini com LLM_MODEL", comModelo("gemini", "gemini-custom", ""), "gemini-custom"},
	}
	for _, tc := range casos {
		s, err := carrega(t, "", tc.env)
		if err != nil {
			t.Fatalf("%s: Load: erro inesperado: %v", tc.nome, err)
		}
		if got := s.ResolvedModel(); got != tc.want {
			t.Errorf("%s: ResolvedModel() = %q; want %q", tc.nome, got, tc.want)
		}
	}
}

// comModelo clona as obrigatórias com o provider e os modelos informados
// (strings vazias = variável ausente).
func comModelo(provider, llmModel, ollamaModel string) map[string]string {
	vars := envObrigatorias(provider)
	if llmModel != "" {
		vars["LLM_MODEL"] = llmModel
	}
	if ollamaModel != "" {
		vars["OLLAMA_MODEL"] = ollamaModel
	}
	return vars
}
```

- [ ] **Step 2: Rodar e verificar falha de compilação**

Run: `go test ./internal/config/ -run TestLoadPrecedenciaModelo -v`
Expected: FAIL — `s.ResolvedModel undefined`.

- [ ] **Step 3: Implementar em `internal/config/config.go`** (logo abaixo do tipo `Settings`)

```go
// ResolvedModel devolve o modelo efetivo (§2): LLM_MODEL > OLLAMA_MODEL
// (provider ollama) > default do provedor. Para gemini/openai o default do
// provedor é definido na spec 02 — até lá devolve "".
func (s Settings) ResolvedModel() string {
	if s.LLMModel != "" {
		return s.LLMModel
	}
	if s.LLMProvider == "ollama" {
		return s.OllamaModel
	}
	return ""
}
```

- [ ] **Step 4: Rodar e verificar passagem**

Run: `go test -race ./internal/config/ -v`
Expected: PASS em tudo.

- [ ] **Step 5: Gates + Commit**

Run: `gofmt -l . && go vet ./... && go test -race ./...`

```bash
git add internal/config/
git commit -m "feat(config): ResolvedModel com precedência LLM_MODEL/OLLAMA_MODEL

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

---

### Task 7: Gates finais da branch

**Files:**
- Modify (apenas se algum gate acusar): qualquer arquivo de `internal/config/`, `go.mod`, `go.sum`.

**Interfaces:**
- Consumes: branch completa (Tasks 1–6).
- Produces: branch pronta para a revisão final do subagent-driven-development.

- [ ] **Step 1: Gates completos no módulo inteiro**

```bash
gofmt -l .
go vet ./...
go mod tidy && git diff --exit-code go.mod go.sum
go test -race ./... -count=1
go build ./...
```

Expected: gofmt sem saída; vet limpo; `go.mod`/`go.sum` estáveis pós-tidy; testes verdes; build ok.

- [ ] **Step 2: Conferir que nenhum secret entra em mensagem de erro** — reler `internal/config/validate.go` e `config.go`: todo `fmt.Errorf` cita apenas nome de variável (e valores não-secretos como enum/URL/porta). Não há `log` no package além de `MustLoad` → `fatalf`.

- [ ] **Step 3: Commit apenas se algum gate exigiu correção**

```bash
git add -A && git commit -m "chore(config): ajustes dos gates finais

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

(se nada mudou, nenhuma commit nesta task)
