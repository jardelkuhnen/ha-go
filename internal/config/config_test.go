package config

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
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
	// Adiciona chaves obrigatórias dos provedores
	if provider == "gemini" {
		vars["GEMINI_API_KEY"] = "gk-test"
	}
	if provider == "openai" {
		vars["OPENAI_API_KEY"] = "sk-test"
	}
	if llmModel != "" {
		vars["LLM_MODEL"] = llmModel
	}
	if ollamaModel != "" {
		vars["OLLAMA_MODEL"] = ollamaModel
	}
	return vars
}

func TestLoadDotenvMalformado(t *testing.T) {
	_, err := carrega(t, "SEM_IGUALDADE_AQUI\n", envObrigatorias("ollama"))
	if err == nil || !strings.Contains(err.Error(), "config: lendo .env:") {
		t.Fatalf("want erro de .env malformado com prefixo config:, veio: %v", err)
	}
}
