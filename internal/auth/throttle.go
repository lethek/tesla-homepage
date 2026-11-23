package auth

import (
	"strings"
	"sync"
	"time"
	"unicode"
)

// Throttle slows down guessing. Each key, such as a username or an address,
// may fail Limit times within Window. After that it is locked out for
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

// Wait returns how long the longest lockout among keys has left to run, or
// zero if none is locked.
func (t *Throttle) Wait(keys ...string) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.Now()
	var wait time.Duration
	for _, k := range keys {
		if e := t.entries[k]; e != nil && e.until.After(now) {
			wait = max(wait, e.until.Sub(now))
		}
	}
	return wait
}

// Fail records a failed attempt against key, which is allowed limit failures
// per window. It reports whether this failure caused a lockout, and for how
// long.
func (t *Throttle) Fail(key string, limit int) (locked bool, d time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.Now()
	if t.entries == nil {
		t.entries = map[string]*entry{}
	}
	if len(t.entries) >= maxEntries {
		t.prune(now)
	}
	e := t.entries[key]
	if e == nil {
		e = &entry{}
		t.entries[key] = e
	}
	e.touched = now
	if e.fails == 0 || now.Sub(e.first) > t.Window {
		if !e.until.After(now) && now.Sub(e.first) > t.Window+t.MaxLock {
			e.lockouts = 0 // quiet for long enough
		}
		e.fails, e.first = 0, now
	}
	e.fails++
	if e.fails < limit {
		return false, 0
	}
	d = t.BaseLock
	for i := 0; i < e.lockouts && d < t.MaxLock; i++ {
		d *= 2
	}
	d = min(d, t.MaxLock)
	e.lockouts++
	e.fails = 0
	e.until = now.Add(d)
	return true, d
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
