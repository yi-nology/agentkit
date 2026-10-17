// Package filestate 读后写一致性状态：编辑/覆写文件前必须读过、且读后文件
// 未被外部改动（mtime/size staleness）。防「模型基于旧版本文件内容做编辑」
// ——这是文件操作类 agent 的真实事故面，对标 ZCode read-file-state.ts + edit.ts
// 的 staleness 门。
//
// 语义要点（生产调过的口径，别放宽）：
//   - 从未读过或只做过部分读（offset/limit 窗口）→ ErrNotRead——部分视野
//     不足以支撑安全编辑；
//   - 读后 mtime 前进（整数毫秒比较，亚毫精度差不算——文件系统精度不一，
//     小数级抖动会产生大量误报）或 size 变化 → ErrStale；
//   - 全量读（offset≤1 且无 limit）且读时内容与当前内容一致 → 豁免 stale
//     （formatter 重写等 mtime 变化但内容未变的场景）；
//   - 编辑成功后 RecordEdit 回写新状态，连锁编辑不误报。
//
// 状态源不限于专用 Read 工具：bash cat、外部回填同经 RecordRead 记录
// （来源追踪归调用方）。
package filestate

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// ErrNotRead 文件从未读过（或仅有部分读视野）——不满足编辑前提。
var ErrNotRead = errors.New("filestate: 文件未完整读过（先读后改）")

// ErrStale 读后文件已被外部改动——基于旧内容的编辑有覆盖风险。
var ErrStale = errors.New("filestate: 文件在读后已变化（重新读取后再编辑）")

// Entry 单次读取记录。
type Entry struct {
	Path    string
	Offset  int // 1 起；≤0 视为 1
	Limit   int // 0 = 未限（全量）
	Partial bool
	MtimeMs int64 // 整数毫秒（截断，不四舍五入）
	Size    int64
	ReadAt  time.Time
	// Content 全量读时的正文（供内容一致豁免；部分读/未知留空）。
	Content string
}

// Tracker 读状态登记簿（并发安全）。零值不可用，经 NewTracker 构造。
// 每路径只保留最新一条记录（豁免判定只需最新；历史与全量 Content 驻留是
// 无界内存——编辑型 agent 每轮 Edit 前都 Read，长会话数百 MB 级）。
type Tracker struct {
	mu     sync.Mutex
	byPath map[string]Entry // 路径归一键 → 最新读取
}

// NewTracker 创建登记簿。
func NewTracker() *Tracker {
	return &Tracker{byPath: map[string]Entry{}}
}

// RecordRead 登记一次读取。offset/limit 为窗口（limit 0=全量）。
func (t *Tracker) RecordRead(path string, offset, limit int, mtime time.Time, size int64, content string) Entry {
	if offset <= 0 {
		offset = 1
	}
	e := Entry{
		Path:    path,
		Offset:  offset,
		Limit:   limit,
		Partial: offset > 1 || limit > 0,
		MtimeMs: mtime.UnixMilli(),
		Size:    size,
		ReadAt:  time.Now(),
	}
	if !e.Partial {
		e.Content = content
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.byPath[normalizePath(path)] = e
	return e
}

// Latest 路径最近一次读取（跨窗口取最新——Bash/formatter 改完后模型重读 range，
// Edit 校验仍应基于最新那次，对标 findLatestReadFileState）。
func (t *Tracker) Latest(path string) (Entry, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.byPath[normalizePath(path)]
	return e, ok
}

// CheckEdit 编辑/覆写前校验。curMtime/curSize/curContent 为此刻文件实测
// （调用方刚读回的）。返回 nil=可编辑。
func (t *Tracker) CheckEdit(path string, curMtime time.Time, curSize int64, curContent string) error {
	last, ok := t.Latest(path)
	if !ok || last.Partial {
		return fmt.Errorf("%w: %s", ErrNotRead, path)
	}
	// mtime 未前进且 size 相同：仍须内容比对兜底——保留 mtime 的同长覆盖
	// （cp -p/rsync -a 回灌）在该分支静默放行是 staleness 检测的真实绕过面
	mtimeAdvanced := curMtime.UnixMilli() > last.MtimeMs
	if !mtimeAdvanced && curSize == last.Size {
		if last.Content == "" || last.Content == curContent {
			return nil
		}
		return fmt.Errorf("%w: %s", ErrStale, path)
	}
	// 变化但内容一致（formatter 重写/无实质修改）：仅全量读且读时内容保留时豁免
	if !last.Partial && last.Offset <= 1 && last.Limit == 0 && last.Content == curContent {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrStale, path)
}

// RecordEdit 编辑成功后回写：以编辑结果登记一次全量读，连锁编辑不误报。
func (t *Tracker) RecordEdit(path string, mtime time.Time, size int64, content string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.byPath[normalizePath(path)] = Entry{
		Path: path, Offset: 1, MtimeMs: mtime.UnixMilli(), Size: size,
		ReadAt: time.Now(), Content: content,
	}
}

// normalizePath 归一比较键：Clean + 大小写不敏感文件系统（darwin/windows）折叠
// 大小写 + 统一分隔符。
func normalizePath(p string) string {
	p = filepath.Clean(p)
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		p = strings.ToLower(p)
	}
	return p
}
