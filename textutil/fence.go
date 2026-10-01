package textutil

import (
	"regexp"
	"strings"
)

// fenceRe markdown 代码围栏（```json/``` 均可；取第一个围栏块内容）。
var fenceRe = regexp.MustCompile("(?s)```(?:json)?\\s*(.*?)\\s*```")

// StripCodeFence 剥离 markdown 代码围栏（全仓**代码围栏**语义单一事实源：
// jsonrepair 宽容解析与快路径切片共用）。取**第一个** ``` 围栏块；无栅栏原样
// TrimSpace 返回。多围栏块输出（模型补多段代码）语义为「取第一块」——与宽容
// 解析链一致。注意与 fence 包的「数据区围栏」（fence.Data，注入卫生面）是两个
// 正交概念：本函数处理 markdown ``` 栅栏，后者圈定不可信数据边界。
func StripCodeFence(s string) string {
	trimmed := strings.TrimSpace(s)
	if m := fenceRe.FindStringSubmatch(trimmed); m != nil {
		return strings.TrimSpace(m[1])
	}
	return trimmed
}
