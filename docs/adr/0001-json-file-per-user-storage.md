# 1. JSON files per User instead of a database

Date: 2026-11-23

## Status

Accepted

## Context

The app has several Users. Each one has a Homepage, a Draft, a password hash and a session counter. SQLite on the data volume was considered, using a pure-Go driver so the build stays cgo-free.

## Decision

State lives as plain JSON files under `DATA_DIR`:

- `users.json`
- `users/<id>/homepage.json` and `users/<id>/draft.json`
- `icons/<sha256>.<ext>`
- `secret`, the key that signs session cookies

All writes go through one process-wide mutex. Each write goes to a temp file that is then renamed into place.

Sessions are HMAC-signed cookies rather than server-side records, so they need no storage.

## Consequences

- The data can be read, backed up and hand-edited with ordinary tools.
- There is no schema migration framework. Changes to the shape of the data must stay backward-compatible with old JSON, or ship a one-off upgrade step.
- One process owns the data directory. Running several replicas against the same volume is not supported.
- Unused icons are cleaned up by scanning every User's Homepage and Draft. That is fine for a household, but not for many Users.
- An individual session can't be revoked. A password change or reset logs out every session for that User.
