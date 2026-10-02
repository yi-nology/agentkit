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
)

// Breaker 单 key 熔断器。
type Breaker struct {
	mu          sync.Mutex
	trip        int
	cooldown    time.Duration
	consecutive int
	openUntil   time.Time
	probing     bool
}

// New 创建熔断器。trip=连续失败阈值，cooldown=冷却期。
func New(trip int, cooldown time.Duration) *Breaker {
	if trip <= 0 {
		trip = DefaultTripThreshold
	}
	if cooldown <= 0 {
		cooldown = DefaultCooldown
	}
	return &Breaker{trip: trip, cooldown: cooldown}
}

// Allow 返回是否放行；半开时仅放行一个探测请求。
func (b *Breaker) Allow(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.openUntil.IsZero() {
		return true // closed
	}
	if now.Before(b.openUntil) {
		return false // 熔断中
	}
	if b.probing {
		return false // 半开探测已在途
	}
	b.probing = true
	return true
}

// Success 关闭熔断。
func (b *Breaker) Success() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.consecutive = 0
	b.openUntil = time.Time{}
	b.probing = false
}

// Failure 记录失败；达阈值开熔断，半开探测失败重新熔断。
func (b *Breaker) Failure(now time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.consecutive++
	if b.probing {
		b.openUntil = now.Add(b.cooldown)
		b.probing = false
		b.consecutive = b.trip
		return
	}
	if b.consecutive >= b.trip {
		b.openUntil = now.Add(b.cooldown)
	}
}

// Opened 是否处于熔断中。
func (b *Breaker) Opened(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return !b.openUntil.IsZero() && now.Before(b.openUntil)
}

// Breakers 按 key 索引的熔断板（并发安全）。
type Breakers struct {
	mu       sync.Mutex
	breakers map[string]*Breaker
	trip     int
	cooldown time.Duration
	Now      func() time.Time
}

// NewBreakers 创建熔断板。
func NewBreakers(trip int, cooldown time.Duration) *Breakers {
	return &Breakers{
		breakers: map[string]*Breaker{},
		trip:     trip,
		cooldown: cooldown,
		Now:      time.Now,
	}
}

func (bs *Breakers) get(name string) *Breaker {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	b, ok := bs.breakers[name]
	if !ok {
		b = New(bs.trip, bs.cooldown)
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

// Opened 按 key 判定是否熔断中。
func (bs *Breakers) Opened(name string) bool { return bs.get(name).Opened(bs.Now()) }
