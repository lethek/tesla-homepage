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

## Icon library

Tiles can use icons from four catalogues, the same ones that [dashboardicons.com](https://dashboardicons.com) indexes:

| Source | Licence |
|---|---|
| [Dashboard Icons](https://dashboardicons.com) | Apache-2.0 |
| [Simple Icons](https://simpleicons.org) | CC0 |
| [selfh.st icons](https://selfh.st/icons) | [CC BY 4.0](https://creativecommons.org/licenses/by/4.0/), credited in the app |
| [LobeHub Icons](https://lobehub.com/icons) | MIT |

Brand names and logos belong to their owners. The catalogues are downloaded the first time anyone searches and refreshed only from the Icon library page in the admin menu. If any download fails, nothing is cached and the next search tries again. A picked icon is copied to the data volume unchanged.

Admins can also add custom icons (name, aliases, tags and an image) from the same page. They are stored apart from the downloaded catalogues, in `library-local.json` and `library-icons/` under `DATA_DIR`, and are searched together with them. Only the custom icons can be edited or deleted, and a refresh never touches them. Tiles that used a deleted custom icon keep their copy.

## Development

```sh
go run ./cmd/homepage   # data goes to ./data
go test ./...
```

CI runs `gofmt`, `go vet` and `go test` on every push and PR. Pushes to `main` publish `latest` and `sha-*` images for amd64 and arm64. `v*` tags also publish semver tags.
