// The Training card: this week's volume next to the 4-week average, the
// personal bests, and every GPS route of the selected range on one map.

import { actLabel, dfShort, fmt, fmtDistance, fmtDuration, parse, signed } from './format.js';
import { personalBests, speedLabel, trainingWeek, workoutIcon, workoutName } from './health.js';
import { $, h, nameCell } from './dom.js';
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
