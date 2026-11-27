#!/usr/bin/env bash
# End-to-end test of in-place updates (PLAN 15b/15d) with real Docker:
#
#   scripts/updater-e2e.sh
#
# A registry on 127.0.0.1:5055 stands in for ghcr.io. deploy/compose.yaml,
# with its paths moved into a temporary folder and the images pointed at
# that registry, runs as the compose project "eumetria-e2e". Then:
#   1. a new release under :1 (Eumetria and the updater): it installs, and
#      the updater replaces itself;
#   2. a broken image under :1: Eumetria never gets healthy, the updater goes
#      back and Eumetria runs the version before;
#   3. the edge channel (:latest): .env gets EUMETRIA_TAG=latest;
#   4. a tag that isn't allowed and a missing token are refused;
#   5. in the browser (scripts/updater-e2e-ui.mjs): an admin clicks the
#      button in Settings → Update, which switches back to stable through
#      Eumetria (backup first), and the page reloads by itself.
# It removes everything it made, also when a step fails (E2E_KEEP=1 keeps
# it for a look).
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
WORK=$(mktemp -d "${TMPDIR:-/tmp}/eumetria-e2e.XXXXXX")
REG=127.0.0.1:5055
PREFIX=$REG/eumetria-labs/eumetria
P=eumetria-e2e
DC=(docker compose --project-name "$P" --project-directory "$WORK" --file "$WORK/compose.yaml")
fails=0

say() { printf '%s\n' "$*"; }
ok() { say "ok   $*"; }
bad() { say "FAIL $*"; fails=$((fails + 1)); }

cleanup() {
  if [ -n "${E2E_KEEP:-}" ]; then
    say "kept: $WORK (project $P); remove with: docker compose -p $P down -v; docker rm -f e2e-registry"
    return
  fi
  "${DC[@]}" down --volumes --remove-orphans >/dev/null 2>&1 || true
  docker rm -f e2e-registry eumetria-updater-handover >/dev/null 2>&1 || true
  # The updater writes .env as root: remove the folder from a container.
  docker run --rm -v "$(dirname "$WORK"):/p" busybox rm -rf "/p/$(basename "$WORK")" >/dev/null 2>&1 || true
  docker rmi -f $(docker image ls -q "$REG/*/*" 2>/dev/null) >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

# ---------- images ----------
say "building the images (version 1.0.0)…"
docker run -d --name e2e-registry -p "$REG:5000" registry:2 >/dev/null
schema=$(ls "$ROOT"/internal/db/migrations/*.sql | wc -l)
docker build -q -t "$PREFIX:1" --build-arg VERSION=1.0.0 --build-arg SCHEMA="$schema" "$ROOT" >/dev/null
docker build -q -t "$PREFIX-updater:1" --build-arg VERSION=1.0.0 -f "$ROOT/deploy/updater/Dockerfile" "$ROOT" >/dev/null
docker push -q "$PREFIX:1" >/dev/null
docker push -q "$PREFIX-updater:1" >/dev/null

# release <tag> <version> [cmd]: a new image of both under tag, made from
# the current one with another version label (and, for Eumetria, another
# command, e.g. one that fails).
# Only the registry gets it: the local tags stay where they were, as on a
# server, where only the updater's pull moves them.
release() {
  local tag=$1 version=$2 cmd=${3:-} repo old
  for repo in "$PREFIX" "$PREFIX-updater"; do
    # Kept under a tag of its own: an image whose last tag moves away is
    # gone by its ID (containerd image store).
    old=$(docker tag "$repo:$tag" "$repo:e2e-keep" 2>/dev/null && echo yes || true)
    { printf 'FROM %s\nLABEL org.opencontainers.image.version=%s\n' "$repo:1" "$version"
      if [ "$repo" = "$PREFIX" ] && [ -n "$cmd" ]; then printf 'CMD %s\n' "$cmd"; fi
    } | docker build -q -t "$repo:$tag" - >/dev/null
    docker push -q "$repo:$tag" >/dev/null
    if [ -n "$old" ]; then docker tag "$repo:e2e-keep" "$repo:$tag" && docker rmi "$repo:e2e-keep" >/dev/null
    else docker rmi -f "$repo:$tag" >/dev/null; fi
  done
}

# ---------- the project ----------
mkdir -p "$WORK/data" "$WORK/backups"
printf "SETTINGS_KEY='%s'\nTZ=Europe/Berlin\n" "$(openssl rand -hex 32)" >"$WORK/eumetria.env"
: >"$WORK/claude-runner.env"
sed -e "s#ghcr.io/eumetria-labs/eumetria#$PREFIX#g" \
    -e "s#/srv/eumetria#$WORK#g" \
    -e "s#/var/backups/eumetria#$WORK/backups#g" \
    -e "s#\"UID:GID\"#\"$(id -u):$(id -g)\"#" \
    -e "s#container_name: #container_name: e2e-#" \
    -e "s#172.30.90.0/24#172.30.99.0/24#" \
    -e 's#127.0.0.1:8090:8080#127.0.0.1:18090:8080#' \
    -e "s#^    container_name: e2e-eumetria-updater\$#&\n    environment: [UPDATER_IMAGE_PREFIX=$PREFIX]#" \
    "$ROOT/deploy/compose.yaml" >"$WORK/compose.yaml"
# As on a server: the project's folder belongs to root (the updater, root
# without capabilities, writes .env there); data and backups to the user
# Eumetria runs as.
docker run --rm -v "$WORK:/w" busybox sh -c 'chown 0:0 /w /w/compose.yaml /w/eumetria.env /w/claude-runner.env && chmod 755 /w' >/dev/null
"${DC[@]}" up --detach --quiet-pull >/dev/null 2>&1 || { "${DC[@]}" up --detach; exit 1; }

health() { docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$1" 2>/dev/null || echo gone; }
version() { docker inspect -f '{{index .Config.Labels "org.opencontainers.image.version"}}' "$(docker inspect -f '{{.Image}}' "$1")"; }
until_healthy() {
  for _ in $(seq 1 90); do [ "$(health "$1")" = healthy ] && return 0; sleep 2; done
  return 1
}
until_healthy e2e-eumetria && ok "Eumetria 1.0.0 runs" || bad "Eumetria didn't get healthy: $(docker logs e2e-eumetria 2>&1 | tail -5)"
until_healthy e2e-eumetria-updater && ok "the updater runs" || bad "the updater didn't get healthy: $(docker logs e2e-eumetria-updater 2>&1 | tail -5)"

TOKEN=$(docker run --rm -v "${P}_updater:/s:ro" busybox cat /s/token)
# Retries while the updater can't be reached: after an update that replaced
# it, the handover recreates its container a few seconds after the job ended.
# (Without --fail, an HTTP answer such as 400 or 401 isn't retried.)
api() { # api METHOD PATH [BODY] [TOKEN]
  docker run --rm --network "${P}_updates" curlimages/curl -s --retry 20 --retry-delay 1 --retry-all-errors -o /dev/stdout -w '\n%{http_code}' \
    -X "$1" -H "Authorization: Bearer ${4-$TOKEN}" -H 'Content-Type: application/json' ${3:+--data "$3"} "http://updater:8080$2"
}
code() { tail -n1 <<<"$1"; }
body() { sed '$d' <<<"$1"; }
# last_job: the newest finished update ("<id> <state>", or "none"), once
# the updater answers (it may just have replaced itself).
last_job() {
  local r
  for _ in $(seq 1 60); do
    r=$(api GET /status)
    if [ "$(code "$r")" = 200 ]; then
      body "$r" | grep -o '"history":\[{"id":"[^"]*"[^}]*"state":"[a-z_]*"' | sed 's/.*"id":"\([^"]*\)".*"state":"\([a-z_]*\)"/\1 \2/' | grep . || echo none
      return
    fi
    sleep 1
  done
  echo unreachable
}
wait_job() {
  local before=$1 now
  for _ in $(seq 1 120); do
    now=$(last_job)
    if [ "$now" != none ] && [ "$now" != unreachable ] && [ "${now%% *}" != "${before%% *}" ]; then echo "${now#* }"; return; fi
    sleep 2
  done
  echo timeout
}

r=$(api GET /status "" "wrong")
[ "$(code "$r")" = 401 ] && ok "a wrong token is refused" || bad "wrong token: $(code "$r")"
r=$(api GET /status)
grep -q '"tag":"1"' <<<"$(body "$r")" && ok "status: tag 1" || bad "status: $r"

# ---------- 1. a new release ----------
release 1 1.1.0
r=$(api POST /check)
grep -q '"newer":true' <<<"$(body "$r")" && ok "check sees 1.1.0" || bad "check: $r"
before=$(last_job)
r=$(api POST /update '{"tag":"1","backup":"e2e"}')
[ "$(code "$r")" = 202 ] && ok "update to 1.1.0 started" || bad "update: $r"
state=$(wait_job "$before")
[ "$state" = done ] && ok "update done" || bad "update ended $state: $(body "$(api GET /status)")"
until_healthy e2e-eumetria
[ "$(version e2e-eumetria)" = 1.1.0 ] && ok "Eumetria runs 1.1.0" || bad "Eumetria runs $(version e2e-eumetria)"
for _ in $(seq 1 30); do [ "$(version e2e-eumetria-updater 2>/dev/null)" = 1.1.0 ] && break; sleep 2; done
until_healthy e2e-eumetria-updater
[ "$(version e2e-eumetria-updater)" = 1.1.0 ] && ok "the updater replaced itself" || bad "updater runs $(version e2e-eumetria-updater): $(docker logs eumetria-updater-handover 2>&1 | tail -3)"

# ---------- 2. a broken release goes back ----------
release 1 1.2.0 '["-no-such-flag"]'
before=$(last_job)
api POST /update '{"tag":"1"}' >/dev/null
state=$(wait_job "$before")
[ "$state" = rolled_back ] && ok "a broken release is rolled back" || bad "broken release ended $state"
until_healthy e2e-eumetria
[ "$(version e2e-eumetria)" = 1.1.0 ] && ok "Eumetria runs 1.1.0 again" || bad "after going back Eumetria runs $(version e2e-eumetria)"

# ---------- 3. edge ----------
release latest main-e2e0000
before=$(last_job)
api POST /update '{"tag":"latest"}' >/dev/null
state=$(wait_job "$before")
[ "$state" = done ] && ok "switched to edge" || bad "edge ended $state"
grep -qx 'EUMETRIA_TAG=latest' "$WORK/.env" && ok ".env has EUMETRIA_TAG=latest" || bad ".env: $(cat "$WORK/.env")"
until_healthy e2e-eumetria
[ "$(version e2e-eumetria)" = main-e2e0000 ] && ok "Eumetria runs the edge build" || bad "edge: Eumetria runs $(version e2e-eumetria)"

# ---------- 4. refused ----------
r=$(api POST /update '{"tag":"1.2.3"}')
[ "$(code "$r")" = 400 ] && ok "a pinned tag is refused" || bad "pinned tag: $r"
r=$(api POST /update '{"tag":"latest"}' "")
[ "$(code "$r")" = 401 ] && ok "no token, no update" || bad "without token: $r"

# ---------- 5. from the page ----------
release 1 1.3.0 # a good :1 again (step 2 left a broken one)
PW=correct-horse-battery-admin
made=$(printf '%s\n' "$PW" | "${DC[@]}" run --rm --no-deps -T eumetria -create-user boss -role admin 2>&1 || true)
enrol=$(grep -o '#enrol=[A-Za-z0-9_-]*' <<<"$made" | head -n1 | cut -d= -f2)
if [ -z "$enrol" ]; then
  bad "making the admin: $made"
elif ! node "$ROOT/scripts/updater-e2e-ui.mjs" http://localhost:18090 boss "$PW" "$enrol"; then
  bad "the update from the page"
  docker logs e2e-eumetria 2>&1 | tail -15
fi

if [ "$fails" -gt 0 ]; then
  say "$fails failed"
  exit 1
fi
say "all passed"
