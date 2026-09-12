# Spec 05 — Tool controle de dispositivos (F06)

Fonte Python: `src/tools/home.py` · Package Go: `internal/tools` (`control_device`)

---

## 1. Objetivo

Ligar, desligar ou alternar um dispositivo do Home Assistant, com **validação estrita do
`entity_id` antes de tocar no HA** (baseline de segurança item 3) e confirmação falável
via apelidos. TTS **não** vive aqui (ADR-0002 — `speak` é passo do fluxo, spec 06).

## 2. Contrato da tool

- **Nome**: `control_device` · **Descrição** (pt): "Liga, desliga ou alterna um dispositivo do Home Assistant."
- **Input**: `{ "action": "on"|"off"|"toggle", "entity_id": string }`
- **Output**: `string` — confirmação ou mensagem de erro falável.

## 3. Validação de `entity_id` (antes do HA)

- Aceita apenas prefixos `switch.`, `light.`, `media_player.` seguidos de sufixo não vazio.
- Inválido → **não chama o HA**; loga warning e retorna a frase de fallback.

## 4. Execução (via `*ha.Client` injetado — decisão 7)

| action | Chamada |
|--------|---------|
| `on` | `ha.TurnOn(entityID)` → `POST {domínio}/turn_on` |
| `off` | `ha.TurnOff(entityID)` → `POST {domínio}/turn_off` |
| `toggle` | `ha.Toggle(entityID)` → `POST homeassistant/toggle` |

Falha de rede/HTTP → fallback. Sucesso → confirmação com apelido.

## 5. Apelidos amigáveis (mapa em memória, v1)

```text
switch.tomada_sala   → "tomada da sala"
switch.tomada_quarto → "tomada do quarto"
light.luz_sala       → "luz da sala"
light.luz_quarto     → "luz do quarto"
media_player.alexa_sala → "Alexa da sala"
```

Entity desconhecido → usa o `entity_id` cru na frase.

- `on` → "Liguei o {apelido}." · `off` → "Desliguei o {apelido}." · `toggle` → "Alternei o {apelido}."
- Fallback: "Não consegui acionar o dispositivo."

## 6. Critérios de aceite (testes unitários, `httptest`)

1. `on` em `switch.tomada_sala` → `POST /api/services/switch/turn_on` + "Liguei o tomada da sala."
2. `off` em `light.luz_sala` → `/api/services/light/turn_off` + "Desliguei o luz da sala."
3. `toggle` em entity sem apelido → `/api/services/homeassistant/toggle` + "Alternei o {entity_id}."
4. `entity_id` = `camera.frente` → nenhum request ao HA + fallback.
5. `entity_id` sem ponto / vazio → fallback.
6. HA responde 500 → fallback (sem panico).