package conversation

import (
	"strings"
	"testing"
)

func mkTurns(n int) []Turn {
	turns := make([]Turn, 0, n)
	for i := 0; i < n; i++ {
		turns = append(turns, Turn{Question: "q", Answer: "a"})
	}
	return turns
}

func TestSplit(t *testing.T) {
	recent, evicted := Split(mkTurns(9), 6)
	if len(recent) != 6 || len(evicted) != 3 {
		t.Fatalf("9 轮 keep6 应切 6/3: %d/%d", len(recent), len(evicted))
	}
	recent, evicted = Split(mkTurns(6), 6)
	if len(recent) != 6 || evicted != nil {
		t.Fatalf("恰好 6 轮无淘汰: %d %d", len(recent), len(evicted))
	}
	recent, evicted = Split(mkTurns(3), 0)
	if len(recent) != 3 || evicted != nil {
		t.Fatalf("keep<=0 应全 verbatim: %d %d", len(recent), len(evicted))
	}
}

func TestRenderTruncates(t *testing.T) {
	long := strings.Repeat("长", 500)
	out := Render([]Turn{{Question: "q", Answer: long}})
	if strings.Count(out, "长") > AnswerCap+1 {
		t.Fatalf("回答应截断: %d", strings.Count(out, "长"))
	}
	if !strings.Contains(out, "用户: q") {
		t.Fatalf("问题应保留: %q", out)
	}
}

func TestCombine(t *testing.T) {
	out := Combine("早期结论 X", mkTurns(1))
	if !strings.Contains(out, "更早对话摘要") || !strings.Contains(out, "早期结论 X") {
		t.Fatalf("摘要应在头部: %q", out)
	}
	if out := Combine("", nil); out != "" {
		t.Fatalf("空会话应空注入: %q", out)
	}
}
