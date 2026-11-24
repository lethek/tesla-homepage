package web

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/lethek/tesla-homepage/internal/auth"
	"github.com/lethek/tesla-homepage/internal/icons"
	"github.com/lethek/tesla-homepage/internal/store"
)

// accountInfo is what the Manage account page shows about the User's homepage.
type accountInfo struct {
	HasHomepage bool
	Sections    int
	Tiles       int
	Unsaved     bool // Edit mode has changes that are not published
}

func (s *Server) accountInfo(u store.User) (accountInfo, error) {
	hp, ok, err := s.store.Homepage(u.ID)
	if err != nil {
		return accountInfo{}, err
	}
	info := accountInfo{HasHomepage: ok, Sections: len(hp.Sections)}
	for _, sec := range hp.Sections {
		info.Tiles += len(sec.Tiles)
	}
	if draft, has, err := s.store.Draft(u.ID); err != nil {
		return accountInfo{}, err
	} else if has {
		info.Unsaved = store.Revision(draft) != store.Revision(hp)
	}
	return info, nil
}

func (s *Server) renderAccount(w http.ResponseWriter, u store.User, status int, msg, errMsg string) {
	info, err := s.accountInfo(u)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, "account", status, view{User: &u, Message: msg, Error: errMsg, Form: info})
}

func (s *Server) accountPage(w http.ResponseWriter, r *http.Request, u store.User) {
	s.renderAccount(w, u, http.StatusOK, "", "")
}

// signOutOthers ends every session except the one making the request.
func (s *Server) signOutOthers(w http.ResponseWriter, r *http.Request, u store.User) {
	u, err := s.store.SignOutEverywhere(u.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.audit(r, "signed out other devices", "target", u.Username)
	s.sessions.SetCookie(w, r, u.ID, u.SessionVersion)
	s.renderAccount(w, u, http.StatusOK, "Other devices have been signed out.", "")
}

// Export and import

const (
	exportFormat    = "tesla-homepage"
	exportVersion   = 1
	maxImportBytes  = 24 << 20
	maxImportItems  = 500
	maxImportGroups = 100
)

// homepageExport is the file a User downloads: their Homepage and the icon
// images its tiles use, so it can be loaded into another account.
type homepageExport struct {
	Format   string            `json:"format"`
	Version  int               `json:"version"`
	Homepage store.Homepage    `json:"homepage"`
	Icons    map[string]string `json:"icons,omitempty"` // icon file name to base64 image
}

func (s *Server) exportHomepage(w http.ResponseWriter, r *http.Request, u store.User) {
	hp, ok, err := s.store.Homepage(u.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	out := homepageExport{Format: exportFormat, Version: exportVersion, Homepage: hp, Icons: map[string]string{}}
	for _, sec := range hp.Sections {
		for _, t := range sec.Tiles {
			if t.Icon == "" || out.Icons[t.Icon] != "" {
				continue
			}
			path, err := s.store.IconPath(t.Icon)
			if err != nil {
				continue
			}
			if data, err := os.ReadFile(path); err == nil {
				out.Icons[t.Icon] = base64.StdEncoding.EncodeToString(data)
			}
		}
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		s.fail(w, err)
		return
	}
	s.audit(r, "homepage exported")
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("Content-Disposition", `attachment; filename="homepage.json"`)
	h.Set("Cache-Control", "no-store")
	w.Write(b)
}

// importHomepage replaces the User's Homepage and Draft with the contents of
// an exported file.
func (s *Server) importHomepage(w http.ResponseWriter, r *http.Request, u store.User) {
	fail := func(msg string) {
		s.auditFailed(r, "homepage import failed", "reason", msg)
		s.renderAccount(w, u, http.StatusUnprocessableEntity, "", msg)
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxImportBytes+1<<20)
	if err := r.ParseMultipartForm(maxImportBytes); err != nil {
		fail("The file is too large.")
		return
	}
	if r.FormValue("confirm") != "1" {
		fail("Tick the box to confirm that your current homepage will be replaced.")
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		fail("Choose an exported homepage file.")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxImportBytes+1))
	if err != nil || len(data) > maxImportBytes {
		fail("The file is too large.")
		return
	}
	hp, skipped, err := s.readExport(data)
	if err != nil {
		fail(err.Error())
		return
	}
	if err := s.store.SaveHomepage(u.ID, hp); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.store.DiscardDraft(u.ID); err != nil {
		s.fail(w, err)
		return
	}
	tiles := 0
	for _, sec := range hp.Sections {
		tiles += len(sec.Tiles)
	}
	s.audit(r, "homepage imported", "sections", len(hp.Sections), "tiles", tiles, "without_icon", skipped)
	msg := fmt.Sprintf("Imported %s and %s.", plural(len(hp.Sections), "section"), plural(tiles, "tile"))
	if skipped > 0 {
		msg += fmt.Sprintf(" %s had no usable icon and show a letter instead.", plural(skipped, "tile"))
	}
	s.renderAccount(w, u, http.StatusOK, msg, "")
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

var errBadExport = errors.New("That isn't a homepage file exported from this app.")

// readExport checks an exported file and returns a clean Homepage. Nothing in
// the file is trusted: every field is validated or replaced, and each icon is
// checked as if it had been uploaded. skipped counts tiles whose icon could not
// be used.
func (s *Server) readExport(data []byte) (hp store.Homepage, skipped int, err error) {
	var in homepageExport
	if json.Unmarshal(data, &in) != nil || in.Format != exportFormat {
		return hp, 0, errBadExport
	}
	if in.Version != exportVersion {
		return hp, 0, errors.New("This file was exported by a different version and can't be imported.")
	}
	if len(in.Homepage.Sections) > maxImportGroups {
		return hp, 0, fmt.Errorf("The file has more than %d sections.", maxImportGroups)
	}
	hp = store.EmptyHomepage()
	hp.Title = clip(in.Homepage.Title, 60)
	if hp.Title == "" {
		hp.Title = store.EmptyHomepage().Title
	}
	if in.Homepage.Overflow == store.OverflowScroll {
		hp.Overflow = store.OverflowScroll
	}
	saved := map[string]string{} // exported icon name to stored name, "" if unusable
	total := 0
	for _, sec := range in.Homepage.Sections {
		out := store.Section{ID: store.NewID(), Title: clip(sec.Title, 60), Tiles: []store.Tile{}}
		for _, t := range sec.Tiles {
			if total++; total > maxImportItems {
				return store.Homepage{}, 0, fmt.Errorf("The file has more than %d tiles.", maxImportItems)
			}
			nt := store.Tile{
				ID: store.NewID(), Title: clip(t.Title, 40), URL: strings.TrimSpace(t.URL),
				Library: clip(t.Library, 100), Fullscreen: t.Fullscreen,
			}
			if nt.Title == "" || !isWebURL(nt.URL) {
				return store.Homepage{}, 0, errors.New("The file has a tile with no title or with a web address that doesn't start with http:// or https://.")
			}
			if t.Plate != "" && hexColour.MatchString(t.Plate) {
				nt.Plate = strings.ToLower(t.Plate)
			}
			if t.Padding > 0 && t.Padding <= store.MaxPadding {
				nt.Padding = t.Padding
			}
			if t.Icon != "" {
				name, done := saved[t.Icon]
				if !done {
					name = s.importIcon(in.Icons[t.Icon])
					saved[t.Icon] = name
				}
				if nt.Icon = name; name == "" {
					nt.Library = ""
					skipped++
				}
			}
			out.Tiles = append(out.Tiles, nt)
		}
		hp.Sections = append(hp.Sections, out)
	}
	return hp, skipped, nil
}

// importIcon stores one exported icon, returning "" if it is missing, too
// large or not an image.
func (s *Server) importIcon(b64 string) string {
	if b64 == "" || len(b64) > icons.MaxSize*2 {
		return ""
	}
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(data) > icons.MaxSize {
		return ""
	}
	name, err := s.icons.FromUpload(data)
	if err != nil {
		return ""
	}
	return name
}

// Delete account

func (s *Server) deleteAccountForm(w http.ResponseWriter, r *http.Request, u store.User) {
	if u.Admin {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	s.render(w, "delete", http.StatusOK, view{User: &u})
}

// deleteAccount lets a User who is not the Admin delete their own account.
func (s *Server) deleteAccount(w http.ResponseWriter, r *http.Request, u store.User) {
	if u.Admin {
		s.auditFailed(r, "account delete rejected", "reason", "admin")
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	// Someone holding a stolen session could otherwise guess the password.
	key := auth.Key("password", u.Username)
	if !s.admit(w, r, "account delete", func(status int, msg string) {
		s.render(w, "delete", status, view{User: &u, Error: msg})
	}, auth.Limit{Key: key, Max: guessesPerUser}) {
		return
	}
	if !auth.CheckPassword(u.PasswordHash, r.FormValue("current")) {
		s.auditFailed(r, "account delete failed", "reason", "wrong password")
		s.render(w, "delete", http.StatusUnprocessableEntity, view{User: &u, Error: "Password is wrong."})
		return
	}
	s.throttle.Reset(key)
	if err := s.store.DeleteUser(u.ID); err != nil {
		s.fail(w, err)
		return
	}
	s.audit(r, "account deleted", "target", u.Username)
	auth.ClearCookie(w, r)
	redirect(w, r, "/login")
}
