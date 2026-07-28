package api

import (
	"sync"
	"time"
)

// Command rate limiting.
//
// Automation webhooks have always been rate limited; ordinary device commands
// were not, so one looping client — a stuck slider, a retry loop, a script — could
// drive a bulb or a TV as fast as the LAN allowed. On router-class hardware that
// is the whole CPU budget, and some devices simply stop answering when hammered.
//
// The budget is a token bucket per caller *and* device: a burst of slider
// commits still lands intact, sustained abuse is throttled, and one busy device
// never starves the others.
const (
	// commandBurst is how many commands may arrive back to back. A slider drag
	// commits far fewer than this (see web/src/lib/slider-commit.svelte.ts).
	commandBurst = 20
	// commandRefillPerSecond is the sustained rate once the burst is spent.
	commandRefillPerSecond = 5
	// limiterMaxKeys bounds memory. A household cannot reach it (accounts ×
	// devices), so it only matters as a ceiling under something pathological.
	limiterMaxKeys = 512
	// limiterIdle is how long an unused bucket is kept before it can be reclaimed.
	limiterIdle = 5 * time.Minute
)

// bucket is one caller's budget for one device.
type bucket struct {
	tokens float64
	seen   time.Time
}

// limiter is a bounded set of token buckets. It is safe for concurrent use.
type limiter struct {
	// now is time.Now in production and a stub in tests.
	now func() time.Time

	mu      sync.Mutex
	buckets map[string]*bucket
}

func newLimiter() *limiter {
	return &limiter{now: time.Now, buckets: make(map[string]*bucket)}
}

// allow consumes one token for key and reports whether the request may proceed.
func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	entry, ok := l.buckets[key]
	if !ok {
		l.reclaim(now)
		entry = &bucket{tokens: commandBurst, seen: now}
		l.buckets[key] = entry
	} else {
		refill := now.Sub(entry.seen).Seconds() * commandRefillPerSecond
		if refill > 0 {
			entry.tokens = min(entry.tokens+refill, commandBurst)
		}
		entry.seen = now
	}
	if entry.tokens < 1 {
		return false
	}
	entry.tokens--
	return true
}

// reclaim keeps the map bounded. It first drops buckets nobody has used for a
// while — which are full again anyway, so forgetting them changes no decision —
// and only then evicts the least recently used one.
func (l *limiter) reclaim(now time.Time) {
	if len(l.buckets) < limiterMaxKeys {
		return
	}
	for key, entry := range l.buckets {
		if now.Sub(entry.seen) > limiterIdle {
			delete(l.buckets, key)
		}
	}
	if len(l.buckets) < limiterMaxKeys {
		return
	}
	var oldestKey string
	var oldest time.Time
	found := false
	for key, entry := range l.buckets {
		if !found || entry.seen.Before(oldest) {
			oldestKey, oldest, found = key, entry.seen, true
		}
	}
	if found {
		delete(l.buckets, oldestKey)
	}
}

// limiterKey identifies one caller's budget for one device. The administrator's
// token is shared by definition, so every admin session shares one bucket.
//
// Which kind of account it is belongs in the key because user ids come from
// names: somebody called "Admin" gets the id "admin", and would otherwise
// silently share the administrator's budget.
func limiterKey(p Principal, deviceID string) string {
	kind := "user"
	if p.Admin {
		kind = "admin"
	}
	return kind + "\x00" + p.UserID + "\x00" + deviceID
}
