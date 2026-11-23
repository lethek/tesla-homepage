package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestCreateUserFirstOnly(t *testing.T) {
	s := open(t)
	if _, err := s.CreateUser("alice", "h", true, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUser("bob", "h", true, true); !errors.Is(err, ErrUsernameTaken) {
		t.Fatalf("second first-run registration: got %v", err)
	}
	if _, err := s.CreateUser("ALICE", "h", false, false); !errors.Is(err, ErrUsernameTaken) {
		t.Fatalf("duplicate username: got %v", err)
	}
}

func TestSetPasswordBumpsSessionVersion(t *testing.T) {
	s := open(t)
	u, _ := s.CreateUser("alice", "h1", true, true)
	u2, err := s.SetPassword(u.ID, "h2")
	if err != nil {
		t.Fatal(err)
	}
	if u2.SessionVersion != u.SessionVersion+1 || u2.PasswordHash != "h2" {
		t.Fatalf("got %+v", u2)
	}
}

func TestDraftPublishAndDiscard(t *testing.T) {
	s := open(t)
	u, _ := s.CreateUser("alice", "h", true, true)
	if err := s.SaveHomepage(u.ID, EmptyHomepage()); err != nil {
		t.Fatal(err)
	}

	if _, resumed, err := s.BeginDraft(u.ID); err != nil || resumed {
		t.Fatalf("BeginDraft: resumed=%v err=%v", resumed, err)
	}
	if _, err := s.UpdateDraft(u.ID, func(h *Homepage) error { h.Title = "Changed"; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, resumed, _ := s.BeginDraft(u.ID); !resumed {
		t.Fatal("expected second BeginDraft to resume")
	}

	// Discard leaves the Homepage untouched.
	if err := s.DiscardDraft(u.ID); err != nil {
		t.Fatal(err)
	}
	hp, _, _ := s.Homepage(u.ID)
	if hp.Title != "Homepage" {
		t.Fatalf("discard changed Homepage: %q", hp.Title)
	}

	// Publish replaces it.
	s.BeginDraft(u.ID)
	s.UpdateDraft(u.ID, func(h *Homepage) error { h.Title = "Published"; return nil })
	if err := s.PublishDraft(u.ID); err != nil {
		t.Fatal(err)
	}
	hp, _, _ = s.Homepage(u.ID)
	if hp.Title != "Published" {
		t.Fatalf("publish: got %q", hp.Title)
	}
	if _, ok, _ := s.Draft(u.ID); ok {
		t.Fatal("Draft still exists after publish")
	}
}

func TestUpdateDraftErrorDoesNotSave(t *testing.T) {
	s := open(t)
	u, _ := s.CreateUser("alice", "h", true, true)
	s.BeginDraft(u.ID)
	boom := errors.New("boom")
	_, err := s.UpdateDraft(u.ID, func(h *Homepage) error { h.Title = "X"; return boom })
	if !errors.Is(err, boom) {
		t.Fatal(err)
	}
	d, _, _ := s.Draft(u.ID)
	if d.Title == "X" {
		t.Fatal("failed update was saved")
	}
}

func TestIconCleanup(t *testing.T) {
	old := iconGrace
	iconGrace = 0
	t.Cleanup(func() { iconGrace = old })

	s := open(t)
	u, _ := s.CreateUser("alice", "h", true, true)
	kept, _ := s.SaveIcon([]byte("kept"), ".png")
	dropped, _ := s.SaveIcon([]byte("dropped"), ".png")

	s.BeginDraft(u.ID)
	s.UpdateDraft(u.ID, func(h *Homepage) error {
		h.Sections = []Section{{ID: "s", Tiles: []Tile{{ID: "t", Icon: kept}}}}
		return nil
	})
	if err := s.PublishDraft(u.ID); err != nil {
		t.Fatal(err)
	}
	if !exists(t, s, kept) {
		t.Fatal("referenced icon deleted")
	}
	if exists(t, s, dropped) {
		t.Fatal("unreferenced icon kept")
	}

	if err := s.DeleteUser(u.ID); err != nil {
		t.Fatal(err)
	}
	if exists(t, s, kept) {
		t.Fatal("icon of deleted user kept")
	}
	if _, err := os.Stat(s.userDir(u.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("user dir not removed")
	}
}

func TestIconGraceKeepsNewIcons(t *testing.T) {
	s := open(t)
	u, _ := s.CreateUser("alice", "h", true, true)
	name, _ := s.SaveIcon([]byte("fresh"), ".png")
	s.DiscardDraft(u.ID)
	if !exists(t, s, name) {
		t.Fatal("icon inside grace period was deleted")
	}
}

func TestIconPathRejectsTraversal(t *testing.T) {
	s := open(t)
	for _, bad := range []string{"", "../users.json", "a/b.png", ".secret"} {
		if _, err := s.IconPath(bad); err == nil {
			t.Errorf("IconPath(%q) accepted", bad)
		}
	}
}

func TestSecretIsStable(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	a, _ := s.Secret()
	s2, _ := Open(dir)
	b, _ := s2.Secret()
	if len(a) != 32 || string(a) != string(b) {
		t.Fatal("secret not persisted")
	}
	if _, err := os.Stat(filepath.Join(dir, "secret")); err != nil {
		t.Fatal(err)
	}
}

func TestReorder(t *testing.T) {
	hp := Homepage{Sections: []Section{
		{ID: "a", Tiles: []Tile{{ID: "1"}, {ID: "2"}}},
		{ID: "b", Tiles: []Tile{{ID: "3"}}},
	}}
	ok := hp.Reorder([]SectionOrder{{ID: "b", Tiles: []string{"3", "1"}}, {ID: "a", Tiles: []string{"2"}}})
	if !ok {
		t.Fatal("valid reorder rejected")
	}
	if hp.Sections[0].ID != "b" || hp.Sections[0].Tiles[1].ID != "1" || len(hp.Sections[1].Tiles) != 1 {
		t.Fatalf("got %+v", hp.Sections)
	}

	for name, bad := range map[string][]SectionOrder{
		"missing tile":    {{ID: "b", Tiles: []string{"3"}}, {ID: "a", Tiles: []string{"2"}}},
		"duplicate tile":  {{ID: "b", Tiles: []string{"3", "1", "1"}}, {ID: "a", Tiles: []string{"2"}}},
		"unknown section": {{ID: "x", Tiles: []string{"3", "1"}}, {ID: "a", Tiles: []string{"2"}}},
		"missing section": {{ID: "b", Tiles: []string{"3", "1", "2"}}},
	} {
		before := hp.Sections[0].ID
		if hp.Reorder(bad) {
			t.Errorf("%s: accepted", name)
		}
		if hp.Sections[0].ID != before {
			t.Errorf("%s: modified homepage", name)
		}
	}
}

func exists(t *testing.T, s *Store, name string) bool {
	p, _ := s.IconPath(name)
	_, err := os.Stat(p)
	return err == nil
}
