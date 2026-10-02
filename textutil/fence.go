package textutil

import (
	"regexp"
	"strings"
)

// fenceRe markdown 代码围栏（```json/``` 均可；取第一个围栏块内容）。
var fenceRe = regexp.MustCompile("(?s)```(?:json)?\\s*(.*?)\\s*```")

// StripFence 剥离 markdown 代码围栏（全仓围栏语义单一事实源：jsonrepair 宽容
// 解析与 llm.ExtractJSON 共用）。取**第一个** ``` 围栏块；无栅栏原样 TrimSpace
// 返回。多围栏块输出（模型补多段代码）语义为「取第一块」——与宽容解析链一致。
func StripFence(s string) string {
	trimmed := strings.TrimSpace(s)
	if m := fenceRe.FindStringSubmatch(trimmed); m != nil {
		return strings.TrimSpace(m[1])
	}
	return trimmed
}
