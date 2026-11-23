// Package library searches the Icon library: ready-made icons from
// homarr-labs/dashboard-icons and from three external catalogues that
// dashboardicons.com also indexes: Simple Icons, selfh.st and LobeHub. The
// catalogues are downloaded once and cached; they are only refreshed when the
// Admin asks.
//
// Every pickable file has an id. A dashboard-icons file's id is its file name
// without the extension, such as "spotify" or "spotify-light". An external
// file's id is a source prefix, the brand's slug and optionally a variant,
// separated by colons, such as "si:soundcloud:white" or "lh:claude:color".
// The prefixes are "si" (Simple Icons), "sh" (selfh.st) and "lh" (LobeHub).
package library

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultBaseURL serves dashboard-icons' metadata.json and png/<name>.png.
	DefaultBaseURL = "https://cdn.jsdelivr.net/gh/homarr-labs/dashboard-icons"
	// DefaultSimpleDataURL serves Simple Icons' list of brands.
	DefaultSimpleDataURL = "https://cdn.jsdelivr.net/npm/simple-icons@latest/data/simple-icons.json"
	// DefaultSimpleBaseURL serves <slug>, <slug>/white and <slug>/black as SVG.
	DefaultSimpleBaseURL = "https://cdn.simpleicons.org"
	// DefaultSelfhstURL serves selfh.st's index.json and png/<reference>.png.
	DefaultSelfhstURL = "https://cdn.jsdelivr.net/gh/selfhst/icons"
	// DefaultLobeDataURL describes LobeHub's static PNG package: GET it for the
	// latest version, then GET it with "@<version>?structure=flat" for the files.
	DefaultLobeDataURL = "https://data.jsdelivr.com/v1/packages/npm/@lobehub/icons-static-png"
	// DefaultLobeBaseURL serves @latest/dark/<name>.png and the like.
	DefaultLobeBaseURL = "https://cdn.jsdelivr.net/npm/@lobehub/icons-static-png"
)

const maxMetadataSize = 20 << 20

// source describes an external catalogue.
type source struct {
	prefix string // in ids
	tag    string // in labels
	title  string // shown to users
}

// externals are listed in the order they win ties in a search.
var externals = []source{
	{"si", "simple", "Simple Icons"},
	{"sh", "selfhst", "selfh.st"},
	{"lh", "lobehub", "LobeHub"},
}

type Icon struct {
	Name       string   `json:"n"`
	Aliases    []string `json:"a,omitempty"`
	Categories []string `json:"c,omitempty"`
	// Variants name the icon's other files, usually single-colour light and
	// dark versions. The base icon (Name) is not included. The metadata
	// doesn't say reliably which variant suits which background, so they are
	// offered to the user as they are.
	Variants []string `json:"v,omitempty"`
}

// Files returns the ids of the icon's base file followed by its variants.
func (i Icon) Files() []string {
	return append([]string{i.Name}, i.Variants...)
}

// Brand is an entry in an external catalogue.
type Brand struct {
	Name    string   `json:"t"`
	Slug    string   `json:"s"`
	Aliases []string `json:"a,omitempty"`
	Tags    []string `json:"g,omitempty"`
	// Variants lists the brand's files. "" is the default file, which has no
	// suffix in its id. The first variant is the one to use when nothing else
	// is known.
	Variants []string `json:"v"`
}

// ids returns the ids of the brand's files.
func (b Brand) ids(prefix string) []string {
	ids := make([]string, 0, len(b.Variants))
	for _, v := range b.Variants {
		ids = append(ids, makeID(prefix, b.Slug, v))
	}
	return ids
}

func makeID(prefix, slug, variant string) string {
	id := prefix + ":" + slug
	if variant != "" {
		id += ":" + variant
	}
	return id
}

// catalogueFormat changes when the cached catalogue's shape does. A cache in
// another format is treated as missing.
const catalogueFormat = 4

type Catalogue struct {
	Format  int       `json:"format"`
	Fetched time.Time `json:"fetched"`
	Icons   []Icon    `json:"icons"`
	// External holds each external catalogue's brands, by source prefix.
	External map[string][]Brand `json:"external"`
}

// Count is the number of icons from one source.
type Count struct {
	Source string
	N      int
}

// Counts returns how many icons each source contributed.
func (c *Catalogue) Counts() []Count {
	counts := []Count{{"Dashboard Icons", len(c.Icons)}}
	for _, src := range externals {
		counts = append(counts, Count{src.title, len(c.External[src.prefix])})
	}
	return counts
}

// WriteFile writes data to path atomically.
type WriteFile func(path string, data []byte) error

type Library struct {
	BaseURL       string
	SimpleDataURL string
	SimpleBaseURL string
	SelfhstURL    string
	LobeDataURL   string
	LobeBaseURL   string
	Client        *http.Client
	path          string
	write         WriteFile

	// LocalPath and LocalDir hold the Admin's custom icons: their metadata,
	// in the same shape as the cached catalogue, and their image files.
	// Custom icons are off while they are empty.
	LocalPath string
	LocalDir  string

	mu         sync.Mutex
	cat        *Catalogue
	local      []Brand
	localReady bool
}

func New(cachePath string, write WriteFile) *Library {
	return &Library{
		BaseURL:       DefaultBaseURL,
		SimpleDataURL: DefaultSimpleDataURL,
		SimpleBaseURL: DefaultSimpleBaseURL,
		SelfhstURL:    DefaultSelfhstURL,
		LobeDataURL:   DefaultLobeDataURL,
		LobeBaseURL:   DefaultLobeBaseURL,
		Client:        &http.Client{Timeout: 30 * time.Second},
		path:          cachePath,
		write:         write,
	}
}

var (
	slugPattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	variantPattern = regexp.MustCompile(`^[a-z]+$`)
)

// parseID splits an external file id into its source prefix, slug and variant
// ("" for the default file). ok is false for any other id.
func parseID(id string) (prefix, slug, variant string, ok bool) {
	parts := strings.Split(id, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return "", "", "", false
	}
	if !slices.ContainsFunc(allSources(), func(s source) bool { return s.prefix == parts[0] }) || !slugPattern.MatchString(parts[1]) {
		return "", "", "", false
	}
	if len(parts) == 3 {
		if !variantPattern.MatchString(parts[2]) {
			return "", "", "", false
		}
		variant = parts[2]
	}
	return parts[0], parts[1], variant, true
}

// ImageURL returns the URL of the image for a file id. dashboard-icons,
// selfh.st and LobeHub files are PNGs and Simple Icons files are SVGs.
func (l *Library) ImageURL(id string) string {
	prefix, slug, variant, ok := parseID(id)
	if !ok {
		return l.BaseURL + "/png/" + url.PathEscape(id) + ".png"
	}
	switch prefix {
	case LocalPrefix:
		return LocalURL(slug)
	case "si":
		u := l.SimpleBaseURL + "/" + slug
		if variant != "" {
			u += "/" + variant
		}
		return u
	case "sh":
		if variant != "" {
			slug += "-" + variant
		}
		return l.SelfhstURL + "/png/" + slug + ".png"
	default: // "lh"
		switch variant {
		case "color":
			return l.LobeBaseURL + "@latest/dark/" + slug + "-color.png"
		case "light":
			return l.LobeBaseURL + "@latest/light/" + slug + ".png"
		default:
			return l.LobeBaseURL + "@latest/dark/" + slug + ".png"
		}
	}
}

// Label returns the text shown under a file's preview.
func Label(id string) string {
	prefix, slug, variant, ok := parseID(id)
	if !ok {
		return id
	}
	label := slug
	if variant != "" {
		label += " " + variant
	}
	for _, src := range allSources() {
		if src.prefix == prefix {
			return label + " (" + src.tag + ")"
		}
	}
	return label
}

// Cached returns the cached catalogue, with the custom icons merged in,
// without downloading. ok is false if there is none yet.
func (l *Library) Cached() (cat *Catalogue, ok bool, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.load(); err != nil {
		return nil, false, err
	}
	if l.cat == nil {
		return nil, false, nil
	}
	merged, err := l.merged(l.cat)
	return merged, err == nil, err
}

// Catalogue returns the cached catalogue, downloading it if there is none. The
// custom icons are merged in.
func (l *Library) Catalogue(ctx context.Context) (*Catalogue, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.load(); err != nil {
		return nil, err
	}
	cat := l.cat
	if cat == nil {
		var err error
		if cat, err = l.fetch(ctx); err != nil {
			return nil, err
		}
	}
	return l.merged(cat)
}

// Refresh downloads the catalogue again and replaces the cache.
func (l *Library) Refresh(ctx context.Context) (*Catalogue, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	cat, err := l.fetch(ctx)
	if err != nil {
		return nil, err
	}
	return l.merged(cat)
}

// load reads the cache file into memory once. Callers must hold l.mu.
func (l *Library) load() error {
	if l.cat != nil {
		return nil
	}
	b, err := os.ReadFile(l.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var cat Catalogue
	if err := json.Unmarshal(b, &cat); err != nil {
		return fmt.Errorf("reading icon library cache: %w", err)
	}
	if cat.Format != catalogueFormat {
		return nil
	}
	l.cat = &cat
	return nil
}

// job is one download.
type job struct {
	what, url string
	body      []byte
	err       error
}

// downloadAll runs the jobs at the same time and returns the first error.
func (l *Library) downloadAll(ctx context.Context, jobs ...*job) error {
	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			j.body, j.err = l.download(ctx, j.what, j.url)
		}()
	}
	wg.Wait()
	for _, j := range jobs {
		if j.err != nil {
			return j.err
		}
	}
	return nil
}

func (l *Library) download(ctx context.Context, what, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := l.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", what, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("downloading %s: %s", what, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxMetadataSize))
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", what, err)
	}
	return body, nil
}

// fetch downloads every catalogue, trims them and caches the result. If any
// download fails nothing is cached. Callers must hold l.mu.
func (l *Library) fetch(ctx context.Context) (*Catalogue, error) {
	native := &job{what: "Dashboard Icons", url: l.BaseURL + "/metadata.json"}
	simple := &job{what: "Simple Icons", url: l.SimpleDataURL}
	selfhst := &job{what: "selfh.st", url: l.SelfhstURL + "/index.json"}
	lobeInfo := &job{what: "LobeHub", url: l.LobeDataURL}
	if err := l.downloadAll(ctx, native, simple, selfhst, lobeInfo); err != nil {
		return nil, err
	}
	var info struct {
		Tags struct {
			Latest string `json:"latest"`
		} `json:"tags"`
	}
	if err := json.Unmarshal(lobeInfo.body, &info); err != nil || info.Tags.Latest == "" {
		return nil, fmt.Errorf("reading LobeHub: no latest version")
	}
	lobe := &job{what: "LobeHub", url: l.LobeDataURL + "@" + info.Tags.Latest + "?structure=flat"}
	if err := l.downloadAll(ctx, lobe); err != nil {
		return nil, err
	}

	cat := &Catalogue{Format: catalogueFormat, Fetched: time.Now().UTC(), External: map[string][]Brand{}}
	var err error
	if cat.Icons, err = parseNative(native.body); err != nil {
		return nil, fmt.Errorf("reading Dashboard Icons: %w", err)
	}
	if cat.External["si"], err = parseSimple(simple.body); err != nil {
		return nil, fmt.Errorf("reading Simple Icons: %w", err)
	}
	if cat.External["sh"], err = parseSelfhst(selfhst.body); err != nil {
		return nil, fmt.Errorf("reading selfh.st: %w", err)
	}
	if cat.External["lh"], err = parseLobe(lobe.body); err != nil {
		return nil, fmt.Errorf("reading LobeHub: %w", err)
	}

	b, err := json.Marshal(cat)
	if err != nil {
		return nil, err
	}
	if err := l.write(l.path, b); err != nil {
		return nil, err
	}
	l.cat = cat
	return cat, nil
}

func parseNative(body []byte) ([]Icon, error) {
	var meta map[string]struct {
		Aliases    []string `json:"aliases"`
		Categories []string `json:"categories"`
		Colors     struct {
			Dark  string `json:"dark"`
			Light string `json:"light"`
		} `json:"colors"`
	}
	if err := json.Unmarshal(body, &meta); err != nil {
		return nil, err
	}
	icons := make([]Icon, 0, len(meta))
	for name, m := range meta {
		icon := Icon{Name: name, Aliases: m.Aliases, Categories: m.Categories}
		for _, v := range []string{m.Colors.Light, m.Colors.Dark} {
			if v != "" && v != name && !slices.Contains(icon.Variants, v) {
				icon.Variants = append(icon.Variants, v)
			}
		}
		icons = append(icons, icon)
	}
	sort.Slice(icons, func(i, j int) bool { return icons[i].Name < icons[j].Name })
	return icons, nil
}

// parseSimple reads Simple Icons' data/simple-icons.json. Each brand is
// offered in its own colour (the default file), white and black.
func parseSimple(body []byte) ([]Brand, error) {
	var list []struct {
		Title   string `json:"title"`
		Slug    string `json:"slug"`
		Aliases struct {
			Aka []string `json:"aka"`
		} `json:"aliases"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, err
	}
	brands := make([]Brand, 0, len(list))
	for _, e := range list {
		if slugPattern.MatchString(e.Slug) {
			brands = append(brands, Brand{Name: e.Title, Slug: e.Slug, Aliases: e.Aliases.Aka, Variants: []string{"", "white", "black"}})
		}
	}
	return brands, nil
}

// parseSelfhst reads selfh.st's index.json. Light and dark versions are
// offered where the index says they exist.
func parseSelfhst(body []byte) ([]Brand, error) {
	var list []struct {
		Name      string `json:"Name"`
		Reference string `json:"Reference"`
		PNG       string `json:"PNG"`
		Light     string `json:"Light"`
		Dark      string `json:"Dark"`
		Tags      string `json:"Tags"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, err
	}
	brands := make([]Brand, 0, len(list))
	for _, e := range list {
		if !slugPattern.MatchString(e.Reference) || e.PNG == "No" {
			continue
		}
		b := Brand{Name: e.Name, Slug: e.Reference, Variants: []string{""}}
		if e.Light == "Yes" {
			b.Variants = append(b.Variants, "light")
		}
		if e.Dark == "Yes" {
			b.Variants = append(b.Variants, "dark")
		}
		for _, t := range strings.Split(e.Tags, ",") {
			if t = strings.TrimSpace(t); t != "" {
				b.Tags = append(b.Tags, t)
			}
		}
		brands = append(brands, b)
	}
	return brands, nil
}

// lobeExtra matches LobeHub files that aren't a brand's plain icon: wordmarks
// and other layouts, which don't suit a square tile.
var lobeExtra = regexp.MustCompile(`-(text|text-cn|brand|brand-color|combine|avatar)$`)

// parseLobe reads the file list of LobeHub's static PNG package, which holds
// dark/<name>.png (a white glyph for dark backgrounds), light/<name>.png (a
// black glyph) and dark/<name>-color.png (the brand's colours). The brand's
// colour version is its default when it has one.
func parseLobe(body []byte) ([]Brand, error) {
	var listing struct {
		Files []struct {
			Name string `json:"name"`
		} `json:"files"`
	}
	if err := json.Unmarshal(body, &listing); err != nil {
		return nil, err
	}
	dark, light, colour := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, f := range listing.Files {
		folder, file, ok := strings.Cut(strings.TrimPrefix(f.Name, "/"), "/")
		name, isPNG := strings.CutSuffix(file, ".png")
		if !ok || !isPNG {
			continue
		}
		switch {
		case lobeExtra.MatchString(name):
			// Not a plain icon.
		case strings.HasSuffix(name, "-color"):
			// Both folders hold the colour version.
			colour[strings.TrimSuffix(name, "-color")] = true
		case folder == "dark":
			dark[name] = true
		case folder == "light":
			light[name] = true
		}
	}
	names := map[string]bool{}
	for n := range dark {
		names[n] = true
	}
	for n := range light {
		names[n] = true
	}
	brands := make([]Brand, 0, len(names))
	for name := range names {
		if !slugPattern.MatchString(name) {
			continue
		}
		b := Brand{Name: name, Slug: name}
		if colour[name] {
			b.Variants = append(b.Variants, "color")
		}
		if dark[name] {
			b.Variants = append(b.Variants, "dark")
		}
		if light[name] {
			b.Variants = append(b.Variants, "light")
		}
		brands = append(brands, b)
	}
	sort.Slice(brands, func(i, j int) bool { return brands[i].Slug < brands[j].Slug })
	return brands, nil
}

// Find returns the dashboard-icons icon with the given name.
func (c *Catalogue) Find(name string) (Icon, bool) {
	i := sort.Search(len(c.Icons), func(i int) bool { return c.Icons[i].Name >= name })
	if i < len(c.Icons) && c.Icons[i].Name == name {
		return c.Icons[i], true
	}
	return Icon{}, false
}

// HasFile reports whether id names a file in the catalogue: a dashboard-icons
// base file or variant, or an external brand's file.
func (c *Catalogue) HasFile(id string) bool {
	if prefix, slug, variant, ok := parseID(id); ok {
		for _, b := range c.External[prefix] {
			if b.Slug == slug {
				return slices.Contains(b.Variants, variant)
			}
		}
		return false
	}
	if _, ok := c.Find(id); ok {
		return true
	}
	for _, icon := range c.Icons {
		if slices.Contains(icon.Variants, id) {
			return true
		}
	}
	return false
}

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

func norm(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "+", " plus")
	s = strings.ReplaceAll(s, "&", " and ")
	return nonAlnum.ReplaceAllString(s, "")
}

// score rates how well the normalised query nq matches an icon's own name or
// one of its aliases. Zero means no match.
func score(nq, name string, aliases []string) int {
	best := 0
	for i, term := range append([]string{name}, aliases...) {
		t := norm(term)
		s := 0
		switch {
		case t == nq:
			s = 100
		case strings.HasPrefix(t, nq):
			s = 80
		case strings.Contains(t, nq):
			s = 60
		}
		if s > 0 && i > 0 {
			s -= 5 // prefer the icon's own name over an alias
		}
		best = max(best, s)
	}
	return best
}

// categoryScore is the score of a match on a category or tag alone.
func categoryScore(nq string, categories []string) int {
	for _, cat := range categories {
		if strings.Contains(norm(cat), nq) {
			return 20
		}
	}
	return 0
}

// Result is one brand matching a search, with the ids of its files.
type Result struct {
	Files []string
	score int
	name  string
	rank  int // 0 for dashboard-icons, then the externals in order
}

// Search returns up to limit brands matching q, best matches first. A brand
// found in several sources appears once for each. On equal matches
// dashboard-icons comes first, then the external sources in order.
func (c *Catalogue) Search(q string, limit int) []Result {
	nq := norm(q)
	if nq == "" {
		return nil
	}
	var hits []Result
	for _, icon := range c.Icons {
		s := score(nq, icon.Name, icon.Aliases)
		if s == 0 {
			s = categoryScore(nq, icon.Categories)
		}
		if s > 0 {
			hits = append(hits, Result{Files: icon.Files(), score: s, name: icon.Name, rank: 1})
		}
	}
	for i, src := range allSources() {
		rank := i + 1
		if src.prefix == LocalPrefix {
			rank = 0
		}
		for _, b := range c.External[src.prefix] {
			s := score(nq, b.Name, append([]string{b.Slug}, b.Aliases...))
			if s == 0 {
				s = categoryScore(nq, b.Tags)
			}
			if s > 0 {
				hits = append(hits, Result{Files: b.ids(src.prefix), score: s, name: b.Slug, rank: rank})
			}
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		if a.score != b.score {
			return a.score > b.score
		}
		if len(a.name) != len(b.name) {
			return len(a.name) < len(b.name)
		}
		return a.rank < b.rank
	})
	return hits[:min(limit, len(hits))]
}

// Exact returns the id of the file whose name or alias exactly matches the
// Tile title, or else the main part of the Tile URL's domain (netflix.com ->
// netflix). It is the base file of a dashboard-icons icon, or else an external
// brand's default file, trying the sources in order.
func (c *Catalogue) Exact(title, siteURL string) (id string, ok bool) {
	candidates := []string{norm(title)}
	if u, err := url.Parse(siteURL); err == nil && u.Hostname() != "" {
		labels := strings.Split(strings.TrimPrefix(u.Hostname(), "www."), ".")
		// Only the registered name. Subdomains such as "monitoring" in
		// monitoring.solaredge.com match unrelated aliases.
		if len(labels) >= 2 {
			candidates = append(candidates, norm(labels[len(labels)-2]))
		}
	}
	for _, cand := range candidates {
		if cand == "" {
			continue
		}
		for _, b := range c.External[LocalPrefix] {
			if norm(b.Name) == cand || norm(b.Slug) == cand || slices.ContainsFunc(b.Aliases, func(a string) bool { return norm(a) == cand }) {
				return makeID(LocalPrefix, b.Slug, b.Variants[0]), true
			}
		}
		for _, icon := range c.Icons {
			if norm(icon.Name) == cand {
				return icon.Name, true
			}
		}
		for _, icon := range c.Icons {
			for _, a := range icon.Aliases {
				if norm(a) == cand {
					return icon.Name, true
				}
			}
		}
		for _, src := range externals {
			for _, b := range c.External[src.prefix] {
				if norm(b.Name) == cand || norm(b.Slug) == cand || slices.ContainsFunc(b.Aliases, func(a string) bool { return norm(a) == cand }) {
					return makeID(src.prefix, b.Slug, b.Variants[0]), true
				}
			}
		}
	}
	return "", false
}
