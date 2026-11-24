package icons

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func dataURL(mediaType string, data []byte) string {
	return "data:" + mediaType + ";base64," + base64.StdEncoding.EncodeToString(data)
}

func TestFromURLDecodesDataURLs(t *testing.T) {
	r, saved := newResolver(t)
	name, err := r.FromURL(context.Background(), dataURL("image/png", png))
	if err != nil || !strings.HasSuffix(name, ".png") || len(saved) != 1 {
		t.Fatalf("got %q, %v, saved %d", name, err, len(saved))
	}
	// The scheme is case insensitive, and Fetch accepts the same URLs.
	if _, ext, err := r.Fetch(context.Background(), "DATA:IMAGE/PNG;BASE64,"+base64.StdEncoding.EncodeToString(png)); err != nil || ext != ".png" {
		t.Fatalf("Fetch: %q %v", ext, err)
	}
}

// The declared media type is never trusted: what is stored depends on what
// the bytes are.
func TestDataURLContentIsSniffed(t *testing.T) {
	r, saved := newResolver(t)
	for name, raw := range map[string]string{
		"html as png":   dataURL("image/png", []byte("<html><script>alert(1)</script></html>")),
		"script as png": dataURL("image/png", []byte("alert(1)")),
		"png as html":   dataURL("text/html", png),
	} {
		if _, err := r.FromURL(context.Background(), raw); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if len(saved) != 0 {
		t.Fatalf("%d files saved", len(saved))
	}
	// A declared SVG is stored as whatever the bytes sniff as.
	name, err := r.FromURL(context.Background(), dataURL("image/svg+xml", png))
	if err != nil || !strings.HasSuffix(name, ".png") {
		t.Fatalf("got %q, %v", name, err)
	}
}

func TestDataURLLimitsAndErrors(t *testing.T) {
	r, saved := newResolver(t)
	secret := "SECRETPAYLOAD"
	big := dataURL("image/png", append(append([]byte{}, png...), make([]byte, MaxSize)...))
	for name, raw := range map[string]string{
		"too big":         big,
		"not base64":      "data:image/png;base64,!!!" + secret,
		"not encoded":     "data:image/png," + secret,
		"not an image":    "data:text/plain;base64," + base64.StdEncoding.EncodeToString([]byte(secret)),
		"no comma":        "data:image/png;base64" + secret,
		"empty":           "data:",
		"no media type":   "data:;base64," + base64.StdEncoding.EncodeToString(png),
		"extra parameter": "data:image/png;charset=x;base64," + base64.StdEncoding.EncodeToString(png),
	} {
		_, err := r.FromURL(context.Background(), raw)
		if name == "extra parameter" {
			// Parameters before ;base64 are harmless, since the bytes are sniffed.
			if err != nil {
				t.Errorf("%s: %v", name, err)
			}
			continue
		}
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		// Errors reach the console log and the form, so they never repeat the URL.
		if strings.Contains(err.Error(), secret) || len(err.Error()) > 200 {
			t.Errorf("%s: error repeats the data: %v", name, err)
		}
	}
	if len(saved) != 1 {
		t.Fatalf("%d files saved, want only the one with a harmless parameter", len(saved))
	}
}

// A page can't make the fetcher store an inline image by naming one as its icon.
func TestSiteIconCannotBeADataURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Write([]byte(`<link rel="apple-touch-icon" sizes="512x512" href="` + dataURL("image/png", png) + `">`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	r, saved := newResolver(t)
	if got := r.FromSite(context.Background(), srv.URL); got != "" || len(saved) != 0 {
		t.Fatalf("got %q, saved %d", got, len(saved))
	}
	if _, _, ok := r.FetchSite(context.Background(), srv.URL); ok {
		t.Fatal("FetchSite used a data: icon")
	}
	for _, u := range r.siteCandidates(context.Background(), srv.URL) {
		if !strings.HasPrefix(u, "http") {
			t.Fatalf("candidate %q", u)
		}
	}
}
