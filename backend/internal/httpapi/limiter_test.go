package httpapi

import (
	"testing"
	"time"
)

func TestLimiterAllowsBurstAndRefills(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	limiter := NewLimiter(func() time.Time { return now })
	if ok, _ := limiter.Allow("client", 2, time.Minute); !ok {
		t.Fatal("first request should be allowed")
	}
	if ok, _ := limiter.Allow("client", 2, time.Minute); !ok {
		t.Fatal("second request should be allowed")
	}
	if ok, retry := limiter.Allow("client", 2, time.Minute); ok || retry <= 0 {
		t.Fatalf("third request should be limited with a retry hint, got ok=%v retry=%v", ok, retry)
	}
	now = now.Add(31 * time.Second)
	if ok, _ := limiter.Allow("client", 2, time.Minute); !ok {
		t.Fatal("refilled bucket should allow a request")
	}
	if ok, _ := limiter.Allow("other", 2, time.Minute); !ok {
		t.Fatal("keys must be independent")
	}
}
