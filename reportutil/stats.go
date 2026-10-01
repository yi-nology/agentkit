// 对比统计：小样本通过率的诚实区间估计与配对二分类差异检验。
// 显著水平对应的 z 由调用方传入（如 1.96）。实现已下沉 ekit/pkg/mathx
// （ekit v0.33.0，agentkit v0.10.41 起单源在彼）；本文件保留公开面薄委托。
package reportutil

import "git.enjoye.top/enjoydream/ekit/pkg/mathx"

// WilsonCI 通过率的 Wilson 得分置信区间（小样本下比正态近似诚实）。
func WilsonCI(passed, total int, z float64) (lo, hi float64) {
	return mathx.WilsonCI(passed, total, z)
}

// McNemarExact McNemar 精确检验（双侧）：n01=甲败乙过数，n10=甲过乙败数。
func McNemarExact(n01, n10 int) float64 {
	return mathx.McNemarExact(n01, n10)
}
