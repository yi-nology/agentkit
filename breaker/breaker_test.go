package breaker

import (
	"testing"
	"time"
)

func TestBreakerClosedToOpen(t *testing.T) {
	b := New(3, 5*time.Minute)
	now := time.Now()

	// closed: 放行
	for i := 0; i < 3; i++ {
		if !b.Allow(now) {
			t.Fatal("closed 应放行")
		}
		b.Failure(now)
	}
	// 3 连败后熔断
	if b.Allow(now) {
		t.Fatal("3 连败后应熔断")
	}
	if !b.Opened(now) {
		t.Fatal("应处于熔断状态")
	}
}

func TestBreakerHalfOpen(t *testing.T) {
	b := New(3, 5*time.Minute)
	now := time.Now()

	for i := 0; i < 3; i++ {
		b.Failure(now)
	}
	// 冷却中
	if b.Allow(now.Add(time.Minute)) {
		t.Fatal("冷却中应拒绝")
	}
	// 冷却过后半开：放行一个探测
	if !b.Allow(now.Add(5*time.Minute + time.Second)) {
		t.Fatal("冷却后应放行一个探测")
	}
	// 探测在途：不放行第二个
	if b.Allow(now.Add(5*time.Minute + time.Second)) {
		t.Fatal("探测在途不放行第二个")
	}
}

func TestBreakerProbeSuccess(t *testing.T) {
	b := New(3, 5*time.Minute)
	now := time.Now()

	for i := 0; i < 3; i++ {
		b.Failure(now)
	}
	probeTime := now.Add(5*time.Minute + time.Second)
	if !b.Allow(probeTime) {
		t.Fatal("冷却后应放行探测")
	}
	b.Success()
	// 探测成功后闭合
	if !b.Allow(probeTime) {
		t.Fatal("探测成功后应闭合")
	}
}

func TestBreakerProbeFailure(t *testing.T) {
	b := New(3, 5*time.Minute)
	now := time.Now()

	for i := 0; i < 3; i++ {
		b.Failure(now)
	}
	probeTime := now.Add(5*time.Minute + time.Second)
	if !b.Allow(probeTime) {
		t.Fatal("冷却后应放行探测")
	}
	b.Failure(probeTime)
	// 探测失败重新熔断
	if b.Allow(probeTime.Add(time.Second)) {
		t.Fatal("探测失败应重新熔断")
	}
}

func TestBreakers(t *testing.T) {
	bs := NewBreakers(0, 0)
	now := time.Now()
	bs.Now = func() time.Time { return now }

	// 不同 key 独立熔断
	for i := 0; i < 3; i++ {
		bs.Failure("plugin-a")
	}
	if bs.Allow("plugin-a") {
		t.Fatal("plugin-a 应熔断")
	}
	if !bs.Allow("plugin-b") {
		t.Fatal("plugin-b 不应受影响")
	}
}

func TestBreakerDefaults(t *testing.T) {
	b := New(0, 0)
	if b.trip != DefaultTripThreshold {
		t.Fatalf("默认 trip 应为 %d, 得到 %d", DefaultTripThreshold, b.trip)
	}
	if b.cooldown != DefaultCooldown {
		t.Fatalf("默认 cooldown 应为 %v, 得到 %v", DefaultCooldown, b.cooldown)
	}
}

func TestOpenPeriodFailureDoesNotExtendCooldown(t *testing.T) {
	// 回归：open 期间被拒请求记录的 Failure 不得续期冷却（否则永远进不了半开）
	b := New(2, time.Minute)
	now := time.Now()
	b.Failure(now)
	b.Failure(now)
	if !b.Opened(now) {
		t.Fatal("达阈值应熔断")
	}
	// 冷却期内大量被拒请求的 Failure
	for i := 0; i < 100; i++ {
		b.Failure(now.Add(time.Duration(i) * time.Second))
	}
	// 冷却期结束后必须能进入半开放行探测
	if !b.Allow(now.Add(2 * time.Minute)) {
		t.Fatal("冷却结束后应放行半开探测（冷却期不得被续期）")
	}
}

func TestStaleProbeDoesNotStallBreaker(t *testing.T) {
	// 回归：探测方失联（未配对 Success/Failure）超过探测时限后，应放行新探测
	b := New(1, time.Minute)
	now := time.Now()
	b.Failure(now)
	if b.Allow(now) {
		t.Fatal("熔断中不应放行")
	}
	if !b.Allow(now.Add(2 * time.Minute)) {
		t.Fatal("冷却结束应放行探测")
	}
	// 探测失联：既不 Success 也不 Failure，超过 probeTimeout 后放行新探测
	if b.Allow(now.Add(2*time.Minute + time.Second)) {
		t.Fatal("探测在途不应放行")
	}
	if !b.Allow(now.Add(2*time.Minute + DefaultProbeTimeout + time.Second)) {
		t.Fatal("探测失联超时应放行新探测（不得永久卡死）")
	}
}
