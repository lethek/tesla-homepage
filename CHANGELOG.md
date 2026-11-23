# Changelog

All notable changes to this project are documented here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html). Below 1.0.0, a breaking change bumps the minor version.

## [Unreleased]

### Added

- Custom icons: Admins can add, edit and delete their own icons on the Icon library page. They are kept in `library-local.json` and `library-icons/` on the data volume, apart from the downloaded catalogues, and are searched together with them.

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

[Unreleased]: https://github.com/lethek/tesla-homepage/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/lethek/tesla-homepage/releases/tag/v0.1.0
