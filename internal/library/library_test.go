package library

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

const metadata = `{
  "netflix": {"aliases": [], "categories": ["Streaming"]},
  "spotify": {"aliases": [], "categories": ["media"], "colors": {"light": "spotify-light", "dark": "spotify-dark"}},
  "soundcloud": {"aliases": [], "categories": ["Music"], "colors": {"light": "soundcloud", "dark": "soundcloud-dark"}},
  "github": {"aliases": ["GitHub", "git-hosting"], "categories": ["Developer-Tools"], "colors": {"dark": "github", "light": "github-light"}},
  "disney-plus": {"aliases": ["Disney+"], "categories": ["Streaming"]},
  "netdata": {"aliases": [], "categories": ["Monitoring"]},
  "azure-application-insights": {"aliases": ["monitoring"], "categories": []}
}`

// simpleData has the shape of Simple Icons' data/simple-icons.json.
const simpleData = `[
  {"title": "SoundCloud", "slug": "soundcloud", "hex": "FF5500"},
  {"title": "Roku", "slug": "roku", "hex": "662D91"},
  {"title": "Fubo", "slug": "fubo", "aliases": {"aka": ["FuboTV"]}},
  {"title": "Netflix", "slug": "netflix"},
  {"title": ".NET", "slug": "dotnet", "aliases": {"aka": ["Dotnet"]}}
]`

// selfhstData has the shape of selfh.st's index.json. The last two entries
// are not usable: a reference that isn't a slug, and an icon with no PNG.
const selfhstData = `[
  {"Name": "Plex", "Reference": "plex", "PNG": "Yes", "Light": "Yes", "Dark": "Yes", "Tags": "Streaming, Media"},
  {"Name": "Soundcloud", "Reference": "soundcloud", "PNG": "Yes", "Light": "Yes", "Dark": "No", "Tags": "Podcasts"},
  {"Name": "2FAuth", "Reference": "2fauth", "PNG": "Yes", "Light": "No", "Dark": "No", "Tags": ""},
  {"Name": "Broken", "Reference": "Bad Slug!", "PNG": "Yes"},
  {"Name": "NoPng", "Reference": "nopng", "PNG": "No"}
]`

// lobeFiles has the shape of jsDelivr's flat listing of LobeHub's PNG package.
// "claude" has a colour version, "lonely" only a white glyph, and "openai" only
// a wordmark layout, so it is not a brand.
const lobeFiles = `{"files": [
  {"name": "/dark/claude.png"}, {"name": "/dark/claude-color.png"}, {"name": "/dark/claude-text.png"}, {"name": "/light/claude.png"},
  {"name": "/light/claude-color.png"}, {"name": "/light/claude-text.png"},
  {"name": "/dark/ollama.png"}, {"name": "/light/ollama.png"}, {"name": "/dark/ollama-text.png"},
  {"name": "/dark/lonely.png"},
  {"name": "/dark/openai-brand.png"}, {"name": "/dark/openai-brand-color.png"},
  {"name": "/package.json"}, {"name": "/README.md"}
]}`

func newLib(t *testing.T) (*Library, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/metadata.json":
			hits.Add(1)
			w.Write([]byte(metadata))
		case "/si-data.json":
			w.Write([]byte(simpleData))
		case "/sh/index.json":
			w.Write([]byte(selfhstData))
		case "/lh-data":
			w.Write([]byte(`{"tags": {"latest": "9.9.9"}}`))
		case "/lh-data@9.9.9":
			if r.URL.Query().Get("structure") != "flat" {
				http.Error(w, "want structure=flat", http.StatusBadRequest)
				return
			}
			w.Write([]byte(lobeFiles))
		case "/lh-bad": // a package whose file list can't be loaded
			w.Write([]byte(`{"tags": {"latest": "0.0.0"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	l := New(filepath.Join(t.TempDir(), "library.json"), func(p string, b []byte) error { return os.WriteFile(p, b, 0o600) })
	l.BaseURL = srv.URL
	l.SimpleDataURL = srv.URL + "/si-data.json"
	l.SimpleBaseURL = srv.URL + "/si"
	l.SelfhstURL = srv.URL + "/sh"
	l.LobeDataURL = srv.URL + "/lh-data"
	l.LobeBaseURL = srv.URL + "/lh"
	return l, &hits
}

func counts(cat *Catalogue) []int {
	var out []int
	for _, c := range cat.Counts() {
		out = append(out, c.N)
	}
	return out
}

func TestCatalogueFetchedOnceAndCached(t *testing.T) {
	l, hits := newLib(t)
	if _, ok, _ := l.Cached(); ok {
		t.Fatal("cache should start empty")
	}
	cat, err := l.Catalogue(context.Background())
	// Dashboard Icons, Simple Icons, selfh.st, LobeHub.
	if want := []int{7, 5, 3, 3}; err != nil || !slices.Equal(counts(cat), want) {
		t.Fatalf("counts %v, want %v (err %v)", counts(cat), want, err)
	}
	if got := cat.Counts()[2].Source; got != "selfh.st" {
		t.Fatalf("source name %q", got)
	}
	l.Catalogue(context.Background())
	if hits.Load() != 1 {
		t.Fatalf("fetched %d times, want 1", hits.Load())
	}

	// A new Library on the same path reads the cache from disk.
	l2 := New(l.path, l.write)
	l2.BaseURL, l2.SimpleDataURL, l2.SelfhstURL, l2.LobeDataURL = "http://127.0.0.1:1", "http://127.0.0.1:1", "http://127.0.0.1:1", "http://127.0.0.1:1"
	if cat, err := l2.Catalogue(context.Background()); err != nil || !slices.Equal(counts(cat), []int{7, 5, 3, 3}) {
		t.Fatalf("cache not reused: %v", err)
	}

	if _, err := l.Refresh(context.Background()); err != nil || hits.Load() != 2 {
		t.Fatalf("refresh: %v, hits %d", err, hits.Load())
	}
}

func TestFilesListBaseFirstThenVariants(t *testing.T) {
	l, _ := newLib(t)
	cat, _ := l.Catalogue(context.Background())
	for name, want := range map[string][]string{
		// Neither variant is the base file, as in the real catalogue.
		"spotify": {"spotify", "spotify-light", "spotify-dark"},
		// One variant is the base file itself and is not repeated.
		"github":  {"github", "github-light"},
		"netflix": {"netflix"},
	} {
		icon, ok := cat.Find(name)
		if !ok {
			t.Fatalf("%s missing", name)
		}
		if got := icon.Files(); !slices.Equal(got, want) {
			t.Errorf("%s: Files() = %v, want %v", name, got, want)
		}
	}
}

func brand(t *testing.T, cat *Catalogue, prefix, slug string) Brand {
	t.Helper()
	for _, b := range cat.External[prefix] {
		if b.Slug == slug {
			return b
		}
	}
	t.Fatalf("%s:%s missing", prefix, slug)
	return Brand{}
}

func TestExternalBrandsAndTheirFiles(t *testing.T) {
	l, _ := newLib(t)
	cat, _ := l.Catalogue(context.Background())
	for _, c := range []struct {
		prefix, slug string
		want         []string
	}{
		// Simple Icons: the brand's colour, white and black.
		{"si", "soundcloud", []string{"si:soundcloud", "si:soundcloud:white", "si:soundcloud:black"}},
		// selfh.st: light and dark only where the index says they exist.
		{"sh", "plex", []string{"sh:plex", "sh:plex:light", "sh:plex:dark"}},
		{"sh", "soundcloud", []string{"sh:soundcloud", "sh:soundcloud:light"}},
		{"sh", "2fauth", []string{"sh:2fauth"}},
		// LobeHub: colour first when there is one, then the glyphs. The "-text"
		// wordmarks are not offered.
		{"lh", "claude", []string{"lh:claude:color", "lh:claude:dark", "lh:claude:light"}},
		{"lh", "ollama", []string{"lh:ollama:dark", "lh:ollama:light"}},
		{"lh", "lonely", []string{"lh:lonely:dark"}},
	} {
		if got := brand(t, cat, c.prefix, c.slug).ids(c.prefix); !slices.Equal(got, c.want) {
			t.Errorf("%s:%s: ids = %v, want %v", c.prefix, c.slug, got, c.want)
		}
	}
	for _, skipped := range []string{"bad-slug", "nopng"} {
		if slices.ContainsFunc(cat.External["sh"], func(b Brand) bool { return b.Slug == skipped }) {
			t.Errorf("selfh.st %q should be skipped", skipped)
		}
	}
	if slices.ContainsFunc(cat.External["lh"], func(b Brand) bool { return b.Slug == "openai" }) {
		t.Error("a LobeHub wordmark-only entry became a brand")
	}
	// The light folder has colour and wordmark files too. They are not brands.
	for _, b := range cat.External["lh"] {
		if strings.HasSuffix(b.Slug, "-color") || strings.HasSuffix(b.Slug, "-text") {
			t.Errorf("LobeHub variant file became a brand: %q", b.Slug)
		}
	}
	if got := brand(t, cat, "sh", "plex").Tags; !slices.Equal(got, []string{"Streaming", "Media"}) {
		t.Errorf("tags = %v", got)
	}
}

func TestHasFile(t *testing.T) {
	l, _ := newLib(t)
	cat, _ := l.Catalogue(context.Background())
	for file, want := range map[string]bool{
		"spotify": true, "spotify-light": true, "github-light": true,
		"si:soundcloud": true, "si:soundcloud:white": true, "si:roku:black": true,
		"sh:plex": true, "sh:plex:light": true, "sh:2fauth": true,
		"lh:claude:color": true, "lh:claude:light": true, "lh:lonely:dark": true,
		"spotify-wordmark-light": false, "": false, "nope": false,
		"si:nope": false, "si:soundcloud:purple": false, "si:": false, "si:../x": false, "si:roku:white:x": false,
		"sh:soundcloud:dark": false, "sh:nopng": false, "sh:bad-slug": false,
		"lh:claude": false, "lh:lonely:light": false, "lh:claude:text": false, "lh:openai:dark": false,
		"xx:roku": false,
	} {
		if got := cat.HasFile(file); got != want {
			t.Errorf("HasFile(%q) = %v, want %v", file, got, want)
		}
	}
}

func TestImageURLAndLabel(t *testing.T) {
	l := New("", nil)
	for id, want := range map[string]string{
		"spotify":             DefaultBaseURL + "/png/spotify.png",
		"spotify-light":       DefaultBaseURL + "/png/spotify-light.png",
		"si:soundcloud":       DefaultSimpleBaseURL + "/soundcloud",
		"si:soundcloud:white": DefaultSimpleBaseURL + "/soundcloud/white",
		"sh:plex":             DefaultSelfhstURL + "/png/plex.png",
		"sh:plex:light":       DefaultSelfhstURL + "/png/plex-light.png",
		"lh:claude:color":     DefaultLobeBaseURL + "@latest/dark/claude-color.png",
		"lh:claude:dark":      DefaultLobeBaseURL + "@latest/dark/claude.png",
		"lh:claude:light":     DefaultLobeBaseURL + "@latest/light/claude.png",
	} {
		if got := l.ImageURL(id); got != want {
			t.Errorf("ImageURL(%q) = %q, want %q", id, got, want)
		}
	}
	for id, want := range map[string]string{
		"spotify-light":       "spotify-light",
		"si:soundcloud":       "soundcloud (simple)",
		"si:soundcloud:black": "soundcloud black (simple)",
		"sh:plex:light":       "plex light (selfhst)",
		"lh:claude:color":     "claude color (lobehub)",
	} {
		if got := Label(id); got != want {
			t.Errorf("Label(%q) = %q, want %q", id, got, want)
		}
	}
}

func TestCacheInOldFormatIsIgnored(t *testing.T) {
	l, hits := newLib(t)
	old := `{"format":3,"fetched":"2026-01-01T00:00:00Z","icons":[{"n":"spotify","v":["spotify-light"]}],"simple":[{"t":"Roku","s":"roku"}]}`
	if err := os.WriteFile(l.path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := l.Cached(); ok || err != nil {
		t.Fatalf("old cache used: ok=%v err=%v", ok, err)
	}
	// It counts as no cache, so the next search downloads a fresh catalogue.
	cat, err := l.Catalogue(context.Background())
	if err != nil || hits.Load() != 1 {
		t.Fatalf("err=%v hits=%d", err, hits.Load())
	}
	if icon, _ := cat.Find("spotify"); len(icon.Variants) != 2 || len(cat.External["lh"]) != 3 {
		t.Fatalf("spotify: %+v, %d LobeHub brands", icon, len(cat.External["lh"]))
	}
}

// firsts returns the first file id of each search result.
func firsts(results []Result) []string {
	var out []string
	for _, r := range results {
		out = append(out, r.Files[0])
	}
	return out
}

func TestSearch(t *testing.T) {
	l, _ := newLib(t)
	cat, _ := l.Catalogue(context.Background())
	if got := firsts(cat.Search("net", 10)); !slices.Contains(got, "netflix") || !slices.Contains(got, "netdata") {
		t.Fatalf("prefix search: %v", got)
	}
	if got := firsts(cat.Search("Disney+", 10)); len(got) == 0 || got[0] != "disney-plus" {
		t.Fatalf("plus normalisation: %v", got)
	}
	if got := firsts(cat.Search("streaming", 10)); !slices.Contains(got, "netflix") || !slices.Contains(got, "disney-plus") {
		t.Fatalf("category search: %v", got)
	}
	if got := cat.Search("  ", 10); got != nil {
		t.Fatalf("empty query: %v", got)
	}
	if got := cat.Search("n", 1); len(got) != 1 {
		t.Fatalf("limit: %v", got)
	}
}

func TestSearchFindsEveryExternalSource(t *testing.T) {
	l, _ := newLib(t)
	cat, _ := l.Catalogue(context.Background())

	for _, c := range []struct {
		q    string
		want []string // first file of each result, in order
	}{
		{"roku", []string{"si:roku"}},                                            // Simple Icons only
		{"fubotv", []string{"si:fubo"}},                                          // an alternative name
		{"claude", []string{"lh:claude:color"}},                                  // LobeHub, colour first
		{"2fauth", []string{"sh:2fauth"}},                                        // selfh.st, slug starting with a digit
		{"podcasts", []string{"sh:soundcloud"}},                                  // selfh.st tag
		{"plex", []string{"sh:plex"}},                                            // selfh.st only
		{"soundcloud", []string{"soundcloud", "si:soundcloud", "sh:soundcloud"}}, // all, dashboard-icons first
	} {
		got := firsts(cat.Search(c.q, 10))
		if !slices.Equal(got, c.want) {
			t.Errorf("Search(%q) = %v, want %v", c.q, got, c.want)
		}
	}
	// A result lists every file of the brand.
	got := cat.Search("plex", 10)[0].Files
	if !slices.Equal(got, []string{"sh:plex", "sh:plex:light", "sh:plex:dark"}) {
		t.Fatalf("files: %v", got)
	}
}

func TestExact(t *testing.T) {
	l, _ := newLib(t)
	cat, _ := l.Catalogue(context.Background())
	for _, c := range []struct{ title, url, want string }{
		{"Netflix", "", "netflix"},
		{"Films", "https://www.netflix.com/browse", "netflix"},
		{"Disney+", "", "disney-plus"},
		{"Code", "https://github.com", "github"},
		// A brand in several sources uses the dashboard-icons base file.
		{"SoundCloud", "", "soundcloud"},
		// Otherwise the first external source with it, using its default file.
		{"Roku", "", "si:roku"},
		{"Channel", "https://www.roku.com/home", "si:roku"},
		{"FuboTV", "", "si:fubo"},
		{"Plex", "", "sh:plex"},
		{"2FAuth", "", "sh:2fauth"},
		{"Claude", "", "lh:claude:color"},
		{"Ollama", "", "lh:ollama:dark"},
		{"SolarEdge", "https://monitoring.solaredge.com/", ""},
		{"Netf", "", ""},
	} {
		got, ok := cat.Exact(c.title, c.url)
		if got != c.want || ok != (c.want != "") {
			t.Errorf("Exact(%q, %q) = %q, %v; want %q", c.title, c.url, got, ok, c.want)
		}
	}
}

// If any source can't be downloaded nothing is cached, so the next search tries
// them all again.
func TestAnySourceFailingCachesNothing(t *testing.T) {
	for name, break_ := range map[string]func(l *Library){
		"Dashboard Icons":   func(l *Library) { l.BaseURL = "http://127.0.0.1:1" },
		"Simple Icons":      func(l *Library) { l.SimpleDataURL = "http://127.0.0.1:1" },
		"selfh.st":          func(l *Library) { l.SelfhstURL = "http://127.0.0.1:1" },
		"LobeHub version":   func(l *Library) { l.LobeDataURL = "http://127.0.0.1:1" },
		"LobeHub file list": func(l *Library) { l.LobeDataURL = l.BaseURL + "/lh-bad" },
	} {
		t.Run(name, func(t *testing.T) {
			l, _ := newLib(t)
			baseURL, simpleURL, selfhstURL, lobeURL := l.BaseURL, l.SimpleDataURL, l.SelfhstURL, l.LobeDataURL
			break_(l)
			if _, err := l.Catalogue(context.Background()); err == nil {
				t.Fatal("expected an error")
			}
			if _, ok, _ := l.Cached(); ok {
				t.Fatal("a partial catalogue was cached")
			}
			if _, err := os.Stat(l.path); err == nil {
				t.Fatal("a partial catalogue was written to disk")
			}
			// Once the source is back, the next try works.
			l.BaseURL, l.SimpleDataURL, l.SelfhstURL, l.LobeDataURL = baseURL, simpleURL, selfhstURL, lobeURL
			if cat, err := l.Catalogue(context.Background()); err != nil || len(cat.External["lh"]) != 3 {
				t.Fatalf("retry: %v", err)
			}
		})
	}
}
