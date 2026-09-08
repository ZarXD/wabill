package whatsapp

import (
	"sync"
	"time"
)

// RateLimiterConfig configures rate limiting thresholds.
type RateLimiterConfig struct {
	MaxRequests    int           // e.g., 5 messages
	WindowDuration time.Duration // e.g., 10 seconds
	Cooldown       time.Duration // e.g., 8 seconds
}

type userLimitState struct {
	timestamps  []time.Time
	cooldownEnd time.Time
	warned      bool
}

// UserRateLimiter implements an in-memory sliding window rate limiter per user.
type UserRateLimiter struct {
	mu     sync.Mutex
	config RateLimiterConfig
	users  map[string]*userLimitState
}

// NewUserRateLimiter creates a new UserRateLimiter.
func NewUserRateLimiter(cfg RateLimiterConfig) *UserRateLimiter {
	if cfg.MaxRequests <= 0 {
		cfg.MaxRequests = 5
	}
	if cfg.WindowDuration <= 0 {
		cfg.WindowDuration = 10 * time.Second
	}
	if cfg.Cooldown <= 0 {
		cfg.Cooldown = 8 * time.Second
	}

	l := &UserRateLimiter{
		config: cfg,
		users:  make(map[string]*userLimitState),
	}

	// Periodic cleanup of idle user entries
	go l.cleanupLoop()

	return l
}

// CheckLimit checks if the user is allowed to proceed.
// Returns:
// - allowed: true if under threshold
// - shouldWarn: true if this is the first spam violation triggering cooldown (to send a gentle warning)
func (l *UserRateLimiter) CheckLimit(userKey string) (allowed bool, shouldWarn bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	state, exists := l.users[userKey]
	if !exists {
		state = &userLimitState{}
		l.users[userKey] = state
	}

	// 1. Check if user is actively in cooldown
	if now.Before(state.cooldownEnd) {
		// Already in cooldown: drop request silently without duplicate spam warnings
		return false, false
	}

	// 2. Prune timestamps outside the current sliding window
	cutoff := now.Add(-l.config.WindowDuration)
	valid := state.timestamps[:0]
	for _, t := range state.timestamps {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}
	state.timestamps = valid

	// 3. Check if limit exceeded
	if len(state.timestamps) >= l.config.MaxRequests {
		state.cooldownEnd = now.Add(l.config.Cooldown)
		// Reset timestamps during cooldown
		state.timestamps = nil
		return false, true // first violation in this window: warn user
	}

	// 4. Allowed: record timestamp
	state.timestamps = append(state.timestamps, now)
	return true, false
}

func (l *UserRateLimiter) cleanupLoop() {
	ticker := time.NewTicker(2 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		l.mu.Lock()
		now := time.Now()
		for key, state := range l.users {
			// If no timestamps in the last 2 minutes and not in cooldown, remove entry
			if now.After(state.cooldownEnd) && len(state.timestamps) == 0 {
				delete(l.users, key)
			}
		}
		l.mu.Unlock()
	}
}

// KeyedMutex provides per-key mutual exclusion so requests for the same user
// are processed sequentially, while requests for different users run concurrently.
type KeyedMutex struct {
	mu    sync.Mutex
	locks map[string]*refMutex
}

type refMutex struct {
	sync.Mutex
	refCount int
}

// NewKeyedMutex creates a new KeyedMutex.
func NewKeyedMutex() *KeyedMutex {
	return &KeyedMutex{
		locks: make(map[string]*refMutex),
	}
}

// Lock acquires a lock for the given key.
func (km *KeyedMutex) Lock(key string) {
	km.mu.Lock()
	m, ok := km.locks[key]
	if !ok {
		m = &refMutex{}
		km.locks[key] = m
	}
	m.refCount++
	km.mu.Unlock()

	m.Lock()
}

// Unlock releases the lock for the given key and removes it if unused.
func (km *KeyedMutex) Unlock(key string) {
	km.mu.Lock()
	m, ok := km.locks[key]
	if !ok {
		km.mu.Unlock()
		return
	}
	m.refCount--
	if m.refCount <= 0 {
		delete(km.locks, key)
	}
	km.mu.Unlock()

	m.Unlock()
}
