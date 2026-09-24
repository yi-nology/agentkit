// 采样聚簇：best-of-N 确定性聚合原语。
//
// 同一任务对同一输入跑 N 次（best-of-N），「多份采样相互复现」是确定性的
// 可信度信号——按调用方提供的签名通道与等价判定把 N 份输出聚簇计数，
// 不引入任何模型判断（与「裁决不走 LLM」的确定性纪律兼容）。
//
// 典型用法：审查/生成类 agent 对高危输入 opt-in N 采样，聚簇后 Count≥2 的
// 簇升级呈现权重、孤立单现的低权重条目标注降权提示；全部簇保留（漏报防线）。
// Fingerprint/NormalizeComment 提供「file+规范化文本」精确指纹通道的 canonical
// 实现（v0.10.9 自 severity 迁入——指纹是聚簇签名的自然组成部分）。
package reportutil

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode"
)

// Fingerprint 精确指纹：规范化评论文本哈希 + file（SHA256 前 16 字节 hex）。
// 行号不入指纹；同一指纹跨轮次即「同一问题」。常用作 Aggregate 的签名通道。
func Fingerprint(file, comment string) string {
	sum := sha256.Sum256([]byte(file + "\x1f" + NormalizeComment(comment)))
	return hex.EncodeToString(sum[:16])
}

// NormalizeComment 规范化：全小写、去所有空白与标点/符号差异。
func NormalizeComment(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range strings.ToLower(s) {
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Group 一簇相互等价的采样结果。
type Group[T any] struct {
	// Representative 首见条目（稳定性依赖输入有序——按采样序传入）。
	Representative T
	// Items 命中本簇的全部条目（含 Representative，按首见序）。
	Items []T
	// Signature 首见条目的签名通道集（可观测/对账用；归属判定以 eq 为准）。
	Signature []string
	// Count = len(Items)（冗余字段，便于不取 Items 的调用方）。
	Count int
}

// Aggregate 把采样输出聚簇。
//
//   - signature：条目的签名通道集（每通道作 O(1) 快速匹配的 map 键，如
//     「精确指纹」「file+规范化文本」双通道）。通道只是快速定位，最终归属
//     以 eq 为准：任一通道命中既有簇且 eq 判定等价 → 并入；
//   - eq(a, b)：a（既有簇代表）与 b 是否为同一问题。实现应只对比代表：
//     本包语义是「与首见者等价才并入」，链式传递匹配不成立（保守，防止
//     漂移链把不同问题串成一簇）。签名命中但 eq 不等价时继续尝试其余
//     通道，全部未命中则自成新簇。
//
// 输入按采样序传入；输出簇按首见序排列。
func Aggregate[T any](items []T, signature func(T) []string, eq func(a, b T) bool) []Group[T] {
	var out []Group[T]
	var groups []*Group[T]
	byChannel := map[string]*Group[T]{}
	for _, item := range items {
		// 签名每 item 只算一次（匹配轮与建簇轮共用）——签名函数可能昂贵或非纯，
		// 两次调用可能返回不同通道集导致注册与匹配不一致
		channels := signature(item)
		joined := false
		for _, ch := range channels {
			g, dup := byChannel[ch]
			if !dup || !eq(g.Representative, item) {
				continue // 未命中或签名命中但不等价（如行号不相容）→ 试下一通道
			}
			g.Items = append(g.Items, item)
			g.Count++
			joined = true
			break
		}
		if joined {
			continue
		}
		g := &Group[T]{Representative: item, Signature: channels, Items: []T{item}, Count: 1}
		groups = append(groups, g)
		for _, ch := range channels {
			// 首见者占位，后到者不抢通道（归属以首见为准）
			if _, seen := byChannel[ch]; !seen {
				byChannel[ch] = g
			}
		}
	}
	for _, g := range groups {
		out = append(out, *g)
	}
	return out
}
