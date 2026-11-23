package web

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lethek/tesla-homepage/internal/auth"
	"github.com/lethek/tesla-homepage/internal/icons"
	"github.com/lethek/tesla-homepage/internal/library"
	"github.com/lethek/tesla-homepage/internal/store"
)

type env struct {
	t     *testing.T
	srv   *httptest.Server
	store *store.Store
	// server lets tests reach inside, such as to move the throttle's clock.
	server *Server
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Every outbound request goes to a fake CDN. It serves the Icon library
	// and its PNGs, and 404s everything else, so site icons fall back to
	// the placeholder.
	cdn := httptest.NewServer(http.HandlerFunc(fakeCDN))
	t.Cleanup(cdn.Close)
	target, _ := url.Parse(cdn.URL)
	client := &http.Client{Transport: rewriteTransport{target}}
	resolver := &icons.Resolver{Client: client, Save: st.SaveIcon}
	lib := library.New(st.LibraryCachePath(), store.WriteFile)
	lib.LocalPath, lib.LocalDir = st.LocalLibraryPath(), st.LocalLibraryDir()
	lib.BaseURL, lib.Client = cdn.URL, client
	lib.SimpleDataURL, lib.SimpleBaseURL = cdn.URL+"/si-data.json", cdn.URL+"/si"
	lib.SelfhstURL = cdn.URL + "/sh"
	lib.LobeDataURL, lib.LobeBaseURL = cdn.URL+"/lh-data", cdn.URL+"/lh"
	s, err := New(st, &auth.Sessions{Secret: []byte("test")}, resolver, lib)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return &env{t: t, srv: srv, store: st, server: s}
}

const pngHeader = "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"

// siteIconRequests counts requests for a site's own icon.
var siteIconRequests atomic.Int32

func fakeCDN(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/favicon.ico" || r.URL.Path == "/apple-touch-icon.png" {
		siteIconRequests.Add(1)
	}
	switch {
	case r.URL.Path == "/metadata.json":
		w.Write([]byte(`{
			"netflix": {"aliases": [], "categories": ["Streaming"]},
			"spotify": {"aliases": [], "categories": ["media"], "colors": {"light": "spotify-light", "dark": "spotify-dark"}},
			"prime-video": {"aliases": [], "categories": [], "colors": {"light": "prime-video-light", "dark": "prime-video"}},
			"github": {"aliases": ["GitHub"], "categories": [], "colors": {"dark": "github", "light": "github-light"}},
			"youtube": {"aliases": [], "categories": ["Streaming"]}
		}`))
	case r.URL.Path == "/si-data.json":
		w.Write([]byte(`[
			{"title": "SoundCloud", "slug": "soundcloud", "hex": "FF5500"},
			{"title": "Roku", "slug": "roku", "hex": "662D91"},
			{"title": "Netflix", "slug": "netflix", "hex": "E50914"}
		]`))
	case r.URL.Path == "/sh/index.json":
		w.Write([]byte(`[{"Name": "Plex", "Reference": "plex", "PNG": "Yes", "Light": "Yes", "Dark": "Yes", "Tags": "Streaming"}]`))
	case r.URL.Path == "/lh-data":
		w.Write([]byte(`{"tags": {"latest": "1.0.0"}}`))
	case r.URL.Path == "/lh-data@1.0.0":
		w.Write([]byte(`{"files": [{"name": "/dark/claude.png"}, {"name": "/dark/claude-color.png"}, {"name": "/light/claude.png"}]}`))
	case strings.HasPrefix(r.URL.Path, "/sh/png/"), strings.HasPrefix(r.URL.Path, "/lh@latest/"):
		// selfh.st and LobeHub serve PNGs. The body names the path so tests can
		// tell files apart.
		w.Write([]byte(pngHeader + r.URL.Path))
	case strings.HasPrefix(r.URL.Path, "/si/"):
		// Simple Icons serves SVGs. The body names the path so tests can tell
		// colours apart.
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><title>` + r.URL.Path + `</title></svg>`))
	case strings.HasPrefix(r.URL.Path, "/png/"):
		// The body ends with the file name so tests can tell variants apart.
		w.Write([]byte(pngHeader + strings.TrimPrefix(r.URL.Path, "/png/")))
	default:
		http.NotFound(w, r)
	}
}

// rewriteTransport sends every request to target, keeping the path.
type rewriteTransport struct{ target *url.URL }

func (t rewriteTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.URL.Scheme, r.URL.Host = t.target.Scheme, t.target.Host
	return http.DefaultTransport.RoundTrip(r)
}

// iconBody returns the stored bytes of a Tile's icon.
func (e *env) iconBody(name string) string {
	p, err := e.store.IconPath(name)
	if err != nil {
		e.t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		e.t.Fatal(err)
	}
	return string(b)
}

// client is a browser with its own cookie jar that doesn't follow redirects.
func (e *env) client() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func (e *env) do(c *http.Client, method, path string, body io.Reader, contentType string, htmx bool) (*http.Response, string) {
	e.t.Helper()
	req, _ := http.NewRequest(method, e.srv.URL+path, body)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	resp, err := c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func (e *env) get(c *http.Client, path string) (*http.Response, string) {
	return e.do(c, http.MethodGet, path, nil, "", false)
}

func (e *env) post(c *http.Client, path string, form url.Values) (*http.Response, string) {
	return e.do(c, http.MethodPost, path, strings.NewReader(form.Encode()), "application/x-www-form-urlencoded", false)
}

func (e *env) hxPost(c *http.Client, path string, form url.Values) (*http.Response, string) {
	return e.do(c, http.MethodPost, path, strings.NewReader(form.Encode()), "application/x-www-form-urlencoded", true)
}

func wantRedirect(t *testing.T, resp *http.Response, path string) {
	t.Helper()
	if got := resp.Header.Get("Location"); resp.StatusCode != http.StatusSeeOther || got != path {
		t.Fatalf("got %d to %q, want redirect to %q", resp.StatusCode, got, path)
	}
}

// signedInAdmin runs first-run setup and starts with an empty Homepage.
func (e *env) signedInAdmin() *http.Client {
	c := e.client()
	resp, _ := e.post(c, "/setup", url.Values{"username": {"admin"}, "password": {"password1"}, "confirm": {"password1"}})
	wantRedirect(e.t, resp, "/welcome")
	resp, _ = e.post(c, "/welcome", url.Values{"start": {"empty"}})
	wantRedirect(e.t, resp, "/")
	return c
}

func (e *env) login(username, password string) *http.Client {
	c := e.client()
	resp, _ := e.post(c, "/login", url.Values{"username": {username}, "password": {password}})
	wantRedirect(e.t, resp, "/")
	return c
}

func TestFirstRunSetup(t *testing.T) {
	e := newEnv(t)
	c := e.client()

	resp, _ := e.get(c, "/")
	wantRedirect(t, resp, "/setup")

	resp, body := e.post(c, "/setup", url.Values{"username": {"admin"}, "password": {"short"}, "confirm": {"short"}})
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "at least 8") {
		t.Fatalf("weak password: %d", resp.StatusCode)
	}

	c = e.signedInAdmin()
	resp, body = e.get(c, "/")
	if resp.StatusCode != 200 || !strings.Contains(body, "Nothing here yet") {
		t.Fatalf("home: %d", resp.StatusCode)
	}

	// A second setup attempt must not create another admin.
	other := e.client()
	resp, _ = e.post(other, "/setup", url.Values{"username": {"evil"}, "password": {"password1"}, "confirm": {"password1"}})
	wantRedirect(t, resp, "/login")
	if users, _ := e.store.Users(); len(users) != 1 {
		t.Fatalf("got %d users", len(users))
	}
}

func TestLoginRequired(t *testing.T) {
	e := newEnv(t)
	e.signedInAdmin()
	anon := e.client()
	for _, path := range []string{"/", "/edit", "/account/password", "/admin/users", "/icons/x.png"} {
		resp, _ := e.get(anon, path)
		wantRedirect(t, resp, "/login")
	}
	resp, _ := e.hxPost(anon, "/edit/sections", url.Values{"title": {"x"}})
	if resp.Header.Get("HX-Redirect") != "/login" {
		t.Fatalf("htmx request not redirected to login: %d", resp.StatusCode)
	}

	resp, _ = e.post(anon, "/login", url.Values{"username": {"admin"}, "password": {"wrong-password"}})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong password: %d", resp.StatusCode)
	}
	e.login("ADMIN", "password1")
}

func TestTamperedCookieRejected(t *testing.T) {
	e := newEnv(t)
	e.signedInAdmin()
	users, _ := e.store.Users()
	forged := (&auth.Sessions{Secret: []byte("not the secret")}).Encode(users[0].ID, 0)

	c := e.client()
	u, _ := url.Parse(e.srv.URL)
	c.Jar.SetCookies(u, []*http.Cookie{{Name: auth.CookieName, Value: forged}})
	resp, _ := e.get(c, "/")
	wantRedirect(t, resp, "/login")
}

func TestDraftCancelAndDone(t *testing.T) {
	e := newEnv(t)
	c := e.signedInAdmin()

	if resp, _ := e.get(c, "/edit"); resp.StatusCode != 200 {
		t.Fatalf("edit: %d", resp.StatusCode)
	}
	_, frag := e.hxPost(c, "/edit/sections", url.Values{"title": {"Streaming"}})
	if !strings.Contains(frag, "Streaming") || strings.Contains(frag, "<html") {
		t.Fatal("expected editor fragment with the new section")
	}
	if _, body := e.get(c, "/"); strings.Contains(body, "Streaming") {
		t.Fatal("Draft change visible before Done")
	}

	// Leaving and coming back resumes the Draft.
	if _, body := e.get(c, "/edit"); !strings.Contains(body, "unsaved changes") || !strings.Contains(body, "Streaming") {
		t.Fatal("Draft not resumed")
	}

	resp, _ := e.post(c, "/edit/cancel", nil)
	wantRedirect(t, resp, "/")
	if _, body := e.get(c, "/"); strings.Contains(body, "Streaming") {
		t.Fatal("cancelled change published")
	}

	e.get(c, "/edit")
	e.hxPost(c, "/edit/sections", url.Values{"title": {"Streaming"}})
	e.hxPost(c, "/edit/settings", url.Values{"title": {"Car"}, "overflow": {"scroll"}})
	resp, _ = e.post(c, "/edit/done", nil)
	wantRedirect(t, resp, "/")
	_, body := e.get(c, "/")
	if !strings.Contains(body, "Streaming") || !strings.Contains(body, "overflow-scroll") || !strings.Contains(body, "<span>Car</span>") {
		t.Fatal("published Homepage missing changes")
	}
}

func TestTiles(t *testing.T) {
	e := newEnv(t)
	c := e.signedInAdmin()
	e.get(c, "/edit")
	e.hxPost(c, "/edit/sections", url.Values{"title": {"Apps"}})
	users, _ := e.store.Users()
	draft, _, _ := e.store.Draft(users[0].ID)
	sid := draft.Sections[0].ID

	// A javascript: URL is rejected and the form is re-targeted into the dialog.
	resp, body := e.hxPost(c, "/edit/sections/"+sid+"/tiles", url.Values{"title": {"Bad"}, "url": {"javascript:alert(1)"}})
	if resp.Header.Get("HX-Retarget") != "#dialog-body" || !strings.Contains(body, "http://") {
		t.Fatalf("bad URL not rejected: %q", resp.Header.Get("HX-Retarget"))
	}

	// Upload an icon.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("title", "Example")
	mw.WriteField("url", "https://example.com")
	fw, _ := mw.CreateFormFile("icon_file", "icon.png")
	fw.Write([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"))
	mw.Close()
	resp, body = e.do(c, http.MethodPost, "/edit/sections/"+sid+"/tiles", &buf, mw.FormDataContentType(), true)
	if resp.StatusCode != 200 || !strings.Contains(body, "Example") || !strings.Contains(body, `src="/icons/`) {
		t.Fatalf("upload tile: %d %s", resp.StatusCode, resp.Header.Get("HX-Retarget"))
	}

	// No icon given and the site has none: placeholder.
	_, body = e.hxPost(c, "/edit/sections/"+sid+"/tiles", url.Values{"title": {"zed"}, "url": {"https://example.org"}})
	if !strings.Contains(body, `placeholder" aria-hidden="true">Z<`) {
		t.Fatal("placeholder with initial not rendered")
	}

	draft, _, _ = e.store.Draft(users[0].ID)
	tiles := draft.Sections[0].Tiles
	icon := tiles[0].Icon
	if resp, _ := e.get(c, "/icons/"+icon); resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("icon: %d", resp.StatusCode)
	}

	// Editing without new icon input keeps the existing icon.
	e.hxPost(c, "/edit/tiles/"+tiles[0].ID, url.Values{"title": {"Renamed"}, "url": {"https://example.com"}})
	draft, _, _ = e.store.Draft(users[0].ID)
	if got := draft.FindTile(tiles[0].ID); got.Title != "Renamed" || got.Icon != icon {
		t.Fatalf("edit tile: %+v", got)
	}

	// Reorder, then delete.
	order := `[{"id":"` + sid + `","tiles":["` + tiles[1].ID + `","` + tiles[0].ID + `"]}]`
	if resp, _ := e.do(c, http.MethodPost, "/edit/order", strings.NewReader(order), "application/json", false); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("order: %d", resp.StatusCode)
	}
	if resp, _ := e.do(c, http.MethodPost, "/edit/order", strings.NewReader(`[]`), "application/json", false); resp.StatusCode != http.StatusConflict {
		t.Fatalf("stale order: %d", resp.StatusCode)
	}
	e.hxPost(c, "/edit/tiles/"+tiles[0].ID+"/delete", nil)
	draft, _, _ = e.store.Draft(users[0].ID)
	if got := draft.Sections[0].Tiles; len(got) != 1 || got[0].ID != tiles[1].ID {
		t.Fatalf("after reorder and delete: %+v", got)
	}
}

func TestAdmin(t *testing.T) {
	e := newEnv(t)
	admin := e.signedInAdmin()

	resp, _ := e.post(admin, "/admin/users", url.Values{"username": {"bob"}, "password": {"bobpassword"}})
	wantRedirect(t, resp, "/admin/users?done=created")
	resp, _ = e.post(admin, "/admin/users", url.Values{"username": {"Bob"}, "password": {"bobpassword"}})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("duplicate user: %d", resp.StatusCode)
	}

	bob := e.login("bob", "bobpassword")
	resp, _ = e.get(bob, "/")
	wantRedirect(t, resp, "/welcome")
	if resp, _ := e.get(bob, "/admin/users"); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("non-admin got %d", resp.StatusCode)
	}
	if _, body := e.get(bob, "/welcome"); strings.Contains(body, "Manage users") {
		t.Fatal("menu shows Manage users to non-admin")
	}

	bobUser, _ := e.store.UserByUsername("bob")
	adminUser, _ := e.store.UserByUsername("admin")

	// Resetting Bob's password signs him out everywhere.
	resp, _ = e.post(admin, "/admin/users/"+bobUser.ID+"/password", url.Values{"password": {"newpassword"}})
	wantRedirect(t, resp, "/admin/users?done=reset")
	resp, _ = e.get(bob, "/welcome")
	wantRedirect(t, resp, "/login")
	bob = e.login("bob", "newpassword")

	if resp, _ := e.post(admin, "/admin/users/"+adminUser.ID+"/delete", nil); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("self-delete: %d", resp.StatusCode)
	}
	resp, _ = e.post(admin, "/admin/users/"+bobUser.ID+"/delete", nil)
	wantRedirect(t, resp, "/admin/users?done=deleted")
	resp, _ = e.get(bob, "/")
	wantRedirect(t, resp, "/login")
}

func TestChangePasswordKeepsThisDevice(t *testing.T) {
	e := newEnv(t)
	c := e.signedInAdmin()
	other := e.login("admin", "password1")

	resp, _ := e.post(c, "/account/password", url.Values{"current": {"password1"}, "password": {"password2"}, "confirm": {"password2"}})
	if resp.StatusCode != 200 {
		t.Fatalf("change password: %d", resp.StatusCode)
	}
	if resp, _ := e.get(c, "/"); resp.StatusCode != 200 {
		t.Fatalf("this device signed out: %d", resp.StatusCode)
	}
	resp, _ = e.get(other, "/")
	wantRedirect(t, resp, "/login")
}

func TestCrossOriginPostRejected(t *testing.T) {
	e := newEnv(t)
	c := e.signedInAdmin()
	e.get(c, "/edit")
	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+"/edit/sections", strings.NewReader("title=x"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-site POST: %d", resp.StatusCode)
	}
}
