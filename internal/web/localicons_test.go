package web

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// localForm posts a custom icon form. An empty image sends no file.
func (e *env) localForm(c *http.Client, path, name, aliases, tags, image string) (*http.Response, string) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("name", name)
	mw.WriteField("aliases", aliases)
	mw.WriteField("tags", tags)
	if image != "" {
		fw, _ := mw.CreateFormFile("image", "icon")
		fw.Write([]byte(image))
	}
	mw.Close()
	return e.do(c, http.MethodPost, path, &buf, mw.FormDataContentType(), false)
}

func TestCustomIconsAreSearchedWithCatalogues(t *testing.T) {
	e := newEnv(t)
	admin := e.signedInAdmin()

	resp, _ := e.localForm(admin, "/admin/library/local", "Home NAS", "storage box", "Homelab", pngHeader+"A")
	wantRedirect(t, resp, "/admin/library?done=saved")

	// The metadata is its own file, shaped like the cached catalogue, and
	// the catalogues' cache is untouched.
	meta, err := os.ReadFile(e.store.LocalLibraryPath())
	if err != nil || !strings.Contains(string(meta), `"lc"`) || !strings.Contains(string(meta), `"s": "home-nas"`) {
		t.Fatalf("metadata file: %v %s", err, meta)
	}
	if _, err := os.Stat(e.store.LibraryCachePath()); err == nil {
		t.Fatal("adding a custom icon must not download or write the catalogue cache")
	}

	// A search finds it next to catalogue results, by name, alias and tag.
	for _, q := range []string{"nas", "storage", "homelab", "github"} {
		_, body := e.get(admin, "/edit/library?q="+q)
		hasCustom := strings.Contains(body, `value="lc:home-nas"`) && strings.Contains(body, `src="/library/local/home-nas"`)
		if (q != "github") != hasCustom {
			t.Fatalf("search %q: custom icon shown=%v\n%s", q, hasCustom, body)
		}
	}
	_, body := e.get(admin, "/edit/library?q=git")
	if !strings.Contains(body, `value="github"`) {
		t.Fatalf("catalogue results missing: %s", body)
	}

	// An exact title match prefers the custom icon.
	_, body = e.get(admin, "/edit/library?q=&title=Home+NAS")
	if !strings.Contains(body, `value="lc:home-nas" checked`) {
		t.Fatalf("exact match not pre-selected: %s", body)
	}

	// Anyone signed in can see the image; nobody else can.
	if resp, b := e.get(admin, "/library/local/home-nas"); resp.StatusCode != 200 || !strings.HasPrefix(b, pngHeader) {
		t.Fatalf("image: %d", resp.StatusCode)
	}
	if resp, _ := e.get(e.client(), "/library/local/home-nas"); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("signed-out image: %d", resp.StatusCode)
	}

	// Picking it copies the image like any other icon.
	e.get(admin, "/edit")
	e.hxPost(admin, "/edit/sections", url.Values{"title": {"Lab"}})
	sid, _, _ := e.draftTile()
	form := url.Values{"title": {"NAS"}, "url": {"https://nas.example.com"}, "source": {"library"}, "library": {"lc:home-nas"}, "plate": {"none"}}
	e.hxPost(admin, "/edit/sections/"+sid+"/tiles", form)
	_, _, tiles := e.draftTile()
	for _, tile := range tiles {
		if tile.Library != "lc:home-nas" || e.iconBody(tile.Icon) != pngHeader+"A" {
			t.Fatalf("tile did not copy the custom icon: %+v", tile)
		}
	}

	// A refresh keeps custom icons.
	e.post(admin, "/admin/library/refresh", nil)
	_, body = e.get(admin, "/edit/library?q=nas")
	if !strings.Contains(body, `value="lc:home-nas"`) {
		t.Fatalf("custom icon lost after refresh: %s", body)
	}
	_, body = e.get(admin, "/admin/library")
	if strings.Contains(body, "Custom icons: ") || !strings.Contains(body, "Dashboard Icons: 5 icons") {
		t.Fatalf("catalogue counts changed: %s", body)
	}
}

func TestCustomIconEditAndDelete(t *testing.T) {
	e := newEnv(t)
	admin := e.signedInAdmin()
	e.localForm(admin, "/admin/library/local", "Home NAS", "", "", pngHeader+"A")

	// Rename and re-tag without a new image: the id and image stay.
	resp, _ := e.localForm(admin, "/admin/library/local/home-nas", "Main NAS", "box", "", "")
	wantRedirect(t, resp, "/admin/library?done=saved")
	_, body := e.get(admin, "/edit/library?q=box")
	if !strings.Contains(body, `value="lc:home-nas"`) || !strings.Contains(body, "home-nas") {
		t.Fatalf("edited alias not searchable: %s", body)
	}
	if _, b := e.get(admin, "/library/local/home-nas"); !strings.HasPrefix(b, pngHeader+"A") {
		t.Fatal("image changed without a new file")
	}

	// Replace the image with an SVG: the old file goes.
	svg := `<svg xmlns="http://www.w3.org/2000/svg"></svg>`
	e.localForm(admin, "/admin/library/local/home-nas", "Main NAS", "box", "", svg)
	if _, b := e.get(admin, "/library/local/home-nas"); b != svg {
		t.Fatalf("image not replaced: %q", b)
	}
	if files, _ := filepath.Glob(filepath.Join(e.store.LocalLibraryDir(), "home-nas.*")); len(files) != 1 {
		t.Fatalf("stale image files: %v", files)
	}

	// Delete.
	resp, _ = e.post(admin, "/admin/library/local/home-nas/delete", nil)
	wantRedirect(t, resp, "/admin/library?done=deleted")
	if resp, _ := e.get(admin, "/library/local/home-nas"); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("deleted image: %d", resp.StatusCode)
	}
	_, body = e.get(admin, "/edit/library?q=nas")
	if strings.Contains(body, "lc:home-nas") {
		t.Fatal("deleted icon still searchable")
	}
	if resp, _ := e.post(admin, "/admin/library/local/home-nas/delete", nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("second delete: %d", resp.StatusCode)
	}
}

func TestCustomIconValidation(t *testing.T) {
	e := newEnv(t)
	admin := e.signedInAdmin()
	e.localForm(admin, "/admin/library/local", "Home NAS", "", "", pngHeader)

	cases := []struct {
		name, path, title, image string
	}{
		{"duplicate", "/admin/library/local", "home nas", pngHeader},
		{"no image", "/admin/library/local", "Router", ""},
		{"not an image", "/admin/library/local", "Router", "plain text"},
		{"no letters", "/admin/library/local", "!!!", pngHeader},
		{"unknown icon", "/admin/library/local/nope", "Router", ""},
	}
	for _, c := range cases {
		resp, _ := e.localForm(admin, c.path, c.title, "", "", c.image)
		if resp.StatusCode < 400 {
			t.Errorf("%s: got %d", c.name, resp.StatusCode)
		}
	}

	// Only Admins can manage custom icons.
	e.post(admin, "/admin/users", url.Values{"username": {"bob"}, "password": {"bobpassword"}})
	bob := e.login("bob", "bobpassword")
	for _, path := range []string{"/admin/library/local", "/admin/library/local/home-nas", "/admin/library/local/home-nas/delete"} {
		if resp, _ := e.localForm(bob, path, "X", "", "", pngHeader); resp.StatusCode != http.StatusForbidden {
			t.Errorf("non-admin POST %s: %d", path, resp.StatusCode)
		}
	}
	// But they can search and use them.
	if _, body := e.get(bob, "/edit/library?q=nas"); !strings.Contains(body, `value="lc:home-nas"`) {
		t.Fatal("non-admin can't find custom icons")
	}
}
