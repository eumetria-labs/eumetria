// The Training card: this week's volume next to the 4-week average, the
// training load and VO2 max trends, the personal bests, and every GPS route
// of the selected range on one map.

import { actLabel, avgOf, dfShort, fmt, fmtDistance, fmtDuration, fmtRatio, parse, signed } from './format.js';
import { LOAD_ZONE, loadLabel, personalBests, speedLabel, trainingWeek, workoutIcon, workoutName } from './health.js';
import { $, h, svg, nameCell } from './dom.js';
import { timeChart, bucketTitle, fadeArea, smoothPath, valuePill } from './charts.js';
import { state } from './state.js';
import { openDay } from './sheet.js';
import { loadLeaflet, loadRoute, mapControls, openWorkout } from './workout.js';
import { t, tn } from './i18n.js';

export function renderTraining(s) {
  const card = $('#training-card');
  const days = state.data.days;
  card.hidden = !days.some((d) => d.workouts?.length);
  if (card.hidden) return;
  renderWeek(days);
  renderTrends(s);
  renderBests(days);
  renderRoutes(s.rows);
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
      g.append(svg('rect', { x: x(0) - ctx.step / 2, y: y0, width: right - x(0) + ctx.step / 2, height: y1 - y0, class: 'load-zone-band' }));
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

let map = null, layer = null, pending = null, drawn = 0;

function renderRoutes(rows) {
  const routes = [];
  for (const r of rows) for (const w of r.workouts || []) if (w.route?.length > 1 || w.route_src) routes.push({ date: r.date, w });
  const part = $('#training-routes');
  part.hidden = !routes.length;
  if (!routes.length) return;
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
  new ResizeObserver(() => map.invalidateSize()).observe(host);
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
  layer.clearLayers();
  for (const { date, w } of todo) {
    if (!(w.route?.length > 1)) continue;
    const type = workoutName(w.type);
    const line = L.polyline(w.route.map((p) => [p[0], p[1]]), { color: color(type), weight: 3, opacity: 0.85, lineJoin: 'round' });
    line.bindTooltip(`${actLabel(type)} · ${dfShort.format(parse(date))} · ${fmtDistance(w.dist || 0)}`, { sticky: true });
    line.on('mouseover', () => line.setStyle({ weight: 5, opacity: 1 }));
    line.on('mouseout', () => line.setStyle({ weight: 3, opacity: 0.85 }));
    line.on('click', () => showWorkout(date, w.id));
    layer.addLayer(line);
  }
  if (layer.getLayers().length) map.fitBounds(layer.getBounds(), { padding: [20, 20], maxZoom: 15 });
  $('#training-legend').replaceChildren(...types.map((type, i) => {
    const li = h('li');
    li.append(h('span', { class: 'key line ' + ['s1', 's2', 's3'][i % 3] }), actLabel(type));
    return li;
  }));
}
