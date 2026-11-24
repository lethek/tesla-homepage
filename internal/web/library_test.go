package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/lethek/tesla-homepage/internal/library"
)

// draftTile returns the only Tile in the admin's Draft.
func (e *env) draftTile() (sectionID string, tileIDs []string, tiles map[string]struct{ Icon, Plate, Library string }) {
	e.t.Helper()
	u, _ := e.store.UserByUsername("admin")
	d, _, _ := e.store.Draft(u.ID)
	tiles = map[string]struct{ Icon, Plate, Library string }{}
	for _, t := range d.Sections[0].Tiles {
		tileIDs = append(tileIDs, t.ID)
		tiles[t.ID] = struct{ Icon, Plate, Library string }{t.Icon, t.Plate, t.Library}
	}
	return d.Sections[0].ID, tileIDs, tiles
}

func TestLibrarySearch(t *testing.T) {
	e := newEnv(t)
	c := e.signedInAdmin()

	_, body := e.get(c, "/edit/library?q=git")
	if !strings.Contains(body, `value="github"`) || !strings.Contains(body, "/png/github-light.png") {
		t.Fatalf("search for git: %s", body)
	}

	// An exact match for the title is pre-selected, even before searching.
	_, body = e.get(c, "/edit/library?q=&title=Netflix")
	if !strings.Contains(body, `value="netflix" checked`) {
		t.Fatalf("exact title match not pre-selected: %s", body)
	}
	// So is a match for the URL's domain.
	_, body = e.get(c, "/edit/library?q=&title=Films&url="+url.QueryEscape("https://www.youtube.com/feed"))
	if !strings.Contains(body, `value="youtube" checked`) {
		t.Fatalf("domain match not pre-selected: %s", body)
	}
	// An existing pick wins over the exact match and stays first.
	_, body = e.get(c, "/edit/library?q=net&title=Netflix&library=github")
	if !strings.Contains(body, `value="github" checked`) || strings.Index(body, "github") > strings.Index(body, "netflix") {
		t.Fatalf("current pick not kept: %s", body)
	}
}

// Spotify's base file is the green brand icon. Its catalogue entry lists only
// light and dark variants, so the base icon must still be offered and
// pre-selected, and picking it must save that file.
func TestBaseIconIsOfferedAndSaved(t *testing.T) {
	e := newEnv(t)
	c := e.signedInAdmin()

	_, body := e.get(c, "/edit/library?q=spotify")
	base, light, dark := strings.Index(body, "/png/spotify.png"), strings.Index(body, "/png/spotify-light.png"), strings.Index(body, "/png/spotify-dark.png")
	if base < 0 || light < 0 || dark < 0 || base > light || base > dark {
		t.Fatalf("want base icon first, then variants: base=%d light=%d dark=%d\n%s", base, light, dark, body)
	}

	_, body = e.get(c, "/edit/library?q=&title=Spotify")
	if !strings.Contains(body, `value="spotify" checked`) || strings.Contains(body, `value="spotify-light" checked`) {
		t.Fatalf("base icon not the pre-selected one: %s", body)
	}

	e.get(c, "/edit")
	e.hxPost(c, "/edit/sections", url.Values{"title": {"Music"}})
	sid, _, _ := e.draftTile()
	form := url.Values{"title": {"Spotify"}, "url": {"https://open.spotify.com"}, "source": {"library"}, "plate": {"none"}}
	form.Set("library", "spotify")
	e.hxPost(c, "/edit/sections/"+sid+"/tiles", form)
	_, ids, tiles := e.draftTile()
	if got := tiles[ids[0]]; got.Library != "spotify" || e.iconBody(got.Icon) != pngHeader+"spotify.png" {
		t.Fatalf("saved %+v, icon %q", got, e.iconBody(got.Icon))
	}

	// Only files the catalogue lists can be picked.
	form.Set("library", "spotify-wordmark-light")
	if resp, _ := e.hxPost(c, "/edit/sections/"+sid+"/tiles", form); resp.Header.Get("HX-Retarget") != "#dialog-body" {
		t.Fatal("file outside the catalogue accepted")
	}
}

// SoundCloud's brand colour is orange, but dashboard-icons has only black and
// white files for it. The orange icon comes from Simple Icons.
func TestSimpleIconsAreOfferedAndSaved(t *testing.T) {
	e := newEnv(t)
	c := e.signedInAdmin()

	_, body := e.get(c, "/edit/library?q=soundcloud")
	for _, want := range []string{
		`value="si:soundcloud"`, `value="si:soundcloud:white"`, `value="si:soundcloud:black"`,
		`soundcloud (simple)`, // label
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %s in results:\n%s", want, body)
		}
	}
	// The preview of the brand-colour file is the plain Simple Icons URL.
	if !strings.Contains(body, `/si/soundcloud"`) {
		t.Fatalf("no preview for the brand-colour file:\n%s", body)
	}

	// A brand in both sources is listed from each, dashboard-icons first, and
	// the dashboard-icons base file is the one pre-selected.
	_, body = e.get(c, "/edit/library?q=netflix&title=Netflix")
	if a, b := strings.Index(body, `value="netflix"`), strings.Index(body, `value="si:netflix"`); a < 0 || b < 0 || a > b {
		t.Fatalf("want both sources, dashboard-icons first: %d %d\n%s", a, b, body)
	}
	if !strings.Contains(body, `value="netflix" checked`) || strings.Contains(body, `value="si:netflix" checked`) {
		t.Fatalf("wrong pre-selection: %s", body)
	}

	// A brand only Simple Icons has is pre-selected on an exact match.
	_, body = e.get(c, "/edit/library?q=&title=Roku")
	if !strings.Contains(body, `value="si:roku" checked`) {
		t.Fatalf("exact match not pre-selected: %s", body)
	}

	e.get(c, "/edit")
	e.hxPost(c, "/edit/sections", url.Values{"title": {"Music"}})
	sid, _, _ := e.draftTile()
	form := url.Values{"title": {"SoundCloud"}, "url": {"https://soundcloud.com"}, "source": {"library"}, "plate": {"none"}}
	form.Set("library", "si:soundcloud")
	e.hxPost(c, "/edit/sections/"+sid+"/tiles", form)
	_, ids, tiles := e.draftTile()
	got := tiles[ids[0]]
	if got.Library != "si:soundcloud" || !strings.HasSuffix(got.Icon, ".svg") || !strings.Contains(e.iconBody(got.Icon), "/si/soundcloud<") {
		t.Fatalf("saved %+v, icon %q", got, e.iconBody(got.Icon))
	}

	// The white version is a different file.
	form.Set("library", "si:soundcloud:white")
	e.hxPost(c, "/edit/sections/"+sid+"/tiles", form)
	_, ids, tiles = e.draftTile()
	if got := tiles[ids[1]]; got.Library != "si:soundcloud:white" || !strings.Contains(e.iconBody(got.Icon), "/si/soundcloud/white<") {
		t.Fatalf("white: %+v, icon %q", got, e.iconBody(got.Icon))
	}

	// The edit dialog reopens with the pick selected and labelled.
	_, body = e.get(c, "/edit/tiles/"+ids[1])
	if !strings.Contains(body, `value="si:soundcloud:white" checked`) {
		t.Fatalf("edit dialog: %s", body)
	}

	// Only brands and colours the catalogue lists can be picked.
	for _, bad := range []string{"si:nope", "si:soundcloud:purple", "si:../x", "si:"} {
		form.Set("library", bad)
		if resp, _ := e.hxPost(c, "/edit/sections/"+sid+"/tiles", form); resp.Header.Get("HX-Retarget") != "#dialog-body" {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestSelfhstAndLobehubAreOfferedAndSaved(t *testing.T) {
	e := newEnv(t)
	c := e.signedInAdmin()

	_, body := e.get(c, "/edit/library?q=plex")
	for _, want := range []string{`value="sh:plex"`, `value="sh:plex:light"`, `value="sh:plex:dark"`, "plex light (selfhst)", "/sh/png/plex-light.png"} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %s:\n%s", want, body)
		}
	}
	// LobeHub: the brand-colour file comes first and is the default.
	_, body = e.get(c, "/edit/library?q=claude")
	if a, b, d := strings.Index(body, `value="lh:claude:color"`), strings.Index(body, `value="lh:claude:dark"`), strings.Index(body, `value="lh:claude:light"`); a < 0 || a > b || b > d {
		t.Fatalf("want colour, dark, light in order: %d %d %d\n%s", a, b, d, body)
	}
	if !strings.Contains(body, "claude color (lobehub)") || !strings.Contains(body, "/lh@latest/dark/claude-color.png") {
		t.Fatalf("label or preview: %s", body)
	}
	_, body = e.get(c, "/edit/library?q=&title=Claude")
	if !strings.Contains(body, `value="lh:claude:color" checked`) {
		t.Fatalf("exact match not pre-selected: %s", body)
	}

	e.get(c, "/edit")
	e.hxPost(c, "/edit/sections", url.Values{"title": {"Apps"}})
	sid, _, _ := e.draftTile()

	// selfh.st's licence needs a credit wherever its icons are offered.
	if _, body := e.get(c, "/edit/sections/"+sid+"/tiles/new"); !strings.Contains(body, "selfh.st") || !strings.Contains(body, "CC BY 4.0") {
		t.Fatalf("no credit in the tile dialog: %s", body)
	}

	for i, c2 := range []struct{ pick, file string }{
		{"sh:plex:light", "/sh/png/plex-light.png"},
		{"sh:plex", "/sh/png/plex.png"},
		{"lh:claude:color", "/lh@latest/dark/claude-color.png"},
		{"lh:claude:light", "/lh@latest/light/claude.png"},
	} {
		form := url.Values{"title": {"App"}, "url": {"https://example.com"}, "source": {"library"}, "plate": {"none"}, "library": {c2.pick}}
		e.hxPost(c, "/edit/sections/"+sid+"/tiles", form)
		_, ids, tiles := e.draftTile()
		got := tiles[ids[i]]
		if got.Library != c2.pick || e.iconBody(got.Icon) != pngHeader+c2.file {
			t.Errorf("%s: saved %+v, icon %q, want file %s", c2.pick, got, e.iconBody(got.Icon), c2.file)
		}
	}

	// Only files the catalogues list can be picked.
	for _, bad := range []string{"sh:plex:purple", "sh:nope", "lh:claude", "lh:claude:text", "lh:nope:dark", "xx:plex"} {
		form := url.Values{"title": {"App"}, "url": {"https://example.com"}, "source": {"library"}, "plate": {"none"}, "library": {bad}}
		if resp, _ := e.hxPost(c, "/edit/sections/"+sid+"/tiles", form); resp.Header.Get("HX-Retarget") != "#dialog-body" {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestLibraryPickAndPlate(t *testing.T) {
	e := newEnv(t)
	c := e.signedInAdmin()
	e.get(c, "/edit")
	e.hxPost(c, "/edit/sections", url.Values{"title": {"Code"}})
	sid, _, _ := e.draftTile()

	resp, _ := e.hxPost(c, "/edit/sections/"+sid+"/tiles", url.Values{
		"title": {"GitHub"}, "url": {"https://github.com"},
		"source": {"library"}, "library": {"github-light"}, "plate": {"dark"},
	})
	if resp.Header.Get("HX-Retarget") != "" {
		t.Fatal("library pick rejected")
	}
	_, ids, tiles := e.draftTile()
	got := tiles[ids[0]]
	if got.Library != "github-light" || got.Plate != plateDark || !strings.HasSuffix(e.iconBody(got.Icon), "github-light.png") {
		t.Fatalf("variant pick: %+v, icon %q", got, e.iconBody(got.Icon))
	}

	// The edit dialog reopens on the Library tab with the variant selected.
	_, body := e.get(c, "/edit/tiles/"+ids[0])
	if !strings.Contains(body, `data-source="library"`) || !strings.Contains(body, `value="github-light" checked`) || !strings.Contains(body, `id="plate-dark" value="dark" checked`) {
		t.Fatalf("edit dialog: %s", body)
	}

	// The Plate never changes which file is saved: the user's pick is used as is.
	e.hxPost(c, "/edit/tiles/"+ids[0], url.Values{
		"title": {"GitHub"}, "url": {"https://github.com"},
		"source": {"library"}, "library": {"github"}, "plate": {"white"},
	})
	_, _, tiles = e.draftTile()
	got = tiles[ids[0]]
	if got.Library != "github" || got.Plate != plateWhite || !strings.HasSuffix(e.iconBody(got.Icon), "github.png") {
		t.Fatalf("base pick on white plate: %+v, icon %q", got, e.iconBody(got.Icon))
	}

	// A custom Plate is rendered on the published Homepage.
	e.hxPost(c, "/edit/tiles/"+ids[0], url.Values{
		"title": {"GitHub"}, "url": {"https://github.com"},
		"source": {"keep"}, "plate": {"custom"}, "plate_custom": {"#E50914"},
	})
	_, _, tiles = e.draftTile()
	if tiles[ids[0]].Plate != "#e50914" || tiles[ids[0]].Library != "github" {
		t.Fatalf("custom plate with kept icon: %+v", tiles[ids[0]])
	}
	e.post(c, "/edit/done", nil)
	if _, body := e.get(c, "/"); !strings.Contains(body, `class="icon plated" style="--plate: #e50914;"`) {
		t.Fatalf("plate not rendered: %s", body)
	}
}

func TestPlateDefaultsToNone(t *testing.T) {
	e := newEnv(t)
	c := e.signedInAdmin()
	e.get(c, "/edit")
	e.hxPost(c, "/edit/sections", url.Values{"title": {"S"}})
	sid, _, _ := e.draftTile()

	_, body := e.get(c, "/edit/sections/"+sid+"/tiles/new")
	if !strings.Contains(body, `id="plate-none" value="none" checked`) {
		t.Fatalf("new tile dialog doesn't default to no plate: %s", body)
	}

	// A library icon with no Plate has no Plate colour.
	e.hxPost(c, "/edit/sections/"+sid+"/tiles", url.Values{
		"title": {"Netflix"}, "url": {"https://www.netflix.com"},
		"source": {"library"}, "library": {"netflix"}, "plate": {"none"},
	})
	_, ids, tiles := e.draftTile()
	if got := tiles[ids[0]]; got.Plate != "" || got.Library != "netflix" || got.Icon == "" {
		t.Fatalf("tile: %+v", got)
	}
	e.post(c, "/edit/done", nil)
	_, body = e.get(c, "/")
	if !strings.Contains(body, `<span class="icon"><img`) || strings.Contains(body, "--plate") {
		t.Fatalf("unplated library icon rendered wrongly: %s", body)
	}
}

func TestPaddingAppliesToAnyIconSource(t *testing.T) {
	e := newEnv(t)
	c := e.signedInAdmin()
	e.get(c, "/edit")
	e.hxPost(c, "/edit/sections", url.Values{"title": {"S"}})
	sid, _, _ := e.draftTile()

	// Padding and Plate are saved the same way for a library pick, a pasted
	// image URL and the site's own icon.
	for i, form := range []url.Values{
		{"title": {"A"}, "source": {"library"}, "library": {"netflix"}},
		{"title": {"B"}, "source": {"url"}, "icon_url": {"https://example.com/png/b.png"}},
		{"title": {"C"}, "source": {"site"}},
	} {
		form.Set("url", "https://example.com")
		form.Set("plate", "white")
		form.Set("padding", "12")
		e.hxPost(c, "/edit/sections/"+sid+"/tiles", form)
		u, _ := e.store.UserByUsername("admin")
		d, _, _ := e.store.Draft(u.ID)
		if n := len(d.Sections[0].Tiles); n != i+1 {
			t.Fatalf("tile %d not saved (%d tiles)", i, n)
		}
		if got := d.Sections[0].Tiles[i]; got.Padding != 12 || got.Plate != plateWhite {
			t.Fatalf("tile %d: %+v", i, got)
		}
	}

	e.post(c, "/edit/done", nil)
	// The site has no icon in this test, so tile C shows the letter
	// placeholder, which has nothing to pad.
	if _, body := e.get(c, "/"); strings.Count(body, "--pad: 12;") != 2 {
		t.Fatalf("padding not rendered on both image tiles: %s", body)
	}

	u, _ := e.store.UserByUsername("admin")
	hp, _, _ := e.store.Homepage(u.ID)
	id := hp.Sections[0].Tiles[0].ID
	e.get(c, "/edit")
	if _, body := e.get(c, "/edit/tiles/"+id); !strings.Contains(body, `name="padding" min="0" max="30" step="1" value="12"`) {
		t.Fatalf("edit dialog lost the padding: %s", body)
	}

	for _, bad := range []string{"31", "-1", "abc"} {
		resp, body := e.hxPost(c, "/edit/tiles/"+id, url.Values{
			"title": {"A"}, "url": {"https://example.com"}, "source": {"keep"}, "padding": {bad},
		})
		if !strings.Contains(body, "Padding must be between 0 and 30.") {
			t.Fatalf("padding %q accepted (%d): %s", bad, resp.StatusCode, body)
		}
	}
}

func TestTileFormValidation(t *testing.T) {
	e := newEnv(t)
	c := e.signedInAdmin()
	e.get(c, "/edit")
	e.hxPost(c, "/edit/sections", url.Values{"title": {"S"}})
	sid, _, _ := e.draftTile()
	base := url.Values{"title": {"X"}, "url": {"https://example.com"}}

	for name, extra := range map[string]url.Values{
		"bad plate":      {"source": {"site"}, "plate": {"custom"}, "plate_custom": {"red; background: url(x)"}},
		"upload no file": {"source": {"upload"}},
		"url tab empty":  {"source": {"url"}},
		"unknown icon":   {"source": {"library"}, "library": {"nope"}},
	} {
		form := url.Values{}
		for k, v := range base {
			form[k] = v
		}
		for k, v := range extra {
			form[k] = v
		}
		resp, _ := e.hxPost(c, "/edit/sections/"+sid+"/tiles", form)
		if resp.Header.Get("HX-Retarget") != "#dialog-body" {
			t.Errorf("%s: accepted", name)
		}
	}

	// Library tab with nothing picked saves the placeholder and never contacts
	// the site. Fetching the site's icon is the From site tab's job.
	before := siteIconRequests.Load()
	form := url.Values{"title": {"X"}, "url": {"https://example.com"}, "source": {"library"}, "plate": {"none"}}
	resp, body := e.hxPost(c, "/edit/sections/"+sid+"/tiles", form)
	if resp.Header.Get("HX-Retarget") != "" {
		t.Fatal("library tab with no pick rejected")
	}
	if !strings.Contains(body, `placeholder" aria-hidden="true">X<`) {
		t.Fatalf("no placeholder: %s", body)
	}
	if n := siteIconRequests.Load() - before; n != 0 {
		t.Fatalf("library tab with no pick made %d requests to the site", n)
	}
	// The From site tab does fetch it.
	form.Set("source", "site")
	e.hxPost(c, "/edit/sections/"+sid+"/tiles", form)
	if siteIconRequests.Load() == before {
		t.Fatal("From site tab did not request the site's icon")
	}
}

func TestStarterUsesLibrary(t *testing.T) {
	e := newEnv(t)
	e.server.library.Seed = library.SeedFS()
	c := e.client()
	e.post(c, "/setup", url.Values{"username": {"admin"}, "password": {"password1"}, "confirm": {"password1"}})
	resp, _ := e.post(c, "/welcome", url.Values{"start": {"starter"}})
	wantRedirect(t, resp, "/")

	u, _ := e.store.UserByUsername("admin")
	hp, _, _ := e.store.Homepage(u.ID)
	found, extras := map[string]bool{}, map[string]bool{}
	seeded := 0
	for _, s := range hp.Sections {
		for _, tile := range s.Tiles {
			switch tile.Title {
			case "ABC iView", "SBS On Demand", "10", "7plus", "9Now":
				// The built-in custom icons, not a download from the site.
				if !strings.HasPrefix(tile.Library, "lc:") || tile.Icon == "" || !tile.Fullscreen {
					t.Errorf("%s: %+v", tile.Title, tile)
				}
				seeded++
			case "Plex":
				if tile.Library == "" || tile.Plate != "" || tile.Icon == "" {
					t.Errorf("%s: %+v", tile.Title, tile)
				}
				found[tile.Title] = true
			case "Spotify":
				// An exact match uses the base file, not a variant.
				if tile.Library != "spotify" {
					t.Errorf("Spotify: %+v", tile)
				}
			case "Prime Video":
				// The starter template names the file to use.
				if tile.Library != "prime-video-light" || tile.Icon == "" {
					t.Errorf("Prime Video: %+v", tile)
				}
			case "SoundCloud":
				// The template names the orange Simple Icons file.
				if tile.Library != "si:soundcloud" || !strings.HasSuffix(tile.Icon, ".svg") {
					t.Errorf("SoundCloud: %+v", tile)
				}
			case "Crunchyroll", "Claude":
				extras[tile.Title] = true
			case "Reddit":
				// Not in the fake library and the fake site has no icon.
				if tile.Library != "" || tile.Icon != "" {
					t.Errorf("Reddit: %+v", tile)
				}
			}
		}
	}
	if seeded != 5 {
		t.Fatalf("%d starter tiles use built-in icons, want 5", seeded)
	}
	if len(found) != 1 {
		t.Fatalf("starter tiles: %+v", found)
	}
	if len(extras) != 2 {
		t.Fatalf("starter set is missing Crunchyroll or Claude: %+v", extras)
	}
}

func TestLibraryAdminPage(t *testing.T) {
	e := newEnv(t)
	admin := e.signedInAdmin()

	if _, body := e.get(admin, "/admin/library"); !strings.Contains(body, "Not downloaded yet") {
		t.Fatal("expected empty cache")
	}
	resp, _ := e.post(admin, "/admin/library/refresh", nil)
	wantRedirect(t, resp, "/admin/library?done=refreshed")
	_, body := e.get(admin, "/admin/library")
	for _, want := range []string{
		"Dashboard Icons: 5 icons", "Simple Icons: 3 icons", "selfh.st: 1 icon</li>", "LobeHub: 1 icon</li>",
		// selfh.st's licence requires a credit.
		"CC BY 4.0",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q after refresh: %s", want, body)
		}
	}

	e.post(admin, "/admin/users", url.Values{"username": {"bob"}, "password": {"bobpassword"}})
	bob := e.login("bob", "bobpassword")
	if resp, _ := e.get(bob, "/admin/library"); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("non-admin: %d", resp.StatusCode)
	}
	if resp, _ := e.post(bob, "/admin/library/refresh", nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("non-admin refresh: %d", resp.StatusCode)
	}
}

func TestFullscreenTileLinksThroughYouTubeRedirect(t *testing.T) {
	e := newEnv(t)
	c := e.signedInAdmin()
	e.get(c, "/edit")
	e.hxPost(c, "/edit/sections", url.Values{"title": {"S"}})
	sid, _, _ := e.draftTile()

	for _, form := range []url.Values{
		{"title": {"Plain"}, "url": {"https://example.com/a?x=1&y=2"}, "source": {"library"}},
		{"title": {"Full"}, "url": {"https://example.com/a?x=1&y=2"}, "source": {"library"}, "fullscreen": {"1"}},
	} {
		e.hxPost(c, "/edit/sections/"+sid+"/tiles", form)
	}
	e.post(c, "/edit/done", nil)

	_, body := e.get(c, "/")
	if !strings.Contains(body, `href="https://example.com/a?x=1&amp;y=2"`) {
		t.Fatalf("plain tile link changed: %s", body)
	}
	want := `href="https://www.youtube.com/redirect?q=https%3A%2F%2Fexample.com%2Fa%3Fx%3D1%26y%3D2"`
	if !strings.Contains(body, want) {
		t.Fatalf("fullscreen tile not redirected: %s", body)
	}

	// The checkbox is remembered, and can be cleared.
	u, _ := e.store.UserByUsername("admin")
	hp, _, _ := e.store.Homepage(u.ID)
	id := hp.Sections[0].Tiles[1].ID
	e.get(c, "/edit")
	if _, body := e.get(c, "/edit/tiles/"+id); !strings.Contains(body, `name="fullscreen" value="1" checked`) {
		t.Fatalf("edit dialog lost the option: %s", body)
	}
	e.hxPost(c, "/edit/tiles/"+id, url.Values{"title": {"Full"}, "url": {"https://example.com"}, "source": {"keep"}})
	d, _, _ := e.store.Draft(u.ID)
	if d.Sections[0].Tiles[1].Fullscreen {
		t.Fatal("option not cleared")
	}
}
