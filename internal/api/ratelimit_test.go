package api

import (
	"strconv"
	"testing"
	"time"
)

// A throttled caller has to recover, or one burst would lock a device out until
// the process restarts.
func TestLimiterRefillsOverTime(t *testing.T) {
	clock := time.Now()
	limiter := newLimiter()
	limiter.now = func() time.Time { return clock }

	for i := range commandBurst {
		if !limiter.allow("k") {
			t.Fatalf("request %d was refused inside the burst", i)
		}
	}
	if limiter.allow("k") {
		t.Fatal("the burst was not bounded")
	}

	// One second buys exactly the sustained rate back, and no more.
	clock = clock.Add(time.Second)
	for i := range commandRefillPerSecond {
		if !limiter.allow("k") {
			t.Fatalf("refilled request %d was refused", i)
		}
	}
	if limiter.allow("k") {
		t.Fatal("a second bought more than the sustained rate")
	}

	// A long idle period restores the full burst, never more.
	clock = clock.Add(time.Hour)
	for i := range commandBurst {
		if !limiter.allow("k") {
			t.Fatalf("request %d was refused after a long idle period", i)
		}
	}
	if limiter.allow("k") {
		t.Fatal("idling accumulated more than one burst")
	}
}

// The map is what an adversarial or merely unlucky installation could grow, so
// it stays bounded whatever it is fed.
func TestLimiterKeepsItsMapBounded(t *testing.T) {
	clock := time.Now()
	limiter := newLimiter()
	limiter.now = func() time.Time { return clock }

	for i := range limiterMaxKeys * 2 {
		limiter.allow(strconv.Itoa(i))
		// Move on slowly enough that nothing ages out: eviction, not the idle
		// sweep, has to be what holds the line.
		clock = clock.Add(time.Millisecond)
	}
	if len(limiter.buckets) > limiterMaxKeys {
		t.Fatalf("limiter holds %d keys, want at most %d", len(limiter.buckets), limiterMaxKeys)
	}
}
