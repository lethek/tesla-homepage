package library

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// LocalPrefix starts the id of a custom icon, such as "lc:my-nas". Custom
// icons are uploaded by the Admin and are listed with the external brands.
const LocalPrefix = "lc"

var (
	ErrLocalExists   = errors.New("a custom icon with that name already exists")
	ErrLocalNotFound = errors.New("no such custom icon")
	ErrLocalName     = errors.New("enter a name with at least one letter or number, up to 40 characters")
	ErrLocalImage    = errors.New("choose an image")
)

var localSource = source{LocalPrefix, "custom", "Custom icons"}

// allSources lists the custom icons first, then the downloaded catalogues.
func allSources() []source { return append([]source{localSource}, externals...) }

// LocalURL is where a custom icon's image is served.
func LocalURL(slug string) string { return "/library/local/" + slug }

// Slugify turns a name into the slug of a custom icon's id. It returns "" if
// the name has no letters or numbers.
func Slugify(name string) string {
	return strings.Trim(nonAlnum.ReplaceAllString(strings.ToLower(name), "-"), "-")
}

// IsLocal reports whether id names a custom icon.
func IsLocal(id string) bool {
	prefix, _, _, ok := parseID(id)
	return ok && prefix == LocalPrefix
}

// loadLocal reads the custom icons' metadata once. Callers must hold l.mu.
func (l *Library) loadLocal() error {
	if l.localReady || l.LocalPath == "" {
		return nil
	}
	b, err := os.ReadFile(l.LocalPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return err
	default:
		var cat Catalogue
		if err := json.Unmarshal(b, &cat); err != nil {
			return fmt.Errorf("reading custom icons: %w", err)
		}
		l.local = cat.External[LocalPrefix]
	}
	l.localReady = true
	return nil
}

// saveLocal writes the custom icons' metadata. Callers must hold l.mu.
func (l *Library) saveLocal(brands []Brand) error {
	cat := Catalogue{Format: catalogueFormat, Fetched: time.Now().UTC(), External: map[string][]Brand{LocalPrefix: brands}}
	b, err := json.MarshalIndent(cat, "", "  ")
	if err != nil {
		return err
	}
	if err := l.write(l.LocalPath, b); err != nil {
		return err
	}
	l.local = brands
	return nil
}

// merged returns a copy of cat that also lists the custom icons. Callers must
// hold l.mu.
func (l *Library) merged(cat *Catalogue) (*Catalogue, error) {
	if err := l.loadLocal(); err != nil {
		return nil, err
	}
	c := *cat
	c.External = make(map[string][]Brand, len(cat.External)+1)
	for k, v := range cat.External {
		c.External[k] = v
	}
	if err := l.loadSeed(); err != nil {
		return nil, err
	}
	all := slices.Clone(l.local)
	for _, b := range l.seed {
		if !slices.ContainsFunc(l.local, func(o Brand) bool { return o.Slug == b.Slug }) {
			all = append(all, b)
		}
	}
	c.External[LocalPrefix] = all
	return &c, nil
}

// LocalIcons lists the custom icons, sorted by name.
func (l *Library) LocalIcons() ([]Brand, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.loadLocal(); err != nil {
		return nil, err
	}
	out := slices.Clone(l.local)
	slices.SortFunc(out, func(a, b Brand) int { return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	return out, nil
}

// LocalImage returns a custom icon's image and its file name. An Admin's own
// icon is used before a built-in one with the same slug.
func (l *Library) LocalImage(slug string) (data []byte, name string, ok bool) {
	if !slugPattern.MatchString(slug) {
		return nil, "", false
	}
	if l.LocalDir != "" {
		matches, _ := filepath.Glob(filepath.Join(l.LocalDir, slug+".*"))
		if len(matches) > 0 {
			if data, err := os.ReadFile(matches[0]); err == nil {
				return data, filepath.Base(matches[0]), true
			}
		}
	}
	return l.seedImage(slug)
}

// LocalBytes reads the image of the custom icon with the given file id.
func (l *Library) LocalBytes(id string) ([]byte, error) {
	_, slug, _, ok := parseID(id)
	if !ok {
		return nil, ErrLocalNotFound
	}
	data, _, ok := l.LocalImage(slug)
	if !ok {
		return nil, ErrLocalNotFound
	}
	return data, nil
}

// SaveLocal adds a custom icon, or changes the one with the given slug. An
// empty slug adds a new icon, whose slug comes from its name. Changing an icon
// keeps its slug, so Tiles' references and the id stay valid. image and ext
// are the new image and its file extension, and may be empty when changing.
// Image bytes must already have been checked by the caller.
func (l *Library) SaveLocal(slug, name string, aliases, tags []string, image []byte, ext string) (Brand, error) {
	return l.save(slug, name, aliases, tags, image, ext, false)
}

// PutLocal adds the custom icon with the given slug, or replaces it if it
// exists. It is for restoring an export, so the slug is kept and an image is
// required.
func (l *Library) PutLocal(slug, name string, aliases, tags []string, image []byte, ext string) (Brand, error) {
	if !slugPattern.MatchString(slug) {
		return Brand{}, ErrLocalName
	}
	if len(image) == 0 {
		return Brand{}, ErrLocalImage
	}
	return l.save(slug, name, aliases, tags, image, ext, true)
}

func (l *Library) save(slug, name string, aliases, tags []string, image []byte, ext string, upsert bool) (Brand, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 40 {
		return Brand{}, ErrLocalName
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.LocalPath == "" || l.LocalDir == "" {
		return Brand{}, errors.New("custom icons are not configured")
	}
	if err := l.loadLocal(); err != nil {
		return Brand{}, err
	}
	brands := slices.Clone(l.local)
	at := slices.IndexFunc(brands, func(b Brand) bool { return b.Slug == slug })
	if slug == "" {
		if slug = Slugify(name); slug == "" || !slugPattern.MatchString(slug) {
			return Brand{}, ErrLocalName
		}
		if slices.ContainsFunc(brands, func(b Brand) bool { return b.Slug == slug }) {
			return Brand{}, ErrLocalExists
		}
		if len(image) == 0 {
			return Brand{}, ErrLocalImage
		}
	} else if at < 0 && !upsert {
		return Brand{}, ErrLocalNotFound
	}
	b := Brand{Name: name, Slug: slug, Aliases: clean(aliases), Tags: clean(tags), Variants: []string{""}}

	if len(image) > 0 {
		if err := os.MkdirAll(l.LocalDir, 0o700); err != nil {
			return Brand{}, err
		}
		old, _ := filepath.Glob(filepath.Join(l.LocalDir, slug+".*"))
		if err := l.write(filepath.Join(l.LocalDir, slug+ext), image); err != nil {
			return Brand{}, err
		}
		for _, f := range old {
			if filepath.Ext(f) != ext {
				os.Remove(f)
			}
		}
	}
	if at < 0 {
		brands = append(brands, b)
	} else {
		brands[at] = b
	}
	if err := l.saveLocal(brands); err != nil {
		if at < 0 {
			os.Remove(filepath.Join(l.LocalDir, slug+ext))
		}
		return Brand{}, err
	}
	return b, nil
}

// DeleteLocal removes a custom icon. Tiles that used it keep their copy.
func (l *Library) DeleteLocal(slug string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.loadLocal(); err != nil {
		return err
	}
	at := slices.IndexFunc(l.local, func(b Brand) bool { return b.Slug == slug })
	if at < 0 {
		return ErrLocalNotFound
	}
	if err := l.saveLocal(slices.Delete(slices.Clone(l.local), at, at+1)); err != nil {
		return err
	}
	files, _ := filepath.Glob(filepath.Join(l.LocalDir, slug+".*"))
	for _, f := range files {
		os.Remove(f)
	}
	return nil
}

// clean trims the entries and drops the empty ones.
func clean(in []string) []string {
	var out []string
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}
