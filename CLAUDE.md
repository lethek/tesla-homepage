# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

# Project rules

## Deploying

After every change to the code, deploy it to the Docker host without being asked:

1. `docker build -q -t tesla-homepage:dev .`
2. `docker rm -f tesla-homepage`
3. `docker run -d --name tesla-homepage -p 8090:8080 -v tesla-homepage-data:/data tesla-homepage:dev`
4. Check `docker logs tesla-homepage` shows `listening`.

Keep the `tesla-homepage-data` volume. Never remove it.

## Commands

CI (`.github/workflows/ci.yml`) runs these, so run them before finishing:

- `test -z "$(gofmt -l .)"` for formatting
- `go vet ./...`
- `go test -race ./...`

Run one test with `go test ./internal/web -run TestName`. Run the app locally with `go run ./cmd/homepage`, which keeps its data in `./data`. Configuration is by environment variables, documented in `README.md`: `ADDR`, `DATA_DIR`, `TRUSTED_PROXIES` and `ICON_ALLOWED_NETWORKS`.

## Architecture

A single Go module with one binary (`cmd/homepage`) and no database. Use the terms in `GLOSSARY.md` (Homepage, Section, Tile, Draft, Plate, Icon library) in code and UI text. `cmd/homepage/main.go` only wires the packages together and reads the environment.

- `internal/store`: all persistence, as JSON files under `DATA_DIR`. One process-wide mutex guards writes, and each write goes to a temp file and is renamed into place. There is no migration framework, so a change to a JSON shape must stay readable from old files.
- `internal/web`: the HTTP layer. Routes are all registered in `Server.Handler` in `server.go`, wrapped by `s.user` or `s.admin`. Pages are server-rendered `html/template` files from the embedded `templates/` directory, with htmx for fragment updates. `static/` is embedded too, so `app.js` and `app.css` ship in the binary.
- `internal/auth`: bcrypt passwords, HMAC-signed session cookies, and `Throttle` for guess lockouts. Sessions are stateless. A user's `SessionVersion` is bumped to sign out their other devices.
- `internal/icons`: turns an upload, image URL, `data:` URL or site address into a stored icon. `guard.go` blocks fetches to private and local networks, checking after DNS and on every redirect. `check.go` fully decodes images before they are saved.
- `internal/library`: the searchable Icon library. It merges downloaded catalogues (cached on first search), admin custom icons under `DATA_DIR`, and read-only seed icons embedded from `internal/library/seed/`. A custom icon with the same slug replaces a seed icon.
- `internal/httplog`: an `http.RoundTripper` that logs every outbound request. It is attached to the icon resolver and library clients in `main.go`.

Edit mode works on a Draft kept separately from the published Homepage. `Done` publishes it and `Cancel` discards it. The draft carries a revision (`store.Revision`) so a stale page can't cancel or overwrite changes made elsewhere (see `errStaleDraft` in `internal/web/edit.go`).

Handlers log state changes through `s.audit` (and refusals through `s.auditFailed`) as `key=value` lines on stdout. Passwords are never logged.

## Tests

`internal/web` tests run the real handlers through `httptest` with a temp-dir store. `newEnv` in `server_test.go` points every outbound request at a fake CDN (`fakeCDN`), so tests never touch the network. Extend `fakeCDN` when a test needs a new catalogue or icon response. Use `env.signedInAdmin()` or `env.login()` for a signed-in client, and `env.server` to move the throttle's clock.

## Changelog and releases

`CHANGELOG.md` is kept in lockstep with the code. Add an entry under `## [Unreleased]` with each user-visible change, and use the `changelog` skill. The `release` skill cuts releases. Pushes to `main` publish `latest` and `sha-*` images, and `v*` tags add semver tags.
