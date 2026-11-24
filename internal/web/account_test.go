package web

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/lethek/tesla-homepage/internal/store"
)

// importFile posts an exported file to the import form.
func (e *env) importFile(c *http.Client, content string) (*http.Response, string) {
	e.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "homepage.json")
	fw.Write([]byte(content))
	mw.Close()
	return e.do(c, http.MethodPost, "/account/import", &buf, mw.FormDataContentType(), false)
}

var exportName = regexp.MustCompile(`^attachment; filename="homepage-\d{4}-\d{2}-\d{2}-\d{6}Z\.json"$`)

func (e *env) adminID() string {
	u, _ := e.store.UserByUsername("admin")
	return u.ID
}

func TestSignOutOtherDevices(t *testing.T) {
	e := newEnv(t)
	c := e.signedInAdmin()
	other := e.login("admin", "password1")

	resp, body := e.post(c, "/account/signout", nil)
	if resp.StatusCode != 200 || !strings.Contains(body, "Other devices have been signed out") {
		t.Fatalf("sign out others: %d", resp.StatusCode)
	}
	if resp, _ := e.get(c, "/"); resp.StatusCode != 200 {
		t.Fatal("this device was signed out")
	}
	resp, _ = e.get(other, "/")
	wantRedirect(t, resp, "/login")
}

func TestAccountPageDetails(t *testing.T) {
	e := newEnv(t)
	admin := e.signedInAdmin()
	e.post(admin, "/admin/users", url.Values{"username": {"bob"}, "password": {"bobpassword"}})
	bob := e.login("bob", "bobpassword")

	e.get(admin, "/edit")
	e.hxPost(admin, "/edit/sections", url.Values{"title": {"One"}})
	e.post(admin, "/edit/done", nil)
	e.get(admin, "/edit")
	e.hxPost(admin, "/edit/sections", url.Values{"title": {"Two"}})

	_, body := e.get(admin, "/account")
	for _, want := range []string{"Signed in as <strong>admin</strong>", "Admin</span>", "1 section and 0 tiles", "unsaved changes"} {
		if !strings.Contains(body, want) {
			t.Errorf("admin account page lacks %q", want)
		}
	}
	if strings.Contains(body, "/account/delete") {
		t.Error("the admin is offered account deletion")
	}
	_, body = e.get(bob, "/account")
	if !strings.Contains(body, "/account/delete") || !strings.Contains(body, "haven't set up a homepage") {
		t.Errorf("unexpected account page for a new user: %s", body)
	}
}

func TestExportThenImportRestoresHomepage(t *testing.T) {
	e := newEnv(t)
	c := e.signedInAdmin()
	icon, err := e.store.SaveIcon([]byte(pngHeader+"mine"), ".png")
	if err != nil {
		t.Fatal(err)
	}
	hp := store.EmptyHomepage()
	hp.Title, hp.Overflow = "Car", store.OverflowScroll
	hp.Sections = []store.Section{{ID: "s1", Title: "Video", Tiles: []store.Tile{
		{ID: "t1", Title: "Plex", URL: "https://app.plex.tv", Icon: icon, Library: "plex", Plate: "#112233", Padding: 5, Fullscreen: true},
		{ID: "t2", Title: "Plain", URL: "https://example.com"},
	}}}
	e.store.SaveHomepage(e.adminID(), hp)

	resp, exported := e.get(c, "/account/export")
	if resp.StatusCode != 200 || !exportName.MatchString(resp.Header.Get("Content-Disposition")) {
		t.Fatalf("export: %d %v", resp.StatusCode, resp.Header)
	}
	e.store.ResetHomepage(e.adminID())

	resp, body := e.importFile(c, exported)
	if resp.StatusCode != 200 || !strings.Contains(body, "Imported 1 section and 2 tiles") {
		t.Fatalf("import: %d %s", resp.StatusCode, body)
	}
	got, ok, _ := e.store.Homepage(e.adminID())
	if !ok || got.Title != "Car" || got.Overflow != store.OverflowScroll || len(got.Sections) != 1 || len(got.Sections[0].Tiles) != 2 {
		t.Fatalf("imported homepage: %+v", got)
	}
	if got.Sections[0].ID == "s1" || got.Sections[0].Tiles[0].ID == "t1" {
		t.Error("imported IDs were not replaced")
	}
	tile := got.Sections[0].Tiles[0]
	if tile.Icon != icon || tile.Library != "plex" || tile.Plate != "#112233" || tile.Padding != 5 || !tile.Fullscreen {
		t.Errorf("tile changed in the round trip: %+v", tile)
	}
	if path, _ := e.store.IconPath(tile.Icon); path == "" {
		t.Error("icon was not restored")
	}
}

func TestImportDiscardsDraft(t *testing.T) {
	e := newEnv(t)
	c := e.signedInAdmin()
	e.get(c, "/edit")
	e.hxPost(c, "/edit/sections", url.Values{"title": {"Unsaved"}})
	file := `{"format":"tesla-homepage","version":1,"homepage":{"title":"New","overflow":"wrap","sections":[]}}`
	if resp, body := e.importFile(c, file); resp.StatusCode != 200 {
		t.Fatalf("import: %d %s", resp.StatusCode, body)
	}
	if _, ok, _ := e.store.Draft(e.adminID()); ok {
		t.Fatal("draft survived the import")
	}
}

func TestImportRejectsBadFiles(t *testing.T) {
	e := newEnv(t)
	c := e.signedInAdmin()
	e.store.SaveHomepage(e.adminID(), store.Homepage{Title: "Keep me", Overflow: store.OverflowWrap, Sections: []store.Section{}})

	tile := func(title, u string) string {
		return `{"format":"tesla-homepage","version":1,"homepage":{"sections":[{"title":"S","tiles":[{"title":"` + title + `","url":"` + u + `"}]}]}}`
	}
	for name, file := range map[string]string{
		"not json":      "hello",
		"wrong format":  `{"format":"other","version":1}`,
		"wrong version": `{"format":"tesla-homepage","version":2}`,
		"script url":    tile("X", "javascript:alert(1)"),
		"no title":      tile("", "https://example.com"),
	} {
		if resp, _ := e.importFile(c, file); resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("%s: status %d, want 422", name, resp.StatusCode)
		}
	}
	if got, _, _ := e.store.Homepage(e.adminID()); got.Title != "Keep me" {
		t.Fatalf("a rejected import changed the homepage: %+v", got)
	}
}

func TestImportDropsUnusableIcons(t *testing.T) {
	e := newEnv(t)
	c := e.signedInAdmin()
	notImage := base64.StdEncoding.EncodeToString([]byte("just text"))
	file, _ := json.Marshal(map[string]any{
		"format": "tesla-homepage", "version": 1,
		"homepage": map[string]any{"sections": []any{map[string]any{"title": "S", "tiles": []any{
			map[string]any{"title": "Bad", "url": "https://example.com", "icon": "bad.png", "library": "x"},
			map[string]any{"title": "Missing", "url": "https://example.org", "icon": "gone.png"},
		}}}},
		"icons": map[string]string{"bad.png": notImage},
	})
	resp, body := e.importFile(c, string(file))
	if resp.StatusCode != 200 || !strings.Contains(body, "2 tiles had no usable icon") {
		t.Fatalf("import: %d %s", resp.StatusCode, body)
	}
	got, _, _ := e.store.Homepage(e.adminID())
	for _, tile := range got.Sections[0].Tiles {
		if tile.Icon != "" || tile.Library != "" {
			t.Errorf("unusable icon kept: %+v", tile)
		}
	}
}

func TestDeleteOwnAccount(t *testing.T) {
	e := newEnv(t)
	admin := e.signedInAdmin()
	e.post(admin, "/admin/users", url.Values{"username": {"bob"}, "password": {"bobpassword"}})
	bob := e.login("bob", "bobpassword")

	if resp, _ := e.get(admin, "/account/delete"); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("admin delete page: %d", resp.StatusCode)
	}
	if resp, _ := e.post(admin, "/account/delete", url.Values{"current": {"password1"}}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("admin delete: %d", resp.StatusCode)
	}
	if _, err := e.store.UserByUsername("admin"); err != nil {
		t.Fatal("the admin was deleted")
	}

	// Opening the page and entering a wrong password delete nothing.
	if _, body := e.get(bob, "/account/delete"); !strings.Contains(body, "cannot be undone") {
		t.Fatal("no warning on the delete page")
	}
	if resp, _ := e.post(bob, "/account/delete", url.Values{"current": {"wrong"}}); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("wrong password: %d", resp.StatusCode)
	}
	if _, err := e.store.UserByUsername("bob"); err != nil {
		t.Fatal("deleted with the wrong password")
	}

	resp, _ := e.post(bob, "/account/delete", url.Values{"current": {"bobpassword"}})
	wantRedirect(t, resp, "/login")
	if _, err := e.store.UserByUsername("bob"); err == nil {
		t.Fatal("account survived deletion")
	}
	resp, _ = e.get(bob, "/")
	wantRedirect(t, resp, "/login")
}

func TestImportKeepsEmptySectionTitles(t *testing.T) {
	e := newEnv(t)
	c := e.signedInAdmin()
	file := `{"format":"tesla-homepage","version":1,"homepage":{"sections":[{"title":"","tiles":[{"title":"A","url":"https://example.com"}]},{"tiles":[]}]}}`
	if resp, body := e.importFile(c, file); resp.StatusCode != 200 {
		t.Fatalf("import: %d %s", resp.StatusCode, body)
	}
	got, _, _ := e.store.Homepage(e.adminID())
	if len(got.Sections) != 2 || got.Sections[0].Title != "" || got.Sections[1].Title != "" {
		t.Fatalf("imported sections: %+v", got.Sections)
	}
}

func TestFormErrorsShowInsideTheirForm(t *testing.T) {
	e := newEnv(t)
	c := e.signedInAdmin()
	_, body := e.post(c, "/account/password", url.Values{"current": {"wrong"}, "password": {"newpassword1"}, "confirm": {"newpassword1"}})
	at := strings.Index(body, `role="alert"`)
	form := strings.Index(body, `action="/account/password"`)
	if at < 0 || form < 0 || at < form || strings.Count(body, `role="alert"`) != 1 {
		t.Fatalf("error at %d, password form at %d, alerts %d", at, form, strings.Count(body, `role="alert"`))
	}
}
