package httplog

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func capture(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	return &buf
}

func TestTransportLogsSuccessesAndFailures(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	client := &http.Client{Transport: Transport(nil)}
	buf := capture(t)

	get := func(path string) {
		if resp, err := client.Get(srv.URL + path); err == nil {
			resp.Body.Close()
		}
	}
	get("/ok?secret=hunter2")
	get("/missing")
	closed := httptest.NewServer(http.NotFoundHandler())
	addr := closed.URL
	closed.Close()
	if resp, err := client.Get(addr + "/gone"); err == nil {
		resp.Body.Close()
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3 log lines, got %d:\n%s", len(lines), buf)
	}
	if !strings.Contains(lines[0], "level=INFO") || !strings.Contains(lines[0], "msg=\"remote request\"") || !strings.Contains(lines[0], "status=200") {
		t.Errorf("success line: %s", lines[0])
	}
	if strings.Contains(buf.String(), "hunter2") {
		t.Error("query string was logged")
	}
	if !strings.Contains(lines[1], "level=WARN") || !strings.Contains(lines[1], "status=404") {
		t.Errorf("error status line: %s", lines[1])
	}
	if !strings.Contains(lines[2], "level=WARN") || !strings.Contains(lines[2], "err=") {
		t.Errorf("connection failure line: %s", lines[2])
	}
}
