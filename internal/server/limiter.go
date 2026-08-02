package server

import (
	"sync"
	"time"
)

type attemptLimiter struct {
	mu        sync.Mutex
	limit     int
	window    time.Duration
	attempts  map[string][]time.Time
	lastPrune time.Time
}

const maxLimiterKeys = 4096

func newAttemptLimiter(limit int, window time.Duration) *attemptLimiter {
	return &attemptLimiter{limit: limit, window: window, attempts: make(map[string][]time.Time)}
}

func (limiter *attemptLimiter) Allow(key string, now time.Time) bool {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()

	cutoff := now.Add(-limiter.window)
	if limiter.lastPrune.IsZero() || now.Sub(limiter.lastPrune) >= limiter.window || len(limiter.attempts) >= maxLimiterKeys {
		limiter.prune(cutoff)
		limiter.lastPrune = now
	}
	if _, exists := limiter.attempts[key]; !exists && len(limiter.attempts) >= maxLimiterKeys {
		return false
	}
	values := limiter.attempts[key]
	firstCurrent := 0
	for firstCurrent < len(values) && values[firstCurrent].Before(cutoff) {
		firstCurrent++
	}
	values = values[firstCurrent:]
	if len(values) >= limiter.limit {
		limiter.attempts[key] = values
		return false
	}
	limiter.attempts[key] = append(values, now)
	return true
}

func (limiter *attemptLimiter) prune(cutoff time.Time) {
	for key, values := range limiter.attempts {
		firstCurrent := 0
		for firstCurrent < len(values) && values[firstCurrent].Before(cutoff) {
			firstCurrent++
		}
		if firstCurrent == len(values) {
			delete(limiter.attempts, key)
			continue
		}
		limiter.attempts[key] = values[firstCurrent:]
	}
}

func (limiter *attemptLimiter) Reset(key string) {
	limiter.mu.Lock()
	delete(limiter.attempts, key)
	limiter.mu.Unlock()
}
