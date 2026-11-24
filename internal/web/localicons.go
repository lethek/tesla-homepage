package web

import (
	"bytes"
	"context"
	"errors"
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
		var ok bool
		if ext, ok = icons.Ext(image); !ok {
			return "", nil, nil, nil, "", icons.ErrNotImage
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
