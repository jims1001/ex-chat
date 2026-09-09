package ratelimit_test

import (
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/pkg/ratelimit"
)

func TestRateLimiter(t *testing.T) {
	t.Run("Basic Allowance and Burst", func(t *testing.T) {
		// 5 tokens capacity, 2 tokens/sec refill
		limiter := ratelimit.New(2.0, 5.0)

		key := "user-ip-127.0.0.1"
		for i := 0; i < 5; i++ {
			if !limiter.Allow(key) {
				t.Errorf("expected token %d to be allowed under capacity", i+1)
			}
		}

		// 6th immediate request should be rejected
		if limiter.Allow(key) {
			t.Errorf("expected 6th immediate request to be rejected")
		}

		// Wait 600ms -> should refill at least 1 token (2 * 0.6 = 1.2)
		time.Sleep(600 * time.Millisecond)
		if !limiter.Allow(key) {
			t.Errorf("expected request to be allowed after token refill")
		}
	})

	t.Run("Multi Key Isolation", func(t *testing.T) {
		limiter := ratelimit.New(1.0, 1.0)

		if !limiter.Allow("account-1") {
			t.Errorf("expected account-1 first request to pass")
		}
		if limiter.Allow("account-1") {
			t.Errorf("expected account-1 second request to be blocked")
		}

		// Account 2 should be unaffected
		if !limiter.Allow("account-2") {
			t.Errorf("expected account-2 first request to pass independently")
		}
	})
}
