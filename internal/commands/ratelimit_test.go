package commands

import (
	"testing"
	"time"
)

func TestLimiter_allowsUnderLimit(t *testing.T) {
	l := newLimiter(5, time.Minute)
	for i := 0; i < 5; i++ {
		if !l.Allow("user1") {
			t.Errorf("request %d should be allowed", i+1)
		}
	}
}

func TestLimiter_blocksOverLimit(t *testing.T) {
	l := newLimiter(3, time.Minute)
	for i := 0; i < 3; i++ {
		l.Allow("user1")
	}
	if l.Allow("user1") {
		t.Error("4th request should be blocked")
	}
}

func TestLimiter_perUser_independent(t *testing.T) {
	l := newLimiter(2, time.Minute)
	l.Allow("alice")
	l.Allow("alice")
	// alice exhausted, bob should still be fine
	if !l.Allow("bob") {
		t.Error("bob should not be affected by alice's limit")
	}
}

func TestLimiter_windowExpiry(t *testing.T) {
	l := newLimiter(2, 50*time.Millisecond)
	l.Allow("user1")
	l.Allow("user1")
	// exhausted
	if l.Allow("user1") {
		t.Error("3rd request in window should be blocked")
	}
	// wait for window to expire
	time.Sleep(60 * time.Millisecond)
	if !l.Allow("user1") {
		t.Error("after window expires, request should be allowed")
	}
}

func TestNewToken_unique(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		tok := newToken()
		if seen[tok] {
			t.Errorf("duplicate token generated: %s", tok)
		}
		seen[tok] = true
		if len(tok) != 32 { // 16 bytes hex = 32 chars
			t.Errorf("token length: got %d want 32", len(tok))
		}
	}
}

func TestNewToken_hexCharsOnly(t *testing.T) {
	tok := newToken()
	for _, c := range tok {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("non-hex character %q in token %q", c, tok)
		}
	}
}
