package pacer

import (
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time { return c.t }

func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestStream(interval time.Duration, chars, capBytes int) (*Stream, *fakeClock) {
	clk := &fakeClock{t: time.Unix(0, 0)}
	return NewStream(interval, chars, capBytes, WithClock(clk.Now)), clk
}

func TestOfferHoldsUntilWindow(t *testing.T) {
	s, clk := newTestStream(400*time.Millisecond, 200, 1<<20)
	if _, ok := s.Offer("片段一"); ok {
		t.Fatal("时间窗与字符窗均未达到，不应发布")
	}
	clk.advance(401 * time.Millisecond)
	d, ok := s.Offer("片段二")
	if !ok || d.Seq != 1 {
		t.Fatalf("越过时间窗应发布，得到 ok=%v delta=%+v", ok, d)
	}
}

func TestOfferFlushesAtCharWindow(t *testing.T) {
	s, clk := newTestStream(400*time.Millisecond, 10, 1<<20)
	// 字符窗按字节计（与母本一致）：10 字节恰好达窗
	d, ok := s.Offer("aaaaaaaaaa")
	if !ok || d.Seq != 1 || len(d.Text) != 10 {
		t.Fatalf("达到字符窗应立即发布，得到 ok=%v delta=%+v", ok, d)
	}
	clk.advance(50 * time.Millisecond)
	if _, ok := s.Offer("短"); ok {
		t.Fatal("发布后时间窗重置，短片不应发布")
	}
}

func TestSeqMonotonicAndTextMerged(t *testing.T) {
	s, clk := newTestStream(400*time.Millisecond, 200, 1<<20)
	s.Offer("a")
	s.Offer("b")
	clk.advance(401 * time.Millisecond)
	d1, ok := s.Offer("c")
	if !ok || d1.Text != "abc" || d1.Seq != 1 {
		t.Fatalf("窗口内分片应合并，得到 %+v ok=%v", d1, ok)
	}
	clk.advance(401 * time.Millisecond)
	d2, ok := s.Offer("d")
	if !ok || d2.Seq != 2 {
		t.Fatalf("seq 应单调递增，得到 %+v", d2)
	}
}

func TestAbsorbDropsLateChunks(t *testing.T) {
	s, _ := newTestStream(400*time.Millisecond, 200, 1<<20)
	s.Offer("x")
	s.Absorb()
	if !s.Sealed() {
		t.Fatal("Absorb 后应 Sealed")
	}
	if _, ok := s.Offer("迟到分片"); ok {
		t.Fatal("收口后迟到分片应丢弃")
	}
	if _, ok := s.Flush(); ok {
		t.Fatal("收口后 Flush 不应产出")
	}
}

func TestCapBytesSilences(t *testing.T) {
	s, _ := newTestStream(0, 5, 12) // capBytes=12
	if _, ok := s.Offer("aaaaaaaaaa"); !ok {
		t.Fatal("第一片达到字符窗应发布")
	}
	// published=10 < 12，第二片发布后 published 超帽
	if _, ok := s.Offer("bbbbbbbbbb"); !ok {
		t.Fatal("第二片仍在帽内应发布")
	}
	if _, ok := s.Offer("cccc"); ok {
		t.Fatal("超帽后应静默停发")
	}
	if _, ok := s.Flush(); ok {
		t.Fatal("超帽后 Flush 不应产出")
	}
}

func TestDefaultsApplied(t *testing.T) {
	s := NewStream(0, 0, 0)
	if s.interval != DefaultInterval || s.chars != DefaultChars || s.capBytes != DefaultCapBytes {
		t.Fatalf("缺省值未生效：%v %d %d", s.interval, s.chars, s.capBytes)
	}
}

func TestFlushDrainsResidue(t *testing.T) {
	s, _ := newTestStream(time.Hour, 1000, 1<<20) // 长窗：永不自动发布
	if _, ok := s.Offer("残尾"); ok {
		t.Fatal("长窗下不应自动发布")
	}
	d, ok := s.Flush()
	if !ok || d.Text != "残尾" || d.Seq != 1 {
		t.Fatalf("Flush 应落地残尾，得到 %+v ok=%v", d, ok)
	}
	if _, ok := s.Flush(); ok {
		t.Fatal("空缓冲 Flush 不应产出")
	}
}

// TestCapBytesNoBufferRetention 回归（第八轮审计）：超帽后迟到分片不得滞留
// 缓冲——此前每个分片仍 s.text += chunk（深度思考尾部可达数百 KB 内存 +
// O(n²) 拷贝），Flush 因超帽恒不发布也不清理。
func TestCapBytesNoBufferRetention(t *testing.T) {
	s := NewStream(time.Hour, 1, 16) // 极小帽：首个发布即触顶
	s.Offer("0123456789abcdefgh")    // 18 字节 > 16 帽
	if _, ok := s.Offer("尾部滞留检查"); ok {
		t.Fatal("超帽后不应再发布")
	}
	if s.Buffered() != 0 {
		t.Fatalf("超帽后缓冲应零滞留, got %d", s.Buffered())
	}
}
