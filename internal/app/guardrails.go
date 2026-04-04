package app

import (
	"log"
	"net/http"
	"sync"
	"time"
)

type loginAttemptState struct {
	windowStarted time.Time
	failures      int
	blockedUntil  time.Time
}

type LoginRateLimiterSnapshot struct {
	Window              time.Duration
	ActiveBlockedKeys   int
	RecentBlockedEvents int
}

type LoginRateLimiter struct {
	mu          sync.Mutex
	window      time.Duration
	maxAttempts int
	entries     map[string]loginAttemptState
	blockedAt   []time.Time
}

func NewLoginRateLimiter(window time.Duration, maxAttempts int) *LoginRateLimiter {
	if window <= 0 || maxAttempts <= 0 {
		return nil
	}
	return &LoginRateLimiter{
		window:      window,
		maxAttempts: maxAttempts,
		entries:     map[string]loginAttemptState{},
	}
}

func (l *LoginRateLimiter) Allow(key string, now time.Time) (bool, time.Duration) {
	if l == nil || key == "" {
		return true, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(now)
	state := l.entries[key]
	if !state.blockedUntil.IsZero() && state.blockedUntil.After(now) {
		return false, state.blockedUntil.Sub(now).Round(time.Second)
	}
	return true, 0
}

func (l *LoginRateLimiter) RegisterFailure(key string, now time.Time) bool {
	if l == nil || key == "" {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(now)
	state := l.entries[key]
	if state.windowStarted.IsZero() || now.Sub(state.windowStarted) > l.window {
		state.windowStarted = now
		state.failures = 0
		state.blockedUntil = time.Time{}
	}
	state.failures++
	if state.failures >= l.maxAttempts {
		state.blockedUntil = now.Add(l.window)
		state.failures = 0
		state.windowStarted = now
		l.blockedAt = append(l.blockedAt, now)
		log.Printf("event=login_throttled blocked_until=%s", state.blockedUntil.UTC().Format(time.RFC3339))
		l.entries[key] = state
		return true
	}
	l.entries[key] = state
	return false
}

func (l *LoginRateLimiter) RegisterSuccess(key string) {
	if l == nil || key == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, key)
}

func (l *LoginRateLimiter) Snapshot(now time.Time) LoginRateLimiterSnapshot {
	if l == nil {
		return LoginRateLimiterSnapshot{}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(now)
	snapshot := LoginRateLimiterSnapshot{Window: l.window}
	for _, state := range l.entries {
		if !state.blockedUntil.IsZero() && state.blockedUntil.After(now) {
			snapshot.ActiveBlockedKeys++
		}
	}
	snapshot.RecentBlockedEvents = len(l.blockedAt)
	return snapshot
}

func (l *LoginRateLimiter) pruneLocked(now time.Time) {
	for key, state := range l.entries {
		if !state.blockedUntil.IsZero() && state.blockedUntil.After(now) {
			continue
		}
		if !state.windowStarted.IsZero() && now.Sub(state.windowStarted) <= l.window {
			continue
		}
		delete(l.entries, key)
	}
	if len(l.blockedAt) == 0 {
		return
	}
	cutoff := now.Add(-l.window)
	trimmed := l.blockedAt[:0]
	for _, blockedAt := range l.blockedAt {
		if !blockedAt.After(cutoff) {
			continue
		}
		trimmed = append(trimmed, blockedAt)
	}
	l.blockedAt = trimmed
}

func (a *App) WithRecovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				requestID := requestIDFromContext(r.Context())
				log.Printf("event=http_panic request_id=%s method=%s path=%s panic=%v", requestID, r.Method, r.URL.Path, recovered)
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
