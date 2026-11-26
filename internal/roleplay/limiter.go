package roleplay

import (
	"sync"
	"time"
)

// limiter is a small per-member token bucket. It keeps a single member from
// spending the model budget or flooding a channel.
type limiter struct {
	mu       sync.Mutex
	capacity int
	window   time.Duration
	now      func() time.Time
	entries  map[string]*limiterEntry
}

type limiterEntry struct {
	remaining int
	reset     time.Time
}

func newLimiter(capacity int, window time.Duration, now func() time.Time) *limiter {
	if capacity < 1 {
		capacity = 1
	}
	if window <= 0 {
		window = 10 * time.Second
	}
	if now == nil {
		now = time.Now
	}
	return &limiter{
		capacity: capacity,
		window:   window,
		now:      now,
		entries:  make(map[string]*limiterEntry),
	}
}

// allow reports whether the member may act now, consuming one token.
func (l *limiter) allow(userID string) bool {
	if l == nil || userID == "" {
		return false
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	entry, ok := l.entries[userID]
	if !ok || now.After(entry.reset) {
		l.entries[userID] = &limiterEntry{
			remaining: l.capacity - 1,
			reset:     now.Add(l.window),
		}
		if len(l.entries) > 1000 {
			l.prune(now)
		}
		return true
	}
	if entry.remaining <= 0 {
		return false
	}
	entry.remaining--
	return true
}

func (l *limiter) prune(now time.Time) {
	for id, entry := range l.entries {
		if now.After(entry.reset) {
			delete(l.entries, id)
		}
	}
}
