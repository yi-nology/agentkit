package acpx

import "testing"

func TestNewAgentNames(t *testing.T) {
	cases := []struct {
		a    Agent
		want string
	}{
		{NewKimi(), "kimi"},
		{NewQwen(), "qwen"},
		{NewGemini(), "gemini"},
		{NewMimo(), "mimo"},
		{NewMinimax(), "minimax"},
	}
	for _, c := range cases {
		if c.a.Name() != c.want {
			t.Errorf("Name = %q, want %q", c.a.Name(), c.want)
		}
	}
}
