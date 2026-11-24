---
title: Maintenance
description: 'Logs, syncing by hand, updates and rollbacks, health checks and troubleshooting.'
---

All commands run on the server in `/srv/eumetria`.

| Task | How |
|---|---|
| Logs | `docker compose logs -f --tail 100` |
| Sync now | the sync button, or `curl -X POST -H "Authorization: Bearer <token>" https://eumetria.example.com/api/sync` with a `sync` token |
| Fetch the whole history again (after editing old days in Yazio) | the sync button's menu → **Full refresh**, or the same `curl` with `/api/sync?all=1` |
| Sync state | `curl -H "Authorization: Bearer <token>" https://eumetria.example.com/api/status` |
| Change a setting | account menu → **Settings**; it applies once saved |
| Update | `docker compose pull && docker compose up -d` (the [cron](/eumetria/install/server/#5-updates) does this nightly) |
| Roll back | `image: ghcr.io/eumetria-labs/eumetria:<commit>` in `compose.yaml`, then `docker compose up -d` |
| Authorize Fitbit again | **Settings → Google → Connect again** |
| Write all weekly reviews again | **Settings → AI** (runs between syncs, shows how far it got) |
| Raw Yazio answers for a day | `docker compose run --rm eumetria -debug-day 2026-10-13` |
| Raw Google answers for a day | `docker compose run --rm eumetria -debug-google 2026-10-13 -google-token /data/google-token.json` |

## Health check

`/healthz` answers `ok`, and turns unhealthy when no sync has succeeded for three intervals, e.g. because Yazio changed its API or your password. Point an uptime monitor at `https://eumetria.example.com/healthz` to hear about it. Docker's own health check uses it too.

## Troubleshooting

**The page says "No account yet".** Create the first admin: see [Quick Start](/eumetria/start/quick-start/#3-create-the-first-admin).

**Signing in does nothing on `http://<ip>:8080`.** The sign-in cookie needs HTTPS, or plain HTTP on `localhost`. Set up [nginx and HTTPS](/eumetria/install/server/#4-nginx-and-https), or forward the port over SSH.

**`unable to open database file` at start.** The data folder isn't writable by the container's user. With Docker Compose, check that `/srv/eumetria/data` belongs to the `eumetria` user and that `compose.yaml` has its ids.

**The log warns about `TRUSTED_PROXY`.** Set it to nginx's address as the container sees it; see [Configuration](/eumetria/install/configuration/).

**`google: not authorized`.** Google revoked the token: **Settings → Google → Connect again**.

**Mail is "sent" but never arrives.** Use a sender address your provider may send for; see [E-mail](/eumetria/security/sign-in/#e-mail).

**MCP or coach requests fail with 413.** nginx is missing the `/api/mcp` or `/api/coach` block from the current [`deploy/nginx.conf`](https://github.com/eumetria-labs/eumetria/blob/main/deploy/nginx.conf).

Still stuck? Ask on [Discord](https://discord.gg/dqJ7JPdsQ4) or open an issue on [GitHub](https://github.com/eumetria-labs/eumetria/issues).
