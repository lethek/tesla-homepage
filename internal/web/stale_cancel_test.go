package web

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

var revPattern = regexp.MustCompile(`data-rev="([0-9a-f]+)"`)

// draftRev reads the Draft revision an editor page was rendered with.
func draftRev(t *testing.T, page string) string {
	t.Helper()
	m := revPattern.FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("no data-rev in: %s", page)
	}
	return m[1]
}

// Two tabs open on the same clean Draft. One makes a change. Cancelling in the
// other tab, which still believes the Draft is clean, must not throw that
// change away.
func TestCancelFromAStaleTabKeepsTheChangedDraft(t *testing.T) {
	e := newEnv(t)
	c := e.signedInAdmin()
	logs := captureLogs(t)

	_, tabA := e.get(c, "/edit")
	_, tabB := e.get(c, "/edit")
	if !strings.Contains(tabB, `data-dirty="false"`) {
		t.Fatal("expected a clean draft")
	}
	e.hxPost(c, "/edit/sections", url.Values{"title": {"Music"}})

	// Tab B was clean when it loaded, so it sends no confirmation of the change.
	resp, _ := e.post(c, "/edit/cancel", url.Values{"confirm_rev": {draftRev(t, tabB)}})
	wantRedirect(t, resp, "/edit?stale=1")
	wantLogged(t, logs, "level=WARN", `msg="edit cancel refused"`)
	_, page := e.get(c, "/edit?stale=1")
	if !strings.Contains(page, "Music") || !strings.Contains(page, "changed in another tab") {
		t.Fatalf("draft lost or no notice: %s", page)
	}

	// Having seen the current state, cancelling works. The revision of the first
	// tab's page, from before the change, would still be refused.
	if resp, _ := e.post(c, "/edit/cancel", url.Values{"confirm_rev": {draftRev(t, tabA)}}); resp.Header.Get("Location") != "/edit?stale=1" {
		t.Fatalf("old revision accepted: %v", resp.Header.Get("Location"))
	}
	resp, _ = e.post(c, "/edit/cancel", url.Values{"confirm_rev": {draftRev(t, page)}})
	wantRedirect(t, resp, "/")
	if _, body := e.get(c, "/edit"); strings.Contains(body, "Music") {
		t.Fatal("draft survived a confirmed cancel")
	}
}

func TestCancelOfACleanDraftNeedsNoConfirmation(t *testing.T) {
	e := newEnv(t)
	c := e.signedInAdmin()
	e.get(c, "/edit")
	// No revision at all, as from a script or an old page.
	resp, _ := e.post(c, "/edit/cancel", nil)
	wantRedirect(t, resp, "/")
}

// Dragging saves without re-rendering the page, so the response hands over the
// new revision. Without it the tab's own Cancel would be refused.
func TestReorderReturnsTheNewRevision(t *testing.T) {
	e := newEnv(t)
	c := e.signedInAdmin()
	e.get(c, "/edit")
	e.hxPost(c, "/edit/sections", url.Values{"title": {"A"}})
	e.hxPost(c, "/edit/sections", url.Values{"title": {"B"}})
	_, page := e.get(c, "/edit")
	ids := regexp.MustCompile(`data-id="([0-9a-f]+)"`).FindAllStringSubmatch(page, -1)
	if len(ids) < 2 {
		t.Fatalf("want two sections: %s", page)
	}
	order := `[{"id":"` + ids[1][1] + `","tiles":[]},{"id":"` + ids[0][1] + `","tiles":[]}]`
	resp, _ := e.do(c, http.MethodPost, "/edit/order", strings.NewReader(order), "application/json", false)
	rev := resp.Header.Get("X-Draft-Rev")
	if resp.StatusCode != http.StatusNoContent || rev == "" || rev == draftRev(t, page) {
		t.Fatalf("reorder: %d rev=%q old=%q", resp.StatusCode, rev, draftRev(t, page))
	}
	resp, _ = e.post(c, "/edit/cancel", url.Values{"confirm_rev": {rev}})
	wantRedirect(t, resp, "/")
}
