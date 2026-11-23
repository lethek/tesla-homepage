package web

import (
	"fmt"
	"net/http"
	"net/netip"
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
	wantLogged(t, logs, "level=WARN", `msg="locked out after repeated failures"`, "key=user:ADMIN")
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

// Sign-in treats "ross" and "roſs" (long s) as one account, so guesses under
// either spelling must count against the same limit.
func TestLoginThrottleCoversEverySpellingOfAUsername(t *testing.T) {
	e, _ := clockedEnv(t)
	admin := e.signedInAdmin()
	e.post(admin, "/admin/users", url.Values{"username": {"ross"}, "password": {"rosspassword"}})

	spellings := []string{"ross", "roſs", "rosſ", "roſſ", "ROSS"}
	for _, name := range spellings {
		if resp, _ := e.attempt(name, "wrong-guess"); resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%q: %d", name, resp.StatusCode)
		}
	}
	for _, name := range append(spellings, "rOſS") {
		if resp, _ := e.attempt(name, "rosspassword"); resp.StatusCode != http.StatusTooManyRequests {
			t.Errorf("%q dodged the lockout: %d", name, resp.StatusCode)
		}
	}
}

var loginSeq int

// loginFrom signs in as a wrong guess with the given X-Forwarded-For values.
func (e *env) loginFrom(xff ...string) *http.Response {
	e.t.Helper()
	// A new name each time, so only the address limit is in play.
	loginSeq++
	name := fmt.Sprintf("someone%d", loginSeq)
	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+"/login", strings.NewReader(url.Values{"username": {name}, "password": {"wrong-guess"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, v := range xff {
		req.Header.Add("X-Forwarded-For", v)
	}
	resp, err := e.client().Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func TestForgedForwardedForDoesNotDodgeTheAddressLimit(t *testing.T) {
	e, _ := clockedEnv(t)
	e.signedInAdmin()
	logs := captureLogs(t)

	// Nobody is a trusted proxy, so the header is ignored: a new "address" per
	// request doesn't give each guess a fresh allowance.
	for i := 1; i <= 25; i++ {
		resp := e.loginFrom(fmt.Sprintf("203.0.113.%d", i))
		if resp.StatusCode == http.StatusTooManyRequests {
			if i != 21 {
				t.Fatalf("locked out at %d, want 21", i)
			}
			if strings.Contains(logs.String(), "203.0.113.") {
				t.Fatal("forged address was logged as the client's")
			}
			return
		}
	}
	t.Fatal("never locked out: X-Forwarded-For was believed")
}

func TestForwardedForIsReadOnlyFromTrustedProxies(t *testing.T) {
	e, _ := clockedEnv(t)
	e.signedInAdmin()
	e.server.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32"), netip.MustParsePrefix("10.0.0.0/8")}
	logs := captureLogs(t)

	// The test client connects from 127.0.0.1, a trusted proxy. The client's
	// address is the rightmost entry that isn't a proxy, so whatever the
	// client forged to the left is ignored.
	e.loginFrom("198.51.100.99, 192.0.2.7, 10.1.2.3")
	wantLogged(t, logs, `msg="login failed"`, "ip=192.0.2.7")
	if strings.Contains(logs.String(), "198.51.100.99") {
		t.Fatal("a forged left-hand entry was believed")
	}

	// Distinct real clients behind the proxy have their own limits.
	for i := 0; i < 25; i++ {
		e.loginFrom(fmt.Sprintf("192.0.2.%d", 100+i))
	}
	if resp := e.loginFrom("192.0.2.200"); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("an unrelated client behind the proxy was locked out: %d", resp.StatusCode)
	}

	// A garbled header isn't guessed at: the proxy's own address is used.
	logs2 := captureLogs(t)
	e.loginFrom("not-an-address")
	wantLogged(t, logs2, `msg="login failed"`, "ip=127.0.0.1")
}
