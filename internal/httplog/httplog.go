// Package httplog logs the requests the server makes to remote resources.
package httplog

import (
	"log/slog"
	"net/http"
	"net/url"
	"time"
)

type transport struct{ next http.RoundTripper }

// Transport wraps next (http.DefaultTransport if nil) so that every outbound
// request is logged: at info level when it succeeds, and at warn level when it
// fails or the remote answers with an error status. Only the scheme, host and
// path are logged, never the query string or credentials.
func Transport(next http.RoundTripper) http.RoundTripper {
	if next == nil {
		next = http.DefaultTransport
	}
	return transport{next}
}

func (t transport) RoundTrip(r *http.Request) (*http.Response, error) {
	start := time.Now()
	resp, err := t.next.RoundTrip(r)
	attrs := []any{"method", r.Method, "url", redact(r.URL), "ms", time.Since(start).Milliseconds()}
	switch {
	case err != nil:
		slog.Warn("remote request failed", append(attrs, "err", err)...)
	case resp.StatusCode >= 400:
		slog.Warn("remote request failed", append(attrs, "status", resp.StatusCode)...)
	default:
		slog.Info("remote request", append(attrs, "status", resp.StatusCode)...)
	}
	return resp, err
}

func redact(u *url.URL) string {
	return (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path}).String()
}
