package compact

import "testing"

func TestRepairInterrupted(t *testing.T) {
	// 调用有结果：原样返回
	intact := []Message{
		{Role: "user", Text: "hi"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "a", Name: "ls"}}},
		{Role: "tool", ToolCallID: "a", Text: "out"},
	}
	if got := RepairInterrupted(intact); len(got) != len(intact) {
		t.Fatalf("无洞不应改动: %d", len(got))
	}
	// 中断洞：调用无结果 → 补占位
	broken := []Message{
		{Role: "user", Text: "hi"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "a", Name: "ls"}, {ID: "b", Name: "grep"}}},
		{Role: "tool", ToolCallID: "a", Text: "out"}, // b 中断未落盘
		{Role: "assistant", Text: "结论"},
	}
	got := RepairInterrupted(broken)
	// 补洞后：b 的占位结果在第二个 assistant 之前
	var repaired []Message
	for _, m := range got {
		if m.Role == "tool" && m.Text == InterruptedToolResult {
			repaired = append(repaired, m)
		}
	}
	if len(repaired) != 1 || repaired[0].ToolCallID != "b" {
		t.Fatalf("应只补 b: %+v", repaired)
	}
	if len(got) != len(broken)+1 {
		t.Fatalf("长度: %d", len(got))
	}
	// 原切片不变
	if len(broken) == len(got) {
		t.Fatal("原切片被改")
	}
}

func TestRepairInterruptedTrailingHole(t *testing.T) {
	// 转写以未应答调用收尾（崩溃在工具执行中）
	broken := []Message{
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "x", Name: "ls"}}},
	}
	got := RepairInterrupted(broken)
	if len(got) != 2 || got[1].Role != "tool" || got[1].ToolCallID != "x" {
		t.Fatalf("尾部补洞: %+v", got)
	}
}
