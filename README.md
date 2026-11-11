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
- 📤 Export as CSV/JSON (with workout notes, lifts, benchmark workouts, weekly reviews and goal changes), a printable report with scores and training, share cards for Instagram & co.
- 🌗 Light and dark mode, English and German, installable on your phone, works offline (when you sign in with "Keep me signed in")

It runs as one small Docker container that serves the page and syncs on a timer.

## 🚀 Setup on a server

You need a Linux server with Docker Compose v2, nginx, certbot and a domain pointing at it. The examples use `eumetria.example.com` and host port `8090`; templates for every file are in [`deploy/`](deploy/). Run everything as root.

**1. User and folders.** The container runs as its own user, which can only write its data folder:

```sh
adduser --system --group --no-create-home --home /nonexistent --shell /usr/sbin/nologin eumetria
install -d -o root -g root -m 755 /srv/eumetria
install -d -o eumetria -g eumetria -m 700 /srv/eumetria/data
install -d -o eumetria -g eumetria -m 700 /var/backups/eumetria
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

Keep values in **single quotes**: unquoted, Compose expands `$` and cuts at ` #`.

| Setting | Default | Meaning |
|---|---|---|
| `SETTINGS_KEY` | – | Encrypts the secrets in the settings and the authenticator apps' secrets. Make it with `openssl rand -hex 32` and **keep a copy outside the server**: it isn't in the backups (that's the point). Without it the settings' secrets have to be typed in again, and authenticator apps stop working (use `-reset-second-factors`, then set them up again). Without the variable, secrets can't be saved in the page and the apps' secrets are kept unencrypted |
| `TZ` | `UTC` | The default time zone, e.g. `Europe/Berlin` |
| `PUBLIC_URL` | – | The site's address, e.g. `https://eumetria.example.org`; also under **Settings** → **General**. Passkeys, Google sign-in and enrolment links use it. Without it, they take the address from each request |
| `TRUSTED_PROXY` | – | nginx's address as the container sees it, `172.30.90.1` with the network in `compose.yaml`: only its `X-Real-IP` counts. Without it, any private address's does. Check with `docker inspect eumetria -f '{{range .NetworkSettings.Networks}}{{.Gateway}}{{end}}'`; a wrong value shows as a warning in the log |

Everything else is set in the page, under account menu → **Settings**, after the first sign-in.

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

It then prints an **enrolment link**: open it, sign in and set up a passkey or an authenticator app there. An admin needs a second factor, and the first one can only be set up with that link (24 hours, one use; `-enrol-link <name>` makes a new one). Set the public URL (`PUBLIC_URL`, or later **Settings** → **General**) to have the whole link printed.

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

**5. Updates.** Copy [`deploy/cron`](deploy/cron) to `/etc/cron.d/eumetria` (mode 644). Every night it pulls the newest image. Backups run inside the app, see [💾 Backups](#-backups).

## 👥 Accounts and roles

Everything but the sign-in page needs an account, and each account has one role. The server sends a user only the data their role allows; the page hides the rest.

| Scope | Gives |
|---|---|
| `view.nutrition` | calories, macros, meals, goals, weight, water |
| `view.foods` | food names and amounts, recipes, Open Food Facts data |
| `view.photos` | meal and recipe photos |
| `view.activity` | steps, Yazio activities |
| `view.health` | Fitbit workouts, heart rate, sleep, recovery, the tracker |
| `view.routes` | GPS tracks of workouts (needs `view.health`) |
| `view.review` | the weekly review |
| `view.notes` | workout notes (needs `view.health`) |
| `edit.notes` | add, change and delete notes (needs `view.notes` and a second factor) |
| `edit.lifts` | add, change and delete personal bests: lifts and benchmark workouts (needs `view.health` and a second factor) |
| `sync` | the sync button, with **Full refresh** in its menu (needs a second factor) |
| `admin` | everything, including the audit log and the credit warning |

Two roles are built in: **admin** (everything) and **viewer** (every `view.*` scope). Make your own for anyone who should see less, e.g. a coach who sees what you eat but not where you run.

**Workout notes** belong to Fitbit workouts: open a workout and write Markdown (headings, bold, italic, lists, inline code, code blocks between ``` lines, `http(s)` links; no HTML). Paste an image with Ctrl+V or use **Add image**: it's uploaded at once, stripped of its metadata and embedded where the cursor is; images from other sites aren't shown. An image no note embeds is deleted after a day and left out of backups; each user can have at most 20 of them waiting and upload 30 an hour, and the folder takes at most 2 GB. A long note shows its start with a **Show more** button. Notes show in the workout, in the activity's history and in the search, and you can add one to a workout's share card (text only). They live in `app.db`, their images in `note-images/` in the data folder, so both are backed up. A note whose workout has gone from Google stays until an admin deletes it (**Users & Roles** → **Notes**).

**Your profile** (account menu → **Profile**) holds a picture, shown in the menu and next to your notes, your name, e-mail and Instagram handle, all optional and visible only to you except the picture. It also lists your API tokens and the devices you're signed in on. Style and language are in the same menu.

Admins manage all of this in the page: account menu → **Users & Roles**. There you add people (with e-mail set up, give their address instead of a password: they get a welcome mail, choose their password through its link, once within three days, and set up a second factor right away if their role needs one; their address then counts as confirmed), give them a role, set a new password for someone who forgot theirs (that also ends their sessions and API tokens), require a second factor (until they set one up they see no data, only their own account settings), disable an account and then delete it for good (what they wrote stays, without an author), edit roles as checkboxes, end anyone's session and read the audit log. Changes need a recent proof of who you are: within 5 minutes of signing in, or the page asks you to confirm it with a passkey, key or code from your app (the password, for an account without a second factor). You can't disable or demote your own account, and the site always keeps one admin. A changed role applies to the user's next request; they stay signed in.

- **Passwords:** at least 12 characters, no common ones, stored as argon2id hashes. Change yours under account menu → **Security**; that signs out your other devices and, unless you untick it, ends your API tokens. If you have passkeys that sign in on their own, you can remove them in the same step: do so if someone else may have had your account.
- **Sessions:** "Keep me signed in" lasts until a week without use, a month at most; without it, until the browser closes (12 hours at most). The account menu (your initial, top right) signs out here or everywhere.
- **What stays on the device:** only with "Keep me signed in" does the page keep your data for offline use. Without it, nothing of the data is stored in the browser, and offline the page waits for the network. Signing out, or the session ending, deletes what was kept and tells the browser to drop its cache of the site. Your photos, routes and year files are checked with the server on every load (cheaply, by ETag) instead of being cached for a year, so a later user of the same browser can't open them.
- **Wrong passwords:** five in 15 minutes lock that account name for 15 minutes from that network (a /24, or a /64 for IPv6), doubling up to a day; twenty from one address lock the address. Wrong codes count the same. A name is never locked for everyone, so nobody can lock you out by typing your name: many failures from anywhere only slow its sign-ins down (up to 5 seconds), and 50 within 15 minutes show up in the audit log.
- **Audit log:** kept for 180 days, at most 100,000 entries; repeated failed tries from one address count up in one entry. **Users & Roles** → **Audit log** can show only the failed tries, or everything else.
- **Second factors** (account menu → **Security**): passkeys (fingerprint, face or PIN, synced by your phone or password manager), security keys such as a YubiKey, an authenticator app, and ten recovery codes for when the others are lost. With one, the password is followed by a second step; a passkey alone signs in without a password, from the button or from the browser's suggestions in the name field. **Admins, roles with `sync` and roles that write (`edit.notes`, `edit.lifts`) need one** before they can sync, open the admin tools or write; a `sync` token stops working while its owner has none. Changing them, making recovery codes or API tokens, and changing the password of an account with a second factor ask you to confirm it's you (a passkey, key or app code; recovery codes don't count). **The first second factor** of an account that needs one is set up with an **enrolment link** (one use, 24 hours): **Users & Roles** → **Enrolment link** next to the user, or `-enrol-link <name>` on the command line; `-create-user` and `-reset-second-factors` print one. The password alone isn't enough, so someone who learned it can't make the account theirs by setting up their own factor first. Changes to how an account signs in show up as a notice for its user (and as "sign-in changed" in the user list) until they dismiss it.
- **E-mail (optional):** with a mail server under **Settings** → **Email** (or `SMTP_HOST`, `SMTP_PORT`, `SMTP_TLS`, `SMTP_USER`, `SMTP_PASSWORD`, `SMTP_FROM`), every change to how an account signs in (a key, an app, recovery codes, a token, the password, a refused copied key, many failed sign-ins) also goes by mail to its owner, which someone holding their session can't dismiss. Admins hear when someone gets admin or sync rights or sets up a first second factor, and enrolment links can be sent by mail (**Enrolment link** → **Send by e-mail**, or `-enrol-link <name> -mail`). Mail goes only to an address its user confirmed with a link sent there (**Profile**); changing it needs you to confirm it's you, and the old address is told. The connection is always encrypted (STARTTLS on 587 or TLS on 465): a server that offers neither gets nothing. There is no password reset by mail. **Use a sender address your mail provider may send for**: with SPF and DKIM for its domain (or a parent domain's `p=reject` DMARC policy), Gmail and GMX refuse mail from anything else after the server accepted it, so the log says "sent" and nothing arrives; a test mail to the provider's own domain can still get through. `-demo` never sends mail.
- **Passkey or security key:** **Add passkey** stores a passkey that signs in on its own; **Add security key** registers a key only as the second step after the password, with nothing stored on it, as most sites do: its PIN once when adding it, then a touch when signing in. Use the latter when a key can't take a passkey, e.g. a YubiKey through the CTAP proxy of Qubes OS.
- **Passkeys only work on the real domain over HTTPS** (and on `http://localhost` for development), not on an IP address.
- **Scripts** use an API token instead of a password (see the table below). It carries scopes, `read` (the data the account's role sees) and/or `sync` (if the role may sync), and an expiry, and is shown only once.

The same from the command line, e.g. for the first admin or when nobody can sign in:

| Task | Command (on the server, in `/srv/eumetria`) |
|---|---|
| Add someone | `docker compose run --rm -it eumetria -create-user <name>` (a viewer; `-role admin` or your own role) |
| Invite someone | `docker compose run --rm eumetria -create-user <name> -role <role> -email <address>`: a welcome mail instead of a password (needs e-mail, the public URL and `SETTINGS_KEY`) |
| Another role | `docker compose run --rm eumetria -set-role <name> -role <role>` |
| Roles, their scopes and users | `docker compose run --rm eumetria -list-roles` |
| Change or delete a role | `-create-role <role> -scopes …` again with the new scopes; `-delete-role <role>` once nobody has it |
| Forgotten password | `docker compose run --rm -it eumetria -reset-password <name>` (ends their sessions and API tokens too) |
| Every second factor lost (and the recovery codes too) | `docker compose run --rm eumetria -reset-second-factors <name>`, then sign in with the password and set up new ones (with the enrolment link it prints, if the role needs a factor) |
| Enrolment link for the first second factor | `docker compose run --rm eumetria -enrol-link <name>` (with `PUBLIC_URL` set, the whole link) |
| Token for a script | Account menu → **Profile** → **API tokens**, or `docker compose run --rm eumetria -create-token <name> -token-name cron -scopes sync -expires 365` |

## 🥗 Yazio

The sync logs in with your Yazio account. It also needs the OAuth client ID and secret of the Yazio app. They are the same for everyone, but they belong to YAZIO, so they are not in this repository. The pair that works with API v22 is in [yazio-api-specification](https://github.com/dornsebastian/yazio-api-specification) (`spec/components/schemas/OAuthTokenRequest.yaml`). If YAZIO changes them, update the settings; no new image is needed.

Each sync fetches the last 3 days again (they may still change) and any missing days. Days you edit further back in the app reach the page only with a full refresh (see [🧰 Maintenance](#-maintenance)).

## ⌚ Fitbit / Google Health

Optional. With a Fitbit or Pixel Watch the page also shows workouts (heart rate by zone, splits, route, elevation), sleep stages, resting heart rate, HRV, breathing rate, blood oxygen, skin temperature, the calories Fitbit counts, active zone minutes and VO2 max with Fitbit's rating (Fitbit's cardio fitness estimate). The Training card shows the training load: the zone minutes of the last 7 days against the last 28, as a plain ratio.

The Training card has three tabs: **Overview** (this week, training load, VO2 max), **Personal bests** and **Routes**; Sleep & Recovery has **Sleep** (scores and stages), **Heart** (heart rate and HRV) and **Patterns**. The page remembers the tab you picked.

**Lifts.** Under **Personal bests** you note your results in lifts such as Power Clean, Strict Press or Split Jerk: weight, reps (1 for a one-rep max), date and an optional note, with **Add result**. Each lift and rep count keeps its heaviest result as the best, with the gain over the one before; selecting it shows every result and the training weights from 50 to 95 % of the one-rep max (estimated from a 3- or 5-rep max with Epley's formula); selecting a weight shows the plates for each side of a 20, 15 or 10 kg bar. The window also charts the results over time and shows each one as a multiple of your bodyweight that week (from the Yazio weigh-ins). Everyone with `view.health` sees them, `edit.lifts` writes them. They live in `app.db`, so they are backed up.

**Benchmark workouts.** Next to the lifts, **New workout** creates a workout you do again and again: a famous one like Fran, Cindy or Murph (typing its name fills in the rest) or one your box repeats. It has a name, how it is scored (for time, rounds and reps, reps or weight) and the workout as written; each result then gets a date, an optional note and **Scaled** when you did it lighter or easier, which keeps its own best. A workout for time can have a time cap: a result that didn't finish in it is noted as the reps done and ranks below every finished time. A result can belong to the Fitbit workout it was done in: open the workout (day window → **Activities**), pick the benchmark workout under **Benchmark workout** (or create it there) and add the result. It takes the workout's date, the day's activity row shows the WOD's name, and in the WOD's history the result links back to its workout. How a workout is scored can't change once it has results. Its window charts the results (faster is up for times). Same scopes as lifts.

**Bests elsewhere.** A lift or workout whose best got better in the last 7 days shows as a chip on the Today card. Each result shows that morning's Readiness, and the tab compares the mornings of all your new bests with every morning in that time. The weekly review gets the week's results, with new bests marked, so the coach can mention them. The day window lists what was noted that day under **Bests**, search finds lifts and workouts by name, and the export adds `lifts`, `benchmark_results` and `benchmark_workouts` tables.

**Calendar.** The Training card's **Calendar** tab shows the last year (as much as the screen fits) with every day trained, darker the longer the workouts were, walks aside; a day with a noted result counts too, and a dot marks a new best. Below it: days trained, per week, the longest run of weeks with training and the days with a new best. A day opens its window.

Scores computed in the Fitbit app (readiness, sleep score, cardio load) aren't in the API and aren't imitated. Eumetria has **scores of its own** instead, each from 0 to 100, with a breakdown in the day's Sleep tab:

- **Sleep:** duration against your sleep goal (50 points), time asleep in bed (15), deep and REM share (15) and how regular the night was compared with the last 14 (20).
- **Recovery:** HRV, resting and sleeping heart rate and breathing rate against your own last 30 days. A usual morning scores about 80, each usual swing better or worse about 10 points. It needs 14 days of data first. A skin temperature clearly above Fitbit's baseline caps it at 60 and says so.
- **Readiness:** half Recovery, 30 % Sleep, 20 % training load. 90 and up: ready for a hard session; below 75: take it easy.

The sleep goal is under **Settings → Google → Scores** (default 8 h). The formulas are in [`internal/scores`](internal/scores/scores.go).

**One-time setup in the [Google Cloud console](https://console.cloud.google.com/):**

1. Create a project and enable the **Google Health API**.
2. **Google Auth Platform → Branding:** an app name (without "Google" or "Fitbit"), your e-mail, and your domain under **Authorized domains**. No logo (a logo needs Google's verification).
3. **Audience → Publish app**, so the status is **In production**. In "Testing", refresh tokens expire after 7 days.
4. **Clients → Create client → Web application**, with `https://eumetria.example.com/api/google/callback` as **Authorized redirect URI**, character for character. **Settings → Google** shows the address to use.

Put the client ID and secret into **Settings → Google**, press **Connect Google** and allow every access. The token is stored in `data/google-token.json` and the running container picks it up at its next sync. If the logs say `google: not authorized`, Google revoked the token (password change, access removed, or six months unused): authorize again.

## 🤖 Weekly review with Claude

With an API key in **Settings → Weekly review**, the first sync after a calendar week has Claude write a short review of it: nutrition, training, sleep and recovery, compared with the week before, in English and German. It's written once per week and stored, about $0.10 per week with Claude Opus 5.5. Claude sees all of the data; who reads the review is up to the `view.review` scope.

The Anthropic API is prepaid: buy credit at [console.anthropic.com](https://console.anthropic.com) and leave auto-reload off, so nothing more can be charged. Enter what you bought under **Credit bought**; the page warns admins when it covers fewer than 4 more reviews.

## 💾 Backups

Every night at 03:30 the app writes an archive to `/var/backups/eumetria`, keeping 14 days: the synced history, photos, pictures, images in notes, the Google token and the database. **Settings → Backups** changes the time and how long they are kept, shows the last runs and has **Back up now**. It doesn't run during a sync.

**With a passphrase** (set it in the same tab, and keep a copy in your password manager) archives are encrypted. Then they can also go to **Google Drive**: enable the **Google Drive API** in the Google Cloud project, turn it on here and save, use **Connect again** under Settings → Google to allow it, and **Test connection**. The app can only see and delete the files it made there. Without a passphrase nothing is uploaded, and local archives are readable by root on the server.

`SETTINGS_KEY` isn't in the archives. Keep it with the passphrase: a restored server needs it to read its secrets, or they have to be typed in again, and the authenticator apps set up again.

**Restore** (`<name>` is the archive, from the server or downloaded from Drive into `/var/backups/eumetria`). An archive ending in `.enc` is decrypted first; it asks for the passphrase and tells you which one:

```sh
cd /srv/eumetria
docker compose run --rm -it eumetria -decrypt-backup /backups/<name>.tar.gz.enc
docker compose down
mv data data.old
tar -xzf /var/backups/eumetria/<name>.tar.gz
chown -R eumetria:eumetria data && chmod 700 data
docker compose up -d
rm /var/backups/eumetria/<name>.tar.gz    # the decrypted copy
```

## 🧰 Maintenance

All commands run on the server in `/srv/eumetria`.

| Task | How |
|---|---|
| Logs | `docker compose logs -f --tail 100` |
| Sync now | the refresh button, or `curl -X POST -H "Authorization: Bearer <token>" https://eumetria.example.com/api/sync` with a `sync` token |
| Fetch the whole history again (after editing old days in Yazio) | the button's menu → **Full refresh**, or the same `curl` with `/api/sync?all=1` |
| Sync state | `curl -H "Authorization: Bearer <token>" https://eumetria.example.com/api/status` |
| Change a setting | account menu → **Settings**; it applies at once |
| Update | `docker compose pull && docker compose up -d` (the cron does this nightly) |
| Roll back | `image: …:<commit>` in `compose.yaml`, then `docker compose up -d` |
| Authorize Fitbit again | **Settings → Google → Connect again** |
| Raw Yazio / Fitbit answers for a day | `docker compose run --rm eumetria -debug-day 2026-10-13` / `… -debug-google 2026-10-13 -google-token /data/google-token.json` |

**Write all weekly reviews again** (e.g. after a prompt change): **Settings → Weekly review**. It runs between syncs and shows how far it got.

`/healthz` turns unhealthy when no sync has succeeded for three intervals, e.g. because Yazio changed its API or your password. Point an uptime monitor at `https://eumetria.example.com/healthz` to hear about it.

## 🛠️ Local development

```sh
# the page with generated sample data (Fitbit data included); sign in as demo / demo
# at http://localhost:8080 (passkeys don't work on 127.0.0.1)
go run ./cmd/eumetria -demo -serve :8080 -site site -data /tmp/eumetria-demo
# -site serves the page from the folder: edits show on reload. Its own -data
# keeps the demo account out of your real data folder. With -demo, notes and
# lifts can be written without a second factor; admin rights still need one.

# the page from plain files, without a server or sign-in: -out writes all of
# the data next to it (any static file server works)
go run ./cmd/eumetria -demo -out site/stats.json

# real data (settings as in the server's env file)
set -a; . path/to/eumetria.env; set +a
go run ./cmd/eumetria -serve :8080 -site site

# tests: Go, the page's logic (Node 22+), and a headless-Chrome smoke test
go vet ./... && go test ./...
node --test tests/*.test.js
go run ./cmd/eumetria -demo -out site/stats.json && node scripts/smoke.js site

# known vulnerabilities in the code's call paths, with the newest Go patch
# release (as CI does; an older local Go reports the standard library's)
GOTOOLCHAIN=go1.26.11 go run golang.org/x/vuln/cmd/govulncheck@latest ./...

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
