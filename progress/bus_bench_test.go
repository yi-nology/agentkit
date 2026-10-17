package progress

import (
	"context"
	"testing"
)

// BenchmarkBusPublish 留档 Publish 单核成本（mutex + 非阻塞 send）。
// 若未来出现多 goroutine 高频发布场景的锁竞争证据，再考虑读写分离/COW。
func BenchmarkBusPublish(b *testing.B) {
	cases := []struct {
		name string
		subs int
	}{
		{"subs=0", 0},
		{"subs=1", 1},
		{"subs=8", 8},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			bus := NewBus[int]()
			for i := 0; i < c.subs; i++ {
				bus.Subscribe(context.Background())
			}
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				bus.Publish(i)
			}
		})
	}
}
