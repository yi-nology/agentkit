// Package llmjson 模型输出 JSON 统一解析入口：llm.ExtractJSON 快路径
// （剥围栏 + 首尾括号切片）之上补 jsonrepair 宽容修复兜底。模型输出的
// 全角结构标点（，：）、尾逗号、非法转义、截断未闭合是高频损坏形态，单靠
// ExtractJSON + encoding/json 会把整轮产出判死；本包让这类半损坏输出仍可
// 回收，宿主的领域 schema 校验（字段语义、枚举约束）仍归调用方。
// （沉自 argus/internal/llmjson，API 原样。）
package llmjson

import (
	"encoding/json"
	"fmt"

	"github.com/yi-nology/agentkit/jsonrepair"
	"github.com/yi-nology/agentkit/llm"
)

// Unmarshal 从模型输出文本解析 JSON 到 v，三级尝试：
//
//  1. llm.ExtractJSON + encoding/json——与历史行为一致，命中则零额外开销；
//  2. 切片结果经 jsonrepair.Repair 做语法级修复（全角结构符/非法转义/尾逗号/
//     未闭合括号）后再解析；
//  3. jsonrepair.ParseLenient 全链兜底（剥围栏→直解→修复→抽对象，可救散文包裹
//     与截断）。
//
// 全部失败时返回同时携带严格与宽容两路错误的包装——调用方把它回喂 LLM 重试时
// 无需再自行拼装失败原因。
func Unmarshal(text string, v any) error {
	var strictErr error
	raw := llm.ExtractJSON(text)
	if raw != "" {
		strictErr = json.Unmarshal([]byte(raw), v)
		if strictErr == nil {
			return nil
		}
		if fixed := jsonrepair.Repair(raw); fixed != raw {
			if e := json.Unmarshal([]byte(fixed), v); e == nil {
				return nil
			}
		}
	}
	if err := jsonrepair.ParseLenient(text, v, nil); err != nil {
		if strictErr == nil {
			strictErr = err
		}
		return fmt.Errorf("llmjson: 输出不可解析为 JSON（严格: %v；宽容: %v）", strictErr, err)
	}
	return nil
}
