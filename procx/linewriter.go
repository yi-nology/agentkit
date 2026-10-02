package procx

import "bytes"

// maxLineLen lineWriter 单行缓冲上限：超限丢弃该行不再回调。partial 若无上限，
// 单行无换行洪泛会绕过 maxChildStdout 限容（claude stream-json 每个事件就是一行，
// 一个含超大 diff 的 result 事件即可冲出数百 MB 峰值）。
const maxLineLen = 1 << 20 // 1MB

// lineWriter 按行拆分 stdout，回调后仍写入 buf 保留全文。
// 单行超过 maxLineLen 时丢弃该行（overflow 置位后不回调，直到扫到行尾重新同步），
// buf 照常限容写入保留全文供兜底。
type lineWriter struct {
	buf      *cappedBuffer
	onLine   func(string)
	partial  []byte
	overflow bool
}

func (w *lineWriter) Write(p []byte) (int, error) {
	_, _ = w.buf.Write(p)
	if w.overflow {
		// 超限行继续流入：不再累积，只找行尾重新同步
		if i := bytes.LastIndexByte(p, '\n'); i >= 0 {
			w.overflow = false
			w.partial = append(w.partial[:0], p[i+1:]...)
		}
		return len(p), nil
	}
	if len(w.partial)+len(p) > maxLineLen {
		w.overflow = true // 整行丢弃（含已累积部分），防 partial 无限放大
		w.partial = w.partial[:0]
		return len(p), nil
	}
	w.partial = append(w.partial, p...)
	for {
		i := bytes.IndexByte(w.partial, '\n')
		if i < 0 {
			break
		}
		line := string(w.partial[:i])
		w.partial = w.partial[i+1:]
		if line != "" {
			w.onLine(line)
		}
	}
	return len(p), nil
}
