# tesla-homepage

A touch-friendly launcher homepage for the Tesla in-car browser. It also works on phones and desktops. Each user signs in and edits their own sections of icon tiles. See [`GLOSSARY.md`](GLOSSARY.md) for the terms used.

It is a single Go binary with no database. All data lives as files in one directory, so it runs happily in a small container on a home server.

## Contents

- [Features](#features)
- [Installing](#installing)
- [Using it](#using-it)
- [Configuration](#configuration)
- [Data and backups](#data-and-backups)
- [Icon library](#icon-library)
- [Your account](#your-account)
- [Security](#security)
- [Logging](#logging)
- [Building and development](#building-and-development)

## Features

- Large icon tiles in titled sections, laid out to suit anything from a phone to the car screen.
- Several users at one address, each with their own homepage. The first user is the Admin.
- Edit mode with a draft: add, change, delete and drag tiles and sections, then publish with Done or discard with Cancel.
- Tile icons from a searchable library of well-known brands, an upload, an image URL, or the site's own icon.
- A background plate colour and padding for icons with transparent backgrounds.
- An Open fullscreen option for tiles, which the Tesla browser shows fullscreen.
- Export and import of a homepage, and of the Admin's custom icons.
- Sign-in lockouts against password guessing, and a log line for every change.

## Installing

### Docker

```sh
docker run -d --name tesla-homepage -p 8080:8080 -v homepage-data:/data ghcr.io/lethek/tesla-homepage:latest
```

Images are published for `amd64` and `arm64`. Pushes to `main` publish `latest` and `sha-*` tags, and release tags (`v*`) also publish `0.3.0`, `0.3` and `0` style tags. Pin one of those if you don't want upgrades to arrive unannounced.

With Docker Compose:

```yaml
services:
  homepage:
    image: ghcr.io/lethek/tesla-homepage:latest
    ports:
      - "8080:8080"
    volumes:
      - homepage-data:/data
    restart: unless-stopped

volumes:
  homepage-data:
```

The container runs as the non-root user `nonroot` (UID 65532). If you use a bind mount instead of a named volume, make the directory writable by that user.

### From source

You need [Go](https://go.dev/dl/) 1.27.4 or newer, as set in `go.mod`.

```sh
go build -o homepage ./cmd/homepage
DATA_DIR=/var/lib/homepage ADDR=:8080 ./homepage
```

The binary has everything it needs built in, including its templates and scripts. It needs no other files at run time.

### Putting it on the internet

Put it behind HTTPS in production. Session cookies get the `Secure` flag when the request arrives over TLS or with `X-Forwarded-Proto: https`, so a reverse proxy that terminates TLS should send that header. Also set `TRUSTED_PROXIES` (see [Configuration](#configuration)), or every user behind the proxy shares one address for lockout purposes.

## Using it

### First run

Open the app in a browser. The first visitor creates the Admin account. After that, the Admin adds other users from **Manage users** in the menu.

Each user is then asked how to start their homepage: from the starter set, or empty. The starter set is a ready-made list of video, audio, everyday and AI sites, and the server fetches their icons. You can change everything afterwards.

### Edit mode

Choose **Edit mode** from the menu. Changes go to a draft, and the published homepage is untouched until you press **Done**. **Cancel** throws the draft away, after asking if it has changes. Escape also cancels. If you leave halfway, the draft is waiting the next time you enter Edit mode.

In Edit mode you can:

- Rename the homepage and each section, or add and delete sections.
- Add a tile to a section, or use the pencil and bin buttons on a tile to change or delete it.
- Drag tiles and sections to reorder them, including moving a tile to another section.
- Choose whether rows of tiles wider than the screen wrap onto the next line or scroll sideways.

### Tiles

A tile has a title, a URL and an icon. The icon comes from one of four sources:

| Source | What it does |
|---|---|
| Library | Search the [Icon library](#icon-library) and pick a file. |
| Upload | Choose or drag in an image. |
| Image URL | The server downloads the image, or decodes a base64 `data:image/...` URL. |
| From site | The server fetches the site's own icon. |

Whichever you choose, the server stores its own copy of the image. If a tile has no icon it shows a letter placeholder.

Each tile can also have a **plate**, a background colour with 0 to 30% padding, which suits logos with transparent backgrounds. **Open fullscreen** makes the tile link through YouTube's redirect page, which the Tesla browser shows fullscreen.

## Configuration

Everything is set with environment variables.

| Variable   | Default             | Meaning                                         |
|------------|---------------------|-------------------------------------------------|
| `ADDR`     | `:8080`             | Listen address                                  |
| `DATA_DIR` | `/data` (container), `./data` (from source) | Users, homepages, icons and the session secret |
| `TRUSTED_PROXIES` | none | Reverse proxies whose `X-Forwarded-For` is believed, as addresses or CIDR ranges |
| `ICON_ALLOWED_NETWORKS` | none | Private or local networks that icons may be fetched from, as addresses or CIDR ranges |

Both list variables take a comma separated list, for example `10.0.0.0/8,172.17.0.1`. The server refuses to start if either is malformed.

## Data and backups

Everything is stored under `DATA_DIR`:

| Path | Holds |
|---|---|
| `users.json` | Users and their password hashes |
| `users/<id>/homepage.json`, `draft.json` | A user's published homepage and draft |
| `icons/` | Stored tile icons |
| `library.json` | The downloaded Icon library catalogues |
| `library-local.json`, `library-icons/` | The Admin's custom icons |
| `secret` | The key that signs session cookies |

The files are plain JSON and images, and every write is made by renaming a finished temporary file into place. To back up, copy the directory. If you lose `secret`, a new one is made and everyone is signed out. One server process must own the directory, so don't run several copies against the same volume.

## Icon library

Tiles can use icons from four catalogues, the same ones that [dashboardicons.com](https://dashboardicons.com) indexes:

| Source | Licence |
|---|---|
| [Dashboard Icons](https://dashboardicons.com) | Apache-2.0 |
| [Simple Icons](https://simpleicons.org) | CC0 |
| [selfh.st icons](https://selfh.st/icons) | [CC BY 4.0](https://creativecommons.org/licenses/by/4.0/), credited in the app |
| [LobeHub Icons](https://lobehub.com/icons) | MIT |

Brand names and logos belong to their owners. The catalogues are downloaded the first time anyone searches and refreshed only from the Icon library page in the admin menu. If any download fails, nothing is cached and the next search tries again. A picked icon is copied to the data volume unchanged.

Admins can also add custom icons (name, aliases, tags and an image) from the same page. They are stored apart from the downloaded catalogues, in `library-local.json` and `library-icons/` under `DATA_DIR`, and are searched together with them. Only the custom icons can be edited or deleted, and a refresh never touches them. Tiles that used a deleted custom icon keep their copy. Under **Export or import** on that page, the Admin can save every custom icon to one file and load it into another install.

A few custom icons are also built into the image, from `internal/library/seed/`: ABC iView, SBS On Demand, 10, 7plus and 9Now. They are searched like any other custom icon but are read-only and don't appear on the Icon library page. A custom icon you add with the same name replaces the built-in one.

## Your account

The **Manage account** page, in the menu, lets each user:

- Change their password.
- Sign out their other devices.
- Export their homepage to a file. It holds the sections, tiles and icon images, but only the saved homepage, not unsaved Edit mode changes.
- Import such a file into any account. This replaces that account's homepage and draft.
- Reset their homepage back to the welcome screen.
- Delete their account, after entering their password. The Admin can't do this.

The Admin can also reset other users' passwords and delete users from **Manage users**.

## Security

### Fetching icons

Anyone who can edit a tile can make the server download an image from an address they choose (the Image URL and From site options). To stop that being used to reach services only the server can see, the server refuses to fetch from loopback, private (`10/8`, `172.16/12`, `192.168/16`, `fc00::/7`), link-local (including `169.254.169.254` cloud metadata), carrier-grade NAT (`100.64/10`, used by Tailscale), multicast and unspecified addresses. The check runs on the address actually connected to, after DNS, and again on every redirect, so a name that resolves to an internal address is refused too. Every refusal gives the same message, so it can't be used to find out which internal hosts or ports exist. Proxy environment variables (`HTTP_PROXY`) are not used for icon downloads.

If your tiles point at internal services and you want their icons fetched with From site, list those networks in `ICON_ALLOWED_NETWORKS`, for example `192.168.1.0/24,10.0.0.5`. Everything listed becomes reachable by every user who can edit a tile, so keep the list narrow.

The Image URL field also takes a base64 `data:image/...;base64,` URL. It is decoded in the server without any download, so the checks above don't apply, and it is held to the same limits as an upload: at most 2 MB once decoded, and the bytes must be a PNG, JPEG, GIF, WebP, SVG or ICO whatever the URL claims. Other `data:` forms, such as percent-encoded SVG, are refused.

Every image, whether uploaded, downloaded, decoded from a `data:` URL or imported, is decoded in full before it is stored, so truncated or damaged files are refused with a message instead of being saved as broken icons. Images over 16 megapixels are refused, and GIFs are checked on their first frame. Animated WebPs and ICOs are checked for structure, since there is no decoder for them, and SVGs must be well-formed XML with an `svg` root element. A page can't name a `data:` URL as its own icon for From site.

### Guessing protection

Passwords are stored as salted bcrypt hashes, so precomputed (rainbow table) attacks don't apply. Guessing is slowed by lockouts, which are logged as `WARN` lines:

- A username gets 5 sign-in attempts in 15 minutes, and an address gets 20. Each attempt is counted before the password is checked, so a burst of simultaneous guesses can't get more than that far, and a successful sign-in gives its attempt back. The next attempt after the allowance is used up starts a lockout. A locked key gets `429 Too many failed attempts`, even with the right password, until its lockout ends.
- A lockout lasts 1 minute, then doubles each time the same key is locked again, up to 1 hour. Nothing is locked for good, and a successful sign-in clears the username's count.
- Wrong guesses of the current password on the Manage account page, including when deleting an account, are limited the same way.
- Names that don't exist are counted too, so the lockout doesn't reveal which names are real.

Every spelling of a username that sign-in accepts (any capitalisation, and look-alike letters such as `ſ` for `s`) shares one count. Counts are kept in memory and reset when the server restarts. A person can be locked out for a while by someone guessing their name.

The client's address is the connection's own address. If the server runs behind a reverse proxy, set `TRUSTED_PROXIES` to the proxy's address or CIDR range. Only then is `X-Forwarded-For` read, from the right, skipping the proxies' own addresses. Without it every client behind the proxy shares the proxy's address and one 20-failure limit. Anyone can forge the header, so it is never believed from other connections.

### Sessions

Sessions are signed cookies that last a year, so there is nothing stored on the server per session. **Sign out other devices** invalidates every other cookie for that user by bumping a counter in their user record.

## Logging

Everything is logged to the console (stdout) as one `key=value` line per event, so `docker logs` shows it. Lines at `WARN` or `ERROR` are failures.

- Sign-ins (including failed attempts), sign-outs and first-run setup.
- Password changes and resets, and users created or deleted.
- Edit mode: sections and tiles added, changed, deleted or reordered, and edits published or cancelled.
- Icon library refreshes and custom icons added, changed or deleted.
- Every request to a remote resource (catalogues, icon images, sites), with its status and how long it took, and any that fail. Query strings are left out.
- Any request that ends in an error status, and internal errors.

Each event names the user and their address, worked out as described under Guessing protection. Passwords are never logged. On startup the server logs a `listening` line with its address and data directory.

## Building and development

### Running and testing

```sh
go run ./cmd/homepage   # data goes to ./data
go test ./...
```

CI runs these on every push and pull request, so run them before you send a change:

```sh
test -z "$(gofmt -l .)"
go vet ./...
go test -race ./...
```

Run one test with `go test ./internal/web -run TestName`. The tests need no network. The web tests send every outbound request to a fake server.

### Building the image

```sh
docker build -t tesla-homepage:dev .
docker run -d --name tesla-homepage -p 8080:8080 -v tesla-homepage-data:/data tesla-homepage:dev
```

The Dockerfile builds a static binary (`CGO_ENABLED=0`) and copies it into a distroless image. For both architectures at once, use `docker buildx build --platform linux/amd64,linux/arm64 .`.

### Project layout

The code is one Go module with one binary, `cmd/homepage`, which reads the environment and wires the packages together. Everything else is under `internal/`.

| Package | Responsibility |
|---|---|
| `internal/web` | HTTP routes, handlers, and the embedded `templates/` and `static/` files. Pages are server-rendered with `html/template` and use htmx for partial updates. |
| `internal/store` | Reads and writes all files under `DATA_DIR`. |
| `internal/auth` | Password hashing, signed session cookies and the sign-in lockouts. |
| `internal/icons` | Turns an upload, URL or site into a stored icon, including the private network guard and image validation. |
| `internal/library` | The searchable Icon library, its catalogue downloads and the custom and built-in icons. |
| `internal/httplog` | Logs every outbound request. |

### More documentation

- [`GLOSSARY.md`](GLOSSARY.md) defines the terms used in the code and the app.
- [`CHANGELOG.md`](CHANGELOG.md) lists what changed in each release. Add an entry under `## [Unreleased]` with any user-visible change.
