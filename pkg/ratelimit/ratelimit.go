package ratelimit

import (
	"sync"
	"time"
)

type bucket struct {
	tokens     float64
	capacity   float64
	rate       float64 // tokens per second
	lastRefill time.Time
}

func (b *bucket) refill(now time.Time) {
	elapsed := now.Sub(b.lastRefill).Seconds()
	b.tokens += elapsed * b.rate
	if b.tokens > b.capacity {
		b.tokens = b.capacity
	}
	b.lastRefill = now
}

func (b *bucket) take(now time.Time, n float64) bool {
	b.refill(now)
	if b.tokens >= n {
		b.tokens -= n
		return true
	}
	return false
}

// Limiter manages multi-key token bucket rate limiters
type Limiter struct {
	rate     float64
	capacity float64
	buckets  map[string]*bucket
	mu       sync.Mutex
	ttl      time.Duration
}

// New creates a new Limiter with rate (tokens/sec) and capacity
func New(rate float64, capacity float64) *Limiter {
	return &Limiter{
		rate:     rate,
		capacity: capacity,
		buckets:  make(map[string]*bucket),
		ttl:      10 * time.Minute,
	}
}

// Allow checks if 1 token is available for the given key
func (l *Limiter) Allow(key string) bool {
	return l.AllowN(key, 1.0)
}

// AllowN checks if n tokens are available for the given key
func (l *Limiter) AllowN(key string, n float64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	b, exists := l.buckets[key]
	if !exists {
		b = &bucket{
			tokens:     l.capacity,
			capacity:   l.capacity,
			rate:       l.rate,
			lastRefill: now,
		}
		l.buckets[key] = b
	}

	return b.take(now, n)
}

// Cleanup removes stale buckets that haven't been accessed within the TTL
func (l *Limiter) Cleanup() {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	for k, b := range l.buckets {
		if now.Sub(b.lastRefill) > l.ttl {
			delete(l.buckets, k)
		}
	}
}
