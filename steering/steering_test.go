package steering

import "testing"

func TestGuideAndQueueFlow(t *testing.T) {
	q := &Queue{}
	q.Steer("改用 grep")
	q.Enqueue("下一个任务")
	q.Steer("注意大小写")

	guides := q.DrainGuides()
	if len(guides) != 2 || guides[0].Text != "改用 grep" || guides[1].Text != "注意大小写" {
		t.Fatalf("guide 取序: %+v", guides)
	}
	if q.Len() != 1 {
		t.Fatalf("queue 保留: %d", q.Len())
	}
	in, ok := q.DequeueQueued()
	if !ok || in.Text != "下一个任务" || in.Delivery != Queued {
		t.Fatalf("queue 取走: %+v ok=%v", in, ok)
	}
	if _, ok := q.DequeueQueued(); ok {
		t.Fatal("空队列应 false")
	}
}

func TestGuideFallback(t *testing.T) {
	q := &Queue{}
	q.Steer("guide1")
	q.Enqueue("queued1")
	// turn 提前收尾：guide 降级排队，不丢
	q.DemoteGuidesToQueued()
	if q.Len() != 2 {
		t.Fatalf("降级不丢: %d", q.Len())
	}
	var texts []string
	for {
		in, ok := q.DequeueQueued()
		if !ok {
			break
		}
		texts = append(texts, in.Text)
	}
	if len(texts) != 2 || texts[0] != "guide1" || texts[1] != "queued1" {
		t.Fatalf("FIFO 顺序: %v", texts)
	}
}
