package dispatch

import (
	"errors"
	"testing"
)

type fakeEdges map[string][]string

func (f fakeEdges) Slugs() []string {
	out := make([]string, 0, len(f))
	for slug := range f {
		out = append(out, slug)
	}
	return out
}
func (f fakeEdges) DispatchAllow(slug string) []string { return f[slug] }

func TestGuardAssert(t *testing.T) {
	g := NewGuard(fakeEdges{"a": {"b"}, "b": {}}, 3)
	if err := g.Assert("a", "b", 0); err != nil {
		t.Fatalf("allow 边应放行: %v", err)
	}
	var deny *DenyError
	if err := g.Assert("a", "c", 0); !errors.As(err, &deny) || deny.Reason != "not_allowed" {
		t.Fatalf("未登记边应 not_allowed: %v", err)
	}
	if err := g.Assert("a", "a", 0); !errors.As(err, &deny) || deny.Reason != "self_dispatch" {
		t.Fatalf("自派发应 self_dispatch: %v", err)
	}
	if err := g.Assert("a", "b", 3); !errors.As(err, &deny) || deny.Reason != "depth_exceeded" {
		t.Fatalf("深度触顶应 depth_exceeded: %v", err)
	}
	if err := g.Assert("x", "b", 0); !errors.As(err, &deny) || deny.Reason != "not_allowed" {
		t.Fatalf("未登记 slug 应 not_allowed: %v", err)
	}
	if err := g.AssertDepth(2); err != nil {
		t.Fatalf("深度内应放行: %v", err)
	}
	if !g.Allowed("a", "b") || g.Allowed("b", "a") || g.Allowed("a", "x") {
		t.Fatal("Allowed 单向：登记边为 true、反向与未登记为 false")
	}
}
