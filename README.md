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
| `TRUSTED_PROXIES` | none | Reverse proxies whose `X-Forwarded-For` is believed, as addresses or CIDR ranges |

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

## Guessing protection

Passwords are stored as salted bcrypt hashes, so precomputed (rainbow table) attacks don't apply. Guessing is slowed by lockouts, which are logged as `WARN` lines:

- A username gets 5 sign-in attempts in 15 minutes, and an address gets 20. Each attempt is counted before the password is checked, so a burst of simultaneous guesses can't get more than that far, and a successful sign-in gives its attempt back. The next attempt after the allowance is used up starts a lockout. A locked key gets `429 Too many failed attempts`, even with the right password, until its lockout ends.
- A lockout lasts 1 minute, then doubles each time the same key is locked again, up to 1 hour. Nothing is locked for good, and a successful sign-in clears the username's count.
- Wrong guesses of the current password on the Change password page are limited the same way.
- Names that don't exist are counted too, so the lockout doesn't reveal which names are real.

Every spelling of a username that sign-in accepts (any capitalisation, and look-alike letters such as `ſ` for `s`) shares one count. Counts are kept in memory and reset when the server restarts. A person can be locked out for a while by someone guessing their name.

The client's address is the connection's own address. If the server runs behind a reverse proxy, set `TRUSTED_PROXIES` to the proxy's address or CIDR range (a comma separated list, for example `10.0.0.0/8,172.17.0.1`). Only then is `X-Forwarded-For` read, from the right, skipping the proxies' own addresses. Without it every client behind the proxy shares the proxy's address and one 20-failure limit. Anyone can forge the header, so it is never believed from other connections.

## Logging

Everything is logged to the console (stdout) as one `key=value` line per event, so `docker logs` shows it. Lines at `WARN` or `ERROR` are failures.

- Sign-ins (including failed attempts), sign-outs and first-run setup.
- Password changes and resets, and users created or deleted.
- Edit mode: sections and tiles added, changed, deleted or reordered, and edits published or cancelled.
- Icon library refreshes and custom icons added, changed or deleted.
- Every request to a remote resource (catalogues, icon images, sites), with its status and how long it took, and any that fail. Query strings are left out.
- Any request that ends in an error status, and internal errors.

Each event names the user and their address, worked out as described under Guessing protection. Passwords are never logged.

## Development

```sh
go run ./cmd/homepage   # data goes to ./data
go test ./...
```

CI runs `gofmt`, `go vet` and `go test` on every push and PR. Pushes to `main` publish `latest` and `sha-*` images for amd64 and arm64. `v*` tags also publish semver tags.
