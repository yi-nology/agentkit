package llm

import (
	"strings"

	"git.enjoye.top/enjoydream/agentkit/textutil"
)

// ExtractJSON 从模型输出中提取 JSON 文本：剥 ``` 围栏（语义单源
// textutil.StripFence——取第一个围栏块）、截取首个 {/[ 到末个 }/]。
// 快路径切片，不做语法修复——半损坏输出的宽容解析归 llmjson/jsonrepair；
// 两者围栏语义一致，llmjson 回退链不会静默换目标块。
func ExtractJSON(s string) string {
	s = textutil.StripFence(s)
	start := strings.IndexAny(s, "{[")
	if start < 0 {
		return s
	}
	openCh := s[start]
	closeCh := byte('}')
	if openCh == '[' {
		closeCh = ']'
	}
	rest := s[start:]
	end := strings.LastIndexByte(rest, closeCh)
	if end < 0 {
		return rest
	}
	return rest[:end+1]
}
