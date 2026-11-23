package web

import (
	"net/url"
	"strings"
	"testing"
)

// Escape in Edit mode asks before discarding only if the Draft has changes,
// so the editor says whether it does.
func TestEditorReportsUnsavedChanges(t *testing.T) {
	e := newEnv(t)
	c := e.signedInAdmin()

	_, body := e.get(c, "/edit")
	if !strings.Contains(body, `data-dirty="false"`) {
		t.Fatalf("fresh Draft reported as changed: %s", body)
	}
	_, body = e.hxPost(c, "/edit/sections", url.Values{"title": {"Music"}})
	if !strings.Contains(body, `data-dirty="true"`) {
		t.Fatalf("changed Draft not reported: %s", body)
	}
	// Resuming the Draft later still reports the changes.
	if _, body = e.get(c, "/edit"); !strings.Contains(body, `data-dirty="true"`) {
		t.Fatal("resumed Draft not reported as changed")
	}
	e.post(c, "/edit/done", nil)
	if _, body = e.get(c, "/edit"); !strings.Contains(body, `data-dirty="false"`) {
		t.Fatal("published Draft still reported as changed")
	}
}
