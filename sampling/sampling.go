// Package sampling 测试时计算放大（test-time compute）的确定性聚合原语。
//
// 同一任务对同一输入跑 N 次（best-of-N），「多份采样相互复现」是确定性的
// 可信度信号——本包按调用方提供的签名通道与等价判定把 N 份输出聚簇计数，
// 不引入任何模型判断（与「裁决不走 LLM」的确定性纪律兼容）。
//
// 典型用法：审查/生成类 agent 对高危输入 opt-in N 采样，聚簇后 Count≥2 的
// 簇升级呈现权重、孤立单现的低权重条目标注降权提示；全部簇保留（漏报防线）。
package sampling

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
		joined := false
		for _, ch := range signature(item) {
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
		g := &Group[T]{Representative: item, Signature: signature(item), Items: []T{item}, Count: 1}
		groups = append(groups, g)
		for _, ch := range g.Signature {
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
