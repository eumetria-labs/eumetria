# Changelog

What changed in each release of Eumetria. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/): a major version for a change that
needs you to do something when you update (a setting, nginx, a manual step),
a minor version for new features, a patch for fixes.

The section of a release becomes its notes on GitHub; see
[Updates and releases](https://eumetria-labs.github.io/eumetria/operations/updates/).

## [Unreleased]

## [1.0.1] - 2026-11-27

### Fixed

- Settings → Update: the Install / Switch button was hidden. Automatic installs weren't affected.

## [1.0.0] - 2026-11-27

The first release. Everything Eumetria does so far, in one container:

### Data

- Yazio sync: meals, calories, macros, micronutrients and alcohol, with meal photos and recipes.
- Fitbit and Pixel Watch through Google Health: sleep, heart rate, steps, workouts with routes and heart-rate zones, VO2 max.
- One SQLite database for everything (`app.db`); older `history.json` files are imported on the first start.

### Dashboard

- Today, Nutrition, Body & Sleep, Training and Insights, on a wide screen and as an installable phone app with a bottom tab bar, shortcuts and a share target.
- Eumetria's own Sleep, Recovery and Readiness scores; sleep timing (regularity, chronotype, social jetlag).
- Morning check-in with day tags, phases and events, goal hit rates and a correlation explorer.
- Year in review with a picture to share: in December for the running year, in January for the last one.
- Search, export (CSV/JSON), a printable report and share pictures.

### Training

- A Training is the container for a workout: its device workouts, sets, lifts, benchmark results and the note, logged with one form.
- Personal bests for lifts and benchmark workouts, e1RM and strength over time, a training calendar.
- "Not logged yet": workouts without a training, by the workout types you choose (Profile → Training). The same types decide the days trained in the calendar, the report and the year in review.
- Whiteboard photos in notes, transcribed by Claude when wanted.

### Claude

- Weekly review and an in-dashboard coach chat, through the Anthropic API or an optional Claude Code runner on your own subscription.
- An MCP server (`/api/mcp`, read-only) for asking Claude Code about your data.

### Accounts and security

- Accounts with roles and scopes, passkeys, security keys and authenticator apps, recovery codes, device cookies.
- Profile and Settings views; secrets encrypted with `SETTINGS_KEY`.
- Nightly backups, encrypted with a passphrase and optionally copied to Google Drive.

### Releases

- Versioned images: `:1.0.0`, `:1.0` and `:1` for releases, `:latest` for the newest commit on main. The footer shows the version the server runs to anyone signed in, linked to its release notes; `-version` prints it.
- In-place updates from **Settings → Update**: two channels, stable (`:1`) and edge (`:latest`), a dot and an "Update available" chip for admins, an optional e-mail, and what's new before you install.
- An optional updater container (`eumetria-updater`) installs an update with one click: a backup first, then the new images, a health check, and the old version back if the new one doesn't come up. Stable updates and security rebuilds install by themselves at 04:00 unless switched off. It replaces the nightly cron (`deploy/cron` is gone).
- A new major version is a notice with its steps, never installed on its own.

[Unreleased]: https://github.com/eumetria-labs/eumetria/compare/v1.0.1...HEAD
[1.0.1]: https://github.com/eumetria-labs/eumetria/compare/v1.0.0...v1.0.1
[1.0.0]: https://github.com/eumetria-labs/eumetria/releases/tag/v1.0.0
