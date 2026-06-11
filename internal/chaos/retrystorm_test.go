package chaos

import (
	"testing"
	"time"
)

func TestRetryCounter_FailsFirstN(t *testing.T) {
	now := time.Unix(1000, 0)
	c := newRetryCounter(func() time.Time { return now })
	for i := 0; i < 3; i++ {
		if !c.shouldFail("k", 3, 60) {
			t.Fatalf("request %d should fail", i)
		}
	}
	if c.shouldFail("k", 3, 60) {
		t.Fatal("4th request should pass")
	}
}

func TestRetryCounter_WindowExpiryResets(t *testing.T) {
	now := time.Unix(1000, 0)
	clock := func() time.Time { return now }
	c := newRetryCounter(clock)
	for i := 0; i < 3; i++ {
		c.shouldFail("k", 3, 60)
	}
	now = now.Add(61 * time.Second)
	if !c.shouldFail("k", 3, 60) {
		t.Fatal("after window expiry the count should reset and fail again")
	}
}

func TestRetryCounter_KeysIndependent(t *testing.T) {
	now := time.Unix(1000, 0)
	c := newRetryCounter(func() time.Time { return now })
	c.shouldFail("a", 1, 60)
	if !c.shouldFail("b", 1, 60) {
		t.Fatal("key b must have its own counter")
	}
}
