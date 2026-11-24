package library

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"testing/fstest"
)

const seedJSON = `{"format": 4, "external": {"lc": [
  {"t": "Seeded", "s": "seeded", "v": [""]},
  {"t": "Both", "s": "both", "v": [""]}
]}}`

func seededLib(t *testing.T) *Library {
	t.Helper()
	l, _ := newLib(t)
	dir := t.TempDir()
	l.LocalPath, l.LocalDir = filepath.Join(dir, "library-local.json"), filepath.Join(dir, "icons")
	l.Seed = fstest.MapFS{
		"seed.json":    {Data: []byte(seedJSON)},
		"seeded.png":   {Data: []byte("seed-png")},
		"both.png":     {Data: []byte("seed-both")},
		"unlisted.gif": {Data: []byte("x")},
	}
	return l
}

func TestSeedIconsAreSearchableAndServed(t *testing.T) {
	l := seededLib(t)
	cat, err := l.Catalogue(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !cat.HasFile("lc:seeded") {
		t.Fatal("seeded icon is not in the catalogue")
	}
	data, name, ok := l.LocalImage("seeded")
	if !ok || name != "seeded.png" || !bytes.Equal(data, []byte("seed-png")) {
		t.Fatalf("LocalImage = %q, %q, %v", data, name, ok)
	}
	if _, err := l.LocalBytes("lc:seeded"); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := l.LocalImage("unlisted-or-missing"); ok {
		t.Fatal("found an icon that does not exist")
	}
}

func TestSeedIconsCannotBeChanged(t *testing.T) {
	l := seededLib(t)
	if icons, _ := l.LocalIcons(); len(icons) != 0 {
		t.Fatalf("seeded icons listed as editable: %+v", icons)
	}
	if _, err := l.SaveLocal("seeded", "Seeded", nil, nil, nil, ""); err != ErrLocalNotFound {
		t.Fatalf("SaveLocal on a seeded icon: %v", err)
	}
	if err := l.DeleteLocal("seeded"); err != ErrLocalNotFound {
		t.Fatalf("DeleteLocal on a seeded icon: %v", err)
	}
}

func TestAdminIconReplacesSeedWithSameSlug(t *testing.T) {
	l := seededLib(t)
	if _, err := l.SaveLocal("", "Both", nil, nil, []byte("mine"), ".png"); err != nil {
		t.Fatal(err)
	}
	data, _, _ := l.LocalImage("both")
	if string(data) != "mine" {
		t.Fatalf("image = %q, want the Admin's", data)
	}
	cat, _ := l.Catalogue(context.Background())
	n := 0
	for _, b := range cat.External[LocalPrefix] {
		if b.Slug == "both" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d entries for the slug, want 1", n)
	}
}

func TestBuiltInSeedIsComplete(t *testing.T) {
	l := &Library{Seed: SeedFS()}
	if err := l.loadSeed(); err != nil {
		t.Fatal(err)
	}
	if len(l.seed) == 0 {
		t.Fatal("no built-in icons")
	}
	for _, b := range l.seed {
		if _, _, ok := l.seedImage(b.Slug); !ok {
			t.Errorf("%s has no image", b.Slug)
		}
	}
}
