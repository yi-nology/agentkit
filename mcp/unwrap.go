package mcp

import "encoding/json"

// UnwrapMCPText 解 MCP 工具返回的信封（{"content":[{"type":"text","text":...}]}）取内层文本；
// 非信封形态原样返回——快照/结果进证据条目时，snippet 配额应留给有效数据而非包装层。
func UnwrapMCPText(raw string) string {
	var env struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal([]byte(raw), &env); err == nil {
		for _, c := range env.Content {
			if c.Text != "" {
				return c.Text
			}
		}
	}
	return raw
}
