package auth

import (
	"strings"
	"sync"
	"sync/atomic"
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

// attempts makes n failed attempts (admitted and never refunded) and returns
// the last result.
func attempts(th *Throttle, l Limit, n int) (wait time.Duration, started []string) {
	for i := 0; i < n; i++ {
		wait, started = th.Admit(l)
	}
	return
}

func TestThrottleLocksAfterLimitAndBacksOff(t *testing.T) {
	th, c := newTestThrottle()
	l := Limit{Key("user", "Admin"), 5}

	// Five attempts are admitted. The sixth locks the key.
	if wait, _ := attempts(th, l, 5); wait != 0 {
		t.Fatalf("refused within the limit: %v", wait)
	}
	wait, started := th.Admit(l)
	if wait != time.Minute || len(started) != 1 {
		t.Fatalf("sixth attempt: wait=%v started=%v", wait, started)
	}
	// Later attempts are refused without starting another lockout.
	if wait, started := th.Admit(l); wait <= 0 || len(started) != 0 {
		t.Fatalf("while locked: wait=%v started=%v", wait, started)
	}
	// Case doesn't dodge the key.
	if wait, _ := th.Admit(Limit{Key("user", "ADMIN"), 5}); wait == 0 {
		t.Fatal("key is case sensitive")
	}

	// It clears by itself, and the next lockout is twice as long.
	c.t = c.t.Add(time.Minute + time.Second)
	if wait, _ := attempts(th, l, 5); wait != 0 {
		t.Fatalf("still locked after the lockout ended: %v", wait)
	}
	if wait, _ := th.Admit(l); wait != 2*time.Minute {
		t.Fatalf("second lockout: %v", wait)
	}
	// The lockout is capped.
	for n := 0; n < 10; n++ {
		c.t = c.t.Add(2 * time.Hour)
		attempts(th, l, 5)
		if wait, _ := th.Admit(l); wait > time.Hour {
			t.Fatalf("lockout %v above the cap", wait)
		}
	}
}

func TestThrottleForgetsOldAttemptsAndRefundsSuccesses(t *testing.T) {
	th, c := newTestThrottle()
	l := Limit{Key("ip", "10.0.0.1"), 5}
	attempts(th, l, 4)
	// Attempts spread over more than a window don't add up.
	c.t = c.t.Add(16 * time.Minute)
	if wait, _ := attempts(th, l, 5); wait != 0 {
		t.Fatalf("old attempts counted: %v", wait)
	}

	// A refund gives an attempt back, so only failures count.
	th.Reset(l.Key)
	for i := 0; i < 20; i++ {
		if wait, _ := th.Admit(l); wait != 0 {
			t.Fatalf("refused success %d", i)
		}
		th.Refund(l.Key)
	}
	// Reset forgets everything.
	attempts(th, l, 3)
	th.Reset(l.Key)
	if wait, _ := attempts(th, l, 5); wait != 0 {
		t.Fatal("attempts survived a reset")
	}
	// Keys are independent.
	if wait, _ := th.Admit(Limit{Key("ip", "10.0.0.2"), 5}); wait != 0 {
		t.Fatal("another key is locked")
	}
}

// A refused attempt charges nothing, on any key.
func TestThrottleChargesAllKeysOrNone(t *testing.T) {
	th, _ := newTestThrottle()
	user := Limit{Key("user", "a"), 2}
	ip := Limit{Key("ip", "1.2.3.4"), 100}
	attempts(th, user, 2)
	th.Admit(user, ip) // user is used up: this locks it and must not charge ip
	th.mu.Lock()
	n := len(th.entries)
	e := th.entries[ip.Key]
	th.mu.Unlock()
	if e != nil || n != 1 {
		t.Fatalf("refused attempt charged the other key: %d entries, ip=%+v", n, e)
	}
}

// Attempts that arrive together all ask before any of them has been checked.
// Only the allowance may get through.
func TestThrottleAdmitsNoMoreThanTheLimitInABurst(t *testing.T) {
	th, _ := newTestThrottle()
	user := Limit{Key("user", "admin"), 5}
	ip := Limit{Key("ip", "1.2.3.4"), 20}

	var admitted atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if wait, _ := th.Admit(user, ip); wait == 0 {
				admitted.Add(1) // would go on to check a password
			}
		}()
	}
	close(start)
	wg.Wait()
	if n := admitted.Load(); n != 5 {
		t.Fatalf("%d of 200 simultaneous attempts were admitted, want 5", n)
	}
}

func TestThrottleBoundsMemory(t *testing.T) {
	th, _ := newTestThrottle()
	for i := 0; i < maxEntries*2; i++ {
		th.Admit(Limit{Key("user", string(rune('a'+i%26))+string(rune(i))), 5})
	}
	if len(th.entries) > maxEntries {
		t.Fatalf("%d entries", len(th.entries))
	}
}

// Sign-in matches usernames with strings.EqualFold, so every spelling it
// treats as one account must share one key.
func TestKeyMatchesEqualFold(t *testing.T) {
	names := []string{"ross", "ROSS", "Ross", "roſs", "roſſ", "ROſS", "θeta", "ϑeta", "ϴETA", "k", "K", "K", "µ", "μ", "Μ", "ǅ", "ǆ", "Ǆ", "İ", "i"}
	for _, a := range names {
		for _, b := range names {
			if got, want := Key("user", a) == Key("user", b), strings.EqualFold(a, b); got != want {
				t.Errorf("Key(%q)==Key(%q) is %v, EqualFold says %v", a, b, got, want)
			}
		}
	}
	if Key("user", " ross ") != Key("user", "ross") {
		t.Error("surrounding space changes the key")
	}
	if Key("user", "x") == Key("ip", "x") {
		t.Error("kinds share keys")
	}
}
