---
title: Docker Compose and nginx
description: 'Set Eumetria up on a Linux server with Docker Compose, nginx and HTTPS, as its own user, with updates from the page.'
---

This is the setup Eumetria is built for: one container behind nginx, with HTTPS from Let's Encrypt, running as its own unprivileged user. It takes about half an hour.

**You need** a Linux server with Docker Compose v2, nginx and certbot, and a domain pointing at it. The examples use `eumetria.example.com` and host port `8090`. Templates for every file are in [`deploy/`](https://github.com/eumetria-labs/eumetria/tree/main/deploy) in the repository. Run everything as root.

## 1. User and folders

The container runs as its own user, which can only write its data folder:

```sh
adduser --system --group --no-create-home --home /nonexistent --shell /usr/sbin/nologin eumetria
install -d -o root -g root -m 755 /srv/eumetria
install -d -o eumetria -g eumetria -m 700 /srv/eumetria/data
install -d -o eumetria -g eumetria -m 700 /var/backups/eumetria
```

:::caution
Don't add `eumetria` to the `docker` group: its members are effectively root.
:::

## 2. Compose file and settings

Copy [`deploy/compose.yaml`](https://github.com/eumetria-labs/eumetria/blob/main/deploy/compose.yaml) to `/srv/eumetria/compose.yaml` and put in the user's ids. Then create the settings file from [`deploy/eumetria.env.example`](https://github.com/eumetria-labs/eumetria/blob/main/deploy/eumetria.env.example):

```sh
cd /srv/eumetria
sed -i "s/\"UID:GID\"/\"$(id -u eumetria):$(id -g eumetria)\"/" compose.yaml
install -m 600 -o root -g root /dev/null eumetria.env
nano eumetria.env          # paste the example and fill it in
docker compose config      # check what gets passed in (prints secrets)
```

Keep values in **single quotes**: unquoted, Compose expands `$` and cuts at ` #`. The settings are explained in [Configuration](/eumetria/install/configuration/); at least set `SETTINGS_KEY`, `TZ`, `PUBLIC_URL` and `TRUSTED_PROXY`. Everything else is set in the page after the first sign-in.

The compose file runs the container read-only, without capabilities, with 256 MB of memory, and publishes its port on `127.0.0.1` only: ports Docker publishes bypass `ufw`, so only nginx on this host can reach it.

## 3. Start it

```sh
docker compose pull && docker compose up -d
docker compose logs -f                   # "serving … syncing every 30m0s"
curl -s http://127.0.0.1:8090/healthz    # ok
```

Then create your account. It asks for a password of at least 12 characters (common ones are refused):

```sh
docker compose run --rm -it eumetria -create-user <name> -role admin
```

It prints an **enrolment link**: open it, sign in and set up a passkey or an authenticator app there. An admin needs a second factor, and the first one can only be set up with that link (24 hours, one use; `-enrol-link <name>` makes a new one). With `PUBLIC_URL` set, the whole link is printed. Until an account exists, the page only says how to make one. The [Quick Start](/eumetria/start/quick-start/#4-sign-in-and-set-up-the-second-factor) shows these screens.

Then [connect Yazio](/eumetria/connect/yazio/). The first sync fetches a year of history and every photo, which takes a few minutes.

## 4. nginx and HTTPS

Copy [`deploy/nginx.conf`](https://github.com/eumetria-labs/eumetria/blob/main/deploy/nginx.conf) to `/etc/nginx/sites-available/eumetria.example.com`, put in your domain, enable it and get a certificate:

```sh
ln -s /etc/nginx/sites-available/eumetria.example.com /etc/nginx/sites-enabled/
nginx -t && systemctl reload nginx
certbot --nginx -d eumetria.example.com --redirect
```

The template also limits request sizes and parallel sign-ins per address, and has the blocks the coach (`/api/coach`, 16 KB questions) and the MCP server (`/api/mcp`) need. A server set up before those features came needs those blocks added.

:::tip[If certbot can't edit nginx]
If certbot gets the certificate but fails at "Deploying certificate" (a version mismatch in certbot's packages), only the nginx edit failed. Use [`deploy/nginx-tls.conf`](https://github.com/eumetria-labs/eumetria/blob/main/deploy/nginx-tls.conf) instead and switch the renewal to webroot:

```sh
install -d -m 755 /var/www/letsencrypt
nano /etc/nginx/sites-available/eumetria.example.com      # replace with deploy/nginx-tls.conf
nginx -t && systemctl reload nginx
certbot certonly --webroot -w /var/www/letsencrypt -d eumetria.example.com \
  --cert-name eumetria.example.com --force-renewal --deploy-hook "systemctl reload nginx"
```
:::

## 5. Updates

Nothing to set up: the compose file has the updater container, `eumetria-updater`, which `docker compose up -d` started with the rest. Eumetria checks for new versions once a day; **Settings → Update** shows what's new and installs it with one click, a backup first. Stable updates and security rebuilds install by themselves at 04:00 unless you switch that off. Backups run inside the app; see [Backups and restore](/eumetria/operations/backups/).

The updater is the only container with the Docker socket. It has no ports, only an internal network to Eumetria, and pulls nothing but Eumetria's own images. It runs as root without capabilities, so `/srv/eumetria` and the `.env` it writes there must belong to root, as step 1 sets them up.

The channel is stable (`:1`, the newest 1.x release) by default. To follow every commit on main instead, or to pin one release by hand, see [Updates and releases](/eumetria/operations/updates/).

### Moving a server off the nightly cron

A server set up before 1.0 updates through `/etc/cron.d/eumetria`. To move it to the updater:

```sh
cd /srv/eumetria
ls -ld . .env 2>/dev/null                # owner root; if not: chown root:root . .env
cp compose.yaml compose.yaml.old
# the new deploy/compose.yaml, with your own edits from compose.yaml.old
# (UID:GID, ports, the network) carried over
nano compose.yaml
docker compose config -q && docker compose pull && docker compose up -d
rm /etc/cron.d/eumetria
```

Then **Settings → Update → Check now**: the Updater row shows its tag and the host name. If it says "The updater doesn't answer", look at `docker compose logs updater`.

## Optional: Claude Code runner

The coach and the weekly review can run on your own Claude subscription through a second container, `claude-runner`. It's in the same compose file under the `claude` profile; see [Claude: review and coach](/eumetria/connect/claude/#claude-code-your-subscription).
