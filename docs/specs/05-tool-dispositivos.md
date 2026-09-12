# Spec 05 — Tool controle de dispositivos (F06)

Fonte Python: `src/tools/home.py` · Package Go: `internal/tools` (`control_device`)

---

## 1. Objetivo

Ligar, desligar ou alternar um dispositivo do Home Assistant, com **validação estrita do
`entity_id` antes de tocar no HA** (baseline de segurança item 3) e confirmação falável
via apelidos. TTS **não** vive aqui (ADR-0002 — `speak` é passo do fluxo, spec 06).

## 2. Contrato da tool

- **Nome**: `control_device` · **Descrição** (pt): "Liga, desliga ou alterna um dispositivo do Home Assistant."
- **Input**: `{ "action": "on"|"off"|"toggle", "entity_id": switch.tomada_sala|switch.tomada_quarto|light.luz_sala|light.luz_quarto|media_player.alexa_sala }` — os dois enums ficam fixados no JSON Schema da tool; desvio é rejeitado pelo Genkit no limite da tool (issue #13).
- **Output**: `string` — confirmação ou mensagem de erro falável.

## 3. Validação de `entity_id` (antes do HA)

- Duas camadas (issue #13): o schema da tool fixa o enum das entidades da casa (§5), e a validação do núcleo aceita apenas as entidades do mapa de apelidos — lista fechada v1. Um entity com prefixo válido mas fora da casa (p. ex. tomada alucinada como `light.tomada_sala`) dispararia `POST /api/services/light/`, que o HA responde com 200 mesmo sem o entity — o erro passaria silencioso. A lista fechada impede o serviço do domínio errado.
- Na borda Genkit, desvio do schema → rejeição com erro antes do núcleo. No núcleo, inválido → **não chama o HA**; loga warning e retorna a frase de fallback.

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

Entity fora da lista é rejeitado na validação (§3) — não aciona o HA.

- `on` → "Liguei o {apelido}." · `off` → "Desliguei o {apelido}." · `toggle` → "Alternei o {apelido}."
- Fallback: "Não consegui acionar o dispositivo."

## 6. Critérios de aceite (testes unitários, `httptest`)

1. `on` em `switch.tomada_sala` → `POST /api/services/switch/turn_on` + "Liguei o tomada da sala."
2. `off` em `light.luz_sala` → `/api/services/light/turn_off` + "Desliguei o luz da sala."
3. `toggle` em `switch.tomada_quarto` → `/api/services/homeassistant/toggle` + "Alternei o tomada do quarto."
4. `entity_id` = `camera.frente` → nenhum request ao HA + fallback (núcleo); pela superfície Genkit, rejeitada no schema.
5. `entity_id` sem ponto / vazio → fallback.
6. HA responde 500 → fallback (sem panico).
7. Issue #13: `entity_id` = `light.tomada_sala` (tomada alucinada) → nenhum request ao HA + fallback.
8. O enum do `entity_id` no schema da tool e o mapa de apelidos (§5) são idênticos — drift travado por teste.
