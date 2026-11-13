// The Training card, in three tabs so the page stays short: the overview
// (this week's volume next to the 4-week average, the training load and VO2
// max trends), the personal bests (lifts and benchmark workouts people note
// themselves, lifts.js and wods.js, and the records of the Fitbit workouts), and every GPS route of the
// selected range on one map.

import { actLabel, addDays, avgOf, dfLong, dfMonth, dfShort, fmt, fmtDistance, fmtDuration, fmtRatio, iso, parse, signed } from './format.js';
import { LOAD_ZONE, bestDays, loadLabel, personalBests, speedLabel, trainingDays, trainingSummary, trainingWeek, wodBestDays, workoutIcon, workoutName } from './health.js';
import { $, h, svg, nameCell, panelTabs, showTip, hideTip } from './dom.js';
import { timeChart, bucketTitle, fadeArea, smoothPath, valuePill, resizer } from './charts.js';
import { state, store } from './state.js';
import { hasLifts, renderLifts } from './lifts.js';
import { hasWods, renderWods } from './wods.js';
import { openDay } from './sheet.js';
import { loadLeaflet, loadRoute, mapControls, openWorkout } from './workout.js';
import { t, tn } from './i18n.js';
import { legendToggles } from './legend.js';
import { openYear } from './yearreview.js';
import { yearsOf } from './year.js';

export function renderTraining(s) {
  const card = $('#training-card');
  const days = state.data.days;
  const workouts = days.some((d) => d.workouts?.length);
  card.hidden = !workouts && !hasLifts() && !hasWods();
  if (card.hidden) return;
  const routes = days.some((d) => d.workouts?.some((w) => w.route_src || w.route?.length > 1));
  panelTabs($('#training-tabs'), [
    workouts && ['overview', t('Overview'), '📈'],
    ['bests', t('Personal bests'), '🏆'],
    ['calendar', t('Calendar'), '📅'],
    routes && ['routes', t('Routes'), '🗺️'],
  ].filter(Boolean), store.get('trainingTab'), (key) => store.set('trainingTab', key));
  if (workouts) {
    renderWeek(days);
    renderTrends(s);
  } else {
    $('#training-sub').textContent = t('Personal bests you noted');
  }
  renderLifts();
  renderWods();
  $('#workout-bests-part').hidden = !workouts;
  renderBests(days);
  renderCalendar();
  if (routes) renderRoutes(s.rows);
}

// After lifts or benchmark workouts changed without new data: only the list is drawn again
// (the map keeps its zoom), unless the card appears or goes.
export function refreshLifts(s) {
  const hide = !state.data.days.some((d) => d.workouts?.length) && !hasLifts() && !hasWods();
  if (hide !== $('#training-card').hidden) return renderTraining(s);
  if (!hide) {
    renderLifts();
    renderWods();
    renderCalendar();
  }
}

/* ---------- this week ---------- */

function renderWeek(days) {
  const { cur, prev } = trainingWeek(days, state.today);
  const items = [
    [t('Workouts'), cur.sessions, prev?.sessions, (v) => fmt(v, v % 1 ? 1 : 0), ''],
    [t('Active time'), cur.dur / 60, prev && prev.dur / 60, (v) => fmtDuration(v), ''],
    [t('Distance'), cur.dist, prev?.dist, (v) => fmtDistance(v), ''],
    [t('Zone minutes'), cur.azm, prev?.azm, (v) => fmt(v), 'min'],
  ];
  $('#training-sub').textContent = tn(cur.sessions, '{n} workout in the last 7 days', '{n} workouts in the last 7 days') +
    (prev ? '' : ' · ' + t('the 4-week average needs 5 weeks of data'));
  $('#training-week').replaceChildren(...items.map(([label, value, before, f, unit]) => {
    const el = h('div', { class: 'stat' });
    el.append(h('div', { class: 'k' }, label));
    const v = h('div', { class: 'v' }, f(value));
    if (unit) v.append(h('small', {}, unit));
    let note = t('Last 7 days');
    if (before != null) {
      note = t('4-week avg {value}', { value: f(before) + (unit ? ' ' + unit : '') });
      if (before > 0) note += ` · ${signed(Math.round((value / before - 1) * 100))} %`;
    }
    el.append(v, h('p', { class: 'note' }, note));
    return el;
  }));
}

/* ---------- trends ---------- */


function renderTrends(s) {
  const load = renderLoad(s), vo2 = renderVO2(s);
  $('#training-trends').hidden = !load && !vo2;
}

// The load ratio per bucket: the day's value, or for a week its last day's
// (the ratio already looks back four weeks, averaging it would blur it).
function renderLoad(s) {
  const { buckets, weekly } = s;
  // Computed on the server from the whole Fitbit history (package scores).
  const all = new Map(state.data.days.filter((d) => d.scores?.load).map((d) => [d.date, d.scores.load]));
  const part = $('#load-part');
  part.hidden = !all.size;
  if (!all.size) return false;
  const vals = buckets.map((b) => {
    for (let i = b.rows.length - 1; i >= 0; i--) {
      const v = all.get(b.rows[i].date);
      if (v) return v;
    }
    return null;
  });
  const ratios = vals.map((v) => v?.ratio ?? null);
  const present = ratios.filter((v) => v != null);
  const last = [...all.values()].at(-1);
  $('#load-sub').textContent = `${fmtRatio(last.ratio)} · ${loadLabel(last.ratio)}`;
  const lo = Math.min(0.5, ...present), hi = Math.max(1.6, ...present);
  const on = legendToggles($('#load-part .legend'), 'load', () => renderLoad(s));
  timeChart($('#chart-load'), {
    aria: t('Training load: active zone minutes of the last 7 days compared with the last 28 days'),
    kind: 'line',
    empty: !present.length,
    n: buckets.length,
    dates: buckets.map((b) => b.start),
    y: [Math.max(0, lo - 0.1), hi + 0.1],
    yFmt: (v) => fmt(v, 1),
    draw(g, ctx) {
      const { x, y, right } = ctx;
      // The usual range as a band behind the line.
      const y0 = y(LOAD_ZONE[1]), y1 = y(LOAD_ZONE[0]);
      if (on('zone')) g.append(svg('rect', { x: x(0) - ctx.step / 2, y: y0, width: right - x(0) + ctx.step / 2, height: y1 - y0, class: 'load-zone-band' }));
      if (!on('ratio')) return;
      g.append(svg('path', { d: smoothPath(ratios, x, y), class: 'mark-line s1', pathLength: 1 }));
      if (buckets.length <= 45) ratios.forEach((v, i) => { if (v != null) g.append(svg('circle', { cx: x(i), cy: y(v), r: 3, class: 'mark-dot s1' })); });
      const li = ratios.findLastIndex((v) => v != null);
      if (li >= 0) valuePill(g, x(li), y(ratios[li]), fmtRatio(ratios[li]), ctx);
    },
    tip(i) {
      const v = vals[i];
      if (!v) return { title: bucketTitle(buckets[i], weekly), rows: [{ value: t('Not enough data'), label: '' }] };
      return {
        title: bucketTitle(buckets[i], weekly),
        rows: [
          { cls: 's1', value: fmtRatio(v.ratio), label: loadLabel(v.ratio) },
          { value: `${fmt(v.acute)} min`, label: t('zone min/week, last 7 days') },
          { value: `${fmt(v.chronic)} min`, label: t('zone min/week, last 28 days') },
        ],
        y: v.ratio,
      };
    },
    onPick: weekly ? null : (i) => buckets[i].rows[0],
    pickTab: 'activities',
  });
  return true;
}

// Fitbit's rating of the VO2 max for age and sex.
const VO2_LEVELS = {
  POOR: t('Poor'), FAIR: t('Fair'), AVERAGE: t('Average'), GOOD: t('Good'), VERY_GOOD: t('Very good'), EXCELLENT: t('Excellent'),
};
const vo2Level = (r) => VO2_LEVELS[r?.vo2_level] || '';

// Fitbit's VO2 max estimate: averaged per bucket; the latest rating and the
// change over the range next to the title.
function renderVO2(s) {
  const { buckets, weekly, complete } = s;
  const has = state.data.days.some((d) => d.vo2max);
  $('#vo2-part').hidden = !has;
  if (!has) return false;
  const vals = buckets.map((b) => avgOf(b.rows, 'vo2max'));
  const present = vals.filter((v) => v != null);
  const measured = complete.filter((r) => r.vo2max);
  const latest = state.data.days.findLast((d) => d.vo2max);
  const parts = [vo2Level(latest)];
  if (measured.length >= 2) parts.push(t('{change} in this range', { change: signed(measured.at(-1).vo2max - measured[0].vo2max, 1) }));
  $('#vo2-sub').textContent = parts.filter(Boolean).join(' · ');
  const lo = Math.min(...present), hi = Math.max(...present);
  const pad = Math.max(1, (hi - lo) * 0.2);
  timeChart($('#chart-vo2'), {
    aria: t('VO2 max estimated by Fitbit'),
    kind: 'line',
    empty: !present.length,
    n: buckets.length,
    dates: buckets.map((b) => b.start),
    y: [lo - pad, hi + pad],
    draw(g, ctx) {
      const { x, y } = ctx;
      // Fitbit skips days; the line bridges the gaps, the dots mark estimates.
      const line = bridge(vals);
      fadeArea(g, 'fade-chart-vo2', 's2', line, ctx);
      g.append(svg('path', { d: smoothPath(line, x, y), class: 'mark-line s2', pathLength: 1 }));
      if (buckets.length <= 45) vals.forEach((v, i) => { if (v != null) g.append(svg('circle', { cx: x(i), cy: y(v), r: 3, class: 'mark-dot s2' })); });
      const li = vals.findLastIndex((v) => v != null);
      if (li >= 0) valuePill(g, x(li), y(vals[li]), fmt(vals[li], 1), ctx, 's2');
    },
    tip(i) {
      if (vals[i] == null) return { title: bucketTitle(buckets[i], weekly), rows: [{ value: t('Not measured'), label: '' }] };
      // The rating of the bucket's last estimate.
      const level = vo2Level(buckets[i].rows.findLast((r) => r.vo2max));
      return {
        title: bucketTitle(buckets[i], weekly),
        rows: [
          { cls: 's2', value: `${fmt(vals[i], 1)} ml/kg/min`, label: (weekly ? t('avg') + ' ' : '') + 'VO2 max' },
          level && { value: level, label: t('Fitbit rating') },
        ],
        y: vals[i],
      };
    },
    onPick: weekly ? null : (i) => buckets[i].rows[0],
    pickTab: 'activities',
  });
  return true;
}

// Fills the gaps between known values linearly; nothing before the first or
// after the last.
function bridge(vals) {
  const out = [...vals];
  let prev = -1;
  vals.forEach((v, i) => {
    if (v == null) return;
    if (prev >= 0) for (let k = prev + 1; k < i; k++) out[k] = vals[prev] + ((vals[i] - vals[prev]) * (k - prev)) / (i - prev);
    prev = i;
  });
  return out;
}

/* ---------- personal bests ---------- */

const BEST_VALUE = {
  dur: (w) => fmtDuration(w.dur / 60),
  energy: (w) => `${fmt(w.energy)} kcal`,
  azm: (w) => `${fmt(w.azm)} min`,
  elev: (w) => `${fmt(w.elev)} m`,
};

function renderBests(days) {
  const bests = personalBests(days);
  $('#training-bests').replaceChildren(...(bests.length ? bests.map((b) => {
    const { w } = b;
    const value = BEST_VALUE[b.key]?.(w) ?? fmtDistance(w.dist);
    const detail = [dfShort.format(parse(b.date)), b.key.startsWith('dist:') ? speedLabel(w) : actLabel(workoutName(w.type))].filter(Boolean).join(' · ');
    const row = h('button', { class: 'day-item workout-row', type: 'button', 'aria-label': t('{label}: {value}, {detail}. Show workout', { label: b.label, value, detail }) });
    row.append(nameCell(b.label, false, workoutIcon(w.type)), h('span', { class: 'amt' }, detail), h('span', { class: 'val' }, value), h('span', { class: 'chev', 'aria-hidden': 'true' }, '›'));
    row.addEventListener('click', () => showWorkout(b.date, w.id));
    return row;
  }) : [h('p', { class: 'empty-tab' }, t('No workouts yet'))]));
}

// Opens the day and its workout, as a link to #day=…&workout=… would.
function showWorkout(date, id) {
  openDay(date, 'activities');
  const day = state.byDate.get(date);
  if (day) openWorkout(day, id, $('#modal .sheet-close'));
}

/* ---------- routes ---------- */

let map = null, layer = null, pending = null, drawn = 0, unfitted = false;

function fitRoutes() {
  unfitted = !$('#training-map').clientWidth;
  if (!unfitted && layer.getLayers().length) map.fitBounds(layer.getBounds(), { padding: [20, 20], maxZoom: 15 });
}

function renderRoutes(rows) {
  const routes = [];
  for (const r of rows) for (const w of r.workouts || []) if (w.route?.length > 1 || w.route_src) routes.push({ date: r.date, w });
  const empty = !routes.length;
  $('#training-routes-empty').hidden = !empty;
  $('#training-map').hidden = empty;
  $('#training-legend').hidden = empty;
  if (empty) {
    $('#training-routes-sub').textContent = '';
    return;
  }
  const n = routes.length;
  $('#training-routes-sub').textContent = tn(n, '{n} route in this range · select one to open it', '{n} routes in this range · select one to open it');
  pending = routes;
  const host = $('#training-map');
  // Leaflet and the map tiles are only loaded once the map scrolls into view.
  if (!map && !host._watching) {
    host._watching = true;
    new IntersectionObserver((entries, obs) => {
      if (!entries.some((e) => e.isIntersecting)) return;
      obs.disconnect();
      initMap(host);
    }, { rootMargin: '200px' }).observe(host);
  } else if (map) drawRoutes();
}

async function initMap(host) {
  let L;
  try {
    L = await loadLeaflet();
  } catch (err) {
    host.replaceChildren(h('p', { class: 'empty-tab' }, t('The map could not be loaded.')));
    return;
  }
  map = L.map(host, { scrollWheelZoom: false, zoomSnap: 0.25, zoomControl: false });
  mapControls(L, map);
  L.tileLayer('https://tile.openstreetmap.org/{z}/{x}/{y}.png', {
    maxZoom: 19,
    // The page sends no referrer; OpenStreetMap's tile servers need one.
    referrerPolicy: 'strict-origin-when-cross-origin',
    attribution: '© <a href="https://www.openstreetmap.org/copyright" target="_blank" rel="noopener">OpenStreetMap</a>',
  }).addTo(map);
  layer = L.featureGroup().addTo(map);
  // A map drawn in a hidden tab has no size: it fits the routes once shown.
  new ResizeObserver(() => {
    map.invalidateSize();
    if (unfitted && host.clientWidth) fitRoutes();
  }).observe(host);
  drawRoutes();
}

// One color per workout type, in the order of the series colors.
const SERIES = ['--s1', '--s2', '--s3'];

async function drawRoutes() {
  // The tracks load on demand; a newer range wins over a slower older one.
  const run = ++drawn;
  const todo = pending;
  await Promise.all(todo.map(({ w }) => loadRoute(w)));
  if (run !== drawn) return;
  const L = window.L;
  const css = getComputedStyle(document.documentElement);
  const types = [...new Set(todo.map((r) => workoutName(r.w.type)))];
  const color = (type) => css.getPropertyValue(SERIES[types.indexOf(type) % SERIES.length]).trim() || '#2a78d6';
  const lines = [];
  for (const { date, w } of todo) {
    if (!(w.route?.length > 1)) continue;
    const type = workoutName(w.type);
    const line = L.polyline(w.route.map((p) => [p[0], p[1]]), { color: color(type), weight: 3, opacity: 0.85, lineJoin: 'round', type });
    // A node, not a string: Leaflet puts strings in as HTML, and the type can
    // be whatever Google sent (pentest F12).
    line.bindTooltip(h('span', {}, `${actLabel(type)} · ${dfShort.format(parse(date))} · ${fmtDistance(w.dist || 0)}`), { sticky: true });
    line.on('mouseover', () => line.setStyle({ weight: 5, opacity: 1 }));
    line.on('mouseout', () => line.setStyle({ weight: 3, opacity: 0.85 }));
    line.on('click', () => showWorkout(date, w.id));
    lines.push(line);
  }
  // The view fits every route; a type switched off in the legend only
  // leaves the map.
  layer.clearLayers();
  for (const l of lines) layer.addLayer(l);
  fitRoutes();
  $('#training-legend').replaceChildren(...types.map((type, i) => {
    const li = h('li', { 'data-s': type });
    li.append(h('span', { class: 'key line ' + ['s1', 's2', 's3'][i % 3] }), actLabel(type));
    return li;
  }));
  const apply = () => {
    const on = legendToggles($('#training-legend'), 'routes', apply);
    layer.clearLayers();
    for (const l of lines) if (on(l.options.type)) layer.addLayer(l);
  };
  apply();
}

/* ---------- the training calendar ---------- */

// Every day trained over the last year, as wide as the card allows (the
// latest weeks on a narrow screen): a cell darker the longer the day's
// workouts, noted lifts and benchmark workouts counting too, and a dot on
// the days a best got better. A day opens its window.
function renderCalendar() {
  const host = $('#train-cal');
  if (!host) return;
  const { byDate } = state;
  const noted = [...state.lifts.map((e) => e.date), ...state.wods.results.map((r) => r.date)];
  const trained = trainingDays(state.data.days, noted, [...bestDays(state.lifts), ...wodBestDays(state.wods.workouts, state.wods.results)]);
  const end = parse(state.today || state.data.days.at(-1).date);
  const draw = () => {
    const W = host.clientWidth;
    if (!W) return;
    const left = 30, top = 18, gap = 3;
    // A year where it fits with cells of at least 9 px, else what fits.
    const weeks = Math.max(8, Math.min(53, Math.floor((W - left) / 12)));
    const start = addDays(end, -((end.getUTCDay() + 6) % 7) - (weeks - 1) * 7);
    const size = Math.max(6, Math.min(18, Math.floor((W - left) / weeks) - gap));
    const H = top + 7 * (size + gap);
    const root = svg('svg', { viewBox: `0 0 ${W} ${H}`, height: H, role: 'img', 'aria-label': t('Calendar of the days trained') });
    const axis = svg('g', { class: 'axis' });
    [t('Mon'), t('Wed'), t('Fri')].forEach((l, k) => axis.append(svg('text', { x: 0, y: top + (k * 2) * (size + gap) + size - 1 }, l)));
    root.append(axis);
    let lastMonth = -1, lastLabelX = -99;
    for (let d = start; d <= end; d = addDays(d, 1)) {
      const col = Math.floor((d - start) / 864e5 / 7), row = (d.getUTCDay() + 6) % 7;
      const x = left + col * (size + gap), y = top + row * (size + gap);
      if (d.getUTCMonth() !== lastMonth && x - lastLabelX > 28) {
        axis.append(svg('text', { x, y: 11 }, dfMonth.format(d)));
        lastMonth = d.getUTCMonth();
        lastLabelX = x;
      }
      const date = iso(d), day = trained.get(date);
      const tier = !day ? 'none' : day.min >= 60 ? 't3' : day.min >= 30 ? 't2' : 't1';
      const cell = svg('rect', { x, y, width: size, height: size, rx: Math.min(4, size / 4), class: `cell cal-cell ${tier}` });
      cell.dataset.date = date;
      root.append(cell);
      if (day?.pb) {
        const dot = svg('circle', { cx: x + size / 2, cy: y + size / 2, r: Math.max(1.5, size / 5), class: 'cal-pb' });
        dot.dataset.date = date;
        root.append(dot);
      }
    }
    root.addEventListener('pointermove', (e) => {
      const date = e.target.dataset?.date;
      if (!date) return hideTip();
      const day = trained.get(date);
      const rows = !day ? [{ value: t('Rest day'), label: '' }] : [
        day.sessions ? { value: fmtDuration(day.min), label: tn(day.sessions, '{n} workout', '{n} workouts') } : null,
        day.noted ? { value: day.pb ? t('New best') : t('Results noted'), label: '' } : null,
      ].filter(Boolean);
      showTip(e.clientX, e.clientY, dfLong.format(parse(date)), rows, byDate.has(date) ? t('Click for day details') : '');
    });
    root.addEventListener('pointerleave', hideTip);
    root.addEventListener('click', (e) => {
      const date = e.target.dataset?.date;
      if (date && byDate.has(date)) { hideTip(); openDay(date, trained.get(date)?.noted ? 'bests' : 'activities'); }
    });
    host.replaceChildren(root);
    const from = iso(start), sum = trainingSummary(trained, from, iso(end));
    $('#train-cal-sub').textContent = t('since {date}', { date: dfShort.format(start) + (start.getUTCFullYear() !== end.getUTCFullYear() ? ` ${start.getUTCFullYear()}` : '') });
    $('#train-cal-summary').textContent = [
      tn(sum.days, '{n} day trained', '{n} days trained'),
      t('{n} per week', { n: fmt(sum.perWeek, 1) }),
      sum.longestWeeks > 1 ? t('{n} weeks in a row at most', { n: sum.longestWeeks }) : '',
      sum.pbs ? tn(sum.pbs, '{n} day with a new best', '{n} days with a new best') : '',
    ].filter(Boolean).join(' · ');
  };
  host._draw = draw;
  resizer.observe(host);
  draw();
  // Each year with data in review.
  $('#train-cal-years').replaceChildren(...yearsOf(state.data.days).map((y) => {
    const b = h('button', { class: 'pill-btn', type: 'button' }, t('{year} in review', { year: y }));
    b.onclick = () => openYear(y, b);
    return b;
  }));
}
