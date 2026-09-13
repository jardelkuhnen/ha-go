// Package ha implementa o client HTTP do Home Assistant (paridade com
// src/services/ha_client.py do projeto Python): acionamento de serviços,
// toggle, turn_on/turn_off, leitura de estados e a síntese de voz via Alexa
// Media Player (notify.alexa_media). O Speak é o mecanismo de TTS do passo
// terminal do fluxo (ADR-0002): TTS não é tool e nunca propaga erro.
package ha

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"home-assistent-go/internal/config"
)

// Error é o erro retornado pelo client em falhas de chamada ao HA (§5).
// Status é o código HTTP quando houve resposta; 0 em falha de rede, timeout
// ou cancelamento do ctx. A mensagem nunca contém o token (sanitize).
type Error struct {
	Status int
	Op     string // operação: "CallService", "GetState", "Speak", …
	Err    error
}

func (e *Error) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("ha: %s: HTTP %d: %v", e.Op, e.Status, e.Err)
	}
	return fmt.Sprintf("ha: %s: %v", e.Op, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

// Client é o client HTTP do Home Assistant. Os campos são injetáveis nos
// testes (decisão 12); em produção construa com NewClient.
type Client struct {
	baseURL          string // ex.: "http://home.local:8123", sem barra final
	token            string // secret — nunca em log nem em mensagem de erro
	alexaMediaEntity string // media_player alvo do notify.alexa_media
	http             *http.Client
}

// NewClient cria o client a partir das configurações carregadas (spec 01):
// um único http.Client com timeout HA_TIMEOUT_S, que vale para toda chamada
// (baseline item 2) — corrige a criação por-chamada do Python (decisão 7).
func NewClient(settings config.Settings) *Client {
	return &Client{
		baseURL:          strings.TrimRight(settings.HAURL, "/"),
		token:            settings.HAToken,
		alexaMediaEntity: settings.AlexaMediaEntity,
		http:             &http.Client{Timeout: settings.HATimeout},
	}
}

// Close libera as conexões ociosas do pool (graceful shutdown, §2).
func (c *Client) Close() { c.http.CloseIdleConnections() }

// CallService faz o POST /api/services/{domain}/{service} (§3). Corpo é {}
// quando serviceData é nil. A resposta JSON do HA vem como map: quando o
// corpo não é um objeto (p. ex. a lista de estados modificados), vem
// embrulhado como {"result": …} — paridade com ha_client.py.
func (c *Client) CallService(ctx context.Context, domain, service string, serviceData map[string]any) (map[string]any, error) {
	data := serviceData
	if data == nil {
		data = map[string]any{}
	}
	resp, err := c.do(ctx, "CallService", http.MethodPost, servicePath(domain, service), data)
	if err != nil {
		return nil, err
	}
	defer drainClose(resp)
	if !okStatus(resp.StatusCode) {
		return nil, statusError("CallService", resp)
	}
	m, err := decodeState(resp.Body)
	if err != nil {
		return nil, &Error{Op: "CallService", Err: err}
	}
	return m, nil
}

// Toggle aciona homeassistant.toggle para o entity (§3). Usa
// homeassistant.toggle (não o domínio do entity) — comportamento da spec §3.
func (c *Client) Toggle(ctx context.Context, entityID string) (map[string]any, error) {
	return c.CallService(ctx, "homeassistant", "toggle", map[string]any{"entity_id": entityID})
}

// TurnOn liga o entity, com o domínio extraído do prefixo antes do primeiro
// "." (§3). O entity_id é esperado validado a montante (spec 05).
func (c *Client) TurnOn(ctx context.Context, entityID string) (map[string]any, error) {
	return c.CallService(ctx, domainOf(entityID), "turn_on", map[string]any{"entity_id": entityID})
}

// TurnOff desliga o entity, com o domínio extraído do prefixo antes do
// primeiro "." (§3). O entity_id é esperado validado a montante (spec 05).
func (c *Client) TurnOff(ctx context.Context, entityID string) (map[string]any, error) {
	return c.CallService(ctx, domainOf(entityID), "turn_off", map[string]any{"entity_id": entityID})
}

// domainOf devolve o prefixo do entity_id antes do primeiro "." — paridade
// com entity_id.split(".", 1)[0] do Python (sem ".", devolve o próprio entity).
func domainOf(entityID string) string {
	domain, _, _ := strings.Cut(entityID, ".")
	return domain
}

// GetState faz o GET /api/states/{entity_id} (§3). entityID vazio faz o HA
// devolver a tabela inteira de estados — valide o id a montante (spec 05).
func (c *Client) GetState(ctx context.Context, entityID string) (map[string]any, error) {
	resp, err := c.do(ctx, "GetState", http.MethodGet, "/api/states/"+url.PathEscape(entityID), nil)
	if err != nil {
		return nil, err
	}
	defer drainClose(resp)
	if !okStatus(resp.StatusCode) {
		return nil, statusError("GetState", resp)
	}
	m, err := decodeState(resp.Body)
	if err != nil {
		return nil, &Error{Op: "GetState", Err: err}
	}
	return m, nil
}

// logCliente emite os logs do client no formato da timeline do motor
// ("HH:MM:SS | Agente | ação | detalhes") — logger próprio sem as flags
// padrão do stdlib, que incluiriam data e duplicariam o horário da linha.
var logCliente = log.New(os.Stderr, "", 0)

// do monta a requisição com os headers padrão (§2) e a envia. Quem chama é
// responsável por drainClose(resp). Erros preservam a cadeia original (%w) —
// quem consome pode usar errors.Is(err, context.DeadlineExceeded) e
// errors.As(err, &net.Error); o token não entra em nenhuma mensagem porque só
// existe no header (nunca ecoado) e a extração de string no Speak passa por
// sanitize. Toda requisição executada gera um log de diagnóstico no formato
// da timeline (paridade com o Python): método, path e status — ou a falha de
// transporte. O path nunca contém o token e o corpo não é logado.
func (c *Client) do(ctx context.Context, op, method, path string, payload any) (*http.Response, error) {
	var body io.Reader
	if payload != nil {
		buf, err := json.Marshal(payload)
		if err != nil {
			return nil, &Error{Op: op, Err: fmt.Errorf("serializando corpo: %w", err)}
		}
		body = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.url(path), body)
	if err != nil {
		return nil, &Error{Op: op, Err: err}
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		logCliente.Printf("%s | %-14s | Requisição executada | method: %s | path: %s | erro: %v",
			time.Now().Format("15:04:05"), "HA Client", method, path, err)
		return nil, &Error{Op: op, Err: err}
	}
	logCliente.Printf("%s | %-14s | Requisição executada | method: %s | path: %s | status: %d",
		time.Now().Format("15:04:05"), "HA Client", method, path, resp.StatusCode)
	return resp, nil
}

// url junta baseURL e path.
func (c *Client) url(path string) string { return c.baseURL + path }

// sanitize remove o token de qualquer mensagem de erro (§5) — defensivo:
// nenhum caminho de erro pode ecoar o secret.
func (c *Client) sanitize(msg string) string {
	if c.token == "" {
		return msg
	}
	return strings.ReplaceAll(msg, c.token, "[token]")
}

// okStatus reporta sucesso 2xx.
func okStatus(status int) bool { return status >= 200 && status <= 299 }

// statusError constrói o *Error para um status ≠ 2xx. A mensagem carrega
// apenas status e motivo — nunca o corpo nem o token (§5).
func statusError(op string, resp *http.Response) *Error {
	txt := http.StatusText(resp.StatusCode)
	if txt == "" {
		txt = "status inesperado"
	}
	return &Error{Status: resp.StatusCode, Op: op, Err: errors.New(txt)}
}

// drainClose drena o corpo (limitado, para reuso da conexão keep-alive) e o
// fecha.
func drainClose(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
}

// servicePath monta o caminho de /api/services com escape de path.
func servicePath(domain, service string) string {
	return "/api/services/" + url.PathEscape(domain) + "/" + url.PathEscape(service)
}

// decodeState decodifica o corpo JSON como map; quando não é um objeto,
// embrulha em {"result": …} — paridade com ha_client.py.
func decodeState(r io.Reader) (map[string]any, error) {
	var raw any
	if err := json.NewDecoder(r).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decodificando resposta JSON: %w", err)
	}
	if m, ok := raw.(map[string]any); ok {
		return m, nil
	}
	return map[string]any{"result": raw}, nil
}
