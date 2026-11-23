package web

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
)

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func captureLogs(t *testing.T) *lockedBuffer {
	t.Helper()
	buf := &lockedBuffer{}
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	return buf
}

func wantLogged(t *testing.T, logs *lockedBuffer, fragments ...string) {
	t.Helper()
	for _, line := range strings.Split(logs.String(), "\n") {
		ok := true
		for _, f := range fragments {
			ok = ok && strings.Contains(line, f)
		}
		if ok {
			return
		}
	}
	t.Fatalf("no log line with all of %q in:\n%s", fragments, logs)
}

func TestAuditLog(t *testing.T) {
	e := newEnv(t)
	logs := captureLogs(t)

	admin := e.signedInAdmin()
	wantLogged(t, logs, `msg="first admin created and signed in"`, "user=admin")
	wantLogged(t, logs, `msg="homepage created"`, "start=empty")

	// Sign-ins, failed or not, and sign-outs.
	e.post(e.client(), "/login", url.Values{"username": {"admin"}, "password": {"wrong-password"}})
	wantLogged(t, logs, "level=WARN", `msg="login failed"`, "username=admin", `reason="wrong password"`)
	e.post(e.client(), "/login", url.Values{"username": {"ghost"}, "password": {"wrong-password"}})
	wantLogged(t, logs, `msg="login failed"`, "username=ghost", `reason="unknown user"`)
	e.post(admin, "/logout", nil)
	wantLogged(t, logs, "msg=logout", "user=admin")
	admin = e.login("admin", "password1")
	wantLogged(t, logs, "msg=login", "user=admin", "ip=")

	// Users and passwords.
	e.post(admin, "/admin/users", url.Values{"username": {"bob"}, "password": {"bobpassword"}})
	wantLogged(t, logs, `msg="user created"`, "user=admin", "target=bob")
	users, _ := e.store.Users()
	var bobID string
	for _, u := range users {
		if u.Username == "bob" {
			bobID = u.ID
		}
	}
	e.post(admin, "/admin/users/"+bobID+"/password", url.Values{"password": {"newpassword1"}})
	wantLogged(t, logs, `msg="password reset"`, "user=admin", "target=bob")
	bob := e.login("bob", "newpassword1")
	e.post(bob, "/account/password", url.Values{"current": {"newpassword1"}, "password": {"another-pass1"}, "confirm": {"another-pass1"}})
	wantLogged(t, logs, `msg="password changed"`, "user=bob")
	if resp, _ := e.get(bob, "/admin/library"); resp.StatusCode != http.StatusForbidden {
		t.Fatal("expected 403")
	}
	wantLogged(t, logs, "level=WARN", `msg="request failed"`, "user=bob", "status=403", "path=/admin/library")
	e.post(admin, "/admin/users/"+bobID+"/delete", nil)
	wantLogged(t, logs, `msg="user deleted"`, "target=bob")

	// Tiles and the library.
	e.get(admin, "/edit")
	e.hxPost(admin, "/edit/sections", url.Values{"title": {"Music"}})
	wantLogged(t, logs, `msg="section added"`, "section=Music")
	sid, _, _ := e.draftTile()
	e.hxPost(admin, "/edit/sections/"+sid+"/tiles", url.Values{"title": {"Spotify"}, "url": {"https://open.spotify.com"}, "source": {"library"}, "library": {"spotify"}, "plate": {"none"}})
	wantLogged(t, logs, `msg="tile added"`, "tile=Spotify", "library_icon=spotify")
	// Library downloads are logged by the remote request log, which main wires up.
	e.post(admin, "/admin/library/refresh", nil)
	wantLogged(t, logs, `msg="icon library refreshed"`, "user=admin")
	e.localForm(admin, "/admin/library/local", "Home NAS", "", "", pngHeader)
	wantLogged(t, logs, `msg="custom icon added"`, "slug=home-nas")
	e.localForm(admin, "/admin/library/local", "Home NAS", "", "", pngHeader)
	wantLogged(t, logs, "level=WARN", `msg="custom icon save failed"`)
	e.post(admin, "/admin/library/local/home-nas/delete", nil)
	wantLogged(t, logs, `msg="custom icon deleted"`, "slug=home-nas")
	e.post(admin, "/edit/done", nil)
	wantLogged(t, logs, `msg="edit published"`)

	for _, secret := range []string{"password1", "bobpassword", "newpassword1", "another-pass1", "wrong-password"} {
		if strings.Contains(logs.String(), secret) {
			t.Errorf("log contains the password %q", secret)
		}
	}
}
