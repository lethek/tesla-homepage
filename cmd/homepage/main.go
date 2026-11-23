package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/lethek/tesla-homepage/internal/auth"
	"github.com/lethek/tesla-homepage/internal/httplog"
	"github.com/lethek/tesla-homepage/internal/icons"
	"github.com/lethek/tesla-homepage/internal/library"
	"github.com/lethek/tesla-homepage/internal/store"
	"github.com/lethek/tesla-homepage/internal/web"
)

func main() {
	// Everything is logged to the console as key=value lines, which the
	// standard log package also feeds into.
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))

	addr := env("ADDR", ":8080")
	dataDir := env("DATA_DIR", "./data")

	st, err := store.Open(dataDir)
	if err != nil {
		log.Fatalf("opening data dir %s: %v", dataDir, err)
	}
	secret, err := st.Secret()
	if err != nil {
		log.Fatalf("loading session secret: %v", err)
	}
	lib := library.New(st.LibraryCachePath(), store.WriteFile)
	lib.LocalPath, lib.LocalDir = st.LocalLibraryPath(), st.LocalLibraryDir()
	resolver := icons.New(st.SaveIcon)
	// Log every request to a remote resource.
	lib.Client.Transport = httplog.Transport(lib.Client.Transport)
	resolver.Client.Transport = httplog.Transport(resolver.Client.Transport)
	srv, err := web.New(st, &auth.Sessions{Secret: secret}, resolver, lib)
	if err != nil {
		log.Fatal(err)
	}

	if srv.TrustedProxies, err = parseProxies(os.Getenv("TRUSTED_PROXIES")); err != nil {
		log.Fatalf("TRUSTED_PROXIES: %v", err)
	}

	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		httpSrv.Shutdown(shutdown)
	}()

	slog.Info("listening", "addr", addr, "data", dataDir)
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	slog.Info("stopped")
}

// parseProxies reads a comma separated list of addresses and CIDR ranges.
func parseProxies(list string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, item := range strings.Split(list, ",") {
		if item = strings.TrimSpace(item); item == "" {
			continue
		}
		if p, err := netip.ParsePrefix(item); err == nil {
			out = append(out, p.Masked())
			continue
		}
		addr, err := netip.ParseAddr(item)
		if err != nil {
			return nil, fmt.Errorf("%q is not an address or CIDR range", item)
		}
		out = append(out, netip.PrefixFrom(addr.Unmap(), addr.Unmap().BitLen()))
	}
	return out, nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
