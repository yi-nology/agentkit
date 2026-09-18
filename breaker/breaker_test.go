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

// Abandon：取消/终止语义——放弃在途半开探测不计失败，立即恢复可探测；
// closed 态无副作用（v0.10.4，配对契约的第三个出口）。
func TestBreakerAbandon(t *testing.T) {
	now := time.Now()
	b := New(3, time.Minute)
	for i := 0; i < 3; i++ {
		b.Failure(now)
	}
	if !b.Opened(now.Add(time.Second)) {
		t.Fatal("连续 3 败应熔断")
	}
	// 冷却期过 → 半开放行探测（在途）
	if !b.Allow(now.Add(2 * time.Minute)) {
		t.Fatal("冷却后应放行探测")
	}
	if b.Allow(now.Add(2*time.Minute + time.Second)) {
		t.Fatal("探测在途不应放行第二个探测")
	}
	// 调用方取消：Abandon 后立即放行新探测（无需等 probeTimeout 失联超时）
	b.Abandon()
	if !b.Allow(now.Add(2*time.Minute + 2*time.Second)) {
		t.Fatal("Abandon 后应立即放行新探测")
	}
	// Abandon 不计失败：连续失败数不因放弃而变化（仍为熔断前的 3）
	// ——熔断态由 openUntil 驱动，Abandon 只清 probing 位。
	b2 := New(3, time.Minute)
	b2.Allow(now)
	b2.Abandon()
	b2.Allow(now.Add(time.Second)) // closed 态探测
	b2.Abandon()
	if b2.Opened(now.Add(2 * time.Second)) {
		t.Fatal("closed 态 Abandon 不得触发熔断")
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

// WithProbeTimeout：探测时限可配置——时限内旧探测视为在途不放行新探测，超时后
// 放行新探测（失联兜底）。慢而健康的操作（> 默认 1min）依赖调大该值避免并发双探测。
func TestWithProbeTimeout(t *testing.T) {
	now := time.Unix(0, 0)
	bs := NewBreakers(3, time.Minute, WithProbeTimeout(10*time.Minute))
	bs.Now = func() time.Time { return now }

	// 3 连败开熔断
	for i := 0; i < 3; i++ {
		if !bs.Allow("p") {
			t.Fatal("closed 应放行")
		}
		bs.Failure("p")
	}
	if bs.Allow("p") {
		t.Fatal("应处于熔断 open")
	}
	// 冷却期过 → 半开放行首个探测
	now = now.Add(time.Minute)
	if !bs.Allow("p") {
		t.Fatal("冷却后应放行探测")
	}
	// 探测时限（10min）内：旧探测在途，不放行新探测（缺省 1min 时此处已双探测）
	now = now.Add(9 * time.Minute)
	if bs.Allow("p") {
		t.Fatal("探测时限内不应放行第二个探测")
	}
	// 超过探测时限：旧探测视为失联，放行新探测
	now = now.Add(2 * time.Minute)
	if !bs.Allow("p") {
		t.Fatal("探测失联超时应放行新探测")
	}
	// 零值 Option 回退缺省
	bs2 := NewBreakers(3, time.Minute, WithProbeTimeout(0))
	if bs2.probeTimeout != DefaultProbeTimeout {
		t.Fatalf("<=0 应回退缺省时限, got %v", bs2.probeTimeout)
	}
	// 回归：默认时钟源必须就位（Allow/Success/Failure 依赖；曾有重构丢失 Now
	// 导致调用方空指针）
	if bs2.Now == nil {
		t.Fatal("NewBreakers 必须注入缺省时钟源")
	}
}
