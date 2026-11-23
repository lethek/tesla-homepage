package auth

import (
	"testing"
	"time"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newTestThrottle() (*Throttle, *clock) {
	c := &clock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	th := NewThrottle()
	th.Now = c.now
	return th, c
}

func TestThrottleLocksAfterLimitAndBacksOff(t *testing.T) {
	th, c := newTestThrottle()
	k := Key("user", "Admin")

	for i := 1; i < 5; i++ {
		if locked, _ := th.Fail(k, 5); locked || th.Wait(k) != 0 {
			t.Fatalf("locked after %d failures", i)
		}
	}
	locked, d := th.Fail(k, 5)
	if !locked || d != time.Minute || th.Wait(k) != time.Minute {
		t.Fatalf("fifth failure: locked=%v d=%v wait=%v", locked, d, th.Wait(k))
	}
	// Case doesn't dodge the key.
	if th.Wait(Key("user", "ADMIN")) == 0 {
		t.Fatal("key is case sensitive")
	}

	// It clears by itself, and the next lockout is twice as long.
	c.t = c.t.Add(time.Minute + time.Second)
	if th.Wait(k) != 0 {
		t.Fatal("still locked after the lockout ended")
	}
	for i := 0; i < 4; i++ {
		th.Fail(k, 5)
	}
	if _, d := th.Fail(k, 5); d != 2*time.Minute {
		t.Fatalf("second lockout: %v", d)
	}
	// The lockout is capped.
	for n := 0; n < 10; n++ {
		c.t = c.t.Add(2 * time.Hour)
		for i := 0; i < 5; i++ {
			if _, d := th.Fail(k, 5); d > time.Hour {
				t.Fatalf("lockout %v above the cap", d)
			}
		}
	}
}

func TestThrottleForgetsOldFailuresAndSuccess(t *testing.T) {
	th, c := newTestThrottle()
	k := Key("ip", "10.0.0.1")
	for i := 0; i < 4; i++ {
		th.Fail(k, 5)
	}
	// Failures spread over more than a window don't add up.
	c.t = c.t.Add(16 * time.Minute)
	if locked, _ := th.Fail(k, 5); locked {
		t.Fatal("old failures counted")
	}
	// A success starts again.
	for i := 0; i < 3; i++ {
		th.Fail(k, 5)
	}
	th.Reset(k)
	for i := 0; i < 4; i++ {
		if locked, _ := th.Fail(k, 5); locked {
			t.Fatal("failures survived a reset")
		}
	}
	// Keys are independent.
	if th.Wait(Key("ip", "10.0.0.2")) != 0 {
		t.Fatal("another key is locked")
	}
}

func TestThrottleBoundsMemory(t *testing.T) {
	th, _ := newTestThrottle()
	for i := 0; i < maxEntries*2; i++ {
		th.Fail(Key("user", string(rune('a'+i%26))+string(rune(i))), 5)
	}
	if len(th.entries) > maxEntries {
		t.Fatalf("%d entries", len(th.entries))
	}
}
