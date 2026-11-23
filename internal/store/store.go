// Package store persists Users, Homepages, Drafts and icons as files under a
// data directory. See docs/adr/0001-json-file-per-user-storage.md.
package store

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var (
	ErrNotFound       = errors.New("not found")
	ErrUsernameTaken  = errors.New("username already taken")
	ErrInvalidIconRef = errors.New("invalid icon name")
)

type Store struct {
	dir string
	mu  sync.Mutex
}

// Open prepares dir for use, creating it if needed.
func Open(dir string) (*Store, error) {
	for _, d := range []string{dir, filepath.Join(dir, "users"), filepath.Join(dir, "icons")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return nil, err
		}
	}
	return &Store{dir: dir}, nil
}

// Secret returns the key used to sign session cookies, generating it on first use.
func (s *Store) Secret() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(s.dir, "secret")
	b, err := os.ReadFile(path)
	if err == nil {
		return b, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	b = make([]byte, 32)
	rand.Read(b)
	return b, writeFile(path, b)
}

// Users

func (s *Store) Users() ([]User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.users()
}

func (s *Store) users() ([]User, error) {
	var users []User
	err := readJSON(filepath.Join(s.dir, "users.json"), &users)
	if errors.Is(err, os.ErrNotExist) {
		return []User{}, nil
	}
	return users, err
}

func (s *Store) saveUsers(users []User) error {
	return writeJSON(filepath.Join(s.dir, "users.json"), users)
}

func (s *Store) UserByID(id string) (User, error) {
	users, err := s.Users()
	if err != nil {
		return User{}, err
	}
	for _, u := range users {
		if u.ID == id {
			return u, nil
		}
	}
	return User{}, ErrNotFound
}

func (s *Store) UserByUsername(name string) (User, error) {
	users, err := s.Users()
	if err != nil {
		return User{}, err
	}
	for _, u := range users {
		if strings.EqualFold(u.Username, name) {
			return u, nil
		}
	}
	return User{}, ErrNotFound
}

// CreateUser adds a User. When firstOnly is set, it fails with ErrUsernameTaken
// unless there are no Users yet, which makes first-run registration race-free.
func (s *Store) CreateUser(username, passwordHash string, admin, firstOnly bool) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	users, err := s.users()
	if err != nil {
		return User{}, err
	}
	if firstOnly && len(users) > 0 {
		return User{}, ErrUsernameTaken
	}
	for _, u := range users {
		if strings.EqualFold(u.Username, username) {
			return User{}, ErrUsernameTaken
		}
	}
	u := User{ID: NewID(), Username: username, PasswordHash: passwordHash, Admin: admin}
	if err := s.saveUsers(append(users, u)); err != nil {
		return User{}, err
	}
	return u, nil
}

// SetPassword replaces a User's password hash and increments their session
// version, which invalidates all of their existing sessions.
func (s *Store) SetPassword(id, passwordHash string) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	users, err := s.users()
	if err != nil {
		return User{}, err
	}
	for i := range users {
		if users[i].ID == id {
			users[i].PasswordHash = passwordHash
			users[i].SessionVersion++
			return users[i], s.saveUsers(users)
		}
	}
	return User{}, ErrNotFound
}

// DeleteUser removes a User with their Homepage and Draft, then deletes any
// icons nothing else references.
func (s *Store) DeleteUser(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	users, err := s.users()
	if err != nil {
		return err
	}
	kept := users[:0]
	for _, u := range users {
		if u.ID != id {
			kept = append(kept, u)
		}
	}
	if len(kept) == len(users) {
		return ErrNotFound
	}
	if err := s.saveUsers(kept); err != nil {
		return err
	}
	if err := os.RemoveAll(s.userDir(id)); err != nil {
		return err
	}
	return s.gcIcons()
}

// Homepages and Drafts

func (s *Store) userDir(id string) string { return filepath.Join(s.dir, "users", id) }

// Homepage returns the User's published Homepage. ok is false if they have
// not chosen one yet.
func (s *Store) Homepage(userID string) (hp Homepage, ok bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.read(userID, "homepage.json")
}

func (s *Store) SaveHomepage(userID string, hp Homepage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.write(userID, "homepage.json", hp)
}

// Draft returns the User's Draft. ok is false if there is none.
func (s *Store) Draft(userID string) (hp Homepage, ok bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.read(userID, "draft.json")
}

// BeginDraft returns the User's Draft, creating it from their Homepage if
// there is none. resumed reports whether an existing Draft was returned.
func (s *Store) BeginDraft(userID string) (hp Homepage, resumed bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	hp, ok, err := s.read(userID, "draft.json")
	if err != nil || ok {
		return hp, ok, err
	}
	hp, ok, err = s.read(userID, "homepage.json")
	if err != nil {
		return hp, false, err
	}
	if !ok {
		hp = EmptyHomepage()
	}
	return hp, false, s.write(userID, "draft.json", hp)
}

// UpdateDraft applies fn to the User's Draft and saves it. If fn returns an
// error, nothing is saved.
func (s *Store) UpdateDraft(userID string, fn func(*Homepage) error) (Homepage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	hp, ok, err := s.read(userID, "draft.json")
	if err != nil {
		return hp, err
	}
	if !ok {
		return hp, ErrNotFound
	}
	if err := fn(&hp); err != nil {
		return hp, err
	}
	return hp, s.write(userID, "draft.json", hp)
}

// PublishDraft makes the Draft the User's Homepage and removes the Draft.
func (s *Store) PublishDraft(userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	hp, ok, err := s.read(userID, "draft.json")
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	if err := s.write(userID, "homepage.json", hp); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(s.userDir(userID), "draft.json")); err != nil {
		return err
	}
	return s.gcIcons()
}

// DiscardDraft removes the User's Draft without publishing it.
func (s *Store) DiscardDraft(userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := os.Remove(filepath.Join(s.userDir(userID), "draft.json"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return s.gcIcons()
}

func (s *Store) read(userID, name string) (Homepage, bool, error) {
	var hp Homepage
	err := readJSON(filepath.Join(s.userDir(userID), name), &hp)
	if errors.Is(err, os.ErrNotExist) {
		return Homepage{}, false, nil
	}
	if hp.Sections == nil {
		hp.Sections = []Section{}
	}
	return hp, err == nil, err
}

func (s *Store) write(userID, name string, hp Homepage) error {
	if err := os.MkdirAll(s.userDir(userID), 0o700); err != nil {
		return err
	}
	return writeJSON(filepath.Join(s.userDir(userID), name), hp)
}

// Icons

// SaveIcon stores icon data under a content-addressed name and returns it.
func (s *Store) SaveIcon(data []byte, ext string) (string, error) {
	sum := sha256.Sum256(data)
	name := hex.EncodeToString(sum[:]) + ext
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(s.dir, "icons", name)
	if _, err := os.Stat(path); err == nil {
		return name, nil
	}
	return name, writeFile(path, data)
}

// IconPath returns the file path of a stored icon.
func (s *Store) IconPath(name string) (string, error) {
	if name == "" || name != filepath.Base(name) || strings.HasPrefix(name, ".") {
		return "", ErrInvalidIconRef
	}
	return filepath.Join(s.dir, "icons", name), nil
}

// gcIcons deletes icons that no Homepage or Draft references. Icons written
// in the last few minutes are kept, because a Tile form may have saved one
// that is not referenced yet. Callers must hold s.mu.
func (s *Store) gcIcons() error {
	used := map[string]bool{}
	users, err := s.users()
	if err != nil {
		return err
	}
	for _, u := range users {
		for _, f := range []string{"homepage.json", "draft.json"} {
			hp, _, err := s.read(u.ID, f)
			if err != nil {
				return err
			}
			hp.icons(used)
		}
	}
	entries, err := os.ReadDir(filepath.Join(s.dir, "icons"))
	if err != nil {
		return err
	}
	for _, e := range entries {
		if used[e.Name()] {
			continue
		}
		if info, err := e.Info(); err == nil && recent(info.ModTime()) {
			continue
		}
		if err := os.Remove(filepath.Join(s.dir, "icons", e.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// File helpers

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(path, b)
}

// writeFile writes data to a temp file and renames it into place so readers
// never see a partial file.
func writeFile(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
