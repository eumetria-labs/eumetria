// Entry point: loads stats.json, keeps the range in sync and renders every card.

import { addDays, iso, logged, parse, syncEvery, todayISO } from './format.js';
import { $, emoji, hashParam, setHashParam } from './dom.js';
import { DATE_LOCALE, LANG, setLang, t, tn, translatePage } from './i18n.js';
import { state, store } from './state.js';
import { heatmap } from './charts.js';
import { openDay } from './sheet.js';
import { openWorkoutFromHash } from './workout.js';
import { openRecipeFromHash } from './recipe.js';
import { openActivity, openFood } from './history.js';
import { renderToday, renderTiles } from './today.js';
import { renderEnergy, renderMeals, renderWeekdays } from './energy.js';
import { renderInsights, renderWeight } from './weight.js';
import { renderMacros } from './macros.js';
import { renderMovement, renderZoneMinutes } from './movement.js';
import { renderFoods, renderTopActivities } from './foods.js';
import { renderSources } from './sources.js';
import { renderRecovery } from './recovery.js';
import { renderTraining } from './training.js';
import { renderDevice, renderNotices } from './device.js';
import { isSealed, unseal } from './lock.js';
import { initAdmin } from './admin.js';
import { renderTable } from './table.js';
import { renderFoodQuality } from './foodquality.js';
import { renderReview } from './weekly.js';
import { initSections, syncSections } from './sections.js';
import { enableSearch, initSearch } from './search.js';
import { rangeDays, referencedFiles } from './query.js';
import { addSnapButtons } from './snapshot.js';
import { addWeightShare } from './cards.js';

const REFRESH_MS = 10 * 60 * 1000;
const RANGES = ['7', '30', '90', '365', 'all', 'custom'];
const ISO = /^\d{4}-\d{2}-\d{2}$/;

// A link's range wins over the one remembered on this device; a custom
// range brings its dates: "#range=custom&from=2026-09-01&to=2026-09-30".
const hashRange = hashParam('range');
if (RANGES.includes(hashRange)) {
  state.range = hashRange;
  if (hashRange === 'custom') [state.from, state.to] = [hashParam('from'), hashParam('to')];
} else if (RANGES.includes(store.get('range'))) {
  state.range = store.get('range');
  [state.from, state.to] = [store.get('rangeFrom'), store.get('rangeTo')];
}
if (!ISO.test(state.from) || !ISO.test(state.to)) state.from = state.to = null;

// Before anything renders: the static texts in the visitor's language.
translatePage();

// Smoke tests (and curious developers) can read these off <html>.
addEventListener('error', (e) => { document.documentElement.dataset.error = e.message; });

initTheme();
initLanguage();
initRange();
initStickyHeader();
initSections();
initSearch();
initOffline();
initAdmin({ onSynced: load, onIdle: renderUpdated });
load();
setInterval(load, REFRESH_MS);
setInterval(renderUpdated, 60 * 1000);

async function load() {
  const main = $('#main');
  if (state.data) main.classList.add('dim');
  try {
    const res = await fetch('stats.json?t=' + Date.now(), { cache: 'no-store' });
    if (!res.ok) throw new Error(res.status);
    const raw = await res.json();
    const recent = isSealed(raw) ? await unseal(raw) : raw;
    if (state.data && recent.generated_at === state.data.generated_at) return;
    const next = await withArchive(recent);
    const first = !state.data;
    state.animateNext = true;
    state.data = next;
    state.byDate = new Map(next.days.map((d) => [d.date, d]));
    main.classList.remove('loading');
    main.setAttribute('aria-busy', 'false');
    render();
    addSnapButtons();
    addWeightShare();
    document.documentElement.dataset.rendered = 'true';
    // Lets the service worker drop cached photos, routes and year files that
    // this data no longer uses.
    navigator.serviceWorker?.controller?.postMessage({ keep: referencedFiles(next) });
    // A shared link like "#day=2026-10-13" opens that day.
    const day = hashParam('day');
    if (first && day && state.byDate.has(day)) {
      openDay(day, hashParam('tab'));
      openWorkoutFromHash(state.byDate.get(day), hashParam('workout'));
      openRecipeFromHash(state.byDate.get(day), hashParam('recipe'), $('#modal .sheet-close'));
      // A food's history opened from the day: "#day=…&food=Skyr".
      if (hashParam('food')) openFood(hashParam('food'), $('#modal .sheet-close'));
    } else if (first && hashParam('food')) {
      // "#food=Skyr" opens that food's window, "#activity=CrossFit" that activity's.
      openFood(hashParam('food'));
    } else if (first && hashParam('activity')) {
      openActivity(hashParam('activity'));
    }
  } catch (err) {
    if (!state.data) {
      main.classList.remove('loading');
      $('#updated').textContent = t('No data yet: run eumetria to generate stats.json.');
    }
    console.error('loading stats.json failed', err);
  } finally {
    main.classList.remove('dim');
  }
}

// The older days come in files of their own, one per year (stats.json lists
// them). Their names change with their content, so the browser caches them
// and a refresh usually only downloads the recent days.
const archive = new Map(); // file → its days
async function withArchive(data) {
  if (!data.archive?.length) return data;
  const parts = await Promise.all(data.archive.map(async (src) => {
    if (!archive.has(src)) {
      const res = await fetch(src);
      if (!res.ok) throw new Error(`${src}: ${res.status}`);
      const raw = await res.json();
      archive.set(src, (isSealed(raw) ? await unseal(raw) : raw).days);
    }
    return archive.get(src);
  }));
  for (const src of archive.keys()) if (!data.archive.includes(src)) archive.delete(src);
  return { ...data, days: [...parts.flat(), ...data.days] };
}

function initRange() {
  const seg = $('#range');
  const thumb = seg.querySelector('.seg-thumb');
  const custom = $('#custom-range');
  const from = $('#range-from'), to = $('#range-to');
  const sync = () => {
    seg.querySelectorAll('button').forEach((b) => b.setAttribute('aria-checked', String(b.dataset.range === state.range)));
    const on = seg.querySelector('[aria-checked="true"]');
    thumb.style.width = on.offsetWidth + 'px';
    thumb.style.transform = `translateX(${on.offsetLeft}px)`;
    custom.hidden = state.range !== 'custom';
  };
  // Range and custom dates go into the link and are remembered on this device.
  const save = () => {
    const c = state.range === 'custom';
    store.set('range', state.range);
    setHashParam('range', state.range);
    setHashParam('from', c ? state.from : null);
    setHashParam('to', c ? state.to : null);
    if (c) { store.set('rangeFrom', state.from); store.set('rangeTo', state.to); }
  };
  const apply = () => {
    save();
    sync();
    state.animateNext = true;
    if (state.data) render();
  };
  seg.addEventListener('click', (e) => {
    const b = e.target.closest('button');
    if (!b || b.dataset.range === state.range) return;
    // Custom starts from the days on screen, ready to be narrowed down.
    if (b.dataset.range === 'custom' && !state.from && state.data?.days.length) {
      const { start, end } = slice();
      [state.from, state.to] = [iso(start), iso(end)];
    }
    state.range = b.dataset.range;
    apply();
  });
  seg.addEventListener('keydown', (e) => {
    const i = RANGES.indexOf(state.range);
    const next = { ArrowLeft: i - 1, ArrowRight: i + 1 }[e.key];
    if (next == null || next < 0 || next >= RANGES.length) return;
    e.preventDefault();
    const b = seg.querySelector(`[data-range="${RANGES[next]}"]`);
    b.click();
    b.focus();
  });
  // "change" fires once a whole date is entered; empty or partial dates wait.
  for (const input of [from, to]) {
    input.addEventListener('change', () => {
      if (!ISO.test(from.value) || !ISO.test(to.value)) return;
      [state.from, state.to] = [from.value, to.value].sort();
      apply();
    });
  }
  sync();
  // Widths change once the web font arrives and when the header wraps.
  document.fonts?.ready.then(sync);
  new ResizeObserver(sync).observe(seg);
}

// English or German, from the footer. The choice is stored and the page
// reloads in that language.
function initLanguage() {
  for (const b of document.querySelectorAll('.lang-switch button')) {
    b.setAttribute('aria-pressed', String(b.dataset.lang === LANG));
    b.addEventListener('click', () => { if (b.dataset.lang !== LANG) setLang(b.dataset.lang); });
  }
}

function initTheme() {
  const saved = store.get('theme');
  if (saved) document.documentElement.dataset.theme = saved;
  $('#theme').addEventListener('click', () => {
    const dark = matchMedia('(prefers-color-scheme: dark)').matches;
    const cur = document.documentElement.dataset.theme || (dark ? 'dark' : 'light');
    const next = cur === 'dark' ? 'light' : 'dark';
    document.documentElement.dataset.theme = next;
    store.set('theme', next);
  });
}

function initStickyHeader() {
  const top = $('#top');
  new IntersectionObserver(([e]) => top.classList.toggle('stuck', !e.isIntersecting)).observe($('#top-sentinel'));
}

// The service worker makes the page installable and keeps the last data available offline.
function initOffline() {
  if (!('serviceWorker' in navigator) || !(location.protocol === 'https:' || location.hostname === 'localhost')) return;
  navigator.serviceWorker.register('sw.js').catch((err) => console.warn('service worker not registered', err));
}

/* ---------- data slicing ---------- */

function slice() {
  const { data, byDate } = state;
  const days = data.days;
  const first = parse(days[0].date);
  const { start, end } = rangeDays(state.range, first, parse(days.at(-1).date), state.from, state.to);
  const rows = [];
  for (let d = start; d <= end; d = addDays(d, 1)) rows.push(byDate.get(iso(d)) || { date: iso(d) });
  // Today is still being logged, so its totals stay out of averages and trends.
  const complete = rows.filter((r) => r.date !== state.today);

  // The equally long period right before, for "vs previous" deltas.
  const prevRows = [];
  if (state.range !== 'all' && start > first) {
    for (let d = addDays(start, -rows.length); d < start; d = addDays(d, 1)) {
      const r = byDate.get(iso(d));
      if (r) prevRows.push(r);
    }
  }

  // Long ranges are bucketed by week so bars stay readable.
  const weekly = rows.length > 120;
  let buckets;
  if (weekly) {
    const map = new Map();
    for (const r of rows) {
      const d = parse(r.date);
      const key = iso(addDays(d, -((d.getUTCDay() + 6) % 7)));
      if (!map.has(key)) map.set(key, { start: parse(key), rows: [] });
      map.get(key).rows.push(r);
    }
    buckets = [...map.values()];
  } else {
    buckets = rows.map((r) => ({ start: parse(r.date), rows: [r] }));
  }
  return { rows, complete, prevRows, buckets, weekly, start, end };
}

function streak() {
  const { data, byDate } = state;
  let n = 0;
  for (let d = parse(data.days.at(-1).date), first = true; ; d = addDays(d, -1), first = false) {
    const r = byDate.get(iso(d));
    if (r && logged(r)) n++;
    else if (!first) break; // today may not be logged yet
    if (d < parse(data.days[0].date)) break;
  }
  return n;
}

/* ---------- render ---------- */

function render() {
  const { data } = state;
  const title = data.title || 'Eumetria';
  $('#title').textContent = title;
  document.title = title;
  state.today = todayISO(data.timezone);
  const every = syncEvery(data.sync_interval_minutes);
  const fitbit = data.days.some((d) => d.workouts?.length || d.sleep || d.rhr);
  const by = { every: every || t('automatically') };
  $('#foot-sync').textContent = fitbit ? t('Data from YAZIO and Fitbit (Google Health), synced {every}.', by) : t('Data from YAZIO, synced {every}.', by);
  $('#foot-off').hidden = !Object.keys(data.products || {}).length;
  renderUpdated();

  if (!data.days.length) {
    $('#updated').textContent = t('No days synced yet.');
    return;
  }
  const s = slice();
  renderRangeDates(s);
  renderToday();
  renderReview();
  renderTiles(s);
  renderEnergy(s);
  renderMeals(s);
  renderWeekdays(s);
  renderWeight(s);
  renderInsights();
  renderMacros(s);
  renderMovement(s);
  renderZoneMinutes(s);
  heatmap(s);
  renderFoods(s);
  renderTopActivities(s);
  renderSources(s);
  renderFoodQuality(s);
  renderRecovery(s);
  renderTraining(s);
  layoutCards();
  renderTable(s);
  syncSections();
  enableSearch();
  state.animateNext = false;
}

// The custom range's inputs show the days on screen, within the synced days.
function renderRangeDates(s) {
  const days = state.data.days;
  for (const [el, d] of [[$('#range-from'), s.start], [$('#range-to'), s.end]]) {
    el.min = days[0].date;
    el.max = days.at(-1).date;
    el.value = iso(d);
  }
  $('#range-days').textContent = tn(s.rows.length, '{n} day', '{n} days');
}

function renderUpdated() {
  const { data } = state;
  if (!data) return;
  const mins = Math.round((Date.now() - new Date(data.generated_at)) / 60000);
  const rtf = new Intl.RelativeTimeFormat(DATE_LOCALE, { numeric: 'auto' });
  const ago = mins < 60 ? rtf.format(-mins, 'minute') : mins < 48 * 60 ? rtf.format(-Math.round(mins / 60), 'hour') : rtf.format(-Math.round(mins / 1440), 'day');
  const text = t('Updated {ago}', { ago }) + ' · ' + tn(data.days.filter(logged).length, '{n} day logged', '{n} days logged');
  const n = data.days.length ? streak() : 0;
  if (n > 1) $('#updated').replaceChildren(text + ' · ', emoji('🔥'), tn(n, '{n}-day streak', '{n}-day streak'));
  else $('#updated').textContent = text;
  renderDevice(data.device);
  renderNotices(data);
}

// The half-width cards come in pairs of similar height; a card whose
// partner is hidden takes the full row.
const PAIRS = [['#macros-card', '#weight-card'], ['#foods-card', '#top-activities-card']];
function layoutCards() {
  for (const [a, b] of PAIRS.map((p) => p.map((id) => $(id)))) {
    a.classList.toggle('span-2', b.hidden);
    b.classList.toggle('span-2', a.hidden);
  }
}
