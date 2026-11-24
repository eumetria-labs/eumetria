// Service worker: makes the page installable and usable offline.
//
// Page code and the data (api/stats, api/notes) are fetched from the network first, so
// a new deploy or sync shows up immediately; the cache is only a fallback
// when offline. Photos, images in notes, pictures, routes, the older days
// (days/), fonts and icons never change under the same name, so they are served from
// the cache first.
//
// The user's data (the API's, and their files) is kept only when the page
// says this device may: the session is "Keep me signed in" (the offline-ok
// entry in the FLAGS cache, account.js). Otherwise it always comes from the
// network and nothing of it stays on the device once the session ends
// (pentest F11).
//
// Two caches: CACHE holds the page and the data and is replaced with each
// new version of the page (the data only once it was loaded: it needs a
// signed-in user, so it can't be precached); DATA holds the files the data
// points to and outlives page updates. Year files and routes get new names
// when their content changes, so the page tells the worker which files the
// current data uses and the others are deleted. Only answers that are OK
// are kept, never a 401 or the sign-in; the page clears the data when the
// user signs out, gets a 401 or another user signs in (account.js).

const CACHE = 'stats-v74';
const DATA = 'stats-data-v1';
const FLAGS = 'eumetria-flags';
// Photos shared to the installed app (manifest share_target), waiting for
// the page to take them: on this device only, and only until it does.
const SHARED = 'eumetria-shared';
const SHELL = [
  './', 'index.html', 'style.css', 'manifest.webmanifest',
  'js/main.js', 'js/format.js', 'js/insights.js', 'js/state.js', 'js/dom.js', 'js/charts.js', 'js/sheet.js', 'js/lightbox.js', 'js/admin.js',
  'js/health.js', 'js/workout.js', 'js/recovery.js', 'js/device.js', 'js/training.js', 'js/scores.js', 'js/layout.js', 'js/legend.js', 'js/year.js', 'js/yearreview.js', 'js/phase.js', 'js/phases.js', 'js/checkin.js', 'js/sleeptiming.js', 'js/correlate.js', 'js/insightscard.js', 'js/sessions.js', 'js/table.js', 'js/query.js', 'js/recipe.js', 'js/quality.js', 'js/foodquality.js', 'js/reviewtext.js', 'js/weekly.js', 'js/history.js', 'js/coach.js', 'js/unlogged.js',
  'js/today.js', 'js/energy.js', 'js/weight.js', 'js/macros.js', 'js/movement.js', 'js/foods.js', 'js/dialog.js', 'js/sections.js', 'js/search.js', 'js/export.js', 'js/zip.js', 'js/report.js', 'js/snapshot.js', 'js/share.js', 'js/cards.js', 'js/account.js', 'js/security.js', 'js/stepup.js', 'js/manage.js', 'js/notes.js', 'js/lifts.js', 'js/links.js', 'js/wods.js', 'js/markdown.js', 'js/profile.js', 'js/settings.js', 'js/prefs.js', 'js/kit.js', 'js/shell.js', 'js/nutrients.js', 'js/upload.js', 'js/webauthn.js', 'js/sources.js', 'js/i18n.js', 'js/de.js',
  'fonts/inter-latin-var.woff2', 'icons/icon-192.png', 'icons/mark.svg',
];
const IMMUTABLE = /\/(photos|note-images|avatars|routes|days|fonts|icons|vendor)\//;
const PERSONAL = /\/(photos|note-images|avatars|routes|days)\//;
const PRUNABLE = /\/(photos|routes|days)\//;
const OFFLINE_API = /\/api\/(stats|notes)$/;

self.addEventListener('install', (e) => {
  e.waitUntil(caches.open(CACHE).then((c) => c.addAll(SHELL)).then(() => self.skipWaiting()));
});

self.addEventListener('activate', (e) => {
  e.waitUntil(
    caches.keys()
      .then((keys) => Promise.all(keys.filter((k) => k !== CACHE && k !== DATA && k !== FLAGS && k !== SHARED).map((k) => caches.delete(k))))
      .then(() => self.clients.claim()),
  );
});

self.addEventListener('fetch', (e) => {
  const url = new URL(e.request.url);
  if (e.request.method === 'POST' && url.origin === location.origin && url.pathname === new URL('share-target', self.registration.scope).pathname) {
    e.respondWith(receiveShare(e.request));
    return;
  }
  if (e.request.method !== 'GET' || url.origin !== location.origin) return;
  // Of the API only the data is kept for offline use; sign-in, sync and the
  // rest always hit the network.
  const data = OFFLINE_API.test(url.pathname);
  if (url.pathname.includes('/api/') && !data) return;
  // The data is stored under one key, whatever query it was asked with.
  const key = data ? new Request(url.origin + url.pathname) : e.request;
  const mine = data || PERSONAL.test(url.pathname);
  e.respondWith((async () => {
    // The user's data, on a device that may not keep it: the network only.
    if (mine && !(await offlineOk())) return fetch(e.request);
    return IMMUTABLE.test(url.pathname) ? cacheFirst(e.request) : networkFirst(e.request, key);
  })());
});

// receiveShare keeps the images shared to the app (Android's share sheet)
// and opens the page, which puts them into a training's note
// (shell.js takeShared). Nothing goes to the server here.
async function receiveShare(request) {
  const page = new URL('./', self.registration.scope);
  try {
    const form = await request.formData();
    const files = form.getAll('image').filter((f) => f instanceof File && f.type.startsWith('image/')).slice(0, 8);
    const cache = await caches.open(SHARED);
    for (const key of await cache.keys()) await cache.delete(key);
    await Promise.all(files.map((f, i) => cache.put(new URL('shared/' + i, page).href, new Response(f, { headers: { 'content-type': f.type } }))));
    page.hash = 'shared=' + files.length;
  } catch {
    page.hash = 'shared=0';
  }
  return Response.redirect(page.href, 303);
}

// offlineOk reports whether this device may keep the user's data.
async function offlineOk() {
  return !!(await (await caches.open(FLAGS)).match('offline-ok'));
}

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
// the data.
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
