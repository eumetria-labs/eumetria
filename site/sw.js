// Service worker: makes the page installable and usable offline.
//
// Page code and stats.json are fetched from the network first, so a new
// deploy or sync shows up immediately; the cache is only a fallback when
// offline. Photos, routes, the older days (days/), fonts and icons never
// change under the same name, so they are served from the cache first.
//
// Two caches: CACHE holds the page and stats.json and is replaced with each
// new version of the page (stats.json only once it was loaded: it needs a
// signed-in user, so it can't be precached); DATA holds the files stats.json points to and
// outlives page updates. Year files and routes get new names when their
// content changes, so the page tells the worker which files the current
// data uses and the others are deleted.

const CACHE = 'stats-v20';
const DATA = 'stats-data-v1';
const SHELL = [
  './', 'index.html', 'style.css', 'manifest.webmanifest',
  'js/main.js', 'js/format.js', 'js/insights.js', 'js/state.js', 'js/dom.js', 'js/charts.js', 'js/sheet.js', 'js/lightbox.js', 'js/lock.js', 'js/admin.js',
  'js/health.js', 'js/workout.js', 'js/recovery.js', 'js/device.js', 'js/training.js', 'js/table.js', 'js/query.js', 'js/recipe.js', 'js/quality.js', 'js/foodquality.js', 'js/reviewtext.js', 'js/weekly.js', 'js/history.js',
  'js/today.js', 'js/energy.js', 'js/weight.js', 'js/macros.js', 'js/movement.js', 'js/foods.js', 'js/dialog.js', 'js/sections.js', 'js/search.js', 'js/export.js', 'js/zip.js', 'js/report.js', 'js/snapshot.js', 'js/share.js', 'js/cards.js', 'js/account.js', 'js/security.js', 'js/webauthn.js', 'js/sources.js', 'js/i18n.js', 'js/de.js',
  'fonts/inter-latin-var.woff2', 'icons/icon-192.png', 'icons/mark.svg',
];
const IMMUTABLE = /\/(photos|routes|days|fonts|icons|vendor)\//;
const PRUNABLE = /\/(photos|routes|days)\//;

self.addEventListener('install', (e) => {
  e.waitUntil(caches.open(CACHE).then((c) => c.addAll(SHELL)).then(() => self.skipWaiting()));
});

self.addEventListener('activate', (e) => {
  e.waitUntil(
    caches.keys()
      .then((keys) => Promise.all(keys.filter((k) => k !== CACHE && k !== DATA).map((k) => caches.delete(k))))
      .then(() => self.clients.claim()),
  );
});

self.addEventListener('fetch', (e) => {
  const url = new URL(e.request.url);
  if (e.request.method !== 'GET' || url.origin !== location.origin) return;
  // The sync API (self-hosted server only) must always hit the network.
  if (url.pathname.includes('/api/')) return;
  // stats.json is requested with a cache-busting query; store it under one key.
  const key = url.pathname.endsWith('/stats.json') ? new Request(url.origin + url.pathname) : e.request;
  e.respondWith(IMMUTABLE.test(url.pathname) ? cacheFirst(e.request) : networkFirst(e.request, key));
});

async function networkFirst(request, key) {
  const cache = await caches.open(CACHE);
  try {
    const res = await fetch(request);
    if (res.ok) cache.put(key, res.clone());
    return res;
  } catch (err) {
    const hit = await cache.match(key, { ignoreSearch: true });
    if (hit) return hit;
    throw err;
  }
}

// Fonts and icons are precached in CACHE; everything else lands in DATA.
async function cacheFirst(request) {
  const hit = await caches.match(request);
  if (hit) return hit;
  const res = await fetch(request);
  if (res.ok) (await caches.open(DATA)).put(request, res.clone());
  return res;
}

// The page posts { keep: ['photos/…', 'routes/…', 'days/…'] } after loading
// stats.json (never while it is still locked, as it has no list then).
self.addEventListener('message', (e) => {
  if (Array.isArray(e.data?.keep)) e.waitUntil(prune(new Set(e.data.keep.map((p) => new URL(p, self.registration.scope).href))));
});

async function prune(keep) {
  const cache = await caches.open(DATA);
  for (const req of await cache.keys()) {
    const url = new URL(req.url);
    if (PRUNABLE.test(url.pathname) && !keep.has(url.origin + url.pathname)) await cache.delete(req);
  }
}
