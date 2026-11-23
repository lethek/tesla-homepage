// Package icons obtains Tile icons from uploads, image URLs or the Tile's own
// site, and saves them to the icon store.
package icons

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// MaxSize is the largest icon accepted, in bytes.
const MaxSize = 2 << 20

var ErrNotImage = errors.New("not a supported image (PNG, JPEG, GIF, WebP, SVG or ICO)")

// Saver stores icon bytes and returns the stored name.
type Saver func(data []byte, ext string) (string, error)

type Resolver struct {
	Client *http.Client
	Save   Saver
}

// New returns a Resolver whose downloads refuse private and local addresses,
// except those inside allow. Anyone who can add a Tile can name a URL to
// fetch, so without the refusal they could reach, and read images from,
// services that only the server can see.
func New(save Saver, allow []netip.Prefix) *Resolver {
	return &Resolver{Client: NewClient(allow), Save: save}
}

// FromUpload validates and stores uploaded icon data.
func (r *Resolver) FromUpload(data []byte) (string, error) {
	ext, ok := imageExt(data)
	if !ok {
		return "", ErrNotImage
	}
	return r.Save(data, ext)
}

// FromURL downloads an image and stores it.
func (r *Resolver) FromURL(ctx context.Context, imageURL string) (string, error) {
	data, err := r.get(ctx, imageURL)
	if err != nil {
		return "", err
	}
	return r.FromUpload(data)
}

// FromSite finds an icon for a web site. It tries the page's declared
// apple-touch-icons (largest first), then /apple-touch-icon.png, then the
// declared icons, then /favicon.ico. It returns "" if none is usable.
func (r *Resolver) FromSite(ctx context.Context, siteURL string) string {
	for _, u := range r.siteCandidates(ctx, siteURL) {
		if name, err := r.FromURL(ctx, u); err == nil {
			return name
		}
	}
	return ""
}

// Fetch downloads an image and returns its bytes and file extension without
// storing it.
func (r *Resolver) Fetch(ctx context.Context, imageURL string) (data []byte, ext string, err error) {
	if data, err = r.get(ctx, imageURL); err != nil {
		return nil, "", err
	}
	ext, ok := imageExt(data)
	if !ok {
		return nil, "", ErrNotImage
	}
	return data, ext, nil
}

// FetchSite finds a site's icon the way FromSite does, but returns the image
// without storing it. ok is false if the site has no usable icon.
func (r *Resolver) FetchSite(ctx context.Context, siteURL string) (data []byte, ext string, ok bool) {
	for _, u := range r.siteCandidates(ctx, siteURL) {
		if data, ext, err := r.Fetch(ctx, u); err == nil {
			return data, ext, true
		}
	}
	return nil, "", false
}

// siteCandidates lists the URLs FromSite tries, in order.
func (r *Resolver) siteCandidates(ctx context.Context, siteURL string) []string {
	base, err := url.Parse(siteURL)
	if err != nil {
		return nil
	}
	var touch, other []candidate
	if page, err := r.get(ctx, siteURL); err == nil {
		touch, other = parseLinks(page)
	}
	var urls []string
	add := func(cs []candidate) {
		sort.SliceStable(cs, func(i, j int) bool { return cs[i].size > cs[j].size })
		for _, c := range cs {
			if u, err := base.Parse(c.href); err == nil {
				urls = append(urls, u.String())
			}
		}
	}
	add(touch)
	urls = append(urls, base.ResolveReference(&url.URL{Path: "/apple-touch-icon.png"}).String())
	add(other)
	urls = append(urls, base.ResolveReference(&url.URL{Path: "/favicon.ico"}).String())
	return urls
}

// get downloads rawURL. Its errors name the address without any credentials
// or query string, because they reach the console log and a URL can carry a
// token.
func (r *Resolver) get(ctx context.Context, rawURL string) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("invalid URL: it must start with http:// or https://")
	}
	shown := (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path}).String()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("%s: invalid URL", shown)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; homepage-icon-fetcher)")
	resp, err := r.Client.Do(req)
	if err != nil {
		// The client's error repeats the full URL, query string included.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		// One fixed message for every refused destination, so the form can't
		// be used to tell which internal hosts or ports exist.
		if errors.Is(err, ErrBlockedAddress) {
			err = ErrBlockedAddress
		}
		return nil, fmt.Errorf("%s: %w", shown, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", shown, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxSize+1))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", shown, err)
	}
	if len(data) > MaxSize {
		return nil, fmt.Errorf("%s: larger than %d bytes", shown, MaxSize)
	}
	return data, nil
}

type candidate struct {
	href string
	size int
}

func parseLinks(page []byte) (touch, other []candidate) {
	z := html.NewTokenizer(bytes.NewReader(page))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return
		case html.StartTagToken, html.SelfClosingTagToken:
			t := z.Token()
			if t.Data == "body" {
				return
			}
			if t.Data != "link" {
				continue
			}
			var rel, href, sizes string
			for _, a := range t.Attr {
				switch a.Key {
				case "rel":
					rel = strings.ToLower(a.Val)
				case "href":
					href = a.Val
				case "sizes":
					sizes = a.Val
				}
			}
			if href == "" {
				continue
			}
			c := candidate{href: href, size: parseSize(sizes)}
			switch {
			case strings.Contains(rel, "apple-touch-icon"):
				touch = append(touch, c)
			case strings.Contains(rel, "icon"):
				other = append(other, c)
			}
		}
	}
}

// parseSize returns the width from a sizes attribute such as "180x180".
func parseSize(s string) int {
	f := strings.Fields(s)
	if len(f) == 0 {
		return 0
	}
	w, _, _ := strings.Cut(strings.ToLower(f[0]), "x")
	n, _ := strconv.Atoi(w)
	return n
}

// Ext returns the file extension for a supported image type. ok is false for
// anything else.
func Ext(data []byte) (ext string, ok bool) { return imageExt(data) }

// imageExt sniffs data and returns the file extension for a supported image type.
func imageExt(data []byte) (string, bool) {
	ct := http.DetectContentType(data)
	switch ct {
	case "image/png":
		return ".png", true
	case "image/jpeg":
		return ".jpg", true
	case "image/gif":
		return ".gif", true
	case "image/webp":
		return ".webp", true
	case "image/x-icon", "image/vnd.microsoft.icon":
		return ".ico", true
	}
	// SVG sniffs as XML or plain text. HTML pages often contain inline <svg>
	// elements, so they must not count.
	if !strings.HasPrefix(ct, "text/xml") && !strings.HasPrefix(ct, "text/plain") {
		return "", false
	}
	head := bytes.ToLower(data[:min(len(data), 1024)])
	if bytes.Contains(head, []byte("<svg")) && !bytes.Contains(head, []byte("<html")) {
		return ".svg", true
	}
	return "", false
}
