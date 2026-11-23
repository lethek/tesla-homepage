package web

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// clockedEnv returns an env whose throttle clock the test controls.
func clockedEnv(t *testing.T) (*env, *time.Time) {
	t.Helper()
	e := newEnv(t)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	e.server.throttle.Now = func() time.Time { return now }
	return e, &now
}

func (e *env) attempt(user, pass string) (*http.Response, string) {
	return e.post(e.client(), "/login", url.Values{"username": {user}, "password": {pass}})
}

func TestLoginIsThrottledPerUsername(t *testing.T) {
	e, now := clockedEnv(t)
	e.signedInAdmin()
	logs := captureLogs(t)

	// Five wrong passwords lock the account, even against the right one.
	for i := 1; i <= 5; i++ {
		if resp, _ := e.attempt("admin", "wrong-guess"); resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("guess %d: %d", i, resp.StatusCode)
		}
	}
	wantLogged(t, logs, "level=WARN", `msg="locked out after repeated failures"`, "key=user:admin")
	resp, body := e.attempt("admin", "password1")
	if resp.StatusCode != http.StatusTooManyRequests || !strings.Contains(body, "Too many failed attempts") || resp.Header.Get("Retry-After") == "" {
		t.Fatalf("locked login: %d %s", resp.StatusCode, body)
	}
	if strings.Contains(body, "Wrong username") {
		t.Fatal("locked page looks like a wrong password")
	}
	wantLogged(t, logs, "level=WARN", `msg="login throttled"`)

	// Another account isn't affected, and the name's case doesn't dodge it.
	if resp, _ := e.attempt("ghost", "wrong-guess"); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("other account: %d", resp.StatusCode)
	}
	if resp, _ := e.attempt("ADMIN", "password1"); resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("upper-case name dodged the lockout: %d", resp.StatusCode)
	}

	// The lockout ends by itself, and a success clears the count.
	*now = now.Add(61 * time.Second)
	e.login("admin", "password1")
	for i := 0; i < 4; i++ {
		e.attempt("admin", "wrong-guess")
	}
	e.login("admin", "password1")
	for i := 0; i < 4; i++ {
		e.attempt("admin", "wrong-guess")
	}
	if resp, _ := e.attempt("admin", "password1"); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("failures survived a success: %d", resp.StatusCode)
	}
}

func TestLoginIsThrottledPerAddress(t *testing.T) {
	e, now := clockedEnv(t)
	e.signedInAdmin()

	// Spreading guesses over many names doesn't help: the address is locked
	// out within 20 failures, and then even a fresh name is refused.
	locked := 0
	for i := 1; i <= 40 && locked == 0; i++ {
		if resp, _ := e.attempt(fmt.Sprintf("user%d", i), "wrong-guess"); resp.StatusCode == http.StatusTooManyRequests {
			locked = i
		}
	}
	if locked != 21 {
		t.Fatalf("address locked out at attempt %d, want 21", locked)
	}
	if resp, _ := e.attempt("admin", "password1"); resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("right password from a locked-out address: %d", resp.StatusCode)
	}
	*now = now.Add(61 * time.Second)
	e.login("admin", "password1")
}

func TestPasswordChangeGuessesAreThrottled(t *testing.T) {
	e, now := clockedEnv(t)
	c := e.signedInAdmin()
	change := func(current string) *http.Response {
		resp, _ := e.post(c, "/account/password", url.Values{"current": {current}, "password": {"new-password1"}, "confirm": {"new-password1"}})
		return resp
	}
	for i := 1; i <= 5; i++ {
		if resp := change("wrong-guess"); resp.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("guess %d: %d", i, resp.StatusCode)
		}
	}
	if resp := change("password1"); resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("right password while locked: %d", resp.StatusCode)
	}
	*now = now.Add(61 * time.Second)
	if resp := change("password1"); resp.StatusCode != http.StatusOK {
		t.Fatalf("after the lockout: %d", resp.StatusCode)
	}
}
