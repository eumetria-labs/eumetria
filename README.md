<p align="center"><img src="site/icons/mark.svg" alt="" height="64"></p>

<h1 align="center">Eumetria</h1>

<p align="center"><b>Your nutrition, training and sleep in one self-hosted dashboard.</b></p>

## ✨ What is it?

Eumetria puts the data of the apps you already use onto one page on your own server. From [Yazio](https://www.yazio.com) it takes calories, macros, meals, foods, recipes, weight, steps and activities. Optionally it adds what a Fitbit or Pixel Watch records through the [Google Health API](https://developers.google.com/health): workouts with heart rate and route, sleep stages and overnight measurements.

- 📊 Charts for calories, macros, weight (with a forecast to your goal), movement, sleep and recovery
- 🔍 Search across your whole history, food and activity histories, a sortable data table
- 🏋️ Workout details: heart rate by zone, splits, route map, elevation
- 🤖 An optional weekly review written by Claude
- 🥫 Nutri-Score and NOVA badges from Open Food Facts
- 📤 Export as CSV/JSON, a printable report, share cards for Instagram & co.
- 🌗 Light and dark mode, English and German, installable on your phone, works offline

It runs as one small Docker container that serves the page and syncs on a timer.

## 🚀 Setup on a server

You need a Linux server with Docker Compose v2, nginx, certbot and a domain pointing at it. The examples use `eumetria.example.com` and host port `8090`; templates for every file are in [`deploy/`](deploy/). Run everything as root.

**1. User and folders.** The container runs as its own user, which can only write its data folder:

```sh
adduser --system --group --no-create-home --home /nonexistent --shell /usr/sbin/nologin eumetria
install -d -o root -g root -m 755 /srv/eumetria
install -d -o eumetria -g eumetria -m 700 /srv/eumetria/data
install -d -o root -g root -m 700 /var/backups/eumetria
```

Don't add `eumetria` to the `docker` group (members are effectively root).

**2. Compose file and settings.** Copy [`deploy/compose.yaml`](deploy/compose.yaml) to `/srv/eumetria/compose.yaml` and put in the user's ids. Then create the settings file from [`deploy/eumetria.env.example`](deploy/eumetria.env.example):

```sh
cd /srv/eumetria
sed -i "s/\"UID:GID\"/\"$(id -u eumetria):$(id -g eumetria)\"/" compose.yaml
install -m 600 -o root -g root /dev/null eumetria.env
nano eumetria.env          # paste the example and fill it in
docker compose config        # check what gets passed in (prints secrets)
```

Keep secret values in **single quotes**: unquoted, Compose expands `$` and cuts at ` #`.

| Setting | Default | Meaning |
|---|---|---|
| `YAZIO_EMAIL`, `YAZIO_PASSWORD` | – | Your Yazio login |
| `YAZIO_CLIENT_ID`, `YAZIO_CLIENT_SECRET` | – | The Yazio app's OAuth client, see [🥗 Yazio](#-yazio) |
| `SYNC_INTERVAL` | `30m` | Time between syncs, at least `5m` |
| `STATS_PASSWORD` | – | Optional extra encryption of the data, see [🔒 Password protection](#-password-protection) |
| `TZ` | `UTC` | Decides what "today" is, e.g. `Europe/Berlin` |
| `STATS_TITLE` | `Eumetria` | Page heading |
| `PUBLISH_FOODS`, `PUBLISH_PHOTOS`, `PUBLISH_HEALTH`, `PUBLISH_ROUTES`, `PUBLISH_REVIEW` | `true` | `false` keeps food names, meal photos, Fitbit data, GPS tracks or the weekly review off the page |
| `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, `GOOGLE_REDIRECT_URL` | – | Fitbit data, see [⌚ Fitbit](#-fitbit--google-health) |
| `ANTHROPIC_API_KEY`, `AI_MODEL`, `AI_EFFORT`, `AI_CREDIT_USD` | –, `claude-opus-5-5`, `medium`, – | Weekly review, see [🤖 Weekly review](#-weekly-review-with-claude) |
| `OPENFOODFACTS` | `true` | `false` skips the Open Food Facts lookups (only barcodes are sent) |
| `YAZIO_SINCE`, `YAZIO_REFRESH_DAYS` | 365 days ago, `3` | First day to fetch; how many recent days every sync fetches again |

**3. Start it.**

```sh
docker compose pull && docker compose up -d
docker compose logs -f                              # "serving … syncing every 30m0s", then the first sync
curl -s http://127.0.0.1:8090/healthz               # ok
```

Then create your account. It asks for a password of at least 12 characters (common ones are refused):

```sh
docker compose run --rm -it eumetria -create-user <name> -role admin
```

Until an account exists the page only says how to make one.

The first sync fetches a year of history and every photo, which takes a few minutes. The container's port is bound to `127.0.0.1` only: ports Docker publishes bypass `ufw`.

**4. nginx and HTTPS.** Copy [`deploy/nginx.conf`](deploy/nginx.conf) to `/etc/nginx/sites-available/eumetria.example.com`, enable it and get a certificate:

```sh
ln -s /etc/nginx/sites-available/eumetria.example.com /etc/nginx/sites-enabled/
nginx -t && systemctl reload nginx
certbot --nginx -d eumetria.example.com --redirect
```

If certbot gets the certificate but fails at "Deploying certificate" (a version mismatch in certbot's packages), only the nginx edit failed. Use [`deploy/nginx-tls.conf`](deploy/nginx-tls.conf) instead and switch the renewal to webroot:

```sh
install -d -m 755 /var/www/letsencrypt
nano /etc/nginx/sites-available/eumetria.example.com      # replace with deploy/nginx-tls.conf
nginx -t && systemctl reload nginx
certbot certonly --webroot -w /var/www/letsencrypt -d eumetria.example.com \
  --cert-name eumetria.example.com --force-renewal --deploy-hook "systemctl reload nginx"
```

**5. Backups and updates.** Copy [`deploy/cron`](deploy/cron) to `/etc/cron.d/eumetria` (mode 644). Every night it backs up `data/` to `/var/backups/eumetria` (14 days are kept) and pulls the newest image, so a push to `main` is live the next morning. The accounts database goes in as the copy the server writes at 03:20 (`data/backup/app.db`), not as the live file. Copy the backups off the server too: the history can be fetched from Yazio again, old photos and accounts can't.

## 👥 Accounts

Everything but the sign-in page needs an account. Admins also see the sync button (its menu has **Full refresh**); viewers only read.

- **Passwords:** at least 12 characters, no common ones, stored as argon2id hashes. Change yours under account menu → **Security**; that signs out your other devices.
- **Sessions:** "Keep me signed in" lasts until a week without use, a month at most; without it, until the browser closes (12 hours at most). The account menu (your initial, top right) signs out here or everywhere.
- **Wrong passwords:** five in 15 minutes lock that account for 15 minutes, doubling up to a day; twenty from one address lock the address. Wrong codes count the same.
- **Second factors** (account menu → **Security**): passkeys (fingerprint, face or PIN, synced by your phone or password manager), security keys such as a YubiKey, an authenticator app, and ten recovery codes for when the others are lost. With one, the password is followed by a second step; a passkey alone signs in without a password, from the button or from the browser's suggestions in the name field. **Admins need one** before they can sync or open the admin tools. Changing them needs a sign-in within the last 10 minutes.
- **Passkeys only work on the real domain over HTTPS** (and on `http://localhost` for development), not on an IP address.
- **Scripts** use an API token instead of a password (see the table below). It carries scopes, `read` (the data) and/or `sync`, and an expiry, and is shown only once.

| Task | Command (on the server, in `/srv/eumetria`) |
|---|---|
| Add someone | `docker compose run --rm -it eumetria -create-user <name>` (`-role admin` for an admin) |
| Forgotten password | `docker compose run --rm -it eumetria -reset-password <name>` (ends their sessions too) |
| Every second factor lost (and the recovery codes too) | `docker compose run --rm eumetria -reset-second-factors <name>`, then sign in with the password and set up new ones |
| Token for a script | `docker compose run --rm eumetria -create-token <name> -token-name cron -scopes sync -expires 365` |

## 🥗 Yazio

The sync logs in with your Yazio account. It also needs the OAuth client ID and secret of the Yazio app. They are the same for everyone, but they belong to YAZIO, so they are not in this repository. The pair that works with API v22 is in [yazio-api-specification](https://github.com/dornsebastian/yazio-api-specification) (`spec/components/schemas/OAuthTokenRequest.yaml`). If YAZIO changes them, update the settings; no new image is needed.

Each sync fetches the last 3 days again (they may still change) and any missing days. Days you edit further back in the app reach the page only with a full refresh (see [🧰 Maintenance](#-maintenance)).

## ⌚ Fitbit / Google Health

Optional. With a Fitbit or Pixel Watch the page also shows workouts (heart rate by zone, splits, route, elevation), sleep stages, resting heart rate, HRV, breathing rate, blood oxygen, skin temperature, the calories Fitbit counts and active zone minutes. Scores that are computed in the Fitbit app (readiness, sleep score, cardio load) aren't in the API and aren't imitated.

**One-time setup in the [Google Cloud console](https://console.cloud.google.com/):**

1. Create a project and enable the **Google Health API**.
2. **Google Auth Platform → Branding:** an app name (without "Google" or "Fitbit"), your e-mail, and your domain under **Authorized domains**. No logo (a logo needs Google's verification).
3. **Audience → Publish app**, so the status is **In production**. In "Testing", refresh tokens expire after 7 days.
4. **Clients → Create client → Web application**, with `https://eumetria.example.com` as **Authorized redirect URI**, character for character as in `GOOGLE_REDIRECT_URL`.

Put the client into `eumetria.env`, run `docker compose up -d`, then authorize once:

```sh
docker compose run --rm -it eumetria -google-auth -google-token /data/google-token.json
```

Open the printed URL, allow every access, and paste the address you land on. The token is stored in `data/google-token.json` and the running container picks it up at its next sync. If the logs say `google: not authorized`, Google revoked the token (password change, access removed, or six months unused): authorize again.

## 🤖 Weekly review with Claude

With `ANTHROPIC_API_KEY` set, the first sync after a calendar week has Claude write a short review of it: nutrition, training, sleep and recovery, compared with the week before, in English and German. It's written once per week and stored, about $0.10 per week with Claude Opus 5.5. Claude only sees what the page publishes.

The Anthropic API is prepaid: buy credit at [console.anthropic.com](https://console.anthropic.com) and leave auto-reload off, so nothing more can be charged. Set `AI_CREDIT_USD` to what you bought; the page warns admins when it covers fewer than 4 more reviews.

## 🔒 Password protection

Optional, on top of the accounts, and on its way out (the accounts make it unnecessary). With `STATS_PASSWORD` set, the sync encrypts the data (AES-256-GCM, key from PBKDF2-SHA256 with 600,000 iterations) and the page asks for the password before decrypting it in the browser. "Remember me" keeps the derived key, never the password, on that device. Changing the password signs every device out.

Use a long passphrase: the encrypted file can be downloaded by anyone and the password guessed offline. Meal photos aren't encrypted, but their names are only listed inside the encrypted data.

## 🧰 Maintenance

All commands run on the server in `/srv/eumetria`.

| Task | How |
|---|---|
| Logs | `docker compose logs -f --tail 100` |
| Sync now | the refresh button, or `curl -X POST -H "Authorization: Bearer <token>" https://eumetria.example.com/api/sync` with a `sync` token |
| Fetch the whole history again (after editing old days in Yazio) | the button's menu → **Full refresh**, or the same `curl` with `/api/sync?all=1` |
| Sync state | `curl -H "Authorization: Bearer <token>" https://eumetria.example.com/api/status` |
| Change a setting | edit `eumetria.env`, then `docker compose up -d` |
| Update | `docker compose pull && docker compose up -d` (the cron does this nightly) |
| Roll back | `image: …:<commit>` in `compose.yaml`, then `docker compose up -d` |
| Authorize Fitbit again | `docker compose run --rm -it eumetria -google-auth -google-token /data/google-token.json` |
| Raw Yazio / Fitbit answers for a day | `docker compose run --rm eumetria -debug-day 2026-10-13` / `… -debug-google 2026-10-13 -google-token /data/google-token.json` |
| Restore a backup | `docker compose down`, `rm -rf data`, `tar -xzf /var/backups/eumetria/data-<date>.tar.gz`, `cp data/backup/app.db data/app.db`, `chown -R eumetria:eumetria data && chmod 700 data`, `docker compose up -d` |

**Write all weekly reviews again** (e.g. after a prompt change). Both the server and this command write the history, so stop the server first:

```sh
docker compose stop eumetria
docker compose run --rm eumetria -rewrite-reviews -history /data/history.json
docker compose up -d
```

`/healthz` turns unhealthy when no sync has succeeded for three intervals, e.g. because Yazio changed its API or your password. Point an uptime monitor at `https://eumetria.example.com/healthz` to hear about it.

## 🛠️ Local development

```sh
# the page with generated sample data (Fitbit data included); sign in as demo / demo
# at http://localhost:8080 (passkeys don't work on 127.0.0.1)
go run ./cmd/eumetria -demo -serve :8080 -site site -data /tmp/eumetria-demo -out site/stats.json
# -site serves the page from the folder: edits show on reload. Its own -data
# keeps the demo account out of your real data folder.

# real data (settings as in the server's env file)
set -a; . path/to/eumetria.env; set +a
go run ./cmd/eumetria -serve :8080 -site site

# tests: Go, the page's logic (Node 22+), and a headless-Chrome smoke test
go vet ./... && go test ./...
node --test tests/*.test.js
go run ./cmd/eumetria -demo -out site/stats.json && node scripts/smoke.js site

# raw API answers for a day, Fitbit authorization, the weekly review of a week
go run ./cmd/eumetria -debug-day 2026-10-13
go run ./cmd/eumetria -google-auth
go run ./cmd/eumetria -debug-google 2026-10-13
go run ./cmd/eumetria -debug-week 2026-10-15 -dry-run   # without -dry-run Claude writes it
```

Links can open a view: `#range=7|30|90|365|all`, `day=YYYY-MM-DD`, `tab=breakfast|lunch|dinner|snack|activities|sleep`, `workout=<id>`, `food=<name>`, `activity=<name>`, e.g. `#range=30&day=2026-10-13&tab=sleep`.

## ⚠️ Unofficial API

This is a personal project. It is not affiliated with, endorsed by or supported by YAZIO. It uses YAZIO's private, undocumented API, which is subject to YAZIO's terms of service and can change or stop working at any time. "YAZIO" is a trademark of its owner and is used here only to say which service the tool works with.

The endpoints come from community work: [Yazio-Exporter](https://github.com/funmelon64/Yazio-Exporter), [juriadams/yazio](https://github.com/juriadams/yazio), [yazio-api-specification](https://github.com/dornsebastian/yazio-api-specification), [saganos/yazio_public_api](https://github.com/saganos/yazio_public_api), [fliptheweb/yazio-mcp](https://github.com/fliptheweb/yazio-mcp), [go-yazio](https://github.com/dumpsabfuck/go-yazio), [itzptk/yazio-go-cli](https://github.com/itzptk/yazio-go-cli), [aleksandr-bogdanov/yazio-exporter](https://github.com/aleksandr-bogdanov/yazio-exporter) and [bserem/yazio-mcp-pyton](https://github.com/bserem/yazio-mcp-pyton). No code from them is included; this repository is MIT-licensed (see `LICENSE`). Food data from [Open Food Facts](https://world.openfoodfacts.org) is available under the Open Database License.
