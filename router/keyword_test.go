package router

import (
	"strings"
	"testing"
)

// TestKeywordHit 否定前置守门：紧前方窗口内出现否定短语即该处作废；
// 否定在后不回看；多处出现任一处未否定即命中。
func TestKeywordHit(t *testing.T) {
	cases := []struct {
		input, kw string
		want      bool
	}{
		{"磁盘满了", "磁盘", true},
		{"帮我执行巡检", "执行", true},        // 无否定，正常命中
		{"主机没有被入侵", "入侵", false},      // 「没有」紧前方 → 否定
		{"没有检测到磁盘异常", "磁盘", false},    // 否定词与关键词隔着修饰语，仍在窗口内
		{"先不要执行这个任务", "执行", false},    // 「不要」
		{"磁盘没有问题", "磁盘", true},        // 否定在后，不回看
		{"不是磁盘就是内存，先查磁盘", "磁盘", true}, // 首处被否定，次处未被否定仍命中
		{"无网络时看磁盘", "磁盘", true},       // 单字否定词在表中不存在，不误伤
	}
	for _, tc := range cases {
		if got := KeywordHit(strings.ToLower(tc.input), tc.kw); got != tc.want {
			t.Errorf("KeywordHit(%q, %q) = %v, 期望 %v", tc.input, tc.kw, got, tc.want)
		}
	}
}

// TestKeywordHitInterrogative 疑问构式（有没有/是不是/要不要）字面包含否定短语——
// 先剥离再判，不得误判为否定。
func TestKeywordHitInterrogative(t *testing.T) {
	cases := []struct {
		input, kw string
		want      bool
	}{
		{"检查一下有没有后门", "后门", true},
		{"看看是不是有漏洞", "漏洞", true},
		{"要不要做一次弱口令检测", "弱口令", true},
		{"没有后门", "后门", false}, // 真否定仍生效
	}
	for _, tc := range cases {
		if got := KeywordHit(strings.ToLower(tc.input), tc.kw); got != tc.want {
			t.Errorf("KeywordHit(%q, %q) = %v, 期望 %v", tc.input, tc.kw, got, tc.want)
		}
	}
}

// TestKeywordHitBoundary 词边界：拉丁词命中处紧邻字符是字母/数字即作废该处。
func TestKeywordHitBoundary(t *testing.T) {
	cases := []struct {
		input, kw string
		want      bool
	}{
		{"kill 掉进程", "kill", true},
		{"skill 检查", "kill", false},    // 前邻字母
		{"进程被 killed", "kill", false},  // 后邻字母
		{"执行 killall", "kill", false},  // 后邻字母
		{"kill，然后重启", "kill", true},    // 标点是边界
		{"kill -9 1234", "kill", true}, // 空格是边界
		{"磁盘满了", "磁盘", true},           // CJK 关键词与 KeywordHit 行为一致
	}
	for _, tc := range cases {
		if got := KeywordHitBoundary(strings.ToLower(tc.input), tc.kw); got != tc.want {
			t.Errorf("KeywordHitBoundary(%q, %q) = %v, 期望 %v", tc.input, tc.kw, got, tc.want)
		}
	}
}

// TestKeywordHitExcept 排除构式按命中位置作废：构式内的命中处作废，构式外的
// 同词命中不受牵连；否定守门与 KeywordHit 一致。
func TestKeywordHitExcept(t *testing.T) {
	excludes := []string{"怎么执行", "可执行", "执行结果"}
	cases := []struct {
		input, kw string
		want      bool
	}{
		{"怎么执行这个脚本", "执行", false},            // 唯一命中落在构式内 → 排除
		{"查看可执行文件权限", "执行", false},           // 名词性构式内 → 排除
		{"先看下执行结果，怎么执行再定", "执行", false},      // 两处均落在构式内
		{"kill 掉进程，稍后看下执行结果", "kill ", true}, // 处置词不受其他构式牵连
		{"确认可执行后立即执行", "执行", true},           // 首处被构式作废，次处构式外仍命中
		{"不要执行结果查询", "执行", false},            // 否定守门优先（双门叠加）
		{"磁盘满了", "磁盘", true},                 // masks 不影响普通词表命中
	}
	for _, tc := range cases {
		if got := KeywordHitExcept(strings.ToLower(tc.input), tc.kw, excludes); got != tc.want {
			t.Errorf("KeywordHitExcept(%q, %q) = %v, 期望 %v", tc.input, tc.kw, got, tc.want)
		}
	}
}

// TestKeywordHitBoundaryExcept 词边界 + 排除构式 + 否定守门三重判定（硬规则形态）。
func TestKeywordHitBoundaryExcept(t *testing.T) {
	masks := []string{"kill 进程吗"}
	cases := []struct {
		input, kw string
		want      bool
	}{
		{"kill -9 1234", "kill", true},
		{"skill 报告", "kill", false},       // 词边界
		{"不要 kill 进程", "kill", false},     // 否定守门
		{"kill 进程吗，会不会误伤", "kill", false}, // 构式内作废（唯一命中处）
	}
	for _, tc := range cases {
		if got := KeywordHitBoundaryExcept(strings.ToLower(tc.input), tc.kw, masks); got != tc.want {
			t.Errorf("KeywordHitBoundaryExcept(%q, %q) = %v, 期望 %v", tc.input, tc.kw, got, tc.want)
		}
	}
}

// TestOnNegationHitHook 观测钩子：否定作废瞬间触发，零命中不触发。
func TestOnNegationHitHook(t *testing.T) {
	prev := OnNegationHit
	defer func() { OnNegationHit = prev }()

	hits := 0
	OnNegationHit = func() { hits++ }
	KeywordHit(strings.ToLower("没有磁盘问题"), "磁盘")
	KeywordHit(strings.ToLower("磁盘正常"), "磁盘")
	if hits != 1 {
		t.Fatalf("否定作废钩子应恰触发 1 次: %d", hits)
	}
}

// TestKeywordPostNegated 后置否定守门：紧贴单字判定，前置守门管不到的谓词否定形态。
func TestKeywordPostNegated(t *testing.T) {
	cases := []struct {
		input, kw string
		want      bool
	}{
		{"负载不高", "负载", true},   // 谓词否定
		{"磁盘不满", "磁盘", true},   // 谓词否定
		{"磁盘没有问题", "磁盘", true}, // 紧随复合否定同作废
		{"负载高", "负载", false},   // 正常陈述
		{"负载有点高", "负载", false}, // 隔字修饰不作废（判定从严只在紧贴位）
		{"磁盘", "磁盘", false},    // 命中在句尾无后文
		{"内存不足", "负载", false},  // 关键词未命中
	}
	for _, tc := range cases {
		if got := KeywordPostNegated(tc.input, tc.kw); got != tc.want {
			t.Errorf("KeywordPostNegated(%q, %q) = %v, 期望 %v", tc.input, tc.kw, got, tc.want)
		}
	}
}
