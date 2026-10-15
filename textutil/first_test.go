package textutil

import "testing"

func TestFirstNonEmpty(t *testing.T) {
	if got := FirstNonEmpty("", "a", "b"); got != "a" {
		t.Fatalf("got %q", got)
	}
	if got := FirstNonEmpty("", "", "b"); got != "b" {
		t.Fatalf("got %q", got)
	}
	if got := FirstNonEmpty(); got != "" {
		t.Fatalf("空参应返回空串, got %q", got)
	}
}
