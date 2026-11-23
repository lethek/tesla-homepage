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
	resp, rev := e.reorder(c, draftRev(t, page), order)
	if resp.StatusCode != http.StatusNoContent || rev == "" || rev == draftRev(t, page) {
		t.Fatalf("reorder: %d rev=%q old=%q", resp.StatusCode, rev, draftRev(t, page))
	}
	resp, _ = e.post(c, "/edit/cancel", url.Values{"confirm_rev": {rev}})
	wantRedirect(t, resp, "/")
}

// reorder posts a drag-and-drop order the way the editor does, naming the
// Draft revision its page was showing.
func (e *env) reorder(c *http.Client, rev, order string) (*http.Response, string) {
	e.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+"/edit/order", strings.NewReader(order))
	req.Header.Set("Content-Type", "application/json")
	if rev != "" {
		req.Header.Set("X-Draft-Rev", rev)
	}
	resp, err := c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp, resp.Header.Get("X-Draft-Rev")
}

// Tab B loaded the Draft. Tab A then changes a tile without changing its ID.
// A reorder from tab B would apply to A's newer Draft and hand B its revision,
// after which B's Cancel could discard A's edit unseen. It must be refused.
func TestReorderFromAStaleTabIsRefused(t *testing.T) {
	e := newEnv(t)
	c := e.signedInAdmin()
	logs := captureLogs(t)
	e.get(c, "/edit")
	e.hxPost(c, "/edit/sections", url.Values{"title": {"Lab"}})
	sid, _, _ := e.draftTile()
	for _, title := range []string{"One", "Two"} {
		e.hxPost(c, "/edit/sections/"+sid+"/tiles", url.Values{"title": {title}, "url": {"https://example.com"}, "source": {"site"}})
	}
	_, tabB := e.get(c, "/edit")
	_, ids, _ := e.draftTile()
	order := `[{"id":"` + sid + `","tiles":["` + ids[1] + `","` + ids[0] + `"]}]`

	// Tab A renames a tile. The IDs, and so the order's validity, are unchanged.
	e.hxPost(c, "/edit/tiles/"+ids[0], url.Values{"title": {"Renamed in A"}, "url": {"https://example.com"}, "source": {"site"}})

	resp, rev := e.reorder(c, draftRev(t, tabB), order)
	if resp.StatusCode != http.StatusConflict || rev != "" {
		t.Fatalf("stale reorder: %d, rev=%q", resp.StatusCode, rev)
	}
	wantLogged(t, logs, "level=WARN", `msg="reorder refused"`)
	_, page := e.get(c, "/edit")
	if !strings.Contains(page, "Renamed in A") || strings.Index(page, "Renamed in A") > strings.Index(page, ">Two<") {
		t.Fatalf("the stale reorder was applied or the edit lost: %s", page)
	}
	// And tab B still can't cancel without seeing the change.
	resp, _ = e.post(c, "/edit/cancel", url.Values{"confirm_rev": {draftRev(t, tabB)}})
	wantRedirect(t, resp, "/edit?stale=1")

	// With the current revision the reorder goes through.
	if resp, _ := e.reorder(c, draftRev(t, page), order); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("current reorder: %d", resp.StatusCode)
	}
	// A request that names no revision is refused too.
	if resp, _ := e.reorder(c, "", order); resp.StatusCode != http.StatusConflict {
		t.Fatalf("reorder without a revision: %d", resp.StatusCode)
	}
}
