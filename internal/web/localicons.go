package web

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/lethek/tesla-homepage/internal/icons"
	"github.com/lethek/tesla-homepage/internal/library"
	"github.com/lethek/tesla-homepage/internal/store"
)

// localIcon is a custom icon as the Icon library page shows it.
type localIcon struct {
	Slug, Name string
	Aliases    string
	Tags       string
	Preview    string
}

func (s *Server) localIcons() ([]localIcon, error) {
	brands, err := s.library.LocalIcons()
	if err != nil {
		return nil, err
	}
	out := make([]localIcon, 0, len(brands))
	for _, b := range brands {
		out = append(out, localIcon{
			Slug: b.Slug, Name: b.Name, Preview: library.LocalURL(b.Slug),
			Aliases: strings.Join(b.Aliases, ", "), Tags: strings.Join(b.Tags, ", "),
		})
	}
	return out, nil
}

// localImage serves a custom icon's image to any signed-in User.
func (s *Server) localImage(w http.ResponseWriter, r *http.Request, _ store.User) {
	data, name, ok := s.library.LocalImage(r.PathValue("slug"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Cache-Control", "private, no-cache")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
}

// readLocalForm reads a custom icon's form. image is nil if no file was sent.
func (s *Server) readLocalForm(w http.ResponseWriter, r *http.Request) (name string, aliases, tags []string, image []byte, ext string, err error) {
	r.Body = http.MaxBytesReader(w, r.Body, icons.MaxSize+1<<20)
	if err := r.ParseMultipartForm(icons.MaxSize); err != nil {
		return "", nil, nil, nil, "", errTooLarge
	}
	name = r.FormValue("name")
	aliases = strings.Split(r.FormValue("aliases"), ",")
	tags = strings.Split(r.FormValue("tags"), ",")
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	switch r.FormValue("source") {
	case "url":
		raw := strings.TrimSpace(r.FormValue("icon_url"))
		if !isImageURL(raw) {
			return "", nil, nil, nil, "", errors.New("the image URL must start with http://, https:// or data:image/")
		}
		image, ext, err = s.icons.Fetch(ctx, raw)
		if err != nil {
			return "", nil, nil, nil, "", err
		}
	case "site":
		raw := strings.TrimSpace(r.FormValue("site_url"))
		if !isWebURL(raw) {
			return "", nil, nil, nil, "", errors.New("the site address must start with http:// or https://")
		}
		var ok bool
		if image, ext, ok = s.icons.FetchSite(ctx, raw); !ok {
			return "", nil, nil, nil, "", errors.New("that site has no usable icon")
		}
	case "upload", "":
		file, _, ferr := r.FormFile("image")
		if ferr != nil {
			break
		}
		defer file.Close()
		image, ferr = io.ReadAll(io.LimitReader(file, icons.MaxSize+1))
		if ferr != nil || len(image) > icons.MaxSize {
			return "", nil, nil, nil, "", errTooLarge
		}
		if ext, ferr = icons.Check(image); ferr != nil {
			return "", nil, nil, nil, "", ferr
		}
	}
	return name, aliases, tags, image, ext, nil
}

// saveLocalIcon adds a custom icon, or changes the one named by the path's slug.
func (s *Server) saveLocalIcon(w http.ResponseWriter, r *http.Request, u store.User) {
	name, aliases, tags, image, ext, err := s.readLocalForm(w, r)
	if err != nil {
		// Whatever went wrong reading the form or fetching the image is the
		// Admin's input to fix.
		s.auditFailed(r, "custom icon save failed", "slug", r.PathValue("slug"), "source", r.FormValue("source"), "err", err)
		s.renderLibrary(w, u, http.StatusBadRequest, "", "Couldn't save that icon: "+err.Error())
		return
	}
	adding := r.PathValue("slug") == ""
	saved, err := s.library.SaveLocal(r.PathValue("slug"), name, aliases, tags, image, ext)
	if err != nil {
		status := http.StatusBadRequest
		switch {
		case errors.Is(err, library.ErrLocalNotFound):
			status = http.StatusNotFound
		case !errors.Is(err, library.ErrLocalExists) && !errors.Is(err, library.ErrLocalName) && !errors.Is(err, library.ErrLocalImage):
			slog.Error("custom icon save failed", auditArgs(r, []any{"slug", r.PathValue("slug"), "err", err})...)
			status = http.StatusInternalServerError
		default:
			s.auditFailed(r, "custom icon save failed", "slug", r.PathValue("slug"), "err", err)
		}
		s.renderLibrary(w, u, status, "", "Couldn't save that icon: "+err.Error())
		return
	}
	event := "custom icon changed"
	if adding {
		event = "custom icon added"
	}
	s.audit(r, event, "slug", saved.Slug, "name", saved.Name, "source", r.FormValue("source"), "image_replaced", len(image) > 0)
	redirect(w, r, "/admin/library?done=saved")
}

var errTooLarge = errors.New("the upload is too large, icons must be 2 MB or less")

func (s *Server) deleteLocalIcon(w http.ResponseWriter, r *http.Request, u store.User) {
	if err := s.library.DeleteLocal(r.PathValue("slug")); err != nil {
		if errors.Is(err, library.ErrLocalNotFound) {
			http.NotFound(w, r)
			return
		}
		s.fail(w, err)
		return
	}
	s.audit(r, "custom icon deleted", "slug", r.PathValue("slug"))
	redirect(w, r, "/admin/library?done=deleted")
}

// Export and import

const (
	iconsExportFormat = "tesla-homepage-icons"
	maxImportIcons    = 500
	maxIconWords      = 20
)

// iconsExport is the file the Admin downloads: every custom icon with its
// image, so the set can be restored on another install.
type iconsExport struct {
	Format  string       `json:"format"`
	Version int          `json:"version"`
	Icons   []iconExport `json:"icons"`
}

type iconExport struct {
	Slug    string   `json:"slug"`
	Name    string   `json:"name"`
	Aliases []string `json:"aliases,omitempty"`
	Tags    []string `json:"tags,omitempty"`
	Image   string   `json:"image"` // base64
}

func (s *Server) exportLocalIcons(w http.ResponseWriter, r *http.Request, _ store.User) {
	brands, err := s.library.LocalIcons()
	if err != nil {
		s.fail(w, err)
		return
	}
	out := iconsExport{Format: iconsExportFormat, Version: exportVersion, Icons: []iconExport{}}
	for _, b := range brands {
		data, _, ok := s.library.LocalImage(b.Slug)
		if !ok {
			continue
		}
		out.Icons = append(out.Icons, iconExport{
			Slug: b.Slug, Name: b.Name, Aliases: b.Aliases, Tags: b.Tags,
			Image: base64.StdEncoding.EncodeToString(data),
		})
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		s.fail(w, err)
		return
	}
	s.audit(r, "custom icons exported", "icons", len(out.Icons))
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("Content-Disposition", `attachment; filename="icons-`+time.Now().UTC().Format("2006-01-02-150405")+`Z.json"`)
	h.Set("Cache-Control", "no-store")
	w.Write(b)
}

// importLocalIcons adds the icons in an exported file. An icon with the same
// slug as an existing one replaces it, and other icons are left alone.
func (s *Server) importLocalIcons(w http.ResponseWriter, r *http.Request, u store.User) {
	fail := func(status int, msg string) {
		s.auditFailed(r, "custom icon import failed", "reason", msg)
		s.renderLibraryBackup(w, u, status, "", msg, true)
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxImportBytes+1<<20)
	if err := r.ParseMultipartForm(maxImportBytes); err != nil {
		fail(http.StatusRequestEntityTooLarge, "The file is too large.")
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		fail(http.StatusBadRequest, "Choose an exported icons file.")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxImportBytes+1))
	if err != nil || len(data) > maxImportBytes {
		fail(http.StatusRequestEntityTooLarge, "The file is too large.")
		return
	}
	var in iconsExport
	if json.Unmarshal(data, &in) != nil || in.Format != iconsExportFormat {
		fail(http.StatusUnprocessableEntity, "That isn't an icons file exported from this app.")
		return
	}
	if in.Version != exportVersion {
		fail(http.StatusUnprocessableEntity, "This file was exported by a different version and can't be imported.")
		return
	}
	if len(in.Icons) > maxImportIcons {
		fail(http.StatusUnprocessableEntity, fmt.Sprintf("The file has more than %d icons.", maxImportIcons))
		return
	}
	added, skipped := 0, 0
	for _, ic := range in.Icons {
		image, err := base64.StdEncoding.DecodeString(ic.Image)
		if err != nil || len(image) > icons.MaxSize {
			skipped++
			continue
		}
		ext, err := icons.Check(image)
		if err != nil {
			skipped++
			continue
		}
		if _, err := s.library.PutLocal(ic.Slug, clip(ic.Name, 40), words(ic.Aliases), words(ic.Tags), image, ext); err != nil {
			skipped++
			continue
		}
		added++
	}
	s.audit(r, "custom icons imported", "added", added, "skipped", skipped)
	if added == 0 && skipped > 0 {
		fail(http.StatusUnprocessableEntity, "None of the icons in that file could be used.")
		return
	}
	msg := "Imported " + plural(added, "icon") + "."
	if skipped > 0 {
		msg += " " + plural(skipped, "icon") + " couldn't be used and were skipped."
	}
	s.renderLibrary(w, u, http.StatusOK, msg, "")
}

// words limits a list of aliases or tags from an imported file.
func words(in []string) []string {
	if len(in) > maxIconWords {
		in = in[:maxIconWords]
	}
	out := make([]string, len(in))
	for i, w := range in {
		out[i] = clip(w, 40)
	}
	return out
}
