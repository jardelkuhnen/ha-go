# Spec 04 — Tool clima: Open-Meteo (F04)

Fonte Python: `src/tools/weather.py` · Package Go: `internal/tools` (`get_weather`)

---

## 1. Objetivo

Previsão do tempo em **frase curta falável**, via Open-Meteo (geocoding + forecast).
Tool Genkit registrada com `genkit.DefineTool` — input/output tipados, disponível ao
motor cognitivo em todos os canais.

## 2. Contrato da tool

- **Nome**: `get_weather` · **Descrição** (para o LLM, pt): "Retorna a previsão do tempo
  para uma cidade, em frase curta para voz."
- **Input**: `{ "location": string }` — nome da cidade (ex.: "São Paulo").
- **Output**: `string` — frase em pt-BR.

## 3. Fluxo interno

1. **Geocoding** — `GET https://geocoding-api.open-meteo.com/v1/search?name={location}&count=1&language=pt`
   → `results[0].latitude/longitude`. Sem resultados → fallback "Não encontrei essa cidade."
2. **Forecast** — `GET https://api.open-meteo.com/v1/forecast` com
   `daily=temperature_2m_max,temperature_2m_min,weather_code`, `current=weather_code`,
   `forecast_days=1`, `timezone=auto`.
3. **Frase** — "Máxima de {max}, mínima de {min}" + ", {condição}" quando houver;
   temperaturas arredondadas. `max/min` ausentes → fallback.

## 4. Tradução WMO (mapa parcial, paridade com o Python)

`0 céu limpo · 1 predominantemente limpo · 2 parcialmente nublado · 3 nublado ·
45 neblina · 48 neblina com geada · 51/53/55 garoa leve/moderada/intensa ·
61/63/65 chuva leve/moderada/forte · 71/73/75 neve fraca/moderada/intensa ·
80/81/82 pancadas de chuva (moderadas/fortes) · 95 tempestade ·
96 tempestade com granizo · 99 tempestade severa com granizo`

Código atual (`current.weather_code`) tem prioridade; senão o primeiro código do dia;
desconhecido → sem condição na frase.

## 5. Design defensivo (paridade F04)

- **Nunca propaga erro** ao motor — qualquer falha vira frase de fallback:
  - cidade não encontrada → "Não encontrei essa cidade."
  - falha de rede/HTTP/timeout → "Não consegui obter o clima agora."
  - resposta sem dados → "Não consegui obter o clima agora."
- **Timeout próprio de 5s** por chamada HTTP (independente do `LLM_TIMEOUT_S`).
- URLs de geocoding e forecast **injetáveis** (decisão 12) para os mocks de teste.

## 6. Critérios de aceite (testes unitários, `httptest`)

1. Geocoding com resultado + forecast com max/min/código → "Máxima de 28, mínima de 19, pancadas de chuva".
2. Geocoding sem `results` → "Não encontrei essa cidade." (e forecast nem é chamado).
3. Forecast sem temperaturas → fallback de falha.
4. Erro de rede no geocoding → fallback de falha.
5. Código WMO `current` vence o `daily`; código desconhecido → frase sem condição.