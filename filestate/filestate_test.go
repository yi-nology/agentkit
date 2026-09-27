package filestate

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

var base = time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)

func TestEditRequiresFullRead(t *testing.T) {
	tr := NewTracker()
	// 从未读
	if err := tr.CheckEdit("/a.go", base, 10, "x"); !errors.Is(err, ErrNotRead) {
		t.Fatalf("未读应 ErrNotRead: %v", err)
	}
	// 部分读同样不满足
	tr.RecordRead("/a.go", 5, 10, base, 100, "")
	if err := tr.CheckEdit("/a.go", base, 10, "x"); !errors.Is(err, ErrNotRead) {
		t.Fatalf("部分读应 ErrNotRead: %v", err)
	}
}

func TestEditFreshFullReadPasses(t *testing.T) {
	tr := NewTracker()
	tr.RecordRead("/a.go", 0, 0, base, 100, "content")
	if err := tr.CheckEdit("/a.go", base, 100, "content"); err != nil {
		t.Fatalf("未变化应放行: %v", err)
	}
}

func TestStaleOnMtimeOrSizeChange(t *testing.T) {
	tr := NewTracker()
	tr.RecordRead("/a.go", 0, 0, base, 100, "content")
	// mtime 前进（整数毫秒）且内容变化
	if err := tr.CheckEdit("/a.go", base.Add(10*time.Millisecond), 100, "changed"); !errors.Is(err, ErrStale) {
		t.Fatalf("mtime 前进应 stale: %v", err)
	}
	// 亚毫秒精度差不判 stale（同毫秒内）
	if err := tr.CheckEdit("/a.go", base.Add(time.Millisecond).Add(-time.Microsecond), 100, "content"); err != nil {
		t.Fatalf("亚毫抖动不应 stale: %v", err)
	}
	// size 变化（内容同时变化）
	if err := tr.CheckEdit("/a.go", base, 101, "changed"); !errors.Is(err, ErrStale) {
		t.Fatalf("size 变化应 stale: %v", err)
	}
}

func TestContentIdenticalExempts(t *testing.T) {
	tr := NewTracker()
	tr.RecordRead("/a.go", 0, 0, base, 100, "same")
	// mtime 前进但内容一致（formatter 重写场景）→ 豁免
	if err := tr.CheckEdit("/a.go", base.Add(time.Second), 100, "same"); err != nil {
		t.Fatalf("内容一致应豁免: %v", err)
	}
	// mtime 前进且内容变化 → stale
	if err := tr.CheckEdit("/a.go", base.Add(time.Second), 100, "changed"); !errors.Is(err, ErrStale) {
		t.Fatalf("内容变化应 stale: %v", err)
	}
}

func TestRecordEditAvoidsChainedFalseStale(t *testing.T) {
	tr := NewTracker()
	tr.RecordRead("/a.go", 0, 0, base, 100, "v1")
	// 第一次编辑
	if err := tr.CheckEdit("/a.go", base, 100, "v1"); err != nil {
		t.Fatal(err)
	}
	newMtime := base.Add(time.Minute)
	tr.RecordEdit("/a.go", newMtime, 102, "v2")
	// 第二次编辑：基于回写后的状态校验，不误报
	if err := tr.CheckEdit("/a.go", newMtime, 102, "v2"); err != nil {
		t.Fatalf("连锁编辑不应误报: %v", err)
	}
}

func TestLatestAcrossWindows(t *testing.T) {
	tr := NewTracker()
	tr.RecordRead("/a.go", 0, 0, base, 100, "full")
	time.Sleep(2 * time.Millisecond) // 保证 ReadAt 严格更新
	tr.RecordRead("/a.go", 100, 10, base, 100, "")
	// 跨窗口取最新（range 读晚于 full 读）→ 最新即 range 读（partial）
	e, ok := tr.Latest("/a.go")
	if !ok || !e.Partial || e.Offset != 100 {
		t.Fatalf("应取最新窗口: %+v", e)
	}
	if err := tr.CheckEdit("/a.go", base, 100, "x"); !errors.Is(err, ErrNotRead) {
		t.Fatalf("最新为部分读应拒绝: %v", err)
	}
}

func TestPathNormalization(t *testing.T) {
	tr := NewTracker()
	// 同文件不同形态路径（./、重复分隔、大小写在 darwin/windows 折叠）
	tr.RecordRead(filepath.Join(".", "sub", "..", "a.go"), 0, 0, base, 10, "")
	for _, alias := range []string{"a.go", "./a.go", "A.GO"} {
		if _, ok := tr.Latest(alias); !ok {
			t.Errorf("别名 %q 应命中同一状态", alias)
		}
	}
}
