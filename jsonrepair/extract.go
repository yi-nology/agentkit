package jsonrepair

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yi-nology/agentkit/textutil"
)

// ExtractJSON 从模型输出中提取 JSON 文本：剥 ``` 围栏（语义单源
// textutil.StripCodeFence——取第一个围栏块）、截取首个 {/[ 到末个 }/]。
// 快路径切片，不做语法修复——半损坏输出的宽容解析走 Unmarshal/ParseLenient；
// 两者围栏语义一致，宽容回退链不会静默换目标块。
func ExtractJSON(s string) string {
	s = textutil.StripCodeFence(s)
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

// Unmarshal 模型输出 JSON 的宽容解析出口，三级尝试：
//
//  1. ExtractJSON + encoding/json——快路径，命中则零额外开销；
//  2. 切片结果经 Repair 做语法级修复（全角结构符/非法转义/尾逗号/未闭合括号）
//     后再解析；
//  3. ParseLenient 全链兜底（剥围栏→直解→修复→抽对象，可救散文包裹与截断）。
//
// 全部失败时返回同时携带严格与宽容两路错误的包装——调用方把它回喂 LLM 重试时
// 无需再自行拼装失败原因。宿主的领域 schema 校验（字段语义、枚举约束）仍归调用方。
func Unmarshal(text string, v any) error {
	var strictErr error
	raw := ExtractJSON(text)
	if raw != "" {
		strictErr = json.Unmarshal([]byte(raw), v)
		if strictErr == nil {
			return nil
		}
		if fixed := Repair(raw); fixed != raw {
			if e := json.Unmarshal([]byte(fixed), v); e == nil {
				return nil
			}
		}
	}
	if err := ParseLenient(text, v, nil); err != nil {
		if strictErr == nil {
			strictErr = err
		}
		return fmt.Errorf("jsonrepair: 输出不可解析为 JSON（严格: %v；宽容: %v）", strictErr, err)
	}
	return nil
}
