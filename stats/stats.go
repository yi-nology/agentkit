// Package stats 评测/对比统计原语（沉淀自 heimdallr 报告层）：
// 小样本通过率的诚实区间估计与配对二分类差异检验。
// 零依赖；显著水平对应的 z 由调用方传入（如 1.96）。
package stats

import "math"

// WilsonCI 通过率的 Wilson 得分置信区间（小样本下比正态近似诚实）。
// 返回 (lo, hi)；total=0 时返回 (0,0)；结果钳到 [0,1]。
func WilsonCI(passed, total int, z float64) (lo, hi float64) {
	if total == 0 {
		return 0, 0
	}
	p := float64(passed) / float64(total)
	n := float64(total)
	denom := 1 + z*z/n
	center := (p + z*z/(2*n)) / denom
	half := z * math.Sqrt(p*(1-p)/n+z*z/(4*n*n)) / denom
	lo, hi = center-half, center+half
	if lo < 0 {
		lo = 0
	}
	if hi > 1 {
		hi = 1
	}
	return lo, hi
}

// McNemarExact McNemar 精确检验（双侧）：n01=甲败乙过数，n10=甲过乙败数。
// 返回 p 值；n01+n10=0（无任何翻转）返回 1（无差异）；p 钳到 1。
func McNemarExact(n01, n10 int) float64 {
	n := n01 + n10
	if n == 0 {
		return 1 // 无任何翻转，无差异
	}
	k := n01
	if n10 < k {
		k = n10
	}
	// p = 2 * Σ_{i=0..k} C(n,i) / 2^n
	cum := 0.0
	for i := 0; i <= k; i++ {
		cum += comb(n, i)
	}
	p := cum / math.Pow(2, float64(n)) * 2
	if p > 1 {
		p = 1
	}
	return p
}

func comb(n, k int) float64 {
	if k < 0 || k > n {
		return 0
	}
	r := 1.0
	for i := 0; i < k; i++ {
		r = r * float64(n-i) / float64(i+1)
	}
	return r
}
