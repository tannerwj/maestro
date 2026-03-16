package orchestrator

import "sync"

type dispatchLimiter interface {
	TryAcquire() bool
	Release()
}

type semaphoreLimiter struct {
	mu       sync.Mutex
	capacity int
	inUse    int
}

type compositeLimiter struct {
	limiters []dispatchLimiter
}

func newSemaphoreLimiter(capacity int) *semaphoreLimiter {
	if capacity < 1 {
		capacity = 1
	}
	return &semaphoreLimiter{capacity: capacity}
}

func newCompositeLimiter(limiters ...dispatchLimiter) dispatchLimiter {
	filtered := make([]dispatchLimiter, 0, len(limiters))
	for _, limiter := range limiters {
		if limiter != nil {
			filtered = append(filtered, limiter)
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	if len(filtered) == 1 {
		return filtered[0]
	}
	return &compositeLimiter{limiters: filtered}
}

func (l *semaphoreLimiter) TryAcquire() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.inUse >= l.capacity {
		return false
	}
	l.inUse++
	return true
}

func (l *semaphoreLimiter) Release() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.inUse > 0 {
		l.inUse--
	}
}

func (l *compositeLimiter) TryAcquire() bool {
	acquired := make([]dispatchLimiter, 0, len(l.limiters))
	for _, limiter := range l.limiters {
		if limiter.TryAcquire() {
			acquired = append(acquired, limiter)
			continue
		}
		for i := len(acquired) - 1; i >= 0; i-- {
			acquired[i].Release()
		}
		return false
	}
	return true
}

func (l *compositeLimiter) Release() {
	for i := len(l.limiters) - 1; i >= 0; i-- {
		l.limiters[i].Release()
	}
}
