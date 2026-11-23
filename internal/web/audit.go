package web

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// Audit events are logged to the console as one line each: the event is the
// message, followed by who did it, from where, and what it touched. Passwords
// and other secrets are never logged.

type actorKey struct{}

// actor records who a request is from. The middleware creates it and the
// handlers fill it in once they know.
type actor struct{ name, ip string }

func actorOf(r *http.Request) *actor {
	a, _ := r.Context().Value(actorKey{}).(*actor)
	return a
}

// setActor names the user behind r in every log line about it.
func setActor(r *http.Request, name string) {
	if a := actorOf(r); a != nil {
		a.name = name
	}
}

// clientIP returns the address the request came from, as worked out once by
// requestLog.
func clientIP(r *http.Request) string {
	if a := actorOf(r); a != nil && a.ip != "" {
		return a.ip
	}
	return remoteHost(r)
}

func remoteHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// resolveIP finds the client's address. X-Forwarded-For is read only when the
// connection comes from one of s.TrustedProxies, because anyone else can put
// anything in it. Even then the entries are read from the right, skipping the
// proxies' own addresses: the left-hand entries are whatever the client sent.
func (s *Server) resolveIP(r *http.Request) string {
	peer := remoteHost(r)
	if !s.isTrustedProxy(peer) {
		return peer
	}
	var hops []string
	for _, h := range r.Header.Values("X-Forwarded-For") {
		for _, p := range strings.Split(h, ",") {
			if p = strings.TrimSpace(p); p != "" {
				hops = append(hops, p)
			}
		}
	}
	for i := len(hops) - 1; i >= 0; i-- {
		if _, err := netip.ParseAddr(hops[i]); err != nil {
			return peer // garbage from the proxy chain: don't guess
		}
		if !s.isTrustedProxy(hops[i]) {
			return hops[i]
		}
	}
	return peer
}

func (s *Server) isTrustedProxy(addr string) bool {
	ip, err := netip.ParseAddr(addr)
	if err != nil {
		return false
	}
	ip = ip.Unmap()
	for _, p := range s.TrustedProxies {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

func auditArgs(r *http.Request, attrs []any) []any {
	name := "-"
	if a := actorOf(r); a != nil && a.name != "" {
		name = a.name
	}
	return append([]any{"user", name, "ip", clientIP(r)}, attrs...)
}

// audit logs something that was done.
func (s *Server) audit(r *http.Request, event string, attrs ...any) {
	slog.Info(event, auditArgs(r, attrs)...)
}

// auditFailed logs something that was refused or didn't work out.
func (s *Server) auditFailed(r *http.Request, event string, attrs ...any) {
	slog.Warn(event, auditArgs(r, attrs)...)
}

// statusWriter remembers the response status.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// requestLog logs every request that ends in an error status, so refusals
// and failures show up even where a handler doesn't log them itself.
func (s *Server) requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a := &actor{ip: s.resolveIP(r)}
		r = r.WithContext(context.WithValue(r.Context(), actorKey{}, a))
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		if sw.status < 400 || strings.HasPrefix(r.URL.Path, "/static/") {
			return
		}
		level := slog.LevelWarn
		if sw.status >= 500 {
			level = slog.LevelError
		}
		slog.Log(r.Context(), level, "request failed", auditArgs(r, []any{"method", r.Method, "path", r.URL.Path, "status", sw.status})...)
	})
}
