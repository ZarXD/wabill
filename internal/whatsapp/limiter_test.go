package whatsapp_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"wabill/internal/whatsapp"
)

func TestUserRateLimiter(t *testing.T) {
	limiter := whatsapp.NewUserRateLimiter(whatsapp.RateLimiterConfig{
		MaxRequests:    3,
		WindowDuration: 500 * time.Millisecond,
		Cooldown:       500 * time.Millisecond,
	})

	user := "user123"

	// 1st request - allowed
	allowed, warn := limiter.CheckLimit(user)
	if !allowed || warn {
		t.Fatalf("Expected 1st request to be allowed, got allowed=%v, warn=%v", allowed, warn)
	}

	// 2nd request - allowed
	allowed, warn = limiter.CheckLimit(user)
	if !allowed || warn {
		t.Fatalf("Expected 2nd request to be allowed, got allowed=%v, warn=%v", allowed, warn)
	}

	// 3rd request - allowed
	allowed, warn = limiter.CheckLimit(user)
	if !allowed || warn {
		t.Fatalf("Expected 3rd request to be allowed, got allowed=%v, warn=%v", allowed, warn)
	}

	// 4th request - exceeded limit, should warn
	allowed, warn = limiter.CheckLimit(user)
	if allowed || !warn {
		t.Fatalf("Expected 4th request to be blocked with warn, got allowed=%v, warn=%v", allowed, warn)
	}

	// 5th request during cooldown - blocked silently (no duplicate warn)
	allowed, warn = limiter.CheckLimit(user)
	if allowed || warn {
		t.Fatalf("Expected 5th request to be blocked without warn, got allowed=%v, warn=%v", allowed, warn)
	}

	// Other user should not be affected
	allowedOther, _ := limiter.CheckLimit("other_user")
	if !allowedOther {
		t.Fatalf("Expected other_user to be allowed")
	}

	// Wait for cooldown to expire
	time.Sleep(600 * time.Millisecond)

	// After cooldown, should be allowed again
	allowed, _ = limiter.CheckLimit(user)
	if !allowed {
		t.Fatalf("Expected user to be allowed again after cooldown")
	}
}

func TestKeyedMutex(t *testing.T) {
	km := whatsapp.NewKeyedMutex()

	user1 := "user_A"
	user2 := "user_B"

	var counter int64
	var wg sync.WaitGroup

	// Test sequential execution for same key
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			km.Lock(user1)
			defer km.Unlock(user1)

			current := atomic.LoadInt64(&counter)
			time.Sleep(10 * time.Millisecond)
			atomic.StoreInt64(&counter, current+1)
		}()
	}

	// Test that different key doesn't block
	wg.Add(1)
	go func() {
		defer wg.Done()
		km.Lock(user2)
		defer km.Unlock(user2)
		// User B executes freely
	}()

	wg.Wait()

	if counter != 5 {
		t.Fatalf("Expected counter to be 5, got %d", counter)
	}
}
