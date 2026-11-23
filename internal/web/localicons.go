package web

import (
	"errors"
	"io"
	"log"
	"net/http"
	"strings"

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
	path, ok := s.library.LocalFile(r.PathValue("slug"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Cache-Control", "private, no-cache")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	http.ServeFile(w, r, path)
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
	file, _, ferr := r.FormFile("image")
	if ferr != nil {
		return name, aliases, tags, nil, "", nil
	}
	defer file.Close()
	image, ferr = io.ReadAll(io.LimitReader(file, icons.MaxSize+1))
	if ferr != nil || len(image) > icons.MaxSize {
		return "", nil, nil, nil, "", errTooLarge
	}
	ext, ok := icons.Ext(image)
	if !ok {
		return "", nil, nil, nil, "", icons.ErrNotImage
	}
	return name, aliases, tags, image, ext, nil
}

// saveLocalIcon adds a custom icon, or changes the one named by the path's slug.
func (s *Server) saveLocalIcon(w http.ResponseWriter, r *http.Request, u store.User) {
	name, aliases, tags, image, ext, err := s.readLocalForm(w, r)
	if err == nil {
		_, err = s.library.SaveLocal(r.PathValue("slug"), name, aliases, tags, image, ext)
	}
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, library.ErrLocalNotFound) {
			status = http.StatusNotFound
		} else if !isLocalInputError(err) {
			log.Printf("saving custom icon: %v", err)
			status = http.StatusInternalServerError
		}
		s.renderLibrary(w, u, status, "", "Couldn't save that icon: "+err.Error())
		return
	}
	redirect(w, r, "/admin/library?done=saved")
}

func isLocalInputError(err error) bool {
	return errors.Is(err, library.ErrLocalExists) || errors.Is(err, library.ErrLocalName) ||
		errors.Is(err, library.ErrLocalImage) || errors.Is(err, icons.ErrNotImage) ||
		errors.Is(err, errTooLarge)
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
	redirect(w, r, "/admin/library?done=deleted")
}
