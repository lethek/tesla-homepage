package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lethek/tesla-homepage/internal/icons"
	"github.com/lethek/tesla-homepage/internal/library"
	"github.com/lethek/tesla-homepage/internal/store"
)

var errBadRequest = errors.New("bad request")

func (s *Server) edit(w http.ResponseWriter, r *http.Request, u store.User) {
	if _, ok, err := s.store.Homepage(u.ID); err != nil {
		s.fail(w, err)
		return
	} else if !ok {
		redirect(w, r, "/welcome")
		return
	}
	draft, resumed, err := s.store.BeginDraft(u.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	if !resumed {
		s.audit(r, "edit mode started")
	}
	s.render(w, "edit", http.StatusOK, view{User: &u, Homepage: draft, Editing: true, Resumed: resumed, Stale: r.URL.Query().Get("stale") != "", Dirty: s.draftDiffers(u.ID, draft), Rev: store.Revision(draft)})
}

// draftDiffers reports whether the Draft has changes the published Homepage
// lacks. If that can't be told, it says yes, so nothing is thrown away unasked.
func (s *Server) draftDiffers(userID string, draft store.Homepage) bool {
	published, ok, err := s.store.Homepage(userID)
	return err != nil || !ok || store.Revision(published) != store.Revision(draft)
}

func (s *Server) editDone(w http.ResponseWriter, r *http.Request, u store.User) {
	if err := s.store.PublishDraft(u.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.fail(w, err)
		return
	}
	s.audit(r, "edit published")
	redirect(w, r, "/")
}

func (s *Server) editCancel(w http.ResponseWriter, r *http.Request, u store.User) {
	// A Draft with changes is only thrown away if the page saw its current
	// state. Another tab may have changed it since this one loaded.
	ok, err := s.store.DiscardDraftUnlessChanged(u.ID, r.FormValue("confirm_rev"))
	if err != nil {
		s.fail(w, err)
		return
	}
	if !ok {
		s.auditFailed(r, "edit cancel refused", "reason", "draft changed since this page loaded")
		redirect(w, r, "/edit?stale=1")
		return
	}
	s.audit(r, "edit cancelled")
	redirect(w, r, "/")
}

// updateDraft applies fn to the Draft and responds with the re-rendered editor.
// event and attrs describe the change for the audit log.
func (s *Server) updateDraft(w http.ResponseWriter, r *http.Request, u store.User, event string, fn func(*store.Homepage) error, attrs ...any) {
	hp, err := s.store.UpdateDraft(u.ID, fn)
	switch {
	case errors.Is(err, store.ErrNotFound):
		// The Draft was published or discarded elsewhere, e.g. in another tab.
		s.auditFailed(r, event+" failed", append(attrs, "reason", "no draft")...)
		redirect(w, r, "/edit")
	case errors.Is(err, errBadRequest):
		s.auditFailed(r, event+" failed", append(attrs, "reason", "no such section or tile")...)
		http.Error(w, "Bad request", http.StatusBadRequest)
	case err != nil:
		s.fail(w, err)
	default:
		s.audit(r, event, attrs...)
		s.fragment(w, "edit", "editor", view{User: &u, Homepage: hp, Editing: true, Dirty: s.draftDiffers(u.ID, hp), Rev: store.Revision(hp)})
	}
}

func (s *Server) editSettings(w http.ResponseWriter, r *http.Request, u store.User) {
	title := clip(r.FormValue("title"), 60)
	if title == "" {
		title = "Homepage"
	}
	overflow := store.Overflow(r.FormValue("overflow"))
	if overflow != store.OverflowWrap && overflow != store.OverflowScroll {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	s.updateDraft(w, r, u, "homepage settings changed", func(hp *store.Homepage) error {
		hp.Title = title
		hp.Overflow = overflow
		return nil
	}, "title", title, "overflow", string(overflow))
}

func (s *Server) editOrder(w http.ResponseWriter, r *http.Request, u store.User) {
	var order []store.SectionOrder
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&order); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	hp, err := s.store.UpdateDraft(u.ID, func(hp *store.Homepage) error {
		if !hp.Reorder(order) {
			return errBadRequest
		}
		return nil
	})
	switch {
	case errors.Is(err, errBadRequest):
		// The page was stale. The browser reloads to get the current Draft.
		http.Error(w, "Order does not match the current draft", http.StatusConflict)
	case errors.Is(err, store.ErrNotFound):
		http.Error(w, "No draft", http.StatusConflict)
	case err != nil:
		s.fail(w, err)
	default:
		s.audit(r, "sections and tiles reordered")
		// The page isn't re-rendered, so it learns the new revision here.
		w.Header().Set("X-Draft-Rev", store.Revision(hp))
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) addSection(w http.ResponseWriter, r *http.Request, u store.User) {
	title := clip(r.FormValue("title"), 60)
	if title == "" {
		title = "New section"
	}
	s.updateDraft(w, r, u, "section added", func(hp *store.Homepage) error {
		hp.Sections = append(hp.Sections, store.Section{ID: store.NewID(), Title: title, Tiles: []store.Tile{}})
		return nil
	}, "section", title)
}

func (s *Server) renameSection(w http.ResponseWriter, r *http.Request, u store.User) {
	title := clip(r.FormValue("title"), 60)
	s.updateDraft(w, r, u, "section renamed", func(hp *store.Homepage) error {
		sec := hp.FindSection(r.PathValue("id"))
		if sec == nil || title == "" {
			return errBadRequest
		}
		sec.Title = title
		return nil
	}, "section", title)
}

func (s *Server) deleteSection(w http.ResponseWriter, r *http.Request, u store.User) {
	title := ""
	if draft, _, err := s.store.Draft(u.ID); err == nil {
		if sec := draft.FindSection(r.PathValue("id")); sec != nil {
			title = sec.Title
		}
	}
	s.updateDraft(w, r, u, "section deleted", func(hp *store.Homepage) error {
		if !hp.DeleteSection(r.PathValue("id")) {
			return errBadRequest
		}
		return nil
	}, "section", title)
}

func (s *Server) deleteTile(w http.ResponseWriter, r *http.Request, u store.User) {
	title := ""
	if draft, _, err := s.store.Draft(u.ID); err == nil {
		if t := draft.FindTile(r.PathValue("id")); t != nil {
			title = t.Title
		}
	}
	s.updateDraft(w, r, u, "tile deleted", func(hp *store.Homepage) error {
		if !hp.DeleteTile(r.PathValue("id")) {
			return errBadRequest
		}
		return nil
	}, "tile", title)
}

// Plates

const (
	plateDark  = "#1f1f1f"
	plateWhite = "#ffffff"
)

var hexColour = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// plateChoice maps a stored Plate to the dialog's radio choice and custom colour.
func plateChoice(plate string) (choice, custom string) {
	switch plate {
	case "":
		return "none", plateDark
	case plateDark:
		return "dark", plateDark
	case plateWhite:
		return "white", plateDark
	}
	return "custom", plate
}

// Tile form

// tileForm is the data behind the add/edit Tile dialog.
type tileForm struct {
	Action      string
	Tile        store.Tile
	Source      string // keep, library, upload, url or site
	Library     string
	Query       string
	IconURL     string
	PlateChoice string
	PlateCustom string
	Error       string
}

func (s *Server) renderTileForm(w http.ResponseWriter, f tileForm, isError bool) {
	if isError {
		// Show the form again inside the dialog instead of replacing the editor.
		w.Header().Set("HX-Retarget", "#dialog-body")
		w.Header().Set("HX-Reswap", "innerHTML")
	}
	s.execute(w, "edit", "tile-form", http.StatusOK, f)
}

func (s *Server) newTileForm(w http.ResponseWriter, r *http.Request, u store.User) {
	s.renderTileForm(w, tileForm{
		Action:      "/edit/sections/" + url.PathEscape(r.PathValue("id")) + "/tiles",
		Source:      "library",
		PlateChoice: "none",
		PlateCustom: plateDark,
	}, false)
}

func (s *Server) editTileForm(w http.ResponseWriter, r *http.Request, u store.User) {
	draft, _, err := s.store.Draft(u.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	t := draft.FindTile(r.PathValue("id"))
	if t == nil {
		http.NotFound(w, r)
		return
	}
	f := tileForm{Action: "/edit/tiles/" + url.PathEscape(t.ID), Tile: *t, Query: t.Title}
	f.PlateChoice, f.PlateCustom = plateChoice(t.Plate)
	switch {
	case t.Library != "":
		f.Source, f.Library = "library", t.Library
	case t.Icon != "":
		f.Source = "keep"
	default:
		f.Source = "library"
	}
	s.renderTileForm(w, f, false)
}

func (s *Server) addTile(w http.ResponseWriter, r *http.Request, u store.User) {
	sectionID := r.PathValue("id")
	f := tileForm{Action: "/edit/sections/" + url.PathEscape(sectionID) + "/tiles"}
	in, ok := s.readTileForm(w, r, &f)
	if !ok {
		return
	}
	icon, lib, ok := s.resolveIcon(w, r, &f, in, store.Tile{})
	if !ok {
		return
	}
	s.updateDraft(w, r, u, "tile added", func(hp *store.Homepage) error {
		sec := hp.FindSection(sectionID)
		if sec == nil {
			return errBadRequest
		}
		sec.Tiles = append(sec.Tiles, store.Tile{ID: store.NewID(), Title: in.title, URL: in.url, Icon: icon, Plate: in.plate, Padding: in.padding, Library: lib})
		return nil
	}, "tile", in.title, "icon_source", in.source, "library_icon", lib)
}

func (s *Server) updateTile(w http.ResponseWriter, r *http.Request, u store.User) {
	tileID := r.PathValue("id")
	draft, _, err := s.store.Draft(u.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	existing := draft.FindTile(tileID)
	if existing == nil {
		http.NotFound(w, r)
		return
	}
	f := tileForm{Action: "/edit/tiles/" + url.PathEscape(tileID), Tile: *existing}
	in, ok := s.readTileForm(w, r, &f)
	if !ok {
		return
	}
	icon, lib, ok := s.resolveIcon(w, r, &f, in, *existing)
	if !ok {
		return
	}
	s.updateDraft(w, r, u, "tile changed", func(hp *store.Homepage) error {
		t := hp.FindTile(tileID)
		if t == nil {
			return errBadRequest
		}
		t.Title, t.URL, t.Icon, t.Plate, t.Padding, t.Library = in.title, in.url, icon, in.plate, in.padding, lib
		return nil
	}, "tile", in.title, "icon_source", in.source, "library_icon", lib)
}

type tileInput struct {
	title, url string
	source     string
	library    string
	iconURL    string
	upload     []byte
	plate      string
	padding    int
}

// readTileForm parses and validates a submitted Tile form. On failure it
// renders the form with an error and returns false.
func (s *Server) readTileForm(w http.ResponseWriter, r *http.Request, f *tileForm) (tileInput, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, icons.MaxSize+1<<20)
	if err := r.ParseMultipartForm(icons.MaxSize); err != nil && !errors.Is(err, http.ErrNotMultipart) {
		f.Error = "The upload is too large. Icons must be 2 MB or less."
		s.renderTileForm(w, *f, true)
		return tileInput{}, false
	}
	in := tileInput{
		title:   clip(r.FormValue("title"), 40),
		url:     strings.TrimSpace(r.FormValue("url")),
		source:  r.FormValue("source"),
		library: r.FormValue("library"),
		iconURL: strings.TrimSpace(r.FormValue("icon_url")),
	}
	f.Tile.Title, f.Tile.URL, f.IconURL = in.title, in.url, in.iconURL
	f.Source, f.Library, f.Query = in.source, in.library, r.FormValue("q")

	switch choice := r.FormValue("plate"); choice {
	case "dark":
		in.plate = plateDark
	case "white":
		in.plate = plateWhite
	case "custom":
		in.plate = strings.ToLower(r.FormValue("plate_custom"))
	}
	f.PlateChoice, f.PlateCustom = plateChoice(in.plate)

	paddingOK := true
	if v := strings.TrimSpace(r.FormValue("padding")); v != "" {
		n, err := strconv.Atoi(v)
		paddingOK = err == nil && n >= 0 && n <= store.MaxPadding
		if paddingOK {
			in.padding = n
		}
	}
	f.Tile.Padding = in.padding

	if file, _, err := r.FormFile("icon_file"); err == nil {
		defer file.Close()
		in.upload, err = io.ReadAll(io.LimitReader(file, icons.MaxSize+1))
		if err != nil || len(in.upload) > icons.MaxSize {
			f.Error = "The upload is too large. Icons must be 2 MB or less."
			s.renderTileForm(w, *f, true)
			return tileInput{}, false
		}
	}

	switch {
	case in.title == "":
		f.Error = "Enter a title."
	case !isWebURL(in.url):
		f.Error = "Enter a web address starting with http:// or https://."
	case in.plate != "" && !hexColour.MatchString(in.plate):
		f.Error = "Choose a valid plate colour."
	case !paddingOK:
		f.Error = "Padding must be between 0 and 30."
	case in.source == "upload" && len(in.upload) == 0:
		f.Error = "Choose an image to upload."
	case in.source == "url" && in.iconURL == "":
		f.Error = "Enter the image's web address."
	case (in.source == "url" || in.source == "") && in.iconURL != "" && !isWebURL(in.iconURL):
		f.Error = "The image URL must start with http:// or https://."
	}
	if f.Error != "" {
		s.renderTileForm(w, *f, true)
		return tileInput{}, false
	}
	return in, true
}

// resolveIcon gets the icon for the chosen source. It returns the stored icon
// name ("" for the placeholder) and the Icon library entry it came from.
func (s *Server) resolveIcon(w http.ResponseWriter, r *http.Request, f *tileForm, in tileInput, current store.Tile) (icon, lib string, ok bool) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	var err error
	switch in.source {
	case "upload":
		icon, err = s.icons.FromUpload(in.upload)
	case "url":
		icon, err = s.icons.FromURL(ctx, in.iconURL)
	case "library":
		if in.library == "" {
			// Nothing picked: the letter placeholder. The From site tab is
			// the way to fetch the site's own icon.
			break
		}
		var cat *library.Catalogue
		if cat, err = s.library.Catalogue(ctx); err == nil {
			icon, err = s.libraryIcon(ctx, cat, in.library)
			lib = in.library
		}
	case "site":
		icon = s.icons.FromSite(ctx, in.url)
	case "keep":
		if current.Icon == "" {
			icon = s.icons.FromSite(ctx, in.url)
		} else {
			icon, lib = current.Icon, current.Library
		}
	default:
		// No tab chosen, e.g. a scripted request: upload, then image URL,
		// then the current icon, then the site's own icon.
		switch {
		case len(in.upload) > 0:
			icon, err = s.icons.FromUpload(in.upload)
		case in.iconURL != "":
			icon, err = s.icons.FromURL(ctx, in.iconURL)
		case current.Icon != "":
			icon, lib = current.Icon, current.Library
		default:
			icon = s.icons.FromSite(ctx, in.url)
		}
	}
	if err != nil {
		s.auditFailed(r, "tile icon failed", "tile", in.title, "icon_source", in.source, "err", err)
		f.Error = "Couldn't use that icon: " + err.Error()
		s.renderTileForm(w, *f, true)
		return "", "", false
	}
	return icon, lib, true
}

// libraryIcon downloads an Icon library file (a base icon or one of its
// variants), stores it and returns the stored name.
func (s *Server) libraryIcon(ctx context.Context, cat *library.Catalogue, file string) (string, error) {
	if !cat.HasFile(file) {
		return "", fmt.Errorf("%q is not in the icon library", file)
	}
	if library.IsLocal(file) {
		data, err := s.library.LocalBytes(file)
		if err != nil {
			return "", err
		}
		return s.icons.FromUpload(data)
	}
	return s.icons.FromURL(ctx, s.library.ImageURL(file))
}

// Icon library search

type libraryResult struct {
	Name     string // the file's id
	Label    string
	Preview  string
	Selected bool
}

type libraryResults struct {
	Results []libraryResult
	Query   string
	Error   string
}

// librarySearch renders the dialog's library results for q. Each matching icon
// is listed as its base file followed by its variants. The current pick stays
// first; with no pick, an exact match for the Tile's title or URL is
// pre-selected.
func (s *Server) librarySearch(w http.ResponseWriter, r *http.Request, u store.User) {
	q := strings.TrimSpace(r.FormValue("q"))
	res := libraryResults{Query: q}
	cat, err := s.library.Catalogue(r.Context())
	if err != nil {
		slog.Error("icon library unavailable for search", auditArgs(r, []any{"err", err})...)
		res.Error = "The icon library is unavailable right now. You can still upload an icon, give an image URL or use the site's own icon."
		s.execute(w, "edit", "library-results", http.StatusOK, res)
		return
	}
	selected := r.FormValue("library")
	if !cat.HasFile(selected) {
		selected = ""
	}
	if selected == "" {
		if match, ok := cat.Exact(r.FormValue("title"), r.FormValue("url")); ok {
			selected = match
		}
	}
	add := func(file string) {
		res.Results = append(res.Results, libraryResult{
			Name: file, Label: library.Label(file), Preview: s.library.ImageURL(file), Selected: file == selected,
		})
	}
	if selected != "" {
		add(selected)
	}
	for _, found := range cat.Search(q, 24) {
		for _, file := range found.Files {
			if file != selected {
				add(file)
			}
		}
	}
	s.execute(w, "edit", "library-results", http.StatusOK, res)
}

func isWebURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// clip trims s and shortens it to at most n characters.
func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > n {
		s = string([]rune(s)[:n])
	}
	return s
}
