package server

import (
	"testing"
	"time"
)

func TestAttemptLimiter(t *testing.T) {
	limiter := newAttemptLimiter(2, time.Minute)
	now := time.Unix(1000, 0)
	if !limiter.Allow("key", now) || !limiter.Allow("key", now) {
		t.Fatal("initial attempts were rejected")
	}
	if limiter.Allow("key", now) {
		t.Fatal("attempt beyond limit was allowed")
	}
	if !limiter.Allow("key", now.Add(2*time.Minute)) {
		t.Fatal("attempt after window was rejected")
	}
	limiter.Reset("key")
	if !limiter.Allow("key", now) {
		t.Fatal("attempt after reset was rejected")
	}
}

func TestRemoteHostDropsTemporaryPort(t *testing.T) {
	if got := remoteHost("192.0.2.10:43210"); got != "192.0.2.10" {
		t.Fatalf("remoteHost() = %q", got)
	}
	if got := remoteHost("[2001:db8::1]:443"); got != "2001:db8::1" {
		t.Fatalf("remoteHost() IPv6 = %q", got)
	}
}

func TestLoginLimitCannotBeBypassedWithAnotherUsername(t *testing.T) {
	handler, _ := newTestHandler(t)
	for index := 0; index < 5; index++ {
		response := performRequest(handler, "POST", "/api/v1/auth/login",
			[]byte(`{"username":"attempt-`+time.Unix(int64(index), 0).Format("150405")+`","password":"wrong password"}`),
			map[string]string{"Content-Type": "application/json"})
		if response.Code != 401 {
			t.Fatalf("attempt %d status = %d", index, response.Code)
		}
	}
	blocked := performRequest(handler, "POST", "/api/v1/auth/login",
		[]byte(`{"username":"another-name","password":"wrong password"}`),
		map[string]string{"Content-Type": "application/json"})
	if blocked.Code != 429 {
		t.Fatalf("different username after limit status = %d", blocked.Code)
	}
}
