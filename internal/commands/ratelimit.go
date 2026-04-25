package commands

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

type limiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	buckets map[string][]time.Time
}

func newLimiter(limit int, window time.Duration) *limiter {
	return &limiter{
		limit:   limit,
		window:  window,
		buckets: make(map[string][]time.Time),
	}
}

func (l *limiter) Allow(userID string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-l.window)

	times := l.buckets[userID]
	var fresh []time.Time
	for _, t := range times {
		if t.After(cutoff) {
			fresh = append(fresh, t)
		}
	}
	if len(fresh) >= l.limit {
		l.buckets[userID] = fresh
		return false
	}
	fresh = append(fresh, now)
	if len(fresh) == 0 {
		delete(l.buckets, userID)
	} else {
		l.buckets[userID] = fresh
	}
	return true
}

func newToken() string {
	b := make([]byte, 16) // 128-bit entropy
	rand.Read(b)
	return hex.EncodeToString(b)
}
