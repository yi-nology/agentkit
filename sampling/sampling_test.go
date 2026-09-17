package sampling

import "testing"

type item struct {
	key  string // 签名通道值
	line int    // 等价判定附加条件
	tag  string
}

func sig(i item) []string { return []string{"fp:" + i.key, "nc:" + i.key} }
func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func TestAggregate_Replication(t *testing.T) {
	items := []item{
		{key: "k1", line: 3, tag: "a"},
		{key: "k1", line: 4, tag: "b"}, // 同签名同行容差 → 复现
		{key: "k2", line: 30, tag: "c"},
	}
	eq := func(a, b item) bool { return a.key == b.key && abs(a.line-b.line) <= 2 }
	groups := Aggregate(items, sig, eq)
	if len(groups) != 2 {
		t.Fatalf("应聚 2 簇: %+v", groups)
	}
	if groups[0].Count != 2 || groups[0].Representative.tag != "a" {
		t.Fatalf("k1 应复现 2 次、代表为首见: %+v", groups[0])
	}
	if groups[1].Count != 1 {
		t.Fatalf("k2 孤立: %+v", groups[1])
	}
}

func TestAggregate_SameSignatureIncompatible(t *testing.T) {
	// 签名命中但等价判定不过（行号不相容）→ 各自成簇（同模板两处不同位置=两个问题）
	items := []item{{key: "k", line: 3}, {key: "k", line: 30}}
	eq := func(a, b item) bool { return a.key == b.key && abs(a.line-b.line) <= 2 }
	groups := Aggregate(items, sig, eq)
	if len(groups) != 2 {
		t.Fatalf("不相容条目应各自成簇: %+v", groups)
	}
}

func TestAggregate_EquivalentViaSecondChannel(t *testing.T) {
	// 首通道签名不同但第二通道（规范化文本通道）命中且 eq 通过 → 并入首见簇
	items := []item{{key: "a", line: 3}, {key: "b", line: 4}}
	eq := func(a, b item) bool { return abs(a.line-b.line) <= 2 }
	// 签名集：首通道=fp（含 key），第二通道=统一常量（模拟规范化后相同）
	ch := func(i item) []string { return []string{"fp:" + i.key, "norm:same"} }
	groups := Aggregate(items, ch, eq)
	if len(groups) != 1 || groups[0].Count != 2 {
		t.Fatalf("第二通道命中应并入: %+v", groups)
	}
	if groups[0].Signature[0] != "fp:a" {
		t.Fatalf("簇签名取首见: %v", groups[0].Signature)
	}
}

func TestAggregate_Empty(t *testing.T) {
	groups := Aggregate[item](nil, sig, func(a, b item) bool { return true })
	if len(groups) != 0 {
		t.Fatalf("空输入应空输出: %+v", groups)
	}
}
