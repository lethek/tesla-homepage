package web

import (
	"encoding/base64"
	"net/url"
	"strings"
	"testing"
)

func TestTileIconFromDataURL(t *testing.T) {
	e := newEnv(t)
	c := e.signedInAdmin()
	e.get(c, "/edit")
	e.hxPost(c, "/edit/sections", url.Values{"title": {"S"}})
	sid, _, _ := e.draftTile()

	png := pngHeader
	good := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte(png))
	e.hxPost(c, "/edit/sections/"+sid+"/tiles", url.Values{
		"title": {"A"}, "url": {"https://example.com"}, "source": {"url"}, "icon_url": {good},
	})
	_, ids, tiles := e.draftTile()
	if len(ids) != 1 || !strings.HasSuffix(tiles[ids[0]].Icon, ".png") {
		t.Fatalf("data URL not saved as an icon: %+v", tiles)
	}

	// The largest allowed image, which is about 2.8 MB once base64 encoded,
	// still fits through the form's request size limit.
	large := []byte(png + strings.Repeat("\x00", 2<<20-len(png)))
	e.hxPost(c, "/edit/sections/"+sid+"/tiles", url.Values{
		"title": {"L"}, "url": {"https://example.com"}, "source": {"url"},
		"icon_url": {"data:image/png;base64," + base64.StdEncoding.EncodeToString(large)},
	})
	if _, ids, _ := e.draftTile(); len(ids) != 2 {
		t.Fatalf("a 2 MB data URL was refused: %d tiles", len(ids))
	}

	// Something that isn't an image is refused, and the form never repeats the data.
	bad := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("<script>SECRETPAYLOAD</script>"))
	_, body := e.hxPost(c, "/edit/sections/"+sid+"/tiles", url.Values{
		"title": {"B"}, "url": {"https://example.com"}, "source": {"url"}, "icon_url": {bad},
	})
	if !strings.Contains(body, "use that icon: not a supported image") || strings.Contains(body, "SECRETPAYLOAD") {
		t.Fatalf("non-image data URL: %s", body)
	}
	if _, ids, _ := e.draftTile(); len(ids) != 2 {
		t.Fatalf("%d tiles, want 2", len(ids))
	}

	// Other schemes are still refused.
	_, body = e.hxPost(c, "/edit/sections/"+sid+"/tiles", url.Values{
		"title": {"C"}, "url": {"https://example.com"}, "source": {"url"}, "icon_url": {"javascript:alert(1)"},
	})
	if !strings.Contains(body, "must start with http://, https:// or data:image/") {
		t.Fatalf("javascript: URL: %s", body)
	}
}
