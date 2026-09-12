package tools

import (
	"testing"
)

func TestValidEntityID(t *testing.T) { // §3: apenas switch/light/media_player + sufixo não vazio
	casos := []struct {
		entity string
		want   bool
	}{
		{"switch.tomada_sala", true},
		{"light.luz_sala", true},
		{"media_player.alexa_sala", true},
		{"switch.tomada.sala", true}, // sufixo não vazio mesmo com ponto extra
		{"camera.frente", false},     // prefixo fora da lista (critério 4)
		{"fan.quarto", false},
		{"tomada", false},  // sem ponto (critério 5)
		{"", false},        // vazio (critério 5)
		{"switch.", false}, // sufixo vazio
		{".tomada", false}, // domínio vazio
	}
	for _, tc := range casos {
		if got := validEntityID(tc.entity); got != tc.want {
			t.Errorf("validEntityID(%q) = %v; want %v", tc.entity, got, tc.want)
		}
	}
}

func TestValidAction(t *testing.T) { // §2: enum estrito on|off|toggle
	casos := []struct {
		acao string
		want bool
	}{
		{"on", true},
		{"off", true},
		{"toggle", true},
		{"ON", false},  // sem normalização
		{"on ", false}, // sem trim
		{"ligar", false},
		{"", false},
	}
	for _, tc := range casos {
		if got := validAction(tc.acao); got != tc.want {
			t.Errorf("validAction(%q) = %v; want %v", tc.acao, got, tc.want)
		}
	}
}

func TestAliasOf(t *testing.T) { // §5: mapa em memória; desconhecido → entity cru
	casos := []struct{ entity, want string }{
		{"switch.tomada_sala", "tomada da sala"},
		{"switch.tomada_quarto", "tomada do quarto"},
		{"light.luz_sala", "luz da sala"},
		{"light.luz_quarto", "luz do quarto"},
		{"media_player.alexa_sala", "Alexa da sala"},
		{"switch.ventilador", "switch.ventilador"}, // desconhecido → entity_id cru
	}
	for _, tc := range casos {
		if got := aliasOf(tc.entity); got != tc.want {
			t.Errorf("aliasOf(%q) = %q; want %q", tc.entity, got, tc.want)
		}
	}
}

func TestConfirmationPhrase(t *testing.T) { // §5: frases fixas por ação
	casos := []struct {
		acao, apelido, want string
	}{
		{"on", "tomada da sala", "Liguei o tomada da sala."},
		{"off", "luz da sala", "Desliguei o luz da sala."},
		{"toggle", "switch.ventilador", "Alternei o switch.ventilador."},
		{"reboot", "tomada da sala", fallbackControlDevice}, // defesa: ação inválida nunca chega aqui
	}
	for _, tc := range casos {
		if got := confirmationPhrase(tc.acao, tc.apelido); got != tc.want {
			t.Errorf("confirmationPhrase(%q, %q) = %q; want %q", tc.acao, tc.apelido, got, tc.want)
		}
	}
}
