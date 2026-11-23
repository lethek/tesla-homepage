package icons

import (
	"errors"
	"net"
	"net/http"
	"net/netip"
	"syscall"
	"time"
)

// ErrBlockedAddress is returned when an icon would have to be fetched from a
// private or local network.
var ErrBlockedAddress = errors.New("that address is on a private or local network, which icons can't be fetched from")

// extraBlocked are ranges that net/netip doesn't class as private or local
// but that reach internal machines: "this network", carrier-grade NAT (also
// used by Tailscale), and the NAT64 prefix, which embeds an IPv4 address.
var extraBlocked = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("64:ff9b::/96"),
}

// blocked reports whether ip is somewhere icons must not be fetched from:
// loopback, private, link-local (which includes cloud metadata services),
// multicast, unspecified, and the ranges in extraBlocked. Addresses inside
// allow are let through.
func blocked(ip netip.Addr, allow []netip.Prefix) bool {
	ip = ip.Unmap()
	for _, p := range allow {
		if p.Contains(ip) {
			return false
		}
	}
	if !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return true
	}
	for _, p := range extraBlocked {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// control is a net.Dialer Control function. It runs on the address that is
// about to be connected to, after any DNS lookup, for every connection. That
// covers redirects and DNS names that resolve to an internal address, which
// checking the URL beforehand would not.
func control(allow []netip.Prefix) func(network, address string, c syscall.RawConn) error {
	return func(network, address string, _ syscall.RawConn) error {
		ap, err := netip.ParseAddrPort(address)
		if err != nil || blocked(ap.Addr(), allow) {
			return ErrBlockedAddress
		}
		return nil
	}
}

// NewClient returns an HTTP client that never connects to a private or local
// address, unless it is inside allow. It doesn't use proxy environment
// variables, because a proxy would make the connection for it.
func NewClient(allow []netip.Prefix) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, Control: control(allow)}
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext:         dialer.DialContext,
			TLSHandshakeTimeout: 10 * time.Second,
			MaxIdleConns:        10,
			IdleConnTimeout:     30 * time.Second,
		},
	}
}
