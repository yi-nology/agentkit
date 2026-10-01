// 会话恢复的中断补洞（对标 ZCode session-history-hydrator 的关键修复）：
// 持久化转写里 assistant 带 toolCalls 但缺对应 tool 结果（进程中断/崩溃时
// 结果未落盘）时，直接回放会被 provider 4xx 拒绝——必须在恢复期插入占位
// 结果补齐调用-结果配对。
package compact

// InterruptedToolResult 中断占位结果（LLM 可感知的诚实形态：明示被中断，
// 模型可自行决定是否重发调用）。
const InterruptedToolResult = "[Tool execution was interrupted before resume]"

// RepairInterrupted 扫描转写，为每个「有调用、无结果」的 ToolCall 插入占位
// tool 结果（按调用原序补在下一个非 tool 消息之前）。返回修复后的新切片，
// 原切片不变；无洞时返回原切片。
func RepairInterrupted(msgs []Message) []Message {
	answered := map[string]bool{}
	for _, m := range msgs {
		if m.Role == "tool" && m.ToolCallID != "" {
			answered[m.ToolCallID] = true
		}
	}
	var holes []string // 依次发现的未应答调用 id
	for _, m := range msgs {
		if m.Role != "assistant" {
			continue
		}
		for _, tc := range m.ToolCalls {
			if tc.ID != "" && !answered[tc.ID] {
				holes = append(holes, tc.ID)
			}
		}
	}
	if len(holes) == 0 {
		return msgs
	}
	holeSet := map[string]bool{}
	for _, id := range holes {
		holeSet[id] = true
	}
	out := make([]Message, 0, len(msgs)+len(holes))
	var pendingHoles []string // 当前调用轮的未应答调用（等既有结果落完再补，保持调用同序）
	flush := func() {
		for _, id := range pendingHoles {
			out = append(out, Message{Role: "tool", ToolCallID: id, Text: InterruptedToolResult})
			delete(holeSet, id)
		}
		pendingHoles = nil
	}
	for _, m := range msgs {
		if m.Role != "tool" {
			flush() // 调用轮的结果串结束：占位补在串尾（与调用同序，provider 不拒）
		}
		out = append(out, m)
		if m.Role == "assistant" {
			for _, tc := range m.ToolCalls {
				if tc.ID != "" && holeSet[tc.ID] {
					pendingHoles = append(pendingHoles, tc.ID)
				}
			}
		}
	}
	flush() // 尾部残余（转写以调用/结果收尾）
	return out
}
