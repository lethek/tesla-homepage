package auth

import (
	"strings"
	"sync"
	"time"
	"unicode"
)

// Throttle slows down guessing. Each key, such as a username or an address,
// may make Limit attempts within Window, counting every attempt that is
// admitted and giving back the ones that succeed. Once a key has used them
// up, the next attempt locks it out for
// BaseLock, and for twice as long each time it is locked out again, up to
// MaxLock. Nothing is locked for good, so an attacker can delay a real user
// but not shut them out.
//
// Counts live in memory and start again when the server restarts.
type Throttle struct {
	Window   time.Duration
	BaseLock time.Duration
	MaxLock  time.Duration
	// Now is the clock. It is time.Now unless a test replaces it.
	Now func() time.Time

	mu      sync.Mutex
	entries map[string]*entry
}

type entry struct {
	fails    int
	first    time.Time // of the first failure in this window
	lockouts int
	until    time.Time
	touched  time.Time
}

// maxEntries bounds memory against an attacker who invents keys.
const maxEntries = 20000

// NewThrottle returns a Throttle with 15 minute windows and lockouts that
// start at one minute and top out at an hour.
func NewThrottle() *Throttle {
	return &Throttle{Window: 15 * time.Minute, BaseLock: time.Minute, MaxLock: time.Hour, Now: time.Now}
}

// Key builds a key from a kind such as "user" or "ip" and a value. Values are
// case-folded and shortened so that case and length can't be used to dodge
// the limit. Two values get the same key exactly when strings.EqualFold says
// they are equal, which is how sign-in matches usernames. Lower-casing is not
// enough: "ross" and "roſs" (long s) name the same account but lower-case
// differently, so each spelling would get its own allowance of guesses.
func Key(kind, value string) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) > 64 {
		runes = runes[:64]
	}
	for i, r := range runes {
		runes[i] = foldRune(r)
	}
	return kind + ":" + string(runes)
}

// foldRune returns the smallest rune that r is equal to under simple case
// folding, the same equivalence strings.EqualFold uses.
func foldRune(r rune) rune {
	least := r
	for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
		least = min(least, f)
	}
	return least
}

// Limit is how many attempts a key gets per window.
type Limit struct {
	Key string
	Max int
}

// Admit asks to make one attempt. It counts the attempt against every limit
// before the password is checked, in one step under one lock. Counting only
// after a failure would let a burst of simultaneous attempts all pass the
// check before any failure was recorded.
//
// If every key has attempts left, they are all charged and wait is zero. The
// caller must call Refund once the attempt succeeds, so that only failures
// stay counted. If any key is locked out, or has used up its attempts, nothing
// is charged and wait is how long to refuse for. started lists the keys whose
// lockout began with this call.
func (t *Throttle) Admit(limits ...Limit) (wait time.Duration, started []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.Now()
	if t.entries == nil {
		t.entries = map[string]*entry{}
	}
	if len(t.entries) >= maxEntries {
		t.prune(now)
	}
	for _, l := range limits {
		if e := t.entries[l.Key]; e != nil && e.until.After(now) {
			wait = max(wait, e.until.Sub(now))
		}
	}
	if wait > 0 {
		return wait, nil
	}
	for _, l := range limits {
		e := t.entries[l.Key]
		if e == nil {
			continue
		}
		if now.Sub(e.first) > t.Window {
			if now.Sub(e.first) > t.Window+t.MaxLock {
				e.lockouts = 0 // quiet for long enough
			}
			e.fails = 0
		}
		if e.fails < l.Max {
			continue
		}
		d := t.BaseLock
		for i := 0; i < e.lockouts && d < t.MaxLock; i++ {
			d *= 2
		}
		d = min(d, t.MaxLock)
		e.lockouts++
		e.fails = 0
		e.until = now.Add(d)
		wait = max(wait, d)
		started = append(started, l.Key)
	}
	if wait > 0 {
		return wait, started
	}
	for _, l := range limits {
		e := t.entries[l.Key]
		if e == nil {
			e = &entry{}
			t.entries[l.Key] = e
		}
		if e.fails == 0 {
			e.first = now
		}
		e.fails++
		e.touched = now
	}
	return 0, nil
}

// Refund gives back the attempt Admit charged to each key, after the attempt
// succeeded.
func (t *Throttle) Refund(keys ...string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, k := range keys {
		if e := t.entries[k]; e != nil && e.fails > 0 {
			e.fails--
		}
	}
}

// Reset forgets key's failures and lockouts, after a successful sign-in.
func (t *Throttle) Reset(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.entries, key)
}

// prune drops entries with nothing left to remember. If that isn't enough it
// drops the least recently used half. Callers must hold t.mu.
func (t *Throttle) prune(now time.Time) {
	for k, e := range t.entries {
		if !e.until.After(now) && now.Sub(e.touched) > t.Window {
			delete(t.entries, k)
		}
	}
	if len(t.entries) < maxEntries {
		return
	}
	cut := now.Add(-t.Window / 2)
	for k, e := range t.entries {
		if e.touched.Before(cut) {
			delete(t.entries, k)
		}
	}
	if len(t.entries) >= maxEntries { // everything is recent: start over
		t.entries = map[string]*entry{}
	}
}
