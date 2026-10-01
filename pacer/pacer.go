// Package pacer 流式增量节流器：LLM token 级分片若逐条发布会洪泛事件通道与
// 前端（每秒数次×千字级文本），按「距上次发布 ≥interval 或缓冲 ≥chars」合并
// 发布；总量帽内单流 seq 自 1 单调递增；权威快照到达即 Absorb 收口（此后迟到
// 分片丢弃）；收尾残尾用 Flush 落地。
//
// 单 goroutine 契约：与消费方事件回调同一约束面（回调在 drain 循环内同步执行，
// 不阻塞不并发），故不加锁；跨 goroutine 使用由调用方自行串行化。
package pacer

import "time"

const (
	// DefaultInterval 缺省时间窗。
	DefaultInterval = 400 * time.Millisecond
	// DefaultChars 缺省字符窗（缓冲达到即提前发布）。
	DefaultChars = 200
	// DefaultCapBytes 单流发布总量帽（深度思考可极长，超帽静默停发）。
	DefaultCapBytes = 64 << 10
)

// Delta 一次待发布增量：Seq 单流内自 1 递增；Text 为合并分片（非累积快照——
// 前端按序追加，避免长文本 O(n²) 重发）。
type Delta struct {
	Seq  int
	Text string
}

// Stream 单条流的增量缓冲与节拍判定。
type Stream struct {
	interval  time.Duration
	chars     int
	capBytes  int
	text      string
	seq       int
	flushedAt time.Time
	published int
	done      bool
	now       func() time.Time
}

// Option Stream 可选项。
type Option func(*Stream)

// WithClock 注入时钟（测试用；缺省 time.Now）。
func WithClock(now func() time.Time) Option {
	return func(s *Stream) { s.now = now }
}

// NewStream 构造；interval<=0 / chars<=0 / capBytes<=0 取对应缺省值。
func NewStream(interval time.Duration, chars, capBytes int, opts ...Option) *Stream {
	if interval <= 0 {
		interval = DefaultInterval
	}
	if chars <= 0 {
		chars = DefaultChars
	}
	if capBytes <= 0 {
		capBytes = DefaultCapBytes
	}
	s := &Stream{
		interval: interval,
		chars:    chars,
		capBytes: capBytes,
		now:      time.Now,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Offer 接收一个增量分片：按节流窗口判定是否发布。done（权威已到）后的
// 迟到分片丢弃。
func (s *Stream) Offer(chunk string) (Delta, bool) {
	if s.done {
		return Delta{}, false
	}
	s.text += chunk
	now := s.now()
	if s.flushedAt.IsZero() {
		s.flushedAt = now
	}
	if now.Sub(s.flushedAt) < s.interval && len(s.text) < s.chars {
		return Delta{}, false
	}
	return s.Flush()
}

// Flush 立即发布缓冲残尾（帽内）；空缓冲/已收口/超帽不动序号。
func (s *Stream) Flush() (Delta, bool) {
	if s.done || s.text == "" || s.published >= s.capBytes {
		return Delta{}, false
	}
	pub := Delta{Seq: s.seq + 1, Text: s.text}
	s.seq, s.text, s.flushedAt = pub.Seq, "", s.now()
	s.published += len(pub.Text)
	return pub, true
}

// Absorb 权威快照到达：增量缓冲作废并收口（消费方以权威整段替换增量累积）。
func (s *Stream) Absorb() {
	s.done = true
	s.text = ""
}

// Sealed 是否已收口（Absorb 之后）。
func (s *Stream) Sealed() bool {
	return s.done
}
