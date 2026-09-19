package mcp

import "encoding/json"

// UnwrapMCPText 解 MCP 工具返回的信封取内层文本；非信封形态原样返回——快照/
// 结果进证据条目时，snippet 配额应留给有效数据而非包装层。
// 信封两种形态：content[].text（标准工具结果，人类可读文本优先）；缺失时
// structuredContent（较新 MCP 规范的结构化结果）序列化为 JSON——直接把结构化
// 对象丢给 LLM 优于丢弃。
func UnwrapMCPText(raw string) string {
	var env struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StructuredContent json.RawMessage `json:"structuredContent"`
	}
	if err := json.Unmarshal([]byte(raw), &env); err == nil {
		for _, c := range env.Content {
			if c.Text != "" {
				return c.Text
			}
		}
		if len(env.StructuredContent) > 0 && string(env.StructuredContent) != "null" {
			return string(env.StructuredContent)
		}
	}
	return raw
}
