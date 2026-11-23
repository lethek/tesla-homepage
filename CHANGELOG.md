# Changelog

All notable changes to this project are documented here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html). Below 1.0.0, a breaking change bumps the minor version.

## [Unreleased]

### Added

- "Open fullscreen" option on a tile. The tile then links through YouTube's redirect page, which the Tesla browser shows fullscreen.

## [0.2.0] - 2026-11-23

### Added

- Custom icons: Admins can add, edit and delete their own icons on the Icon library page, from an upload, an image URL or a site's own icon. They are kept in `library-local.json` and `library-icons/` on the data volume, apart from the downloaded catalogues, and are searched together with them.
- Audit log on the console: sign-ins, sign-outs, password changes and resets, user, tile, section and Icon library changes, every request to a remote resource, and all errors.
- Sign-in lockouts against password guessing. After 5 wrong passwords for a username, or 20 from one address, within 15 minutes, sign-in shows "Too many failed attempts" and refuses even the right password for a minute. The wait doubles each time it happens again, up to an hour, and a successful sign-in clears the username's count. The current-password check on Change password has the same limit. Every spelling of a username that sign-in accepts shares one count. Counts reset when the server restarts.
- `TRUSTED_PROXIES` setting for servers behind a reverse proxy. `X-Forwarded-For` is only believed from the proxies listed, and is used for the address limit and the audit log. Without it, every client behind the proxy shares the proxy's address and one limit.
- Escape leaves Edit mode by cancelling the Draft, after asking if it has unsaved changes. A Cancel from a tab that hasn't seen the Draft's latest changes, for example after editing in another tab, is refused instead of discarding them.

### Changed

- **Breaking for internal icons:** icon downloads (Image URL, From site, custom icons) now refuse loopback, private, link-local, carrier-grade NAT and multicast addresses, including after DNS and redirects, so tile editors can't use the server to reach internal services. Set `ICON_ALLOWED_NETWORKS` to the networks whose icons should still be fetched. Proxy environment variables are no longer used for these downloads.
- A tile saved from the Library tab with no icon picked now gets the letter placeholder and no longer contacts the site. Use the From site tab to fetch the site's icon.
- The Icon library page is a two-column layout: custom icons as a grid that opens edit dialogs, and the catalogues and licences in a side panel.
- Tiles in Edit mode use pencil and trash icon buttons instead of "Edit" and "✕".
- The menu has a separator between the admin links and Change password.

## [0.1.0] - 2026-11-23

### Added

- Launcher homepage for the Tesla in-car browser, fluid from phone to desktop, with Sections of icon Tiles.
- Edit mode with a Draft that is published with Done or discarded with Cancel: add, remove and edit Sections and Tiles, drag and drop between Sections, and a wrap or horizontal scroll setting.
- Multi-user accounts with bcrypt passwords and long-lived signed cookie sessions. The first user is the Admin, who can create users, reset passwords and delete users.
- Starter Set or empty Homepage, chosen at first login.
- Icon library searchable from the tile dialog, covering Dashboard Icons, Simple Icons, selfh.st (CC BY 4.0, credited in the app) and LobeHub, cached locally and refreshed manually by the Admin.
- Icons by upload, by image URL or fetched from the site, with a letter placeholder as the fallback.
- Per-tile Plate background colour and padding (0 to 30%) for any icon.
- Container image published to GHCR for amd64 and arm64 by GitHub Actions.

[Unreleased]: https://github.com/lethek/tesla-homepage/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/lethek/tesla-homepage/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/lethek/tesla-homepage/releases/tag/v0.1.0
