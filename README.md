<p align="center"><img src="site/icons/mark.svg" alt="" height="64"></p>

<h1 align="center">Eumetria</h1>

<p align="center"><b>Your nutrition, training and sleep in one self-hosted dashboard.</b></p>

## ✨ What is it?

Eumetria puts the data of the apps you already use onto one page on your own server. From [Yazio](https://www.yazio.com) it takes calories, macros, meals, foods, recipes, weight, steps and activities. Optionally it adds what a Fitbit or Pixel Watch records through the [Google Health API](https://developers.google.com/health): workouts with heart rate and route, sleep stages and overnight measurements.

- 📊 Charts for calories, macros, weight (with a forecast to your goal), movement, sleep and recovery
- 💪 Macro sources per food, and protein per meal against 0.4 g per kg of bodyweight (30 g without a weigh-in)
- 🥦 Alcohol, fibre, minerals and vitamins from the foods Yazio knows, against the DGE/ÖGE/SGE reference values, with how much of what you ate the data cover
- 📍 Phases and events: shade a cut or bulk and mark a competition on the charts, with each phase's numbers
- 🎉 A year in review: the year's numbers, new bests and favourite foods, as a picture to share too
- 🔍 Search across your whole history, food and activity histories, a sortable data table
- 🏋️ Workout details: heart rate by zone, splits, route map, elevation
- 🤖 An optional weekly review and a coach chat written by Claude, and an MCP server so Claude Code (or another AI app) can answer questions about your data
- 🥫 Nutri-Score and NOVA badges from Open Food Facts
- 📤 Export as CSV/JSON (with workout notes, lifts, benchmark workouts, phases, weekly reviews and goal changes), a printable report with scores and training, share cards for Instagram & co.
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

Everything else is set in the page, under the account menu (your avatar, top right) → **Settings**, after the first sign-in.

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

**Workout notes** belong to Fitbit workouts: open a workout and write Markdown (headings, bold, italic, lists, inline code, code blocks between ``` lines, `http(s)` links; no HTML). Paste an image with Ctrl+V or use **Add image**: it's uploaded at once, stripped of its metadata and embedded where the cursor is; images from other sites aren't shown. An image no note embeds is deleted after a day and left out of backups; each user can have at most 20 of them waiting and upload 30 an hour, and the folder takes at most 2 GB. A long note shows its start with a **Show more** button. Notes show in the workout, in the activity's history and in the search, and you can add one to a workout's share card (text only). They live in `app.db`, their images in `note-images/` in the data folder, so both are backed up. A note whose workout has gone from Google stays until an admin deletes it (**Settings** → **Users & Roles** → **Notes**).

**The page** has a sidebar on wide screens: the parts of the page (Today, Nutrition, Body, Sleep, Training, Foods, Data), with the one in view marked, your watch's battery and you at its foot; between 900 and 1100 px wide it keeps only the icons. The cards are grouped under the same names, in the order you chose. On phones a tab bar at the bottom shows one part at a time (Today, Nutrition, Body & Sleep, Training, More; More has search, the year in review, Profile and Settings), and card tabs whose words don't fit show their icons. **+ Log** (a round button above the tab bar on phones) logs a lift, a benchmark workout's result, a phase or event, or a note on today's workout, for whoever may write them.

**The account menu** (your avatar, top right) is short: **Profile**, **Settings**, the style (light, dark or **Auto**, as your device), the language, and **Sign out**, which asks whether to sign out on this device or everywhere. Profile and Settings open one page with a switch between the two: a sidebar of sections on wide screens, a list to tap through on phones. **Profile** → **Account** holds a picture, shown in the menu and next to your notes, your name, e-mail and Instagram handle, and your sex and date of birth (for the nutrients' reference values, and a greeting on your birthday), all optional and visible only to you except the picture. The weekly review compares with the reference values of the oldest admin account, whose data the site shows. Settings that keep values show a bar with **Discard** and **Save** once something changed; password fields have an eye to show what you typed (saved secrets are never shown again).

**Customize Dashboard** (**Profile**) sets the order of the cards and hides the ones you don't need. It's stored with your account, so it's the same on every device; **Reset** brings back the page's own order. On a wide screen two half-width cards next to each other (Weight, Movement, Most logged foods, Top activities) share a row; one alone takes the full width.

**Phases & Events** (**Profile**) note a stretch of days with a purpose (**Cut**, **Bulk**, **Maintain** or your own, with an optional name; leave the end empty while it runs) and one-day events such as a competition, also ahead. The weight, calorie, macro, score and training load charts shade the phases and mark the events with 🏁, and a chart's tooltip names them. The page sums up each phase: its days, the weight change (7-day average in the first week against the last), average calories and protein on logged days, and training days per week. A line under the Today card's title shows the running phase ("Maintain · day 71") and the next event within 90 days, and opens it. The weekly review knows them too (the coach judges calories and weight against a cut or bulk and mentions an event ahead), the printable report lists them with their numbers, and the export adds a `phases` table. Whoever sees nutrition or health data sees them, `edit.lifts` writes them; they live in `app.db`.

**Check-in.** Once a day, the Today card asks *How do you feel today?*: energy, soreness, stress and mood on five steps from much less to much more (⇊ ↓ ∼ ↑ ⇈, each with a word, green where it's good for you and orange where it isn't; stored as 1 to 5), each optional, a short note, and tags that explain an unusual day (alcohol, sick, travel, late meal, late caffeine, work stress, a warm or noisy room, injury). Afterwards the card shows it in one line, each answer as its coloured symbol ("↑ Energy · ↓ Soreness · ✈️ Travel"); before noon the form can still switch to yesterday, the day window shows and changes any day's, and **+ Log** has **Check-in** too. A day with 10 g of alcohol or more in the foods logged is tagged by itself; untick it and it stays away. Check-ins don't change the scores, but days tagged **sick** or **travel** stay out of the baselines Recovery compares with, so a cold doesn't move what counts as usual afterwards; on a sick day Readiness says *Sick: rest first*. The charts band sick and travel days like phases, Patterns compares the nights after each tag (and after stressful days) with the checked-in days without it, the printable report averages the answers and counts the tags, the export adds a `checkins` table, and the weekly review sees them as context. `view.health` reads them, the `edit.checkin` permission writes them (with a second factor, like notes and lifts); they live in `app.db`.

**Sleep timing.** The Sleep tab opens with three numbers about *when* you sleep, for the range picked:
- **Regularity:** the Sleep Regularity Index (0–100: how likely you are asleep or awake at the same minute as 24 hours before, from the hypnograms; 80 or more is regular), with how much bedtime and wake time vary (±min) and its course week by week.
- **Chronotype:** the middle of your sleep on free nights (those before a day off), corrected for sleep caught up after the week (MSFsc), named on a five-step scale from clear early bird to clear night owl (adults lie around 4:00 on average; 2:30–3:30 is a slight early bird, 5:00–6:00 a slight night owl) and shown on a small scale with that average.
- **Social jetlag:** how far that middle moves between work and free nights.

**Insights.** A card of its own (in the sidebar after Training; under **More** on phones) with two tabs, both for the range picked:
- **Goals:** how often each goal was met. Protein at or above its goal; calories by phase (in a cut at or below the goal, in a bulk at or above it, otherwise within ±10 %, the goal including the day's activity); steps; the sleep goal; and days without alcohol, once alcohol was ever logged. Days without data don't count, today is left out, and there are no streaks. The weekly review gets the same counts for its week.
- **Explore:** pick any two daily measures from nutrition (macros, alcohol, fibre, sodium, magnesium), movement and training load, sleep (duration, deep, REM, bedtime, wake time, mid-sleep), recovery, the scores, the check-in and the weight trend. Then pick when the second one is read (the same day, the day after or two days after; sleep belongs to the morning it ended, so "the day after" a day is the night that followed). A scatter plot shows one dot per day with a trend line, Spearman's r (−1 to 1, in words from "no relation" to "strong"), the number of days and the period. Hover a dot for its values; click it to open the day. Sick and travel days can be left out. It needs at least 10 days with both, and says plainly that it is a pattern, not proof. The Patterns tab links to it.

**Retest hints.** A lift or benchmark workout without a result for six months gets a "Retest?" mark in **Personal bests** and one chip on the Today card ("Time to retest Overhead Squat? 8 months ago", or "3 to retest" for several, which opens the Personal bests). **Profile → Customize Dashboard → Retest hints** sets the months (3 to 24) or turns it off, per account.

**Training sessions.** Optional, for logging a session set by set without turning Eumetria into a training app; personal bests and benchmark workouts stay as they are.
- **The form:** **+ Log → Session** takes a date (ticked to that day's Fitbit workout when there is just one), a name, the effort (RPE 1–10), a note and exercises. Each exercise is a lift (names from your lifts, earlier sessions and the common ones) with sets of reps × kg, marked **W** for a warm-up; 0 kg is bodyweight. **+ Set** copies the last set, and **Repeat last time** fills in the sets of the last session with that lift.
- **New bests:** a work set that beats the lift's best (heavier, or as heavy for more reps) is offered as **Save as PB?**, ticked. Saving the session then adds the best, linked to the session. Deleting the session leaves the best, without the link.
- **Where they show:**
  - The Training card's **Sessions** tab lists them by week, with work sets and volume (kg × reps, warm-ups left out) per lift.
  - The lift window shows the **Last sessions** with that lift, and its strength over time also uses the work sets.
  - The calendar counts session days as trained, and the workout window shows a linked session's sets.
  - The weekly review gets the week's sessions, and the export adds `training_sessions` and `training_sets` tables.
  - The printable report counts work sets and volume, and the year in review counts the work sets logged.
- **Access:** `view.health` reads them and `edit.lifts` writes them (the API is `/api/training/sessions`). They live in `app.db`.

**Withings scale (pre-staged).** For a Withings body composition scale. It is built and tested against Withings' documented API shapes, but is switched off until an app is registered and connected.
- **Setup:** register an app at developer.withings.com (Public Health Data API, with a Withings account of its own). Put its client ID and secret under **Settings → Withings**, register the callback URL shown there, and **Connect Withings**. The token is kept sealed in the settings table (it needs `SETTINGS_KEY`); Withings rotates it with every refresh, and each new one is saved before use.
- **Syncing:** each sync fetches the weigh-ins since the last one (`lastupdate`); there is no webhook. On a day with a weigh-in, the scale's first weigh-in of the day counts as the day's weight instead of Yazio's, and Yazio stays the fallback, so the history before the scale is unchanged. A failing sync warns like a failing Fitbit sync, and an expired grant asks to connect again.
- **On the page:**
  - The Weight card gets a **Body composition** tab: body fat over time with its 7-day average, and fat, lean, muscle, water and bone mass and the visceral fat index, each with its change over the range.
  - Each phase's weight change is split into fat and lean mass.
  - A weigh-in this morning is a chip on the Today card.
  - The export adds a `body` table and `weight_source` in `days`; the report and the weekly review get the body composition.
- **Unconfirmed API details:** points the readable documentation left open are marked "(verify)" in `internal/withings` and checked at the live test.

Each tile explains itself behind an ⓘ. A day off is one of your usual weekdays off (**Profile → Sleep**, Saturday and Sunday unless you change them), a public holiday of your state (Germany's 16 states or Austria, computed from the rules; only holidays of the whole state), a day of a **Vacation** (a kind under **Phases & Events**, which may be planned ahead) or a day tagged **Day off** in the check-in. **Profile → Sleep** also holds the sleep goal in hours and minutes, and only the site's owner sees it: the sleep shown is theirs. Shifts that change from week to week don't fit a weekday list; the tags and vacations can cover the exceptions. The chronotype needs at least four free and four work nights in the range, and nights tagged travel don't count. The times are the nights' local clock times, so a time-zone or daylight-saving change doesn't skew them. The export adds each night's mid-sleep and whether it was free or work to the `sleep` table, plus a `sleep_timing` table for the range; the weekly review gets the week's regularity and the chronotype of the last four weeks. The page (`site/js/sleeptiming.js`) and the server (`internal/stats/sleeptiming.go`) compute the same numbers, checked against shared cases in `tests/fixtures/sleeptiming.json`.

**Training vs rest days.** The Macros tab compares calories, protein, carbs and fat on training days (a Fitbit workout, a Yazio activity or a noted lift or benchmark result) with rest days over the selected range, next to protein per kg split the same way.

Admins manage all of this in the page: **Settings** → **Users & Roles**. There you add people (with e-mail set up, give their address instead of a password: they get a welcome mail, choose their password through its link, once within three days, and set up a second factor right away if their role needs one; their address then counts as confirmed), give them a role, set a new password for someone who forgot theirs (that also ends their sessions and API tokens), require a second factor (until they set one up they see no data, only their own account settings), disable an account and then delete it for good (what they wrote stays, without an author), edit roles as checkboxes, end anyone's session and read the audit log. Changes need a recent proof of who you are: within 5 minutes of signing in, or the page asks you to confirm it with a passkey, key or code from your app (the password, for an account without a second factor). You can't disable or demote your own account, and the site always keeps one admin. A changed role applies to the user's next request; they stay signed in.

- **Passwords:** at least 12 characters, no common ones, stored as argon2id hashes. Change yours under **Settings** → **Sign-in**; that signs out your other devices and, unless you untick it, ends your API tokens. If you have passkeys that sign in on their own, you can remove them in the same step: do so if someone else may have had your account.
- **Sessions:** "Keep me signed in" lasts until a week without use, a month at most; without it, until the browser closes (12 hours at most). The account menu (your avatar, top right) signs out here or everywhere.
- **What stays on the device:** only with "Keep me signed in" does the page keep your data for offline use. Without it, nothing of the data is stored in the browser, and offline the page waits for the network. Signing out, or the session ending, deletes what was kept and tells the browser to drop its cache of the site. Your photos, routes and year files are checked with the server on every load (cheaply, by ETag) instead of being cached for a year, so a later user of the same browser can't open them.
- **Wrong passwords:** five in 15 minutes lock that account name for 15 minutes from that network (a /24, or a /64 for IPv6), doubling up to a day; twenty from one address lock the address. Wrong codes count the same. A name is never locked for everyone, so nobody can lock you out by typing your name: after more than 10 failures from anywhere, sign-ins for that name from browsers that never signed in to it run one at a time, slowed down (up to 5 seconds), and share 20 tries an hour. Browsers you signed in with before carry a device cookie and aren't affected; a new password makes those cookies worthless. 25 failures within 15 minutes show up in the audit log and, with a confirmed address, in a mail to the account's owner.
- **Busy server:** password checks need memory (19 MiB each), so at most four run at once, and only one image upload is worked on at a time. When more arrive, the extra ones get "The server is busy" (HTTP 503) after a moment instead of piling up; that doesn't count as a wrong password. nginx (`deploy/nginx*.conf`) also allows only a few such requests in progress per address.
- **Audit log:** kept for 180 days, at most 100,000 entries; failed tries have their own limit (30 days, at most 50,000), so a flood of them can't push older security events out. Repeated failed tries from one address count up in one entry. **Settings** → **Audit log** can show only the failed tries, or everything else.
- **Second factors** (**Settings** → **Sign-in**): passkeys (fingerprint, face or PIN, synced by your phone or password manager), security keys such as a YubiKey, an authenticator app, and ten recovery codes for when the others are lost. With one, the password is followed by a second step; a passkey alone signs in without a password, from the button or from the browser's suggestions in the name field. **Admins, roles with `sync` and roles that write (`edit.notes`, `edit.lifts`) need one** before they can sync, open the admin tools or write; a `sync` token stops working while its owner has none. Changing them, making recovery codes or API tokens, and changing the password of an account with a second factor ask you to confirm it's you (a passkey, key or app code; recovery codes don't count). **The first second factor** of an account that needs one is set up with an **enrolment link** (one use, 24 hours): **Settings** → **Users** → **Enrolment link** next to the user, or `-enrol-link <name>` on the command line; `-create-user` and `-reset-second-factors` print one. The password alone isn't enough, so someone who learned it can't make the account theirs by setting up their own factor first. That holds for promotions too: when an account starts to need a second factor (a new role, more scopes for its role, or **Require a second factor**), the factors it set up without an enrolment link are removed, its sessions end and the admin gets a new enrolment link to hand over (also by mail to a confirmed address; `-set-role` and `-create-role` print it). **Sign-in** shows for each factor when, from where and under which role it was set up. Changes to how an account signs in show up as a notice for its user (and as "sign-in changed" in the user list) until they dismiss it; a notice of a new key, app or API token takes a confirmation that it's you to dismiss.
- **E-mail (optional):** with a mail server under **Settings** → **E-mail** (or `SMTP_HOST`, `SMTP_PORT`, `SMTP_TLS`, `SMTP_USER`, `SMTP_PASSWORD`, `SMTP_FROM`), every change to how an account signs in (a key, an app, recovery codes, a token, the password, a refused copied key, many failed sign-ins) also goes by mail to its owner, which someone holding their session can't dismiss. Admins hear when someone gets admin or sync rights or sets up a first second factor, and enrolment links can be sent by mail (**Enrolment link** → **Send by e-mail**, or `-enrol-link <name> -mail`). Mail goes only to an address its user confirmed with a link sent there (**Profile** → **Account**); changing it needs you to confirm it's you, and the old address is told. The connection is always encrypted (STARTTLS on 587 or TLS on 465): a server that offers neither gets nothing. There is no password reset by mail. Mail needs the public URL (**Settings** → **General**): links in mails only ever point there. Changing the mail server's host, port, encryption or user name removes its stored password, so enter it again in the same step. Security alerts have a budget of their own and go first; confirmation mails and invitations share a smaller one (30 an hour, 3 per account). **Use a sender address your mail provider may send for**: with SPF and DKIM for its domain (or a parent domain's `p=reject` DMARC policy), Gmail and GMX refuse mail from anything else after the server accepted it, so the log says "sent" and nothing arrives; a test mail to the provider's own domain can still get through. `-demo` never sends mail.
- **Passkey or security key:** **Add passkey** stores a passkey that signs in on its own; **Add security key** registers a key only as the second step after the password, with nothing stored on it, as most sites do: its PIN once when adding it, then a touch when signing in. Use the latter when a key can't take a passkey, e.g. a YubiKey through the CTAP proxy of Qubes OS.
- **Passkeys only work on the real domain over HTTPS** (and on `http://localhost` for development), not on an IP address.
- **Scripts** use an API token instead of a password (see the table below). It carries scopes, `read` (the data the account's role sees), `sync` (if the role may sync) and/or `mcp` (only the MCP server, see "Ask Claude about your data"), and an expiry, and is shown only once.

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
| Token for a script | **Settings** → **Devices & API tokens**, or `docker compose run --rm eumetria -create-token <name> -token-name cron -scopes sync -expires 365` |

## 🥗 Yazio

The sync logs in with your Yazio account. It also needs the OAuth client ID and secret of the Yazio app. They are the same for everyone, but they belong to YAZIO, so they are not in this repository. The pair that works with API v22 is in [yazio-api-specification](https://github.com/dornsebastian/yazio-api-specification) (`spec/components/schemas/OAuthTokenRequest.yaml`). If YAZIO changes them, update the settings; no new image is needed.

**Nutrients.** Yazio lists alcohol, fibre, minerals and vitamins for most products and recipes. **Calories → Nutrients** shows the range's average a day: alcohol (grams, its kcal, days without, days with 10 g or more), then sodium, potassium, magnesium, calcium, iron, zinc, iodine, vitamins A, B12, C, D, E, folate and fibre. Not every food lists every nutrient, and AI entries list none, so each value says how much of the kcal eaten it covers; below half it is greyed out. With your sex and date of birth under **Profile → Account**, each value is compared with the daily reference values of the German, Austrian and Swiss nutrition societies (DGE/ÖGE/SGE, as published in 2025), as a reference and not a goal; zinc assumes a medium phytate intake, sodium comes from the salt where only that is listed, and alcohol has no reference (the DGE names no safe amount). The day window lists the day's values, **Macro sources** has **Alcohol** and **Vitamins & minerals** tabs, the Patterns tab compares nights after 10 g of alcohol or more with nights after none, and the weekly review, the export (`alcohol_g`, a `nutrients` table) and the report include them. The first sync after the update fetches every day once more, to learn which product each entry was (a few minutes).

Each sync fetches the last 3 days again (they may still change) and any missing days. Days you edit further back in the app reach the page only with a full refresh (see [🧰 Maintenance](#-maintenance)).

## ⌚ Fitbit / Google Health

Optional. With a Fitbit or Pixel Watch the page also shows workouts (heart rate by zone, splits, route, elevation), sleep stages, resting heart rate, HRV, breathing rate, blood oxygen, skin temperature, the calories Fitbit counts, active zone minutes and VO2 max with Fitbit's rating (Fitbit's cardio fitness estimate). The Training card shows the training load: the zone minutes of the last 7 days against the last 28, as a plain ratio.

The Training card has three tabs: **Overview** (this week, training load, VO2 max), **Personal bests** and **Routes**; Sleep & Recovery has **Sleep** (scores and stages), **Heart** (heart rate and HRV) and **Patterns**. The Movement card has **Steps**, **Burned** and **Zone minutes**. The page remembers the tab you picked.

Every chart's legend switches its lines, bars or zones on and off with a tap (one always stays), and the device remembers it per chart; on the routes map it does the same per workout type. The calorie chart starts with what you ate, the goal incl. activity and the base goal; the 7-day average and **Burned (Fitbit)** are a tap away. The Eumetria scores chart shows 7-day averages when a range has more than a week of days; the tooltip has the day's own score too.

**Lifts.** Under **Personal bests** you note your results in lifts such as Power Clean, Strict Press or Split Jerk: weight, reps (1 for a one-rep max), date and an optional note, with **Add result**. Each lift has one best whatever the reps: its heaviest result, and at the same weight the one with more reps (2 × 50 kg beats 1 × 50 kg; 5 × 48 kg doesn't), with the gain over the one before. Selecting it shows every result, the heaviest per rep count (**Rep maxes**), the progress with one line per rep count, and the training weights from 50 to 95 % of the one-rep max (estimated from a 3- or 5-rep max with Epley's formula); selecting a weight shows the plates for each side of a 20, 15 or 10 kg bar. The window also shows each result as a multiple of your bodyweight that week (from the Yazio weigh-ins). A result can belong to the Fitbit workout it was lifted in: open the workout (day window → **Activities**) and **Add lift** under **Lifts**. It takes the workout's date, the day's activity row shows the lift's name, and in the lift's history the result links back to its workout. Results noted before they were linked (lifts and benchmark results on a day with Fitbit workouts, walks aside) show up as a note in the **Personal bests** tab: **Review** lists them, a day with one workout ticked already, a day with several asking which, and **Link** links them. A new result's form likewise ticks the link when its day has exactly one workout. Everyone with `view.health` sees them, `edit.lifts` writes them. They live in `app.db`, so they are backed up.

**Benchmark workouts.** Next to the lifts, **New workout** creates a workout you do again and again: a famous one like Fran, Cindy or Murph (typing its name fills in the rest) or one your box repeats. It has a name, how it is scored (for time, rounds and reps, reps or weight) and the workout as written; each result then gets a date, an optional note and **Scaled** when you did it lighter or easier, which keeps its own best. A workout for time can have a time cap: a result that didn't finish in it is noted as the reps done and ranks below every finished time. A result can belong to the Fitbit workout it was done in: open the workout (day window → **Activities**), pick the benchmark workout under **Benchmark workout** (or create it there) and add the result. It takes the workout's date, the day's activity row shows the WOD's name, and in the WOD's history the result links back to its workout. How a workout is scored can't change once it has results. Its window charts the results (faster is up for times). Same scopes as lifts.

**Bests elsewhere.** A lift or workout whose best got better in the last 7 days shows as a chip on the Today card; several share one chip ("🏆 3 new PBs") that opens the Personal bests tab. The card shows at most three chips, the rest behind a "+n" chip. Each result shows that morning's Readiness, and the tab compares the mornings of all your new bests with every morning in that time. The weekly review gets the week's results, with new bests marked, so the coach can mention them. The day window lists what was noted that day under **Bests**, search finds lifts and workouts by name, and the export adds `lifts`, `benchmark_results` and `benchmark_workouts` tables.

**Strength over time.** A lift's window shows its estimated 1 RM over time with every rep count in it (a 5-rep max counts by Epley's formula), and the same as a multiple of your bodyweight that week. The list of bests shows that multiple too.

**Ready for a PB.** On a morning whose Readiness is 90 or more (ready for a hard session), someone who notes lifts or benchmark workouts sees "⚡ Go for a PB" under the Today card's Readiness ring instead of the usual word.

**Calendar.** The Training card's **Calendar** tab shows the last year (as much as the screen fits) with every day trained, darker the longer the workouts were, walks aside; a day with a noted result counts too, and a dot marks a new best. Below it: days trained, per week, the longest run of weeks with training and the days with a new best. A day opens its window. Under it, **2026 in review** (one button per year) opens the year in review: days trained (per month too), hours, distance, the new bests with where each lift started the year, steps, sleep, weight and the foods eaten most often, each only with the data your role sees. In December and January the Today card offers it as well, and `#year=2026` opens it from a link.

Scores computed in the Fitbit app (readiness, sleep score, cardio load) aren't in the API and aren't imitated. Eumetria has **scores of its own** instead, each from 0 to 100, with a breakdown in the day's Sleep tab:

- **Sleep:** duration against your sleep goal (50 points), time asleep in bed (15), deep and REM share (15) and how regular the night was compared with the last 14 (20).
- **Recovery:** HRV, resting and sleeping heart rate and breathing rate against your own last 30 days. A usual morning scores about 80, each usual swing better or worse about 10 points. It needs 14 days of data first. A skin temperature clearly above Fitbit's baseline caps it at 60 and says so.
- **Readiness:** half Recovery, 30 % Sleep, 20 % training load. 90 and up: ready for a hard session; below 75: take it easy.

The sleep goal is under **Profile → Sleep** of the site's owner (the oldest admin account, whose Fitbit it is; default 8 h; the old site setting still counts until it's set there). The formulas are in [`internal/scores`](internal/scores/scores.go).

**One-time setup in the [Google Cloud console](https://console.cloud.google.com/):**

1. Create a project and enable the **Google Health API**.
2. **Google Auth Platform → Branding:** an app name (without "Google" or "Fitbit"), your e-mail, and your domain under **Authorized domains**. No logo (a logo needs Google's verification).
3. **Audience → Publish app**, so the status is **In production**. In "Testing", refresh tokens expire after 7 days.
4. **Clients → Create client → Web application**, with `https://eumetria.example.com/api/google/callback` as **Authorized redirect URI**, character for character. **Settings → Google** shows the address to use.

Put the client ID and secret into **Settings → Google**, press **Connect Google** and allow every access. The token is stored in `data/google-token.json` and the running container picks it up at its next sync. If the logs say `google: not authorized`, Google revoked the token (password change, access removed, or six months unused): authorize again.

## 🤖 Claude: weekly review and coach

Both live under **Settings → AI** and use the Anthropic API key entered there.

**Weekly review.** The first sync after a calendar week has Claude write a short review of it: nutrition, training, sleep and recovery, compared with the week before, in English and German. It's written once per week and stored, about $0.10 per week with Claude Opus 5.5. Claude sees all of the data; who reads the review is up to the `view.review` scope.

**Coach.** A chat on the dashboard (the speech-bubble button in the top bar, or **More → Coach** on phones). You ask about your data ("How did I sleep this week compared to last week?", "Where does my protein come from?") and Claude looks it up with the same read-only tools as the MCP server before it answers. The answer streams in, with a line for each lookup ("sleep · Nov 9 – Nov 20"). Claude sees only what the asker's role sees.
- Off by default. Switch it on under **Settings → AI → Coach** (backend: Anthropic API).
- Only roles with **Ask the coach** (`use.coach`) can use it; admins always can.
- The model is a setting. Claude Sonnet 5.5 (the default) costs about $0.10 per question, Opus 5.5 about $0.20, and Haiku 5.5 under a cent, for simple questions. Admins see each answer's cost under it.
- The instructions and tools are cached, so follow-up questions in the same chat cost less.
- Chats are their asker's alone, kept in `app.db`, and can be deleted. A follow-up question sends the last 10 questions and answers of the chat along.
- No diagnoses, and the data is treated as data: text in food names or notes is never followed as an instruction.
- **nginx:** a server set up before the coach needs the `location /api/coach` block from [`deploy/nginx.conf`](deploy/nginx.conf). It allows questions up to 16 KB; the server's default 1 KB would refuse longer ones.

The Anthropic API is prepaid: buy credit at [console.anthropic.com](https://console.anthropic.com) and leave auto-reload off, so nothing more can be charged. Enter what you bought under **Credit bought**; the page warns admins when it covers fewer than 4 more reviews.

## 🧠 Ask Claude about your data (MCP)

Eumetria is an [MCP](https://modelcontextprotocol.io) server at `https://eumetria.example.com/api/mcp`. An AI app connected to it can look your data up while it answers: "How did my sleep change since the cut started?", "Where does my sodium come from?", "Compare my squat sessions this month". The tools are read-only: `overview`, `daily_metrics`, `entries`, `food_sources`, `workouts`, `sleep`, `body`, `checkins`, `lifts`, `sessions`, `wods` and `phases`.

0. **nginx:** a server set up before the MCP server came needs the `location = /api/mcp` block from [`deploy/nginx.conf`](deploy/nginx.conf) (or `nginx-tls.conf`); without it, nginx refuses MCP messages over 1 KB with a 413. Then `nginx -t && systemctl reload nginx`.
1. Make a token with **Claude and other AI apps (MCP)** ticked under **Settings → Devices & API tokens** (or `-create-token <name> -token-name claude -scopes mcp`). An `mcp` token works only on `/api/mcp`, not on the rest of the API. It shows what the account's role may see: a token of a role without `view.foods` gets no food names, and without `view.health` no Fitbit tools.
2. **Claude Code:**
   ```sh
   claude mcp add --transport http --scope user eumetria https://eumetria.example.com/api/mcp \
     --header "Authorization: Bearer eum_…"
   ```
   `claude mcp list` should then say `✔ Connected`. In a project's `.mcp.json` the entry is `{"type": "http", "url": "…/api/mcp", "headers": {"Authorization": "Bearer ${EUMETRIA_TOKEN}"}}`.
3. **claude.ai, Claude Desktop and the phone apps** connect to remote servers through **Customize → Connectors** on claude.ai, which needs OAuth; Eumetria doesn't offer OAuth (yet). Only if your account shows a **Request headers** field there (a beta Anthropic offers to some accounts) can you add `Authorization` with the value `Bearer eum_…`.

Each tool call counts against the token (60 a minute) and is written to the audit log with its arguments, never with its result. Revoking the token cuts the app off at once. What the app does with the answers depends on the app; with Claude, it's covered by your Claude plan's terms, not by this server.

## 💾 Backups

Every night at 03:30 the app writes an archive to `/var/backups/eumetria`, keeping 14 days: the database (`app.db`, with the synced history, accounts, notes, lifts and settings), photos, pictures, images in notes and the Google token. Archives from before November 2026 keep the synced history in `history.json`: restored, the app imports it into the database at the next start and renames it to `history.json.imported-<date>` (a file older than the database's last sync is only renamed). **Settings → Backups** changes the time and how long they are kept, shows the last runs and has **Back up now**. It doesn't run during a sync.

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
| Change a setting | account menu → **Settings**; it applies once saved |
| Update | `docker compose pull && docker compose up -d` (the cron does this nightly) |
| Roll back | `image: …:<commit>` in `compose.yaml`, then `docker compose up -d` |
| Authorize Fitbit again | **Settings → Google → Connect again** |
| Raw Yazio / Fitbit answers for a day | `docker compose run --rm eumetria -debug-day 2026-10-13` / `… -debug-google 2026-10-13 -google-token /data/google-token.json` |

**Write all weekly reviews again** (e.g. after a prompt change): **Settings → AI**. It runs between syncs and shows how far it got.

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
# (also loads views as a 360 and 320 px phone and fails when the page is
# wider than the screen)
go vet ./... && go test ./...
node --test tests/*.test.js
go run ./cmd/eumetria -demo -out site/stats.json && node scripts/smoke.js site
# the sample data depends only on the time it is made for: -now repeats a
# CI run (its log prints the time), and the page's clock follows the data
go run ./cmd/eumetria -demo -now 2026-11-17T13:24:00Z -out site/stats.json && node scripts/smoke.js site

# how fast the page renders on a slow phone (390 px, CPU 4× slower; median of 5)
node scripts/perf.js site

# the account menu and the Profile / Settings view against a demo server
# (makes an admin with an authenticator app and a viewer; about 2 minutes)
go build -o /tmp/eumetria ./cmd/eumetria && node scripts/prefs-check.js /tmp/eumetria

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

# Community

Join the Discord Community and start connecting.

[![Join Discord](https://dcbadge.limes.pink/api/server/https://discord.gg/dqJ7JPdsQ4)](https://discord.gg/dqJ7JPdsQ4)

## ⚠️ Unofficial API

This is a personal project. It is not affiliated with, endorsed by or supported by YAZIO. It uses YAZIO's private, undocumented API, which is subject to YAZIO's terms of service and can change or stop working at any time. "YAZIO" is a trademark of its owner and is used here only to say which service the tool works with.

The endpoints come from community work: [Yazio-Exporter](https://github.com/funmelon64/Yazio-Exporter), [juriadams/yazio](https://github.com/juriadams/yazio), [yazio-api-specification](https://github.com/dornsebastian/yazio-api-specification), [saganos/yazio_public_api](https://github.com/saganos/yazio_public_api), [fliptheweb/yazio-mcp](https://github.com/fliptheweb/yazio-mcp), [go-yazio](https://github.com/dumpsabfuck/go-yazio), [itzptk/yazio-go-cli](https://github.com/itzptk/yazio-go-cli), [aleksandr-bogdanov/yazio-exporter](https://github.com/aleksandr-bogdanov/yazio-exporter) and [bserem/yazio-mcp-pyton](https://github.com/bserem/yazio-mcp-pyton). No code from them is included; this repository is MIT-licensed (see `LICENSE`). Food data from [Open Food Facts](https://world.openfoodfacts.org) is available under the Open Database License.
