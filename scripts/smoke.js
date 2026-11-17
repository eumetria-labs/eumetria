// Smoke test: serves a site directory, loads it in headless Chrome for a few
// views and fails if the page did not render or threw a JavaScript error.
//
//   node scripts/smoke.js site     (site/stats.json, site/routes/ and site/days/ must exist, e.g. from -demo -out)
//
// It serves plain files, as without -serve: the page finds no API (api/me and
// api/stats answer 404), needs no sign-in and reads stats.json instead.
//
// The page marks <html data-rendered="true"> after rendering and
// <html data-error="…"> on any uncaught error.
//
// Then it loads some views as a phone (360 and 320 px wide, through the
// DevTools protocol: a headless window can't be narrower than about 500 px)
// and fails if the page or an open window is wider than the screen.

import { createServer } from 'node:http';
import { mkdtemp, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { spawn, spawnSync } from 'node:child_process';
import { extname, join, normalize, resolve } from 'node:path';

// Whatever goes wrong outside a view's checks (Chrome not starting, the
// DevTools port, a page that never answers) still says why it failed.
for (const ev of ['uncaughtException', 'unhandledRejection']) {
  process.on(ev, (err) => {
    console.error(`FAIL smoke test aborted (${ev}): ${err?.stack || err}`);
    process.exit(1);
  });
}

const root = resolve(process.argv[2] || 'site');
const TYPES = {
  '.html': 'text/html', '.js': 'text/javascript', '.css': 'text/css', '.json': 'application/json',
  '.webmanifest': 'application/manifest+json', '.png': 'image/png', '.jpg': 'image/jpeg', '.woff2': 'font/woff2', '.txt': 'text/plain',
};

const server = createServer(async (req, res) => {
  const path = normalize(decodeURIComponent(new URL(req.url, 'http://x').pathname)).replace(/^(\.\.[/\\])+/, '');
  try {
    const body = await readFile(join(root, path.endsWith('/') ? path + 'index.html' : path));
    res.writeHead(200, { 'content-type': TYPES[extname(path)] || 'application/octet-stream' });
    res.end(body);
  } catch {
    res.writeHead(404).end();
  }
});
await new Promise((ok) => server.listen(0, '127.0.0.1', ok));
const base = `http://127.0.0.1:${server.address().port}/`;

const chrome = [process.env.CHROME_BIN, 'google-chrome', 'chromium', 'chromium-browser']
  .find((c) => c && spawnSync(c, ['--version']).status === 0);
if (!chrome) {
  console.error('No Chrome found; set CHROME_BIN.');
  process.exit(2);
}

const stats = JSON.parse(await readFile(join(root, 'stats.json'), 'utf8'));
const day = stats.days?.findLast((d) => d.foods?.length || d.activities?.length)?.date;
// A Fitbit workout with heart rate, if the data has one (the demo does).
const wday = stats.days?.findLast((d) => d.workouts?.some((w) => w.hr?.length));
const workout = wday?.workouts.find((w) => w.hr?.length);
// And one with a GPS route, which loads from its own file.
const gday = stats.days?.findLast((d) => d.workouts?.some((w) => w.route_src));
const gps = gday?.workouts.find((w) => w.route_src);
// A recipe entry, which opens a window with its ingredients.
const rday = stats.days?.findLast((d) => d.foods?.some((f) => f.r && stats.recipes?.[f.r]));
const recipe = rday?.foods.find((f) => f.r && stats.recipes?.[f.r]);
// A food, which opens its window with the whole history.
const food = stats.days?.findLast((d) => d.foods?.length)?.foods[0].n;
// An activity title as the page shows it, from the rendered card (the name
// depends on a linked Fitbit workout).
const activity = stats.days?.some((d) => d.activities?.length)
  ? (await dump(base + 'index.html#range=30')).match(/id="top-activities"[\s\S]*?class="txt">([^<]+)</)?.[1].replace(/&amp;/g, '&')
  : null;
// The first day of the oldest archive file (the older days live in days/).
const oldDay = stats.archive?.length ? JSON.parse(await readFile(join(root, stats.archive[0]), 'utf8')).days?.find((d) => d.foods?.length)?.date : null;
const views = [
  { hash: 'range=7', expect: ['id="today-ring"', 'class="card tile"', 'class="coach-bubble teaser"'] },
  { hash: 'range=all', expect: ['id="chart-energy"'] },
  // German ("#lang=de" in a link), page and a day.
  { hash: 'range=30&lang=de', expect: ['<html lang="de"', 'Tagesdetails', 'Meistgegessen'] },
  { hash: `range=30&lang=de&day=${day}`, expect: ['<dialog class="dlg sheet-overlay"', 'Vorheriger Tag'] },
  { hash: `range=30&day=${day}`, expect: ['class="sheet"', '<dialog class="dlg sheet-overlay"'] },
  ...(workout ? [
    { hash: 'range=30', expect: ['id="recovery-card"', 'mark-bar st-deep', 'Most calories', 'class="q-bar"'] },
    { hash: `range=30&day=${wday.date}&workout=${workout.id}`, expect: ['class="wo-panel"', 'class="hr-line"', 'class="wo-zones"'] },
  ] : []),
  ...(recipe ? [{ hash: `range=30&day=${rday.date}&tab=${recipe.m}&recipe=${recipe.r}`, expect: ['recipe-row', 'class="rc-photo"', 'rc-producer', 'class="q-badge'] }] : []),
  ...(food ? [{ hash: `range=30&food=${encodeURIComponent(food)}`, expect: ['aria-labelledby="hs-title"', 'class="hs-months"', 'hbar-link'] }] : []),
  ...(activity ? [{ hash: `range=30&activity=${encodeURIComponent(activity)}`, expect: ['aria-labelledby="hs-title"', 'class="hs-months"', 'hs-row'] }] : []),
  ...(oldDay ? [{ hash: `range=all&day=${oldDay}`, expect: ['class="sheet"', '<dialog class="dlg sheet-overlay"'] }] : []),
  ...(gps ? [{ hash: `range=7&day=${gday.date}&workout=${gps.id}`, expect: ['class="wo-panel"', 'class="chart wo-elev"', 'leaflet-interactive'] }] : []),
];

let failed = 0;
for (const v of views) {
  const dom = await dump(base + 'index.html#' + v.hash);
  const problems = [];
  if (!dom.includes('data-rendered="true"')) problems.push('page did not finish rendering');
  const err = dom.match(/data-error="([^"]*)"/);
  if (err) problems.push('JavaScript error: ' + err[1]);
  for (const s of v.expect) if (!dom.includes(s)) problems.push('missing ' + s);
  console.log(`${problems.length ? 'FAIL' : 'ok  '} #${v.hash}${problems.length ? '\n     ' + problems.join('\n     ') : ''}`);
  failed += problems.length > 0;
}
failed += await phones([
  'range=7', 'range=30&lang=de', 'range=all',
  `range=30&lang=de&day=${day}`,
  ...(workout ? [`range=30&day=${wday.date}&workout=${workout.id}`] : []),
  ...(food ? [`range=30&food=${encodeURIComponent(food)}`] : []),
]);
server.close();
process.exit(failed ? 1 : 0);

// phones loads each view at phone widths and reports what sticks out past
// the screen's right edge: the innermost such elements, which hold the
// content that doesn't fit. It returns the number of failed views.
async function phones(hashes) {
  const profile = await mkdtemp(join(tmpdir(), 'smoke-'));
  const p = spawn(chrome, ['--headless=new', '--no-sandbox', '--disable-gpu', '--disable-dev-shm-usage',
    '--remote-debugging-port=0', `--user-data-dir=${profile}`, 'about:blank'], { stdio: 'ignore' });
  let failures = 0;
  try {
    const cdp = await connect(profile);
    await cdp.send('Page.enable');
    for (const width of [360, 320]) {
      await cdp.send('Emulation.setDeviceMetricsOverride', { width, height: 800, deviceScaleFactor: 2, mobile: true });
      for (const hash of hashes) {
        // Through a blank page: a change of the hash alone doesn't reload.
        await cdp.load('about:blank');
        await cdp.load(base + 'index.html#' + hash);
        const res = await cdp.eval(`new Promise((ok) => {
          const t0 = Date.now();
          const check = () => {
            if (document.documentElement.dataset.rendered !== 'true' && Date.now() - t0 < 10000) return setTimeout(check, 100);
            // Charts and windows draw a moment after the page. A layout still
            // settling is measured again a little later before it counts.
            const measure = () => {
              const W = document.documentElement.clientWidth;
              // Inside something in the window that clips (a map: Leaflet's
              // panes are always wider) content may be wider. The search stops
              // at the window's scrolling body, whose overflow-y: auto makes
              // overflow-x auto too, and so doesn't count what the dialog
              // around it clips.
              const clipped = (el) => {
                for (let p = el.parentElement; p && p.tagName !== 'DIALOG'; p = p.parentElement) {
                  const cs = getComputedStyle(p);
                  if (['auto', 'scroll'].includes(cs.overflowY)) return false;
                  if (['hidden', 'clip'].includes(cs.overflowX)) return true;
                }
                return false;
              };
              const wide = [document.documentElement, ...document.querySelectorAll('dialog[open], dialog[open] *')]
                .filter((el) => el.scrollWidth > el.clientWidth + 1 && getComputedStyle(el).overflowX === 'visible' && !clipped(el) || el === document.documentElement && el.scrollWidth > W + 1);
              const out = [...document.querySelectorAll('body *')].filter((el) => {
                const r = el.getBoundingClientRect();
                if (!(r.width > 0 && r.right > W + 1) || getComputedStyle(el).position === 'fixed') return false;
                // Inside something that scrolls or clips sideways (the section
                // nav, a map) it is meant to be wider.
                for (let p = el.parentElement; p && p !== document.body; p = p.parentElement) {
                  if (getComputedStyle(p).overflowX !== 'visible') return false;
                }
                return true;
              });
              const inner = out.filter((el) => !out.some((o) => o !== el && el.contains(o)));
              // Each named after the nearest element with an id: where to look.
              const name = (el) => {
                const host = el.parentElement?.closest('[id]');
                const cls = typeof el.className === 'string' && el.className.trim() ? '.' + el.className.trim().split(' ')[0] : '';
                return (host ? '#' + host.id + ' ' : '') + el.tagName.toLowerCase() + (el.id ? '#' + el.id : cls);
              };
              // The innermost too-wide elements in a window, with their widths:
              // what holds the content that doesn't fit.
              const wideInner = wide.filter((el) => !wide.some((o) => o !== el && el.contains(o)))
                .map((el) => name(el) + ' (' + el.scrollWidth + ' px in ' + el.clientWidth + ')');
              return { rendered: document.documentElement.dataset.rendered === 'true', error: document.documentElement.dataset.error || '',
                scroll: document.documentElement.scrollWidth, W, wide: wide.length, inner: [...new Set(inner.map(name))].slice(0, 6), wideInner: [...new Set(wideInner)].slice(0, 6) };
            };
            setTimeout(() => {
              const first = measure();
              if (!(first.scroll > first.W + 1 || first.wide)) return ok(first);
              setTimeout(() => ok(measure()), 1500);
            }, 800);
          };
          check();
        })`);
        const problems = [];
        if (!res.rendered) problems.push('page did not finish rendering');
        if (res.error) problems.push('JavaScript error: ' + res.error);
        if (res.scroll > res.W + 1 || res.wide) problems.push(`wider than the screen (${res.scroll} px on ${res.W} px): ${[...res.inner, ...res.wideInner].join(', ') || 'see the open window'}`);
        console.log(`${problems.length ? 'FAIL' : 'ok  '} ${width} px #${hash}${problems.length ? '\n     ' + problems.join('\n     ') : ''}`);
        failures += problems.length > 0;
      }
    }
    cdp.close();
  } finally {
    p.kill();
    await new Promise((ok) => p.once('exit', ok));
    await rm(profile, { recursive: true, force: true });
  }
  return failures;
}

// connect opens the DevTools protocol on the page of the Chrome whose
// profile is profile: send(method, params) answers its result, eval(js)
// the value of an expression (a promise is awaited).
async function connect(profile) {
  let port;
  for (let i = 0; i < 100 && !port; i++) {
    port = (await readFile(join(profile, 'DevToolsActivePort'), 'utf8').catch(() => '')).split('\n')[0];
    if (!port) await new Promise((ok) => setTimeout(ok, 100));
  }
  if (!port) throw new Error('Chrome did not open its DevTools port');
  const targets = await (await fetch(`http://127.0.0.1:${port}/json`)).json();
  const ws = new WebSocket(targets.find((t) => t.type === 'page').webSocketDebuggerUrl);
  await new Promise((ok, fail) => { ws.onopen = ok; ws.onerror = fail; });
  let id = 0;
  const pending = new Map();
  let loaded = null;
  ws.onmessage = (e) => {
    const m = JSON.parse(e.data);
    if (m.method === 'Page.loadEventFired') loaded?.();
    if (!pending.has(m.id)) return;
    const [ok, fail] = pending.get(m.id);
    pending.delete(m.id);
    m.error ? fail(new Error(m.error.message)) : ok(m.result);
  };
  const send = (method, params = {}) => new Promise((ok, fail) => {
    pending.set(++id, [ok, fail]);
    ws.send(JSON.stringify({ id, method, params }));
  });
  return {
    send,
    // load navigates to url and waits for its load event.
    load: async (url) => {
      const done = new Promise((ok) => { loaded = ok; });
      await send('Page.navigate', { url });
      await done;
    },
    eval: async (expression) => {
      const r = await send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
      if (r.exceptionDetails) throw new Error(r.exceptionDetails.exception?.description || r.exceptionDetails.text);
      return r.result.value;
    },
    close: () => ws.close(),
  };
}

async function dump(url) {
  // Chrome runs asynchronously so this process can keep serving its requests.
  return new Promise((ok, fail) => {
    const p = spawn(chrome, [
      '--headless=new', '--no-sandbox', '--disable-gpu', '--disable-dev-shm-usage',
      '--window-size=1280,900', '--virtual-time-budget=5000', '--dump-dom', url,
    ]);
    let out = '';
    p.stdout.on('data', (b) => { out += b; });
    p.on('error', fail);
    p.on('close', () => ok(out));
  });
}
