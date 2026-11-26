---
title: Updates and releases
description: 'Release channels, pinning a version, rolling back, and how a release is made.'
---

Eumetria comes as a container image in GitHub's registry, `ghcr.io/eumetria-labs/eumetria`. The tag in `compose.yaml` (or in your `docker run`) picks which versions you get:

| Tag | What you get | For |
|---|---|---|
| `:1` | the newest 1.x release | most servers: the default |
| `:1.2` | the newest patch of 1.2 | only fixes, no new features |
| `:1.2.3` | exactly that release | holding one version, or going back to it |
| `:latest` | every commit on main, a few minutes after it lands | trying what's next |
| `:<commit>` | one build from main (`:3ba126d`) | going back on `:latest` |

The release tags are built again every Monday from the same code, so they get Go's and the base image's security fixes without a new release. The nightly [cron](/eumetria/install/server/#5-updates) picks those up too.

A new major version (`2.x`) means something needs doing when you update: a setting, the nginx configuration, a step by hand. `:1` never moves to it on its own; the [changelog](https://github.com/eumetria-labs/eumetria/blob/main/CHANGELOG.md) says what to do before you change the tag.

The optional `claude-runner` image has the same tags. Keep both on the same one.

## Which version runs

**Settings → General** shows it at the bottom, with a link to what's new in that release. On the server:

```sh
docker compose run --rm eumetria -version     # eumetria 1.2.3, or main-3ba126d on :latest
```

## Update, pin or roll back

```sh
cd /srv/eumetria
# compose.yaml: image: ghcr.io/eumetria-labs/eumetria:1.2.3
docker compose pull && docker compose up -d
```

A database migration runs on the first start of a newer version. Going back to an older version after that isn't always possible, because older versions don't know the newer tables. To go back safely, restore the [backup](/eumetria/operations/backups/#restore) from before the update. Eumetria writes one every night, and **Settings → Backups → Back up now** makes one right before you update.

## Release notes

Each release's notes are on [GitHub Releases](https://github.com/eumetria-labs/eumetria/releases) and in [`CHANGELOG.md`](https://github.com/eumetria-labs/eumetria/blob/main/CHANGELOG.md). To hear about new ones, use **Watch → Custom → Releases** on the repository.

## Making a release

For maintainers:

1. Move what's under `## [Unreleased]` in `CHANGELOG.md` to a new section `## [1.2.0] - <date>`, and add its link at the bottom.
2. Commit that on main and wait for CI to pass.
3. Tag and push:

   ```sh
   git tag -a v1.2.0 -m "Eumetria 1.2.0"
   git push origin main v1.2.0
   ```

The [release workflow](https://github.com/eumetria-labs/eumetria/blob/main/.github/workflows/release.yml) tests the tag, pushes the images as `:1.2.0`, `:1.2` and `:1`, and makes the GitHub release with that changelog section as its notes. If the section is missing, it stops before pushing anything. `scripts/release-notes.sh v1.2.0` shows locally what the notes will say.
