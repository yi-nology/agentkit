package textutil

import "testing"

func TestStripCodeFence(t *testing.T) {
	cases := []struct{ in, want string }{
		{"```json\n{\"a\":1}\n```", `{"a":1}`},
		{"```\n{\"a\":1}\n```", `{"a":1}`},
		{`{"a":1}`, `{"a":1}`},
		{"  {\"a\":1}  ", `{"a":1}`},
	}
	for _, c := range cases {
		if got := StripCodeFence(c.in); got != c.want {
			t.Errorf("StripCodeFence(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
