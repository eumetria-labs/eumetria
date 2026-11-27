---
title: Local development
description: 'Run Eumetria from source with sample data, run the tests, and work on these docs.'
---

You need Go (the version in `go.mod`), Node 22 or newer for the page's tests, and Chrome or Chromium for the browser checks.

## Run it

```sh
# the page with generated sample data (Fitbit data included); sign in as demo / demo
# at http://localhost:8080 (passkeys don't work on 127.0.0.1)
go run ./cmd/eumetria -demo -serve :8080 -site site -data /tmp/eumetria-demo
```

`-site` serves the page from the folder, so edits show on reload. Its own `-data` keeps the demo account out of your real data folder. With `-demo`, notes and lifts can be written without a second factor; admin rights still need one.

```sh
# the page from plain files, without a server or sign-in: -out writes all of
# the data next to it (any static file server works)
go run ./cmd/eumetria -demo -out site/stats.json

# real data (settings as in the server's env file)
set -a; . path/to/eumetria.env; set +a
go run ./cmd/eumetria -serve :8080 -site site
```

## Tests

```sh
# Go, the page's logic (Node 22+), and a headless-Chrome smoke test (also
# loads views as a 412 and 360 px phone and fails when the page is wider
# than the screen)
go vet ./... && go test ./...
node --test tests/*.test.js
go run ./cmd/eumetria -demo -out site/stats.json && node scripts/smoke.js site

# the sample data depends only on the time it's made for: -now repeats a
# CI run (its log prints the time)
go run ./cmd/eumetria -demo -now 2026-11-17T13:24:00Z -out site/stats.json && node scripts/smoke.js site

# how fast the page renders on a slow phone (CPU 4× slower; median of 5)
node scripts/perf.js site

# the account menu, Profile / Settings, trainings and more against a demo
# server (makes an admin with an authenticator app and a viewer; ~2 minutes)
go build -o /tmp/eumetria ./cmd/eumetria && node scripts/prefs-check.js /tmp/eumetria

# known vulnerabilities in the code's call paths
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
```

## Debugging the sources

```sh
go run ./cmd/eumetria -debug-day 2026-10-13             # raw Yazio answers for a day
go run ./cmd/eumetria -google-auth                      # authorize Fitbit from the terminal
go run ./cmd/eumetria -debug-google 2026-10-13          # raw Google answers for a day
go run ./cmd/eumetria -debug-week 2026-10-15 -dry-run   # the weekly review's input; without -dry-run Claude writes it
```

## Layout of the code

| Folder | What's there |
|---|---|
| `cmd/eumetria` | the server: HTTP API, accounts, sync loop, MCP server, command-line tools |
| `internal/…` | one package per concern, e.g. `yazio` and `google` (the sources), `stats` and `scores` (what the page shows), `sessions`, `lifts`, `wods`, `notes`, `checkins` and `phases` (what people log), `review` and `coach` (Claude), `auth`, `backup`, `db` (with the SQL migrations) |
| `site` | the page: plain HTML, CSS and JavaScript modules, no build step; compiled into the binary |
| `tests` | the page's logic, run with `node --test` |
| `scripts` | browser checks (smoke, perf, prefs) and the docs' screenshots |
| `deploy` | Docker Compose, nginx and updater templates |
| `docs` | this documentation (Starlight) |

## These docs

The docs are Markdown in `docs/src/content/docs`, built with [Starlight](https://starlight.astro.build) and published to GitHub Pages on every push to `main` that touches `docs/`.

```sh
cd docs
npm install
npm run dev        # http://localhost:4321/eumetria/
npm run build      # into docs/dist, as CI does
```

The screenshots in `docs/src/assets/screens` are taken from a running Eumetria. After a change to the page, take them all again:

```sh
go build -o /tmp/eumetria ./cmd/eumetria && node scripts/docs-shots.js /tmp/eumetria
```

It starts a fresh server (for the setup screens) and a demo server (for everything else), each with an admin, and writes every screenshot the docs use. Screens outside Eumetria, such as the Google Cloud console, are described in words instead.
