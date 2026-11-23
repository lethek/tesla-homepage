package icons

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
)

func TestBlockedAddresses(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1":            true,
		"::1":                  true,
		"10.1.2.3":             true,
		"172.16.0.1":           true,
		"172.31.255.255":       true,
		"192.168.1.1":          true,
		"169.254.169.254":      true, // cloud metadata
		"fe80::1":              true,
		"fd00::1":              true, // unique local
		"0.0.0.0":              true,
		"0.1.2.3":              true,
		"100.64.0.1":           true, // carrier-grade NAT, Tailscale
		"224.0.0.1":            true,
		"::":                   true,
		"::ffff:10.0.0.1":      true, // IPv4 inside IPv6
		"::ffff:127.0.0.1":     true,
		"64:ff9b::a00:1":       true, // NAT64 for 10.0.0.1
		"255.255.255.255":      true,
		"8.8.8.8":              false,
		"1.1.1.1":              false,
		"172.32.0.1":           false, // just outside 172.16.0.0/12
		"100.128.0.1":          false, // just outside 100.64.0.0/10
		"2606:4700:4700::1111": false,
		"::ffff:8.8.8.8":       false,
	} {
		if got := blocked(netip.MustParseAddr(addr), nil); got != want {
			t.Errorf("blocked(%s) = %v, want %v", addr, got, want)
		}
	}
	allow := []netip.Prefix{netip.MustParsePrefix("192.168.1.0/24")}
	if blocked(netip.MustParseAddr("192.168.1.5"), allow) {
		t.Error("allowed address blocked")
	}
	if !blocked(netip.MustParseAddr("192.168.2.5"), allow) {
		t.Error("address outside the allowed range let through")
	}
}

// counting returns a server that answers every request with a PNG and counts
// the requests it gets.
func counting(t *testing.T, addr string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	l, err := net.Listen("tcp", addr)
	if err != nil {
		t.Skipf("can't listen on %s: %v", addr, err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.Write(png)
	}))
	srv.Listener = l
	srv.Start()
	t.Cleanup(srv.Close)
	return srv, &n
}

func guarded(allow ...netip.Prefix) *Resolver {
	return New(func(data []byte, ext string) (string, error) { return "saved" + ext, nil }, allow)
}

func TestResolverRefusesLocalAndPrivateAddresses(t *testing.T) {
	srv, hits := counting(t, "127.0.0.1:0")
	r := guarded()

	// By address, by name that resolves to loopback, and from a site's own page.
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	for _, u := range []string{srv.URL + "/icon.png", "http://localhost:" + port + "/icon.png", "http://[::ffff:127.0.0.1]:" + port + "/icon.png"} {
		if _, err := r.FromURL(context.Background(), u); !errors.Is(err, ErrBlockedAddress) {
			t.Errorf("%s: got %v", u, err)
		}
	}
	if name := r.FromSite(context.Background(), srv.URL); name != "" {
		t.Errorf("site icon fetched from a local address: %q", name)
	}
	if _, _, ok := r.FetchSite(context.Background(), srv.URL); ok {
		t.Error("FetchSite reached a local address")
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("the local server received %d requests", n)
	}
}

// Whatever the address and port, the refusal is the same message, so the form
// can't be used to find out which internal hosts and ports are open.
func TestRefusalDoesNotRevealWhatIsListening(t *testing.T) {
	srv, _ := counting(t, "127.0.0.1:0")
	closed, _ := counting(t, "127.0.0.1:0")
	closedURL := closed.URL
	closed.Close()
	r := guarded()

	_, errOpen := r.FromURL(context.Background(), srv.URL)
	_, errClosed := r.FromURL(context.Background(), closedURL)
	_, errOther := r.FromURL(context.Background(), "http://10.255.255.1:81/x.png")
	for _, err := range []error{errOpen, errClosed, errOther} {
		if err == nil || !strings.HasSuffix(err.Error(), ErrBlockedAddress.Error()) {
			t.Fatalf("not the fixed refusal: %v", err)
		}
		if strings.Contains(err.Error(), "dial") || strings.Contains(err.Error(), "refused") {
			t.Errorf("error reveals the transport: %v", err)
		}
	}
}

func TestResolverAllowsListedNetworks(t *testing.T) {
	srv, hits := counting(t, "127.0.0.1:0")
	r := guarded(netip.MustParsePrefix("127.0.0.0/8"))
	if name, err := r.FromURL(context.Background(), srv.URL+"/icon.png"); err != nil || name != "saved.png" {
		t.Fatalf("listed network refused: %q %v", name, err)
	}
	if hits.Load() != 1 {
		t.Fatalf("%d requests", hits.Load())
	}
}

// A redirect is a new connection, so it is checked too. The first server is on
// the allowed list and sends the client to a second one that isn't.
func TestRedirectToAForbiddenAddressIsRefused(t *testing.T) {
	target, hits := counting(t, "127.0.0.2:0")
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/secret.png", http.StatusFound)
	}))
	t.Cleanup(front.Close)

	r := guarded(netip.MustParsePrefix("127.0.0.1/32"))
	_, err := r.FromURL(context.Background(), front.URL)
	if !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("redirect to a forbidden address: %v", err)
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("the redirect target received %d requests", n)
	}
	// With the target allowed as well, the same redirect is followed.
	r = guarded(netip.MustParsePrefix("127.0.0.1/32"), netip.MustParsePrefix("127.0.0.2/32"))
	if _, err := r.FromURL(context.Background(), front.URL); err != nil {
		t.Fatalf("redirect between allowed addresses: %v", err)
	}
}
