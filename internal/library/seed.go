package library

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
)

//go:embed seed
var seedFiles embed.FS

// SeedFS is the custom icons built into the binary: a seed.json in the same
// shape as the data volume's library-local.json, and the image files it names.
func SeedFS() fs.FS {
	sub, _ := fs.Sub(seedFiles, "seed")
	return sub
}

// loadSeed reads the built-in icons' metadata once. Callers must hold l.mu.
func (l *Library) loadSeed() error {
	if l.seedReady || l.Seed == nil {
		return nil
	}
	b, err := fs.ReadFile(l.Seed, "seed.json")
	if err != nil {
		return fmt.Errorf("reading built-in icons: %w", err)
	}
	var cat Catalogue
	if err := json.Unmarshal(b, &cat); err != nil {
		return fmt.Errorf("reading built-in icons: %w", err)
	}
	l.seed = cat.External[LocalPrefix]
	l.seedReady = true
	return nil
}

// seedImage reads a built-in icon's image.
func (l *Library) seedImage(slug string) (data []byte, name string, ok bool) {
	if l.Seed == nil {
		return nil, "", false
	}
	matches, _ := fs.Glob(l.Seed, slug+".*")
	for _, m := range matches {
		if filepath.Ext(m) == ".json" {
			continue
		}
		if data, err := fs.ReadFile(l.Seed, m); err == nil {
			return data, m, true
		}
	}
	return nil, "", false
}
