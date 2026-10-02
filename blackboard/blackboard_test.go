package blackboard

import (
	"context"
	"strings"
	"testing"
)

func TestBoardWriteAndCursor(t *testing.T) {
	b := NewBoard()
	b.Seed("material", "需求文本")
	b.Write("reviewer", "finding", "发现1")
	e := b.Write("reviewer", "finding", "发现2")

	if e.Seq != 3 {
		t.Fatalf("Seq 应单调: %d", e.Seq)
	}
	if got := b.EntriesAfter(1); len(got) != 2 || got[0].Content != "发现1" {
		t.Fatalf("增量观察不符: %+v", got)
	}
	if latest, ok := b.Latest("finding"); !ok || latest.Content != "发现2" {
		t.Fatalf("Latest 不符: %+v", latest)
	}
	if len(b.Entries()) != 3 {
		t.Fatalf("全量快照不符: %d", len(b.Entries()))
	}
}

func TestConveneRunsUntilConsensus(t *testing.T) {
	b := NewBoard()
	b.Seed("material", "待审材料")

	// security 专家：第一轮发现 1 条，第二轮无补充
	secRounds := 0
	sec := Specialist{Name: "security", Act: func(ctx context.Context, b *Board, since int64) (bool, error) {
		secRounds++
		if secRounds == 1 {
			b.Write("security", "finding", "SQL 注入风险")
			return true, nil
		}
		return false, nil
	}}
	// verifier 专家：看到 security 的 finding 后补充一次验证结论（写过后沉默）
	var sawFinding, wrote bool
	verifier := Specialist{Name: "verifier", Act: func(ctx context.Context, b *Board, since int64) (bool, error) {
		for _, e := range b.EntriesAfter(since) {
			if e.Author == "security" {
				sawFinding = true
			}
		}
		if sawFinding && !wrote {
			wrote = true
			b.Write("verifier", "verdict", "已复核，属实")
			return true, nil
		}
		return false, nil
	}}

	res, err := Convene(context.Background(), b, []Specialist{sec, verifier}, &ConveneOptions{MaxRounds: 5})
	if err != nil {
		t.Fatal(err)
	}
	if !sawFinding {
		t.Fatal("verifier 应观察到 security 的增量")
	}
	if res.Total != 3 { // seed + finding + verdict
		t.Fatalf("条目数不符: %d", res.Total)
	}
	// 共识后停止：轮数应 < MaxRounds
	if res.Rounds >= 5 {
		t.Fatalf("共识达成后不应跑满轮次: %d", res.Rounds)
	}
	// verifier 的结论在黑板上
	if _, ok := b.Latest("verdict"); !ok {
		t.Fatal("verdict 缺失")
	}
}

func TestConveneMaxRoundsAndValidation(t *testing.T) {
	b := NewBoard()
	chatty := Specialist{Name: "chatty", Act: func(ctx context.Context, b *Board, since int64) (bool, error) {
		b.Write("chatty", "note", "又一轮")
		return true, nil
	}}
	res, err := Convene(context.Background(), b, []Specialist{chatty}, &ConveneOptions{MaxRounds: 3})
	if err != nil {
		t.Fatal(err)
	}
	if res.Rounds != 3 {
		t.Fatalf("话痨专家应跑满轮次: %d", res.Rounds)
	}
	if _, err := Convene(context.Background(), b, nil, nil); err == nil {
		t.Fatal("空专家表应报错")
	}
}

func TestConveneSpecialistError(t *testing.T) {
	b := NewBoard()
	bad := Specialist{Name: "bad", Act: func(ctx context.Context, b *Board, since int64) (bool, error) {
		return false, context.Canceled
	}}
	if _, err := Convene(context.Background(), b, []Specialist{bad}, nil); err == nil {
		t.Fatal("专家错误应上抛")
	}
}

func TestConveneRejectsDuplicateNames(t *testing.T) {
	// 观察游标按名键控：重名/空名构建期 fail-fast（v0.10.9 起）。
	b := NewBoard()
	if _, err := Convene(context.Background(), b, []Specialist{
		{Name: "a", Act: func(context.Context, *Board, int64) (bool, error) { return false, nil }},
		{Name: "a", Act: func(context.Context, *Board, int64) (bool, error) { return false, nil }},
	}, nil); err == nil || !strings.Contains(err.Error(), "重复") {
		t.Fatalf("重名专家应 fail-fast: %v", err)
	}
	if _, err := Convene(context.Background(), b, []Specialist{
		{Name: "", Act: nil},
	}, nil); err == nil || !strings.Contains(err.Error(), "不能为空") {
		t.Fatalf("空名专家应 fail-fast: %v", err)
	}
}
