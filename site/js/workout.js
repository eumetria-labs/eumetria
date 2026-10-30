// The workout window: opened from a workout in the day sheet. Key numbers,
// the heart rate curve over the zone bands, time per zone and, for workouts
// recorded with GPS, the route on a map.

import { actLabel, actName, dfLong, fmt, fmtDistance, fmtDuration, parse } from './format.js';
import { ZONES, speedLabel, workoutIcon, workoutName, zoneOf, zoneRuns, zoneShares } from './health.js';
import { $, h, svg, emoji, showTip, hideTip, setHashParam, textWidth } from './dom.js';
import { openDialog } from './dialog.js';
import { workoutShareButton } from './cards.js';
import { t } from './i18n.js';

let win = null;

export function openWorkout(day, id, returnFocus) {
  const w = (day.workouts || []).find((x) => x.id === id);
  if (!w) return;
  closeWorkout(true);
  setHashParam('workout', id);

  const close = h('button', { class: 'sheet-close', type: 'button', 'aria-label': t('Close workout') }, '×');
  const title = h('h2', { id: 'wo-title' });
  title.append(emoji(workoutIcon(w.type)), ' ', actLabel(workoutName(w.type)));
  const sub = [dfLong.format(parse(day.date)), t('started {time}', { time: w.start })];
  // Guided sessions carry their own title; plain ones just repeat the type
  // in the app's language ("Radfahren").
  if (w.name && actName(w.name) !== workoutName(w.type)) sub.unshift(w.name);
  const head = h('div', { class: 'sheet-head wo-head' });
  const titles = h('div', { class: 'wo-titles' });
  titles.append(title, h('p', { class: 'muted' }, sub.join(' · ')));
  head.append(titles, workoutShareButton(day, w), close);

  const body = h('div', { class: 'wo-body' });
  body.append(stats(w));
  if (w.hr?.length > 1) {
    const sec = section(t('Heart rate'), w.limits ? t('Dotted lines mark your zone limits that day') : '');
    const chart = h('div', { class: 'chart wo-hr' });
    sec.append(chart);
    body.append(sec);
    requestAnimationFrame(() => hrChart(chart, w));
  }
  if (w.zones?.some((z) => z > 0)) body.append(zonesSection(w));
  if (w.route?.length > 1 || w.route_src) {
    const sec = section(t('Route'), fmtDistance(w.dist || 0));
    const map = h('div', { class: 'wo-map', role: 'img', 'aria-label': t('Map of the route: {name}', { name: actLabel(workoutName(w.type)) }) });
    sec.append(map);
    body.append(sec);
    loadRoute(w).then((route) => {
      if (!map.isConnected) return;
      if (!route || route.length < 2) {
        map.replaceWith(h('p', { class: 'empty-tab' }, t('The route could not be loaded.')));
        return;
      }
      if (w.limits && route.some((p) => p[4] > 0)) sec.querySelector('.muted').append(' · ' + t('colored by heart rate zone'));
      const climb = route.some((p) => p[3]) ? h('div', { class: 'chart wo-elev' }) : null;
      if (climb) {
        const esec = section(t('Elevation'), w.elev ? t('{m} m gained', { m: fmt(w.elev) }) : '');
        esec.append(climb);
        sec.after(esec);
      }
      const marker = drawMap(map, w);
      if (climb) elevationChart(climb, w, marker);
    });
  }
  if (w.splits?.length > 1) body.append(splitsSection(w));

  const panel = h('div', { class: 'wo-panel' });
  panel.append(head, body);
  win = openDialog({ cls: 'wo-overlay', labelledBy: 'wo-title', content: [panel], returnFocus, onClose: () => { hideTip(); setHashParam('workout', null); } });
  close.onclick = () => closeWorkout();
  close.focus();
}

export function closeWorkout(instant) {
  win?.close(instant);
  win = null;
}

function section(name, note) {
  const sec = h('section', { class: 'wo-sec' });
  const hd = h('div', { class: 'wo-sec-head' });
  hd.append(h('h3', {}, name));
  if (note) hd.append(h('span', { class: 'muted' }, note));
  sec.append(hd);
  return sec;
}

function stats(w) {
  const items = [
    [t('Duration'), w.dur ? fmtDuration(w.dur / 60) : '–', w.elapsed - w.dur > 90 ? t('{time} total', { time: fmtDuration(w.elapsed / 60) }) : ''],
    [t('Calories'), w.energy ? fmt(w.energy) : '–', 'kcal'],
    [t('Avg heart rate'), w.hr_avg ? fmt(w.hr_avg) : '–', 'bpm'],
    [t('Max heart rate'), w.hr_max ? fmt(w.hr_max) : '–', 'bpm'],
    [t('Zone minutes'), w.azm != null ? fmt(w.azm) : '–', 'min'],
  ];
  if (w.dist) items.push([t('Distance'), fmtDistance(w.dist), ''], [/RUN|WALK|HIK/.test(w.type) ? t('Avg pace') : t('Avg speed'), speedLabel(w), '']);
  if (w.steps) items.push([t('Steps'), fmt(w.steps), '']);
  if (w.elev) items.push([t('Elevation gain'), fmt(w.elev), 'm']);
  const grid = h('div', { class: 'wo-stats' });
  for (const [label, value, unit] of items) {
    const d = h('div');
    const b = h('b', {}, value);
    if (unit && value !== '–') b.append(h('small', {}, unit));
    d.append(h('span', {}, label), b);
    grid.append(d);
  }
  return grid;
}

// Hardest zone first, as the Fitbit app lists them.
function zonesSection(w) {
  const sec = section(t('Heart rate zones'), w.azm ? t('{n} active zone minutes', { n: fmt(w.azm) }) : '');
  const shares = zoneShares(w.zones);
  const list = h('div', { class: 'wo-zones' });
  for (let i = ZONES.length - 1; i >= 0; i--) {
    const z = ZONES[i], s = shares[i];
    const row = h('div', { class: 'wo-zone' });
    const name = h('span', { class: 'wo-zone-name' });
    name.append(h('i', { class: 'key ' + z.cls }), z.label);
    const range = w.limits ? zoneRange(i, w.limits) : '';
    if (range) name.append(h('small', {}, range));
    const track = h('span', { class: 'wo-zone-track' });
    const bar = h('i', { class: z.cls });
    bar.style.width = s.pct + '%';
    track.append(bar);
    row.append(name, track, h('span', { class: 'wo-zone-val' }, `${s.min} min`), h('span', { class: 'wo-zone-pct' }, `${s.pct} %`));
    list.append(row);
  }
  sec.append(list);
  return sec;
}

function zoneRange(i, limits) {
  if (i === 0) return `< ${limits[0]} bpm`;
  if (i === 3) return `≥ ${limits[2]} bpm`;
  return `${limits[i - 1]}–${limits[i] - 1} bpm`;
}

/* ---------- heart rate chart ---------- */

// The heart rate curve over the zone limits, into root (an <svg> or <g>)
// at W × H: axes, zone names in the right margin, pauses, and the line in
// the colour of the zone it is in. Shared with the share card (cards.js);
// returns the scales for the pointer.
export function drawHeartRate(root, w, W, H) {
  const pts = w.hr;
  // Zone names sit in the right margin, clear of the curve.
  const m = { t: 8, r: w.limits ? Math.max(66, zoneLabelWidth() + 18) : 8, b: 24, l: 40 };
  const pw = W - m.l - m.r, ph = H - m.t - m.b;
  const end = Math.max(w.elapsed || 0, pts.at(-1)[0] + 60);
  const vals = pts.map((p) => p[1]);
  let lo = Math.min(...vals), hi = Math.max(...vals, w.hr_max || 0);
  lo = Math.floor((lo - 8) / 10) * 10;
  hi = Math.ceil((hi + 6) / 10) * 10;
  const x = (s) => m.l + (s / end) * pw;
  const y = (v) => m.t + ph - ((v - lo) / (hi - lo)) * ph;

  // Each zone's lower bound as a dotted line in its color; the zone names
  // sit in the right margin next to a color key.
  if (w.limits) {
    const bounds = [lo, ...w.limits, hi];
    for (let z = 0; z < 4; z++) {
      const a = Math.max(lo, bounds[z]), b = Math.min(hi, bounds[z + 1]);
      if (z > 0 && bounds[z] > lo && bounds[z] < hi) {
        root.append(svg('line', { x1: m.l, x2: W - m.r, y1: y(bounds[z]), y2: y(bounds[z]), class: 'zone-limit ' + ZONES[z].cls }));
      }
      if (b <= a) continue;
      root.append(svg('rect', { x: W - m.r + 6, width: 3, y: y(b) + 1, height: Math.max(1, y(a) - y(b) - 2), rx: 1.5, class: 'zone-key ' + ZONES[z].cls }));
      if (y(a) - y(b) >= 12) root.append(svg('text', { x: W - m.r + 14, y: (y(a) + y(b)) / 2 + 4, class: 'zone-label' }, ZONES[z].label));
    }
  }
  for (const [a, b] of w.pauses || []) {
    root.append(svg('rect', { x: x(a), width: Math.max(1, x(b) - x(a)), y: m.t, height: ph, class: 'pause-band' }));
  }
  const axis = svg('g', { class: 'axis' });
  const step = hi - lo > 60 ? 20 : 10;
  for (let v = lo; v <= hi; v += step) {
    axis.append(svg('line', { x1: m.l, x2: W - m.r, y1: y(v), y2: y(v), class: v === lo ? 'baseline' : 'gridline' }));
    axis.append(svg('text', { x: m.l - 8, y: y(v) + 4, 'text-anchor': 'end' }, fmt(v)));
  }
  const mins = end / 60;
  const tick = [5, 10, 15, 30, 60].find((n) => mins / n <= Math.max(2, Math.floor(pw / 70))) || 60;
  for (let at = 0; at <= mins; at += tick) {
    axis.append(svg('text', { x: x(at * 60), y: H - 6, 'text-anchor': at === 0 ? 'start' : 'middle' }, at === 0 ? '0' : `${at} min`));
  }
  root.append(axis);
  // Minutes without a reading (e.g. a pause) break the line.
  let d = '';
  pts.forEach((p, i) => {
    const gap = i > 0 && p[0] - pts[i - 1][0] > 150;
    d += (i === 0 || gap ? 'M' : 'L') + x(p[0] + 30).toFixed(1) + ',' + y(p[1]).toFixed(1);
  });
  const line = svg('path', { d, class: 'hr-line' });
  if (w.limits) {
    // A vertical gradient with hard stops at the zone limits colors the
    // curve by the zone it is in.
    const id = 'hr-zones-' + w.id;
    const grad = svg('linearGradient', { id, gradientUnits: 'userSpaceOnUse', x1: 0, x2: 0, y1: m.t, y2: m.t + ph });
    const off = (v) => Math.max(0, Math.min(1, (y(v) - m.t) / ph));
    const edges = [0, off(w.limits[2]), off(w.limits[1]), off(w.limits[0]), 1];
    for (let k = 0; k < 4; k++) {
      const cls = 'hr-stop ' + ZONES[3 - k].cls;
      grad.append(svg('stop', { offset: edges[k], class: cls }), svg('stop', { offset: edges[k + 1], class: cls }));
    }
    const defs = svg('defs');
    defs.append(grad);
    root.append(defs);
    line.setAttribute('stroke', `url(#${id})`);
    line.style.stroke = `url(#${id})`;
  }
  root.append(line);
  return { x, y, m, ph, lo, hi };
}

function hrChart(host, w) {
  const draw = () => {
    const W = host.clientWidth, H = host.clientHeight;
    if (!W) return;
    const pts = w.hr;
    const root = svg('svg', { viewBox: `0 0 ${W} ${H}`, tabindex: 0, role: 'img' });
    const { x, y, m, ph, lo, hi } = drawHeartRate(root, w, W, H);
    root.setAttribute('aria-label', t('Heart rate during the workout, {lo} to {hi} bpm. Use arrow keys to read values.', { lo: fmt(lo), hi: fmt(hi) }));
    const cross = svg('line', { y1: m.t, y2: m.t + ph, class: 'crosshair', visibility: 'hidden' });
    const dot = svg('circle', { r: 5, class: 'focus-dot', visibility: 'hidden' });
    root.append(cross, dot);

    let active = -1;
    const set = (i) => {
      active = i;
      if (i < 0) { cross.setAttribute('visibility', 'hidden'); dot.setAttribute('visibility', 'hidden'); return hideTip(); }
      const [s, v] = pts[i], px = x(s + 30);
      cross.setAttribute('x1', px); cross.setAttribute('x2', px); cross.setAttribute('visibility', 'visible');
      dot.setAttribute('cx', px); dot.setAttribute('cy', y(v)); dot.setAttribute('visibility', 'visible');
      const z = zoneOf(v, w.limits);
      const r = root.getBoundingClientRect();
      showTip(r.left + px * (r.width / W), r.top + y(v) * (r.height / H), t('Minute {n}', { n: Math.floor(s / 60) + 1 }),
        [{ cls: z >= 0 ? ZONES[z].cls : 's1', value: `${fmt(v)} bpm`, label: z >= 0 ? ZONES[z].label : '' }]);
    };
    const nearest = (clientX) => {
      const r = root.getBoundingClientRect();
      const px = (clientX - r.left) * (W / r.width);
      let best = 0;
      pts.forEach((p, i) => { if (Math.abs(x(p[0] + 30) - px) < Math.abs(x(pts[best][0] + 30) - px)) best = i; });
      return best;
    };
    root.addEventListener('pointermove', (e) => set(nearest(e.clientX)));
    root.addEventListener('pointerdown', (e) => set(nearest(e.clientX)));
    root.addEventListener('pointerleave', (e) => { if (e.pointerType !== 'touch') set(-1); });
    root.addEventListener('focus', () => set(pts.length - 1));
    root.addEventListener('blur', () => set(-1));
    root.addEventListener('keydown', (e) => {
      const moves = { ArrowLeft: -1, ArrowRight: 1, Home: -Infinity, End: Infinity };
      if (!(e.key in moves)) return;
      e.preventDefault();
      e.stopPropagation();
      set(Math.max(0, Math.min(pts.length - 1, active + moves[e.key])));
    });
    host.replaceChildren(root);
  };
  host._draw = draw;
  new ResizeObserver(() => draw()).observe(host);
  draw();
}

/* ---------- map ---------- */

let leaflet = null;
const routes = new Map(); // route_src → Promise of the points

// The workout's GPS track, which the server keeps in a file of its own.
// Resolves to null when it cannot be loaded; once loaded it is kept on
// w.route.
export function loadRoute(w) {
  if (w.route) return Promise.resolve(w.route);
  if (!w.route_src) return Promise.resolve(null);
  if (!routes.has(w.route_src)) {
    routes.set(w.route_src, (async () => {
      const res = await fetch(w.route_src);
      if (!res.ok) throw new Error(res.status);
      return res.json();
    })().catch((err) => {
      console.error('loading route failed', w.route_src, err);
      routes.delete(w.route_src); // try again next time
      return null;
    }));
  }
  return routes.get(w.route_src).then((route) => (route ? (w.route = route) : null));
}

// Leaflet is only loaded when a route is shown.
export function loadLeaflet() {
  leaflet ??= new Promise((resolve, reject) => {
    const css = h('link', { rel: 'stylesheet', href: 'vendor/leaflet-1.9.4/leaflet.css' });
    const js = h('script', { src: 'vendor/leaflet-1.9.4/leaflet.js' });
    js.onload = () => resolve(window.L);
    js.onerror = () => { leaflet = null; reject(new Error('could not load the map library')); };
    document.head.append(css, js);
  });
  return leaflet;
}

// Returns a function that marks route point i on the map (-1 hides it).
function drawMap(host, w) {
  let mark = () => {};
  drawMapAsync(host, w).then((m) => { if (m) mark = m; });
  return (i) => mark(i);
}

async function drawMapAsync(host, w) {
  const route = w.route;
  let L;
  try {
    L = await loadLeaflet();
  } catch (err) {
    host.replaceChildren(h('p', { class: 'empty-tab' }, t('The map could not be loaded.')));
    return;
  }
  if (!host.isConnected) return;
  const map = L.map(host, { scrollWheelZoom: false, zoomSnap: 0.25, attributionControl: true, zoomControl: false });
  mapControls(L, map);
  L.tileLayer('https://tile.openstreetmap.org/{z}/{x}/{y}.png', {
    maxZoom: 19,
    // The page sends no referrer; OpenStreetMap's tile servers need one.
    referrerPolicy: 'strict-origin-when-cross-origin',
    attribution: '© <a href="https://www.openstreetmap.org/copyright" target="_blank" rel="noopener">OpenStreetMap</a>',
  }).addTo(map);
  const css = getComputedStyle(document.documentElement);
  const token = (name, fallback) => css.getPropertyValue(name).trim() || fallback;
  const color = token('--s1', '#2a78d6');
  // The line is drawn per heart rate zone, in the zone colors.
  const zoneColor = (z) => (z < 0 ? color : token('--' + ZONES[z].cls, color));
  const runs = zoneRuns(route, w.limits);
  const lines = runs.map((r) => L.polyline(r.pts, { color: zoneColor(r.zone), weight: 4, opacity: 0.95, lineJoin: 'round', lineCap: 'round' }).addTo(map));
  const ring = token('--surface', '#fff');
  const ends = { radius: 6, color: ring, weight: 2, fillOpacity: 1 };
  L.circleMarker(route[0].slice(0, 2), { ...ends, fillColor: token('--ink', '#222') }).bindTooltip(t('Start')).addTo(map);
  L.circleMarker(route.at(-1).slice(0, 2), { ...ends, fillColor: token('--ink-2', '#555') }).bindTooltip(t('Finish')).addTo(map);
  map.fitBounds(L.featureGroup(lines).getBounds(), { padding: [20, 20] });
  new ResizeObserver(() => map.invalidateSize()).observe(host);
  // The point under the cursor in the elevation profile.
  const here = L.circleMarker(route[0].slice(0, 2), { radius: 7, color: ring, weight: 3, fillColor: token('--ink', '#222'), fillOpacity: 1 });
  return (i) => {
    if (i < 0) { here.remove(); return; }
    here.setLatLng(route[i].slice(0, 2)).addTo(map);
  };
}

/* ---------- elevation ---------- */

function elevationChart(host, w, mark) {
  const pts = w.route;
  const draw = () => {
    const W = host.clientWidth, H = host.clientHeight;
    if (!W) return;
    const m = { t: 8, r: 8, b: 24, l: 40 };
    const pw = W - m.l - m.r, ph = H - m.t - m.b;
    const total = pts.at(-1)[2] || 1;
    const alts = pts.map((p) => p[3]);
    let lo = Math.min(...alts), hi = Math.max(...alts);
    const pad = Math.max(5, (hi - lo) * 0.15);
    lo = Math.floor((lo - pad) / 5) * 5;
    hi = Math.ceil((hi + pad) / 5) * 5;
    const x = (d) => m.l + (d / total) * pw;
    const y = (v) => m.t + ph - ((v - lo) / (hi - lo)) * ph;
    const root = svg('svg', { viewBox: `0 0 ${W} ${H}`, tabindex: 0, role: 'img', 'aria-label': t('Elevation from {lo} to {hi} m along the route. Use arrow keys to read values.', { lo: fmt(lo), hi: fmt(hi) }) });
    const axis = svg('g', { class: 'axis' });
    const span = hi - lo, step = span > 200 ? 100 : span > 80 ? 40 : span > 40 ? 20 : span > 20 ? 10 : 5;
    for (let v = Math.ceil(lo / step) * step; v <= hi; v += step) {
      axis.append(svg('line', { x1: m.l, x2: W - m.r, y1: y(v), y2: y(v), class: 'gridline' }));
      axis.append(svg('text', { x: m.l - 8, y: y(v) + 4, 'text-anchor': 'end' }, fmt(v)));
    }
    axis.append(svg('line', { x1: m.l, x2: W - m.r, y1: y(lo), y2: y(lo), class: 'baseline' }));
    const km = total / 1000;
    const tick = [0.5, 1, 2, 5, 10, 20, 50].find((n) => km / n <= Math.max(2, Math.floor(pw / 70))) || 50;
    for (let at = 0; at <= km + 1e-9; at += tick) {
      axis.append(svg('text', { x: x(at * 1000), y: H - 6, 'text-anchor': at === 0 ? 'start' : 'middle' }, at === 0 ? '0' : `${fmt(at, at % 1 ? 1 : 0)} km`));
    }
    root.append(axis);
    const line = pts.map((p, i) => (i ? 'L' : 'M') + x(p[2]).toFixed(1) + ',' + y(p[3]).toFixed(1)).join('');
    root.append(svg('path', { d: line + `L${x(total).toFixed(1)},${y(lo)}L${x(0)},${y(lo)}Z`, class: 'elev-area' }));
    root.append(svg('path', { d: line, class: 'elev-line' }));
    const cross = svg('line', { y1: m.t, y2: m.t + ph, class: 'crosshair', visibility: 'hidden' });
    const dot = svg('circle', { r: 5, class: 'focus-dot', visibility: 'hidden' });
    root.append(cross, dot);
    let active = -1;
    const set = (i) => {
      active = i;
      mark(i);
      if (i < 0) { cross.setAttribute('visibility', 'hidden'); dot.setAttribute('visibility', 'hidden'); return hideTip(); }
      const p = pts[i], px = x(p[2]);
      cross.setAttribute('x1', px); cross.setAttribute('x2', px); cross.setAttribute('visibility', 'visible');
      dot.setAttribute('cx', px); dot.setAttribute('cy', y(p[3])); dot.setAttribute('visibility', 'visible');
      const z = p[4] > 0 ? zoneOf(p[4], w.limits) : -1;
      const r = root.getBoundingClientRect();
      showTip(r.left + px * (r.width / W), r.top + y(p[3]) * (r.height / H), fmtDistance(p[2]), [
        { value: `${fmt(p[3])} m`, label: t('elevation') },
        p[4] > 0 && { cls: z >= 0 ? ZONES[z].cls : 's1', value: `${fmt(p[4])} bpm`, label: z >= 0 ? ZONES[z].label : t('heart rate') },
      ].filter(Boolean));
    };
    const nearest = (clientX) => {
      const r = root.getBoundingClientRect();
      const d = (((clientX - r.left) * (W / r.width)) - m.l) / pw * total;
      let best = 0;
      pts.forEach((p, i) => { if (Math.abs(p[2] - d) < Math.abs(pts[best][2] - d)) best = i; });
      return best;
    };
    root.addEventListener('pointermove', (e) => set(nearest(e.clientX)));
    root.addEventListener('pointerdown', (e) => set(nearest(e.clientX)));
    root.addEventListener('pointerleave', (e) => { if (e.pointerType !== 'touch') set(-1); });
    root.addEventListener('focus', () => set(0));
    root.addEventListener('blur', () => set(-1));
    root.addEventListener('keydown', (e) => {
      const moves = { ArrowLeft: -1, ArrowRight: 1, Home: -Infinity, End: Infinity };
      if (!(e.key in moves)) return;
      e.preventDefault();
      e.stopPropagation();
      // Step about 1/50 of the route per key press.
      const stepN = Math.max(1, Math.round(pts.length / 50));
      set(Math.max(0, Math.min(pts.length - 1, active + moves[e.key] * stepN)));
    });
    host.replaceChildren(root);
  };
  new ResizeObserver(() => draw()).observe(host);
  draw();
}

/* ---------- splits ---------- */

// One row per kilometre: pace (or speed for rides) with a bar that is
// longest for the fastest split.
function splitsSection(w) {
  const pace = /RUN|WALK|HIK/.test(w.type);
  const sec = section(t('Splits'), pace ? t('Pace per kilometre') : t('Speed per kilometre'));
  const speeds = w.splits.map(([d, s]) => (s ? d / s : 0));
  const fastest = Math.max(...speeds);
  const list = h('div', { class: 'wo-splits', role: 'table', 'aria-label': t('Splits') });
  w.splits.forEach(([d, s], i) => {
    const row = h('div', { class: 'wo-split', role: 'row' });
    const label = d >= 995 ? `${i + 1}` : `${i + 1} · ${fmtDistance(d)}`;
    const value = speedLabel({ type: w.type, dist: d, dur: s });
    const track = h('span', { class: 'wo-split-track' });
    const bar = h('i');
    bar.style.width = (fastest ? Math.max(4, (speeds[i] / fastest) * 100) : 0) + '%';
    if (speeds[i] === fastest) bar.classList.add('best');
    track.append(bar);
    row.append(h('span', { class: 'wo-split-km', role: 'cell' }, label), track, h('span', { class: 'wo-split-val', role: 'cell' }, value), h('span', { class: 'wo-split-time', role: 'cell' }, clock(s)));
    list.append(row);
  });
  sec.append(list);
  return sec;
}

// Leaflet's zoom buttons and credit in the page's language.
export function mapControls(L, map) {
  L.control.zoom({ zoomInTitle: t('Zoom in'), zoomOutTitle: t('Zoom out') }).addTo(map);
  const credit = map.attributionControl;
  if (credit) credit.setPrefix(credit.options.prefix.replace('A JavaScript library for interactive maps', t('A JavaScript library for interactive maps')));
}

// The widest zone name, so the margin right of the heart rate chart fits it
// in every language ("Höchstleistung").
const zoneLabelWidth = () => Math.max(...ZONES.map((z) => textWidth(z.label, 11)));

const clock = (sec) => `${Math.floor(sec / 60)}:${String(Math.round(sec % 60)).padStart(2, '0')}`;

// Opens the workout of a shared link ("#day=…&workout=…") once the day is open.
export function openWorkoutFromHash(day, id) {
  if (id && day?.workouts?.some((w) => w.id === id)) openWorkout(day, id, $('#modal .sheet-close'));
}
