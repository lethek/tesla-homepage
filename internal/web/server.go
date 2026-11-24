// Package web serves the Homepage, edit mode, login and user management.
package web

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/lethek/tesla-homepage/internal/auth"
	"github.com/lethek/tesla-homepage/internal/icons"
	"github.com/lethek/tesla-homepage/internal/library"
	"github.com/lethek/tesla-homepage/internal/store"
)

//go:embed templates static
var assets embed.FS

type Server struct {
	store    *store.Store
	sessions *auth.Sessions
	icons    *icons.Resolver
	library  *library.Library
	pages    map[string]*template.Template
	// throttle slows password guessing, at sign-in and when changing a password.
	throttle *auth.Throttle
	// TrustedProxies are the reverse proxies whose X-Forwarded-For header is
	// believed. With none, the client's address is the connection's.
	TrustedProxies []netip.Prefix
}

// view is the data passed to every template.
type view struct {
	User     *store.User
	Homepage store.Homepage
	Editing  bool
	Resumed  bool
	Dirty    bool   // the Draft differs from the published Homepage
	Rev      string // identifies the Draft's current contents
	Stale    bool   // a Cancel was refused because the Draft had changed
	Error    string
	Message  string
	Users    []store.User
	Form     any
}

var pageNames = []string{"home", "edit", "login", "setup", "welcome", "account", "reset", "users", "library"}

func New(st *store.Store, sessions *auth.Sessions, resolver *icons.Resolver, lib *library.Library) (*Server, error) {
	funcs := template.FuncMap{
		"initial": func(s string) string {
			r, _ := utf8.DecodeRuneInString(strings.TrimSpace(s))
			if r == utf8.RuneError {
				return "?"
			}
			return string(unicode.ToUpper(r))
		},
	}
	pages := map[string]*template.Template{}
	for _, name := range pageNames {
		t, err := template.New(name).Funcs(funcs).ParseFS(assets,
			"templates/base.html", "templates/partials.html", "templates/"+name+".html")
		if err != nil {
			return nil, err
		}
		pages[name] = t
	}
	return &Server{store: st, sessions: sessions, icons: resolver, library: lib, pages: pages, throttle: auth.NewThrottle()}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	static, _ := fs.Sub(assets, "static")
	staticHandler := http.StripPrefix("/static/", http.FileServerFS(static))
	mux.Handle("GET /static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=300")
		staticHandler.ServeHTTP(w, r)
	}))

	mux.HandleFunc("GET /setup", s.setupForm)
	mux.HandleFunc("POST /setup", s.setup)
	mux.HandleFunc("GET /login", s.loginForm)
	mux.HandleFunc("POST /login", s.login)
	mux.HandleFunc("POST /logout", s.logout)

	mux.Handle("GET /{$}", s.user(s.home))
	mux.Handle("GET /icons/{name}", s.user(s.icon))
	mux.Handle("GET /welcome", s.user(s.welcomeForm))
	mux.Handle("POST /welcome", s.user(s.welcome))
	mux.Handle("GET /account", s.user(s.accountPage))
	mux.Handle("GET /account/reset", s.user(s.resetForm))
	mux.Handle("POST /account/reset", s.user(s.resetAccount))
	mux.Handle("POST /account/password", s.user(s.changePassword))

	mux.Handle("GET /edit", s.user(s.edit))
	mux.Handle("POST /edit/done", s.user(s.editDone))
	mux.Handle("POST /edit/cancel", s.user(s.editCancel))
	mux.Handle("POST /edit/settings", s.user(s.editSettings))
	mux.Handle("POST /edit/order", s.user(s.editOrder))
	mux.Handle("POST /edit/sections", s.user(s.addSection))
	mux.Handle("POST /edit/sections/{id}", s.user(s.renameSection))
	mux.Handle("POST /edit/sections/{id}/delete", s.user(s.deleteSection))
	mux.Handle("GET /edit/sections/{id}/tiles/new", s.user(s.newTileForm))
	mux.Handle("POST /edit/sections/{id}/tiles", s.user(s.addTile))
	mux.Handle("GET /edit/library", s.user(s.librarySearch))
	mux.Handle("GET /edit/tiles/{id}", s.user(s.editTileForm))
	mux.Handle("POST /edit/tiles/{id}", s.user(s.updateTile))
	mux.Handle("POST /edit/tiles/{id}/delete", s.user(s.deleteTile))

	mux.Handle("GET /admin/users", s.admin(s.usersPage))
	mux.Handle("POST /admin/users", s.admin(s.createUser))
	mux.Handle("POST /admin/users/{id}/password", s.admin(s.resetPassword))
	mux.Handle("POST /admin/users/{id}/delete", s.admin(s.deleteUser))
	mux.Handle("GET /admin/library", s.admin(s.libraryPage))
	mux.Handle("POST /admin/library/refresh", s.admin(s.refreshLibrary))
	mux.Handle("POST /admin/library/local", s.admin(s.saveLocalIcon))
	mux.Handle("POST /admin/library/local/{slug}", s.admin(s.saveLocalIcon))
	mux.Handle("POST /admin/library/local/{slug}/delete", s.admin(s.deleteLocalIcon))
	mux.Handle("GET /library/local/{slug}", s.user(s.localImage))

	return s.requestLog(http.NewCrossOriginProtection().Handler(securityHeaders(mux)))
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

type userHandler func(w http.ResponseWriter, r *http.Request, u store.User)

// user wraps h so it only runs for a signed-in User. Otherwise it sends the
// browser to first-run setup or to the login page.
func (s *Server) user(h userHandler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, err := s.currentUser(r)
		if err != nil {
			users, uerr := s.store.Users()
			if uerr != nil {
				s.fail(w, uerr)
				return
			}
			if len(users) == 0 {
				redirect(w, r, "/setup")
			} else {
				redirect(w, r, "/login")
			}
			return
		}
		setActor(r, u.Username)
		h(w, r, u)
	})
}

func (s *Server) admin(h userHandler) http.Handler {
	return s.user(func(w http.ResponseWriter, r *http.Request, u store.User) {
		if !u.Admin {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		h(w, r, u)
	})
}

func (s *Server) currentUser(r *http.Request) (store.User, error) {
	c, err := r.Cookie(auth.CookieName)
	if err != nil {
		return store.User{}, err
	}
	sess, err := s.sessions.Decode(c.Value)
	if err != nil {
		return store.User{}, err
	}
	u, err := s.store.UserByID(sess.UserID)
	if err != nil {
		return store.User{}, err
	}
	if u.SessionVersion != sess.Version {
		return store.User{}, auth.ErrInvalidSession
	}
	return u, nil
}

// redirect sends the browser to path, using HX-Redirect for htmx requests so
// the whole page navigates rather than a fragment being replaced.
func redirect(w http.ResponseWriter, r *http.Request, path string) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", path)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, path, http.StatusSeeOther)
}

func (s *Server) render(w http.ResponseWriter, page string, status int, v view) {
	s.execute(w, page, "base", status, v)
}

func (s *Server) fragment(w http.ResponseWriter, page, name string, v view) {
	s.execute(w, page, name, http.StatusOK, v)
}

func (s *Server) execute(w http.ResponseWriter, page, name string, status int, v any) {
	var buf bytes.Buffer
	if err := s.pages[page].ExecuteTemplate(&buf, name, v); err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	buf.WriteTo(w)
}

func (s *Server) fail(w http.ResponseWriter, err error) {
	slog.Error("internal error", "err", err)
	http.Error(w, "Internal server error", http.StatusInternalServerError)
}

// Homepage

func (s *Server) home(w http.ResponseWriter, r *http.Request, u store.User) {
	hp, ok, err := s.store.Homepage(u.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	if !ok {
		redirect(w, r, "/welcome")
		return
	}
	s.render(w, "home", http.StatusOK, view{User: &u, Homepage: hp})
}

func (s *Server) icon(w http.ResponseWriter, r *http.Request, _ store.User) {
	path, err := s.store.IconPath(r.PathValue("name"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Cache-Control", "private, max-age=31536000, immutable")
	// Icons may be SVG. Stop any script in them from running if opened directly.
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	http.ServeFile(w, r, path)
}

func (s *Server) welcomeForm(w http.ResponseWriter, r *http.Request, u store.User) {
	if _, ok, _ := s.store.Homepage(u.ID); ok {
		redirect(w, r, "/")
		return
	}
	s.render(w, "welcome", http.StatusOK, view{User: &u})
}

func (s *Server) welcome(w http.ResponseWriter, r *http.Request, u store.User) {
	if _, ok, _ := s.store.Homepage(u.ID); ok {
		redirect(w, r, "/")
		return
	}
	hp := store.EmptyHomepage()
	start := "empty"
	if r.FormValue("start") == "starter" {
		start = "starter"
		hp = store.StarterHomepage()
		s.fetchStarterIcons(r.Context(), &hp)
	}
	if err := s.store.SaveHomepage(u.ID, hp); err != nil {
		s.fail(w, err)
		return
	}
	s.audit(r, "homepage created", "start", start)
	redirect(w, r, "/")
}

// fetchStarterIcons gives each Tile its exact Icon library match, or else the
// site's own icon. Tiles with neither keep the placeholder.
func (s *Server) fetchStarterIcons(ctx context.Context, hp *store.Homepage) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cat, err := s.library.Catalogue(ctx)
	if err != nil {
		slog.Warn("icon library unavailable for starter set", "err", err)
	}
	done := make(chan struct{})
	n := 0
	for i := range hp.Sections {
		for j := range hp.Sections[i].Tiles {
			t := &hp.Sections[i].Tiles[j]
			n++
			go func() {
				defer func() { done <- struct{}{} }()
				// The template may name the file to use; otherwise use the
				// base file of an exact match.
				file := t.Library
				t.Library = ""
				if cat != nil {
					if file == "" {
						if match, ok := cat.Exact(t.Title, t.URL); ok {
							file = match
						}
					}
					if file != "" {
						if icon, err := s.libraryIcon(ctx, cat, file); err == nil {
							t.Icon, t.Library = icon, file
							return
						}
					}
				}
				t.Icon = s.icons.FromSite(ctx, t.URL)
			}()
		}
	}
	for range n {
		<-done
	}
}

// Icon library admin

type libraryStatus struct {
	Cached  bool
	Fetched time.Time
	Counts  []library.Count
	Local   []localIcon
	New     localIcon // the blank form for adding
}

func (s *Server) libraryPage(w http.ResponseWriter, r *http.Request, u store.User) {
	msg := ""
	switch r.URL.Query().Get("done") {
	case "refreshed":
		msg = "Icon library refreshed."
	case "saved":
		msg = "Custom icon saved."
	case "deleted":
		msg = "Custom icon deleted."
	}
	s.renderLibrary(w, u, http.StatusOK, msg, "")
}

func (s *Server) renderLibrary(w http.ResponseWriter, u store.User, status int, msg, errMsg string) {
	cat, ok, err := s.library.Cached()
	if err != nil {
		s.fail(w, err)
		return
	}
	st := libraryStatus{Cached: ok}
	if ok {
		st.Fetched, st.Counts = cat.Fetched, cat.Counts()
	}
	if st.Local, err = s.localIcons(); err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, "library", status, view{User: &u, Message: msg, Error: errMsg, Form: st})
}

func (s *Server) refreshLibrary(w http.ResponseWriter, r *http.Request, u store.User) {
	if _, err := s.library.Refresh(r.Context()); err != nil {
		slog.Error("icon library refresh failed", auditArgs(r, []any{"err", err})...)
		s.renderLibrary(w, u, http.StatusBadGateway, "", "Couldn't download the icon library: "+err.Error())
		return
	}
	s.audit(r, "icon library refreshed")
	redirect(w, r, "/admin/library?done=refreshed")
}

// Setup, login and logout

func (s *Server) setupForm(w http.ResponseWriter, r *http.Request) {
	if users, _ := s.store.Users(); len(users) > 0 {
		redirect(w, r, "/login")
		return
	}
	s.render(w, "setup", http.StatusOK, view{})
}

func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	if msg := validateCredentials(username, password, r.FormValue("confirm")); msg != "" {
		s.auditFailed(r, "setup rejected", "reason", msg)
		s.render(w, "setup", http.StatusUnprocessableEntity, view{Error: msg, Form: username})
		return
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		s.fail(w, err)
		return
	}
	u, err := s.store.CreateUser(username, hash, true, true)
	if errors.Is(err, store.ErrUsernameTaken) {
		redirect(w, r, "/login")
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	setActor(r, u.Username)
	s.audit(r, "first admin created and signed in")
	s.sessions.SetCookie(w, r, u.ID, u.SessionVersion)
	redirect(w, r, "/welcome")
}

// dummyHash is compared against when a username doesn't exist, so a failed
// login takes the same time either way.
var dummyHash, _ = auth.HashPassword("dummy password")

func (s *Server) loginForm(w http.ResponseWriter, r *http.Request) {
	if users, _ := s.store.Users(); len(users) == 0 {
		redirect(w, r, "/setup")
		return
	}
	s.render(w, "login", http.StatusOK, view{})
}

// Failures allowed per 15 minutes before a lockout. A name is guarded more
// tightly than an address, because an address may be shared by many people
// behind one proxy, while a name is attacked one account at a time.
const (
	guessesPerUser = 5
	guessesPerIP   = 20
)

// admit counts an attempt against limits before any password is checked, so
// simultaneous attempts can't all get through before a failure is recorded.
// If one of them is locked out it sends the refusal, using render to show the
// page, and returns false. The attempt is refunded with s.throttle.Refund if
// it succeeds.
func (s *Server) admit(w http.ResponseWriter, r *http.Request, what string, render func(status int, msg string), limits ...auth.Limit) bool {
	wait, started := s.throttle.Admit(limits...)
	if wait <= 0 {
		return true
	}
	for _, key := range started {
		s.auditFailed(r, "locked out after repeated failures", "key", key, "for", wait.String())
	}
	s.auditFailed(r, what+" throttled", "retry_after", wait.Round(time.Second).String())
	w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
	render(http.StatusTooManyRequests, "Too many failed attempts. Try again in "+humanWait(wait)+".")
	return false
}

func humanWait(d time.Duration) string {
	switch mins := int((d + time.Minute - 1) / time.Minute); {
	case d < time.Minute:
		return "less than a minute"
	case mins == 1:
		return "1 minute"
	default:
		return strconv.Itoa(mins) + " minutes"
	}
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	username := strings.TrimSpace(r.FormValue("username"))
	userKey, ipKey := auth.Key("user", username), auth.Key("ip", clientIP(r))
	// Admit before spending time hashing, so a locked-out attacker costs little.
	if !s.admit(w, r, "login", func(status int, msg string) {
		s.render(w, "login", status, view{Error: msg, Form: username})
	}, auth.Limit{Key: userKey, Max: guessesPerUser}, auth.Limit{Key: ipKey, Max: guessesPerIP}) {
		return
	}
	u, err := s.store.UserByUsername(username)
	hash := dummyHash
	if err == nil {
		hash = u.PasswordHash
	}
	if !auth.CheckPassword(hash, r.FormValue("password")) || err != nil {
		// The reason is for the log only. The page doesn't say which part was wrong.
		reason := "wrong password"
		if err != nil {
			reason = "unknown user"
		}
		s.auditFailed(r, "login failed", "username", username, "reason", reason)
		s.render(w, "login", http.StatusUnauthorized, view{Error: "Wrong username or password.", Form: username})
		return
	}
	// A success clears the account's count. The address gets back only this
	// attempt, so signing in to an account of one's own between guesses
	// doesn't wipe the address's failures.
	s.throttle.Reset(userKey)
	s.throttle.Refund(ipKey)
	setActor(r, u.Username)
	s.audit(r, "login")
	s.sessions.SetCookie(w, r, u.ID, u.SessionVersion)
	redirect(w, r, "/")
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	// The cookie says who is signing out. A stale or missing one is logged as no one.
	if u, err := s.currentUser(r); err == nil {
		setActor(r, u.Username)
	}
	s.audit(r, "logout")
	auth.ClearCookie(w, r)
	redirect(w, r, "/login")
}

func validateCredentials(username, password, confirm string) string {
	switch {
	case username == "" || utf8.RuneCountInString(username) > 32:
		return "Username must be 1 to 32 characters."
	case utf8.RuneCountInString(password) < 8:
		return "Password must be at least 8 characters."
	case password != confirm:
		return "Passwords don't match."
	}
	return ""
}

// Account

func (s *Server) accountPage(w http.ResponseWriter, r *http.Request, u store.User) {
	s.render(w, "account", http.StatusOK, view{User: &u})
}

func (s *Server) resetForm(w http.ResponseWriter, r *http.Request, u store.User) {
	s.render(w, "reset", http.StatusOK, view{User: &u})
}

// resetAccount deletes the User's Homepage and Draft and sends them to the
// welcome screen.
func (s *Server) resetAccount(w http.ResponseWriter, r *http.Request, u store.User) {
	if err := s.store.ResetHomepage(u.ID); err != nil {
		s.fail(w, err)
		return
	}
	s.audit(r, "account reset", "target", u.Username)
	redirect(w, r, "/welcome")
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request, u store.User) {
	// Someone holding a stolen session could otherwise guess the current password.
	key := auth.Key("password", u.Username)
	if !s.admit(w, r, "password change", func(status int, msg string) {
		s.render(w, "account", status, view{User: &u, Error: msg})
	}, auth.Limit{Key: key, Max: guessesPerUser}) {
		return
	}
	if !auth.CheckPassword(u.PasswordHash, r.FormValue("current")) {
		s.auditFailed(r, "password change failed", "reason", "wrong current password")
		s.render(w, "account", http.StatusUnprocessableEntity, view{User: &u, Error: "Current password is wrong."})
		return
	}
	s.throttle.Reset(key) // the current password was right
	password := r.FormValue("password")
	if msg := validateCredentials(u.Username, password, r.FormValue("confirm")); msg != "" {
		s.auditFailed(r, "password change failed", "reason", msg)
		s.render(w, "account", http.StatusUnprocessableEntity, view{User: &u, Error: msg})
		return
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		s.fail(w, err)
		return
	}
	u, err = s.store.SetPassword(u.ID, hash)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.audit(r, "password changed", "target", u.Username)
	// Other devices are signed out; keep this one signed in.
	s.sessions.SetCookie(w, r, u.ID, u.SessionVersion)
	s.render(w, "account", http.StatusOK, view{User: &u, Message: "Password changed. Other devices have been signed out."})
}

// Admin

func (s *Server) usersPage(w http.ResponseWriter, r *http.Request, u store.User) {
	s.renderUsers(w, u, http.StatusOK, adminMessages[r.URL.Query().Get("done")], "")
}

var adminMessages = map[string]string{
	"created": "User created.",
	"reset":   "Password reset. That user has been signed out everywhere.",
	"deleted": "User deleted.",
}

func (s *Server) renderUsers(w http.ResponseWriter, u store.User, status int, msg, errMsg string) {
	users, err := s.store.Users()
	if err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, "users", status, view{User: &u, Users: users, Message: msg, Error: errMsg})
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request, u store.User) {
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	if msg := validateCredentials(username, password, password); msg != "" {
		s.auditFailed(r, "user create rejected", "target", username, "reason", msg)
		s.renderUsers(w, u, http.StatusUnprocessableEntity, "", msg)
		return
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		s.fail(w, err)
		return
	}
	if _, err := s.store.CreateUser(username, hash, false, false); errors.Is(err, store.ErrUsernameTaken) {
		s.auditFailed(r, "user create rejected", "target", username, "reason", "username taken")
		s.renderUsers(w, u, http.StatusUnprocessableEntity, "", "That username is already taken.")
		return
	} else if err != nil {
		s.fail(w, err)
		return
	}
	s.audit(r, "user created", "target", username)
	redirect(w, r, "/admin/users?done=created")
}

func (s *Server) resetPassword(w http.ResponseWriter, r *http.Request, u store.User) {
	password := r.FormValue("password")
	if msg := validateCredentials("x", password, password); msg != "" {
		s.auditFailed(r, "password reset rejected", "target_id", r.PathValue("id"), "reason", msg)
		s.renderUsers(w, u, http.StatusUnprocessableEntity, "", msg)
		return
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		s.fail(w, err)
		return
	}
	updated, err := s.store.SetPassword(r.PathValue("id"), hash)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	s.audit(r, "password reset", "target", updated.Username)
	if updated.ID == u.ID {
		s.sessions.SetCookie(w, r, updated.ID, updated.SessionVersion)
	}
	redirect(w, r, "/admin/users?done=reset")
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request, u store.User) {
	id := r.PathValue("id")
	if id == u.ID {
		s.auditFailed(r, "user delete rejected", "target", u.Username, "reason", "own account")
		s.renderUsers(w, u, http.StatusUnprocessableEntity, "", "You can't delete your own account.")
		return
	}
	target := id
	if t, err := s.store.UserByID(id); err == nil {
		target = t.Username
	}
	if err := s.store.DeleteUser(id); errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, err)
		return
	}
	s.audit(r, "user deleted", "target", target)
	redirect(w, r, "/admin/users?done=deleted")
}
