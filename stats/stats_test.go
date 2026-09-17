package stats

import (
	"math"
	"testing"
)

func TestWilsonCI(t *testing.T) {
	lo, hi := WilsonCI(0, 0, 1.96)
	if lo != 0 || hi != 0 {
		t.Fatal("empty total must yield (0,0)")
	}
	// 8/10 → CI 约 [0.490, 0.943]
	lo, hi = WilsonCI(8, 10, 1.96)
	if lo < 0.48 || lo > 0.50 || hi < 0.94 || hi > 0.95 {
		t.Fatalf("Wilson(8,10)=[%v,%v] out of expected range", lo, hi)
	}
	// 全过的小样本：Wilson 上界为 1，但下界 ~0.566——"至少多好"才是诚实口径
	lo, hi = WilsonCI(5, 5, 1.96)
	if hi < 0.999 || lo <= 0.5 {
		t.Fatalf("Wilson(5,5)=[%v,%v]: lower bound must stay honest", lo, hi)
	}
	// 大样本趋近点估计
	lo, hi = WilsonCI(5000, 10000, 1.96)
	if hi-lo > 0.02 {
		t.Fatalf("Wilson(5000,10000) interval too wide: [%v,%v]", lo, hi)
	}
}

func TestMcNemarExact(t *testing.T) {
	cases := []struct {
		n01, n10 int
		want     float64 // 容差 1e-9；-1 = 只验证方向
		sig      bool
	}{
		{0, 0, 1, false},
		{1, 0, 1, false},        // 1 次翻转无意义
		{5, 0, 0.0625, false},   // 5:0 未达 0.05
		{6, 0, 0.03125, true},   // 6:0 显著变差
		{0, 6, 0.03125, true},   // 6:0 显著变好
		{8, 1, 0.0390625, true}, // 8:1 显著
	}
	for _, tc := range cases {
		got := McNemarExact(tc.n01, tc.n10)
		if tc.want >= 0 && math.Abs(got-tc.want) > 1e-9 {
			t.Fatalf("McNemar(%d,%d)=%v want %v", tc.n01, tc.n10, got, tc.want)
		}
		if (got < 0.05) != tc.sig {
			t.Fatalf("McNemar(%d,%d)=%v significant=%v want %v", tc.n01, tc.n10, got, got < 0.05, tc.sig)
		}
	}
}
