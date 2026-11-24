package icons

import (
	"bytes"
	"context"
	"image"
	imgpng "image/png"
	"net/http"
	"net/http/httptest"
	"testing"
)

func tinyPNG() []byte {
	var b bytes.Buffer
	imgpng.Encode(&b, image.NewRGBA(image.Rect(0, 0, 1, 1)))
	return b.Bytes()
}

// png is a real 1x1 PNG. Bytes after its end are ignored by decoders, so tests
// append markers to tell copies apart.
var png = tinyPNG()

func newResolver(t *testing.T) (*Resolver, map[string][]byte) {
	saved := map[string][]byte{}
	r := &Resolver{Client: http.DefaultClient, Save: func(data []byte, ext string) (string, error) {
		name := string(data[len(data)-1:]) + ext
		saved[name] = data
		return name, nil
	}}
	return r, saved
}

func TestFromUploadRejectsNonImages(t *testing.T) {
	r, _ := newResolver(t)
	if _, err := r.FromUpload([]byte("<html>hi</html>")); err == nil {
		t.Fatal("HTML accepted as icon")
	}
	if _, err := r.FromUpload([]byte(`<!doctype html><html><body><svg viewBox="0 0 1 1"></svg></body></html>`)); err == nil {
		t.Fatal("HTML page containing inline SVG accepted as icon")
	}
	if _, err := r.FromUpload(png); err != nil {
		t.Fatal(err)
	}
	if name, err := r.FromUpload([]byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"/>`)); err != nil || name[len(name)-4:] != ".svg" {
		t.Fatalf("svg: %q %v", name, err)
	}
}

func TestFromSitePrefersLargestTouchIcon(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`<html><head>
			<link rel="icon" href="/small.png">
			<link rel="apple-touch-icon" sizes="120x120" href="/t120.png">
			<link rel="apple-touch-icon" sizes="180x180" href="/t180.png">
		</head><body></body></html>`))
	})
	for p, mark := range map[string]byte{"/small.png": 's', "/t120.png": 'a', "/t180.png": 'b'} {
		mux.HandleFunc(p, func(w http.ResponseWriter, r *http.Request) {
			w.Write(append(append([]byte{}, png...), mark))
		})
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()

	r, _ := newResolver(t)
	if got := r.FromSite(context.Background(), srv.URL); got != "b.png" {
		t.Fatalf("got %q, want the 180px touch icon", got)
	}
}

func TestFromSiteFallsBackToFavicon(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.Write(append(append([]byte{}, png...), 'f'))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	r, _ := newResolver(t)
	if got := r.FromSite(context.Background(), srv.URL+"/some/page"); got != "f.png" {
		t.Fatalf("got %q", got)
	}
}

func TestFromSiteReturnsEmptyWhenNothingUsable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not an image"))
	}))
	defer srv.Close()

	r, saved := newResolver(t)
	if got := r.FromSite(context.Background(), srv.URL); got != "" || len(saved) != 0 {
		t.Fatalf("got %q, saved %d", got, len(saved))
	}
}

func TestFromURLRejectsOversize(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(png)
		w.Write(make([]byte, MaxSize))
	}))
	defer srv.Close()
	r, _ := newResolver(t)
	if _, err := r.FromURL(context.Background(), srv.URL); err == nil {
		t.Fatal("oversize icon accepted")
	}
}

func TestParseSize(t *testing.T) {
	for in, want := range map[string]int{"": 0, "180x180": 180, "any": 0, "32X32 16x16": 32} {
		if got := parseSize(in); got != want {
			t.Errorf("parseSize(%q) = %d, want %d", in, got, want)
		}
	}
}
