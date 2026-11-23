# tesla-homepage

A touch-friendly launcher homepage for the Tesla in-car browser. It also works on phones and desktops. Each user signs in and edits their own sections of icon tiles. See `GLOSSARY.md` for the terms used.

## Running

```sh
docker run -d -p 8080:8080 -v homepage-data:/data ghcr.io/lethek/tesla-homepage:latest
```

Open the app in a browser. The first visitor creates the admin account, and the admin adds other users from the menu.

| Variable   | Default             | Meaning                                         |
|------------|---------------------|-------------------------------------------------|
| `ADDR`     | `:8080`             | Listen address                                  |
| `DATA_DIR` | `/data` (container) | Users, homepages, icons and the session secret |

Put it behind HTTPS in production. Session cookies get the `Secure` flag when the request arrives over TLS or with `X-Forwarded-Proto: https`.

## Development

```sh
go run ./cmd/homepage   # data goes to ./data
go test ./...
```

CI runs `gofmt`, `go vet` and `go test` on every push and PR. Pushes to `main` publish `latest` and `sha-*` images for amd64 and arm64. `v*` tags also publish semver tags.
