// Package breaker 熔断器（closed → open → half-open → closed/reopen）。
// 零外部依赖，纯并发安全的状态机。
package breaker

import (
	"sync"
	"time"
)

// 默认参数。
const (
	DefaultTripThreshold = 3
	DefaultCooldown      = 5 * time.Minute
	// DefaultProbeTimeout 半开探测的缺省时限：探测方"失联"（Allow 放行后未配对
	// Success/Failure，如调用方在探测期间 ctx 取消提前返回）超过此时限后，
	// Allow 放行新的探测，避免熔断器被陈旧探测永久卡死在半开。
	DefaultProbeTimeout = time.Minute
)

// Breaker 单 key 熔断器。
type Breaker struct {
	mu            sync.Mutex
	trip          int
	cooldown      time.Duration
	probeTimeout  time.Duration
	consecutive   int
	openUntil     time.Time
	probing       bool
	probeGen      uint64 // 探测世代：放行探测时 +1，上报携带 gen 甄别迟到
	probeDeadline time.Time
}

// New 创建熔断器。trip=连续失败阈值，cooldown=冷却期。
func New(trip int, cooldown time.Duration) *Breaker {
	if trip <= 0 {
		trip = DefaultTripThreshold
	}
	if cooldown <= 0 {
		cooldown = DefaultCooldown
	}
	return &Breaker{trip: trip, cooldown: cooldown, probeTimeout: DefaultProbeTimeout}
}

// WithProbeTimeout 构建期配置半开探测时限（链式；与 Breakers.WithProbeTimeout
// 对齐——独立使用 Breaker 的调用方不再被钉死在 DefaultProbeTimeout）。
func (b *Breaker) WithProbeTimeout(d time.Duration) *Breaker {
	if d > 0 {
		b.probeTimeout = d
	}
	return b
}

// Allow 返回是否放行；半开时仅放行一个探测请求。
// 调用契约：返回 true 后必须恰好配对一次 Success、Failure 或 Abandon（探测
// 失联超过 DefaultProbeTimeout 后本方法会放行新探测，不再无限等待旧探测）。
func (b *Breaker) Allow(now time.Time) bool {
	ok, _ := b.AllowGen(now)
	return ok
}

// AllowGen Allow 的世代形态：放行探测时返回递增 gen，供 SuccessGen/FailureGen/
// AbandonGen 携带上报——陈旧 gen（超时兜底已放行新探测后旧探测迟到上报）被
// 显式忽略，不再依赖时间窗甄别（v0.12.4 世代化；gen==0 表示 closed 态直行，
// 旧三方法仍可用）。
func (b *Breaker) AllowGen(now time.Time) (bool, uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.openUntil.IsZero() {
		return true, 0 // closed（gen=0：closed 态上报不走世代甄别）
	}
	if now.Before(b.openUntil) {
		return false, 0 // 熔断中
	}
	if b.probing && now.Before(b.probeDeadline) {
		return false, 0 // 半开探测已在途
	}
	// 首次探测，或旧探测已超时失联——放行新探测（世代 +1：旧探测的迟到
	// 上报因 gen 不匹配被显式忽略）
	b.probing = true
	b.probeGen++
	b.probeDeadline = now.Add(b.probeTimeout)
	return true, b.probeGen
}

// Success 关闭熔断（配对 Allow==true 的成功结果）。
func (b *Breaker) Success() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.consecutive = 0
	b.openUntil = time.Time{}
	b.probing = false
	b.probeDeadline = time.Time{}
}

// Abandon 放弃在途的半开探测：Allow==true 但调用方在取得结果前终止（任务级
// 取消/优雅停机）——结果未知，既非成功也非失败，不计入统计。半开态回到
// "可立即放行新探测"（不必等 probeTimeout 失联超时）；closed 态无副作用。
// 与 Success/Failure 共同构成 Allow 的配对契约（v0.10.4）。
func (b *Breaker) Abandon() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.probing = false
	b.probeDeadline = time.Time{}
}

// SuccessGen Success 的世代形态：gen 与当前探测不匹配（陈旧迟到上报）时忽略。
func (b *Breaker) SuccessGen(gen uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if gen != 0 && b.probing && gen != b.probeGen {
		return // 陈旧探测的成功：不关闭（当前探测窗口不受旧探测污染）
	}
	b.consecutive = 0
	b.openUntil = time.Time{}
	b.probing = false
	b.probeDeadline = time.Time{}
}

// FailureGen Failure 的世代形态：gen 与当前探测不匹配（陈旧迟到上报）时
// **完全忽略**——旧探测已被失联宣告，其结果对新窗口无意义（重熔会振荡，
// 计数会污染；迟到甄别的结构性解，替代时间窗缓冲启发）。
func (b *Breaker) FailureGen(now time.Time, gen uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if gen != 0 && b.probing && gen != b.probeGen {
		return // 陈旧探测的迟到失败：忽略
	}
	b.consecutive++
	b.probeDeadline = time.Time{}
	if b.probing {
		// 当前探测失败：重新熔断
		b.openUntil = now.Add(b.cooldown)
		b.probing = false
		b.consecutive = b.trip
		return
	}
	if b.consecutive >= b.trip && !b.openAt(now) {
		b.openUntil = now.Add(b.cooldown)
	}
}

// AbandonGen Abandon 的世代形态：gen 不匹配时忽略（旧探测已失联，无占位可解）。
func (b *Breaker) AbandonGen(gen uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if gen != 0 && gen != b.probeGen {
		return
	}
	b.probing = false
	b.probeDeadline = time.Time{}
}

// Failure 记录一次真实发起且失败的调用；达阈值开熔断，半开探测失败重新熔断。
// 配对契约：仅对 Allow==true 放行过的调用上报（被拒调用从未发起，上报 Failure
// 会被当作探测失败而重新熔断）；结果未知（取消/放弃）上报 Abandon。
// 熔断 open 期间被拒请求记录的 Failure 不续期冷却（否则高流量下 openUntil 被
// 无限顺延，永远进不了半开）。
func (b *Breaker) Failure(now time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.consecutive++
	deadline := b.probeDeadline // 迟到上报甄别用（清零前快照）
	b.probeDeadline = time.Time{}
	if b.probing {
		// 迟到上报甄别（第十轮最小修）：超时兜底放行新探测后，旧探测的
		// 迟到 Failure 会误判为「当前探测失败」把新探测窗口瞬间重新熔断
		//（状态振荡）。仅在当前探测的时限窗内（含缓冲）才接受为探测失败，
		// 否则按 closed 态普通失败计。世代化 API 见重构机会（v2）。
		if !now.Before(deadline.Add(-2 * time.Second)) {
			b.openUntil = now.Add(b.cooldown)
			b.probing = false
			b.consecutive = b.trip
			return
		}
		b.probing = false // 陈旧上报：不重熔断，落回普通失败计数
	}
	if b.consecutive >= b.trip && !b.openAt(now) {
		b.openUntil = now.Add(b.cooldown)
	}
}

// openAt 当前是否处于熔断 open 期。
func (b *Breaker) openAt(now time.Time) bool {
	return !b.openUntil.IsZero() && now.Before(b.openUntil)
}

// Opened 是否处于熔断中。
func (b *Breaker) Opened(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.openAt(now)
}

// Breakers 按 key 索引的熔断板（并发安全）。
type Breakers struct {
	mu       sync.Mutex
	breakers map[string]*Breaker
	trip     int
	cooldown time.Duration
	// probeTimeout 半开探测时限（半开探测的 Breaker 级缺省见 DefaultProbeTimeout）。
	// 被保护操作的正常耗时会超过缺省时限时必须调大：探测时限内旧探测未返回就会被
	// 并发放行新探测（慢而健康的操作被并发双跑）。
	probeTimeout time.Duration
	// Now 时钟源（测试注入用；仅启动期可写，运行期并发改写有数据竞争）。
	Now func() time.Time
}

// Option 熔断板可选参数。
type Option func(*Breakers)

// WithProbeTimeout 覆盖半开探测时限（<=0 回退 DefaultProbeTimeout）。
// 典型场景：被保护操作带长超时（如 LLM agent 600s），探测时限须 >= 最长正常耗时。
func WithProbeTimeout(d time.Duration) Option {
	return func(bs *Breakers) {
		if d > 0 {
			bs.probeTimeout = d
		}
	}
}

// NewBreakers 创建熔断板。opts 可选（零值 = 全部采用缺省，既有调用方不受影响）。
func NewBreakers(trip int, cooldown time.Duration, opts ...Option) *Breakers {
	bs := &Breakers{
		breakers:     map[string]*Breaker{},
		trip:         trip,
		cooldown:     cooldown,
		probeTimeout: DefaultProbeTimeout,
		Now:          time.Now,
	}
	for _, o := range opts {
		o(bs)
	}
	return bs
}

func (bs *Breakers) get(name string) *Breaker {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	b, ok := bs.breakers[name]
	if !ok {
		b = New(bs.trip, bs.cooldown)
		b.probeTimeout = bs.probeTimeout
		bs.breakers[name] = b
	}
	return b
}

// Allow 按 key 判定是否放行。
func (bs *Breakers) Allow(name string) bool { return bs.get(name).Allow(bs.Now()) }

// Success 按 key 记录成功。
func (bs *Breakers) Success(name string) { bs.get(name).Success() }

// Failure 按 key 记录失败。
func (bs *Breakers) Failure(name string) { bs.get(name).Failure(bs.Now()) }

// Abandon 按 key 放弃在途半开探测（取消/终止语义，不计失败）。
func (bs *Breakers) Abandon(name string) { bs.get(name).Abandon() }

// AllowGen/SuccessGen/FailureGen/AbandonGen 世代形态（Breaker 同名方法的
// 多路转发——调用方持有 gen 跨请求上报）。
func (bs *Breakers) AllowGen(name string) (bool, uint64) {
	return bs.get(name).AllowGen(bs.Now())
}

func (bs *Breakers) SuccessGen(name string, gen uint64) { bs.get(name).SuccessGen(gen) }

func (bs *Breakers) FailureGen(name string, gen uint64) { bs.get(name).FailureGen(bs.Now(), gen) }

func (bs *Breakers) AbandonGen(name string, gen uint64) { bs.get(name).AbandonGen(gen) }

// Opened 按 key 判定是否熔断中。只读——未登记的 key 返回 false，不为查询
// 创建状态条目（监控轮询不该撑大内部 map；Allow/Success/Failure 才是登记时机）。
func (bs *Breakers) Opened(name string) bool {
	bs.mu.Lock()
	b, ok := bs.breakers[name]
	bs.mu.Unlock()
	if !ok {
		return false
	}
	return b.Opened(bs.Now())
}
