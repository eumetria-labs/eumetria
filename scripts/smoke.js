// Smoke test: serves a site directory, loads it in headless Chrome for a few
// views and fails if the page did not render or threw a JavaScript error.
//
//   node scripts/smoke.js site     (site/stats.json, site/routes/ and site/days/ must exist, e.g. from -demo)
//
// With a password-protected stats.json (STATS_PASSWORD) it only checks that
// the page shows the password prompt.
//
// The page marks <html data-rendered="true"> after rendering and
// <html data-error="…"> on any uncaught error.

import { createServer } from 'node:http';
import { readFile } from 'node:fs/promises';
import { spawn, spawnSync } from 'node:child_process';
import { extname, join, normalize, resolve } from 'node:path';

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
const sealed = stats.sealed === 1;
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
const views = sealed ? [{ hash: 'range=7', expect: ['data-locked="true"'] }] : [
  { hash: 'range=7', expect: ['id="today-ring"', 'class="card tile"', 'class="coach-bubble teaser"'] },
  { hash: 'range=all', expect: ['id="chart-energy"'] },
  // German (the footer's language switch), page and a day.
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
  if (!sealed && !dom.includes('data-rendered="true"')) problems.push('page did not finish rendering');
  const err = dom.match(/data-error="([^"]*)"/);
  if (err) problems.push('JavaScript error: ' + err[1]);
  for (const s of v.expect) if (!dom.includes(s)) problems.push('missing ' + s);
  console.log(`${problems.length ? 'FAIL' : 'ok  '} #${v.hash}${problems.length ? '\n     ' + problems.join('\n     ') : ''}`);
  failed += problems.length > 0;
}
server.close();
process.exit(failed ? 1 : 0);

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
