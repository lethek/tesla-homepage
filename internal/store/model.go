package store

import (
	"crypto/rand"
	"encoding/hex"
)

// Overflow controls how rows of Tiles wider than the screen are laid out.
type Overflow string

const (
	OverflowWrap   Overflow = "wrap"
	OverflowScroll Overflow = "scroll"
)

// MaxPadding is the largest Tile padding percentage.
const MaxPadding = 30

type Tile struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	URL   string `json:"url"`
	// Icon is a file name in the icon store, or empty for the placeholder.
	Icon string `json:"icon,omitempty"`
	// Plate is the "#rrggbb" colour shown behind the icon, or empty for none.
	Plate string `json:"plate,omitempty"`
	// Padding is the space around the icon as a percentage of the tile, 0 to MaxPadding.
	Padding int `json:"padding,omitempty"`
	// Library names the Icon library entry the icon came from, if any.
	Library string `json:"library,omitempty"`
}

type Section struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Tiles []Tile `json:"tiles"`
}

type Homepage struct {
	Title    string    `json:"title"`
	Overflow Overflow  `json:"overflow"`
	Sections []Section `json:"sections"`
}

type User struct {
	ID             string `json:"id"`
	Username       string `json:"username"`
	PasswordHash   string `json:"passwordHash"`
	Admin          bool   `json:"admin"`
	SessionVersion int    `json:"sessionVersion"`
}

// NewID returns a random identifier for Users, Sections and Tiles.
func NewID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// EmptyHomepage is the Homepage a User gets when they choose to start empty.
func EmptyHomepage() Homepage {
	return Homepage{Title: "Homepage", Overflow: OverflowWrap, Sections: []Section{}}
}

// StarterHomepage is the starter template. Icons are left empty for the
// caller to resolve.
func StarterHomepage() Homepage {
	section := func(title string, tiles ...Tile) Section {
		for i := range tiles {
			tiles[i].ID = NewID()
		}
		return Section{ID: NewID(), Title: title, Tiles: tiles}
	}
	hp := EmptyHomepage()
	hp.Sections = []Section{
		section("Streaming",
			Tile{Title: "YouTube", URL: "https://www.youtube.com"},
			Tile{Title: "Netflix", URL: "https://www.netflix.com"},
			Tile{Title: "Disney+", URL: "https://www.disneyplus.com"},
			// Library names the icon file to use where the base icon is
			// dark and would be hard to see on a dark tile.
			Tile{Title: "Prime Video", URL: "https://www.primevideo.com", Library: "prime-video-light"},
			Tile{Title: "Plex", URL: "https://app.plex.tv"},
			Tile{Title: "Crunchyroll", URL: "https://www.crunchyroll.com"},
		),
		section("Music",
			Tile{Title: "Spotify", URL: "https://open.spotify.com"},
			Tile{Title: "SoundCloud", URL: "https://soundcloud.com", Library: "si:soundcloud"},
		),
		section("Everyday",
			Tile{Title: "Google", URL: "https://www.google.com"},
			Tile{Title: "Wikipedia", URL: "https://www.wikipedia.org"},
			Tile{Title: "Reddit", URL: "https://www.reddit.com"},
		),
	}
	return hp
}

// FindSection returns a pointer to the Section with the given ID, or nil.
func (h *Homepage) FindSection(id string) *Section {
	for i := range h.Sections {
		if h.Sections[i].ID == id {
			return &h.Sections[i]
		}
	}
	return nil
}

// FindTile returns a pointer to the Tile with the given ID, or nil.
func (h *Homepage) FindTile(id string) *Tile {
	for i := range h.Sections {
		for j := range h.Sections[i].Tiles {
			if h.Sections[i].Tiles[j].ID == id {
				return &h.Sections[i].Tiles[j]
			}
		}
	}
	return nil
}

// DeleteSection removes the Section with the given ID. It reports whether one was removed.
func (h *Homepage) DeleteSection(id string) bool {
	for i := range h.Sections {
		if h.Sections[i].ID == id {
			h.Sections = append(h.Sections[:i], h.Sections[i+1:]...)
			return true
		}
	}
	return false
}

// DeleteTile removes the Tile with the given ID. It reports whether one was removed.
func (h *Homepage) DeleteTile(id string) bool {
	for i := range h.Sections {
		tiles := h.Sections[i].Tiles
		for j := range tiles {
			if tiles[j].ID == id {
				h.Sections[i].Tiles = append(tiles[:j], tiles[j+1:]...)
				return true
			}
		}
	}
	return false
}

// SectionOrder is the desired order of Sections and of the Tiles within each.
type SectionOrder struct {
	ID    string   `json:"id"`
	Tiles []string `json:"tiles"`
}

// Reorder rearranges Sections and Tiles to match order. Tiles may move
// between Sections. The order must name every existing Section and Tile
// exactly once, otherwise it returns false and leaves h unchanged.
func (h *Homepage) Reorder(order []SectionOrder) bool {
	sections := map[string]Section{}
	tiles := map[string]Tile{}
	for _, s := range h.Sections {
		sections[s.ID] = s
		for _, t := range s.Tiles {
			tiles[t.ID] = t
		}
	}
	if len(order) != len(sections) {
		return false
	}
	seenS := map[string]bool{}
	seenT := map[string]bool{}
	result := make([]Section, 0, len(order))
	for _, o := range order {
		s, ok := sections[o.ID]
		if !ok || seenS[o.ID] {
			return false
		}
		seenS[o.ID] = true
		s.Tiles = make([]Tile, 0, len(o.Tiles))
		for _, tid := range o.Tiles {
			t, ok := tiles[tid]
			if !ok || seenT[tid] {
				return false
			}
			seenT[tid] = true
			s.Tiles = append(s.Tiles, t)
		}
		result = append(result, s)
	}
	if len(seenT) != len(tiles) {
		return false
	}
	h.Sections = result
	return true
}

func (h Homepage) icons(into map[string]bool) {
	for _, s := range h.Sections {
		for _, t := range s.Tiles {
			if t.Icon != "" {
				into[t.Icon] = true
			}
		}
	}
}
