// Smoke test: serves a site directory, loads it in headless Chrome for a few
// views and fails if the page did not render or threw a JavaScript error.
//
//   node scripts/smoke.js site     (site/stats.json, site/routes/ and site/days/ must exist, e.g. from -demo -out)
//
// It serves plain files, as without -serve: the page finds no API (api/me and
// api/stats answer 404), needs no sign-in and reads stats.json instead.
//
// The page marks <html data-rendered="true"> after rendering and
// <html data-error="…"> on any uncaught error. Its clock is set to when the
// data was made (stats.json's generated_at), so a run on sample data from
// -demo -now <time> gives the same result on any day: a failure in CI can be
// repeated locally with the time the run prints.
//
// Then it loads some views as a phone (412 and 360 px wide, through the
// DevTools protocol: a headless window can't be narrower than about 500 px)
// and fails if the page or an open window is wider than the screen.

import { readFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { findChrome, launch, serve } from './browser.js';

// Whatever goes wrong outside a view's checks (Chrome not starting, the
// DevTools port, a page that never answers) still says why it failed.
for (const ev of ['uncaughtException', 'unhandledRejection']) {
  process.on(ev, (err) => {
    console.error(`FAIL smoke test aborted (${ev}): ${err?.stack || err}`);
    process.exit(1);
  });
}

const root = resolve(process.argv[2] || 'site');
const chrome = findChrome();
if (!chrome) {
  console.error('No Chrome found; set CHROME_BIN.');
  process.exit(2);
}

const stats = JSON.parse(await readFile(join(root, 'stats.json'), 'utf8'));
const madeAt = stats.generated_at ? Date.parse(stats.generated_at) : undefined;
console.log(`data made for ${stats.generated_at || 'an unknown time'}${madeAt ? '; the page\'s clock is set to it' : ''}`);
const server = await serve(root);
const page = server.base + 'index.html#';
const browser = await launch(chrome, { now: madeAt });

const desktop = { width: 1280, height: 900 };
// html loads a view on a desktop screen and answers its DOM once it holds
// every string of expect (or after 5 s: what is missing is then reported).
async function html(hash, expect = []) {
  await browser.view(page + hash, desktop);
  return browser.eval(`new Promise((ok) => {
    const want = ${JSON.stringify(expect)}, t0 = performance.now();
    const check = () => {
      const dom = document.documentElement.outerHTML;
      if (want.every((s) => dom.includes(s)) || performance.now() - t0 > 5000) return ok(dom);
      setTimeout(check, 100);
    };
    check();
  })`);
}

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
  ? (await html('range=30', ['id="top-activities"'])).match(/id="top-activities"[\s\S]*?class="txt">([^<]+)</)?.[1].replace(/&amp;/g, '&')
  : null;
// The first day of the oldest archive file (the older days live in days/).
const oldDay = stats.archive?.length ? JSON.parse(await readFile(join(root, stats.archive[0]), 'utf8')).days?.find((d) => d.foods?.length)?.date : null;
const views = [
  { hash: 'range=7', expect: ['id="today-ring"', 'class="card tile"', 'class="coach-bubble teaser"', 'Time to retest Overhead Squat?'] },
  { hash: 'range=all', expect: ['id="chart-energy"'] },
  // Calories → Nutrients and the alcohol line (the demo's Pils).
  { hash: 'range=30&view=nutrition', expect: ['class="hbar nut-row', 'class="alcohol-line"'] },
  // This morning's check-in on the Today card, the demo's cold as a band.
  { hash: 'range=30', expect: ['class="checkin-line"', 'phase-band ph-sick'] },
  // Insights: goal hit rates, and the scatter of the default pair.
  { hash: 'range=90', expect: ['class="hbars goal-hits"', 'class="ex-r"'] },
  // Personal bests: a best of several reps shows its estimated 1 RM.
  { hash: 'range=30', expect: ['class="lift-e1rm"'] },
  // German ("#lang=de" in a link), page and a day.
  { hash: 'range=30&lang=de', expect: ['<html lang="de"', 'Tagesdetails', 'Meistgegessen'] },
  { hash: `range=30&lang=de&day=${day}`, expect: ['<dialog class="dlg sheet-overlay"', 'Vorheriger Tag'] },
  { hash: `range=30&day=${day}`, expect: ['class="sheet"', '<dialog class="dlg sheet-overlay"', 'class="sheet-checkin"'] },
  ...(workout ? [
    { hash: 'range=30', expect: ['id="recovery-card"', 'mark-bar st-deep', 'Most calories', 'class="q-bar"', 'class="timing"', 'class="info-btn"'] },
    { hash: `range=30&day=${wday.date}&workout=${workout.id}`, expect: ['class="wo-panel"', 'class="hr-line"', 'class="wo-zones"'] },
  ] : []),
  ...(recipe ? [{ hash: `range=30&day=${rday.date}&tab=${recipe.m}&recipe=${recipe.r}`, expect: ['recipe-row', 'class="rc-photo"', 'rc-producer', 'class="q-badge'] }] : []),
  ...(food ? [{ hash: `range=30&food=${encodeURIComponent(food)}`, expect: ['aria-labelledby="hs-title"', 'class="hs-months"', 'hbar-link'] }] : []),
  ...(activity ? [{ hash: `range=30&activity=${encodeURIComponent(activity)}`, expect: ['aria-labelledby="hs-title"', 'class="hs-months"', 'hs-row'] }] : []),
  ...(oldDay ? [{ hash: `range=all&day=${oldDay}`, expect: ['class="sheet"', '<dialog class="dlg sheet-overlay"'] }] : []),
  ...(gps ? [{ hash: `range=7&day=${gday.date}&workout=${gps.id}`, expect: ['class="wo-panel"', 'class="chart wo-elev"', 'leaflet-interactive'] }] : []),
];

let failed = 0;
try {
  for (const v of views) {
    const dom = await html(v.hash, v.expect);
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
    // Each part of the page a phone's tab bar shows.
    'range=30&view=nutrition', 'range=30&view=body', 'range=30&lang=de&view=training', 'range=30&view=insights',
    `range=30&lang=de&day=${day}`,
    ...(workout ? [`range=30&day=${wday.date}&workout=${workout.id}`] : []),
    ...(food ? [`range=30&food=${encodeURIComponent(food)}`] : []),
  ]);
} finally {
  await browser.close();
  server.close();
}
if (failed && madeAt) {
  console.log(`\nTo repeat this run: go run ./cmd/eumetria -demo -now ${stats.generated_at} -out <dir>/stats.json (copy site/ to <dir> first), then node scripts/smoke.js <dir>`);
}
process.exit(failed ? 1 : 0);

// phones loads each view at phone widths and reports what sticks out past
// the screen's right edge: the innermost such elements, which hold the
// content that doesn't fit. It returns the number of failed views.
async function phones(hashes) {
  let failures = 0;
  for (const width of [412, 360]) {
    for (const hash of hashes) {
      await browser.view(page + hash, { width, height: 800, mobile: true });
      const res = await browser.eval(`new Promise((ok) => {
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
      })`);
      const problems = [];
      if (!res.rendered) problems.push('page did not finish rendering');
      if (res.error) problems.push('JavaScript error: ' + res.error);
      if (res.scroll > res.W + 1 || res.wide) problems.push(`wider than the screen (${res.scroll} px on ${res.W} px): ${[...res.inner, ...res.wideInner].join(', ') || 'see the open window'}`);
      console.log(`${problems.length ? 'FAIL' : 'ok  '} ${width} px #${hash}${problems.length ? '\n     ' + problems.join('\n     ') : ''}`);
      failures += problems.length > 0;
    }
  }
  return failures;
}
