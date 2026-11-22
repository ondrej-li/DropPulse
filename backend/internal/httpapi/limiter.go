package httpapi

import (
	"math"
	"sync"
	"time"
)

type tokenBucket struct {
	tokens   float64
	last     time.Time
	lastSeen time.Time
}

// Limiter is an in-process token bucket keyed by an arbitrary string. It is
// deliberately small: one backend process is the deployment target. A
// multi-instance deployment must move this state to a shared store.
type Limiter struct {
	mu         sync.Mutex
	buckets    map[string]*tokenBucket
	now        func() time.Time
	maxEntries int
}

// NewLimiter creates a limiter. now may be nil for the wall clock.
func NewLimiter(now func() time.Time) *Limiter {
	if now == nil {
		now = time.Now
	}
	return &Limiter{
		buckets:    make(map[string]*tokenBucket),
		now:        now,
		maxEntries: 50_000,
	}
}

// Allow consumes one token if available. The returned duration is a
// Retry-After hint when the request is rejected.
func (l *Limiter) Allow(key string, limit int, window time.Duration) (bool, time.Duration) {
	if limit <= 0 || window <= 0 {
		return false, window
	}
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()

	bucket, ok := l.buckets[key]
	if !ok {
		if len(l.buckets) >= l.maxEntries {
			l.cleanupLocked(now)
		}
		bucket = &tokenBucket{
			tokens:   float64(limit),
			last:     now,
			lastSeen: now,
		}
		l.buckets[key] = bucket
	}
	elapsed := now.Sub(bucket.last).Seconds()
	if elapsed > 0 {
		rate := float64(limit) / window.Seconds()
		bucket.tokens = math.Min(float64(limit), bucket.tokens+elapsed*rate)
		bucket.last = now
	}
	bucket.lastSeen = now
	if bucket.tokens >= 1 {
		bucket.tokens--
		return true, 0
	}
	rate := float64(limit) / window.Seconds()
	wait := time.Duration(math.Ceil((1-bucket.tokens)/rate)) * time.Second
	if wait < time.Second {
		wait = time.Second
	}
	return false, wait
}

func (l *Limiter) cleanupLocked(now time.Time) {
	cutoff := now.Add(-10 * time.Minute)
	for key, bucket := range l.buckets {
		if bucket.lastSeen.Before(cutoff) {
			delete(l.buckets, key)
		}
	}
	if len(l.buckets) >= l.maxEntries {
		// Bound memory under a sustained key-spray. Resetting admits a burst,
		// but the alternative is unbounded allocation from untrusted keys.
		clear(l.buckets)
	}
}
