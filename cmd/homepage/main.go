package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lethek/tesla-homepage/internal/auth"
	"github.com/lethek/tesla-homepage/internal/icons"
	"github.com/lethek/tesla-homepage/internal/library"
	"github.com/lethek/tesla-homepage/internal/store"
	"github.com/lethek/tesla-homepage/internal/web"
)

func main() {
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
	srv, err := web.New(st, &auth.Sessions{Secret: secret}, icons.New(st.SaveIcon), lib)
	if err != nil {
		log.Fatal(err)
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

	log.Printf("listening on %s, data in %s", addr, dataDir)
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
