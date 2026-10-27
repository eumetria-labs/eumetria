// Hand-built SVG charts: time charts (lines, areas, bars), sparklines, the
// calorie ring and the calendar heatmap.

import { compact, dfLong, dfMonth, dfShort, fmt, hasDetail, iso, logged, parse, addDays, signed } from './format.js';
import { $, h, svg, countUp, showTip, hideTip, textWidth } from './dom.js';
import { t } from './i18n.js';
import { state, reduceMotion } from './state.js';
import { openDay } from './sheet.js';
import { goalChangesIn } from './insights.js';

export const resizer = new ResizeObserver((entries) => entries.forEach((e) => e.target._draw?.()));

export const bucketTitle = (b, weekly) => (weekly ? t('Week of {date}', { date: dfShort.format(b.start) }) : dfLong.format(b.start));

export function timeChart(host, cfg) {
  host._draw = () => drawTimeChart(host, cfg, false);
  host.classList.toggle('pickable', !!cfg.onPick);
  resizer.observe(host);
  drawTimeChart(host, cfg, state.animateNext && !reduceMotion.matches);
}

function drawTimeChart(host, cfg, animate) {
  const W = host.clientWidth, H = host.clientHeight;
  if (!W) return;
  if (cfg.empty) {
    host.replaceChildren(h('div', { class: 'empty' }, t('No data in this range')));
    return;
  }
  const ticks = niceTicks(cfg.y[0], cfg.y[1], H < 180 ? 3 : H < 220 ? 4 : 5);
  const tickStep = ticks[1] - ticks[0];
  const labels = ticks.map((v) => (cfg.yFmt ? cfg.yFmt(v) : v >= 1000 ? compact.format(v) : tickStep < 1 ? fmt(v, 1) : fmt(v)));
  // Room for the widest y label ("15.000" is wider than the usual 44 px).
  const m = { t: 10, r: 8, b: cfg.noXLabels ? 8 : 24, l: Math.max(44, Math.max(...labels.map((l) => textWidth(l, 11))) + 12) };
  const pw = W - m.l - m.r, ph = H - m.t - m.b;
  const d0 = ticks[0], d1 = ticks.at(-1);
  const y = (v) => m.t + ph - ((v - d0) / (d1 - d0)) * ph;
  const step = pw / cfg.n;
  const x = (i) => m.l + (i + 0.5) * step;
  const bw = Math.max(1, Math.min(24, step - 2));

  const root = svg('svg', { viewBox: `0 0 ${W} ${H}`, tabindex: 0, role: 'img', 'aria-label': cfg.aria + '. ' + (cfg.onPick ? t('Use arrow keys to read values, Enter for day details.') : t('Use arrow keys to read values.')) });
  if (animate) root.classList.add('enter');
  const axis = svg('g', { class: 'axis' });
  ticks.forEach((v, k) => {
    axis.append(svg('line', { x1: m.l, x2: W - m.r, y1: y(v), y2: y(v), class: k === 0 ? 'baseline' : 'gridline' }));
    axis.append(svg('text', { x: m.l - 8, y: y(v) + 4, 'text-anchor': 'end' }, labels[k]));
  });
  if (!cfg.noXLabels) {
    // X labels anchored at the latest slot, spaced to avoid collisions.
    const every = Math.max(1, Math.ceil(cfg.n / Math.max(2, Math.floor(pw / 72))));
    for (let i = cfg.n - 1; i >= 0; i -= every) {
      const anchor = i === cfg.n - 1 && x(i) > W - 36 ? 'end' : x(i) - m.l < 30 ? 'start' : 'middle';
      axis.append(svg('text', { x: anchor === 'end' ? W - m.r : x(i), y: H - 6, 'text-anchor': anchor }, dfShort.format(cfg.dates[i])));
    }
  }
  root.append(axis);

  const hl = svg('rect', { y: m.t, height: ph, width: step, class: 'band-hl', visibility: 'hidden' });
  const cross = svg('line', { y1: m.t, y2: m.t + ph, class: 'crosshair', visibility: 'hidden' });
  root.append(cfg.kind === 'band' ? hl : cross);

  const marks = svg('g');
  cfg.draw(marks, { x, y, bw, step, top: m.t, bottom: m.t + ph, right: W - m.r });
  root.append(marks);

  if (cfg.goal) {
    const gy = y(cfg.goal.value);
    root.append(svg('line', { x1: m.l, x2: W - m.r, y1: gy, y2: gy, class: 'goal-line' }));
    root.append(svg('text', { x: m.l + 6, y: gy - 6, class: 'goal-label' }, cfg.goal.label));
  }

  const focusDot = svg('circle', { r: 5, class: cfg.focusCls || 'focus-dot', visibility: 'hidden' });
  root.append(focusDot);

  let active = -1;
  const setActive = (i, clientY) => {
    active = i;
    marks.querySelectorAll('.hover').forEach((n) => n.classList.remove('hover'));
    if (i < 0) {
      hl.setAttribute('visibility', 'hidden');
      cross.setAttribute('visibility', 'hidden');
      focusDot.setAttribute('visibility', 'hidden');
      return hideTip();
    }
    hl.setAttribute('x', x(i) - step / 2);
    hl.setAttribute('visibility', 'visible');
    cross.setAttribute('x1', x(i));
    cross.setAttribute('x2', x(i));
    cross.setAttribute('visibility', 'visible');
    marks.querySelectorAll(`[data-i="${i}"]`).forEach((n) => n.classList.add('hover'));
    const tip = cfg.tip(i);
    if (!tip) { focusDot.setAttribute('visibility', 'hidden'); return hideTip(); }
    if (tip.y != null) {
      focusDot.setAttribute('cx', x(i));
      focusDot.setAttribute('cy', y(tip.y));
      focusDot.setAttribute('visibility', 'visible');
    } else focusDot.setAttribute('visibility', 'hidden');
    const rect = root.getBoundingClientRect();
    const pickable = cfg.onPick && hasDetail(cfg.onPick(i));
    showTip(rect.left + x(i) * (rect.width / W), clientY ?? rect.top + (tip.y != null ? y(tip.y) : m.t + ph / 2) * (rect.height / H),
      tip.title, tip.rows.filter(Boolean), pickable ? (cfg.pickTab === 'sleep' ? t('Click for sleep details') : t('Click for day details')) : '');
  };
  const indexAt = (clientX) => {
    const rect = root.getBoundingClientRect();
    const px = (clientX - rect.left) * (W / rect.width);
    return Math.max(0, Math.min(cfg.n - 1, Math.floor((px - m.l) / step)));
  };
  const pick = (i) => {
    const day = cfg.onPick?.(i);
    if (hasDetail(day)) openDay(day.date, cfg.pickTab);
  };
  let tapped = -1;
  root.addEventListener('pointermove', (e) => { if (e.pointerType !== 'touch') setActive(indexAt(e.clientX), e.clientY); });
  root.addEventListener('pointerleave', (e) => { if (e.pointerType !== 'touch') setActive(-1); });
  root.addEventListener('pointerup', (e) => {
    const i = indexAt(e.clientX);
    // Touch: the first tap shows the values, a second tap on the same day opens it.
    if (e.pointerType === 'touch' && tapped !== i) { tapped = i; setActive(i, e.clientY); return; }
    tapped = -1;
    pick(i);
  });
  // Focus starts on the latest day; cfg.last keeps it off days ahead (forecasts).
  const lastDay = cfg.last ?? cfg.n - 1;
  root.addEventListener('focus', () => setActive(lastDay));
  root.addEventListener('blur', () => { setActive(-1); tapped = -1; });
  root.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' && active >= 0) { e.preventDefault(); pick(active); return; }
    const moves = { ArrowLeft: -1, ArrowRight: 1, Home: -Infinity };
    if (!(e.key in moves) && e.key !== 'End') return;
    e.preventDefault();
    setActive(e.key === 'End' ? lastDay : Math.max(0, Math.min(cfg.n - 1, active + moves[e.key])));
  });

  host.replaceChildren(root);
}

// The first and last ISO date the buckets cover. A weekly bucket starts on
// its Monday, which can lie before the range; its rows don't.
const bucketSpan = (buckets) => [buckets[0].rows[0]?.date ?? iso(buckets[0].start), buckets.at(-1).rows.at(-1)?.date ?? iso(buckets.at(-1).start)];

// The index of the bucket holding the ISO date, or -1 outside the buckets.
export function bucketIndex(buckets, date) {
  if (!buckets.length) return -1;
  const [from, to] = bucketSpan(buckets);
  if (date < from || date > to) return -1;
  return buckets.findLastIndex((b) => iso(b.start) <= date);
}

// A step line through values, one level per slot (null leaves a gap), as
// goals are drawn: a change shows as a step on the day it happened.
export function stepPath(values, x, y, step) {
  let d = '';
  values.forEach((v, i) => {
    if (v == null) return;
    d += (d && values[i - 1] != null ? 'L' : 'M') + (x(i) - step / 2).toFixed(1) + ',' + y(v).toFixed(1) + 'H' + (x(i) + step / 2).toFixed(1);
  });
  return d;
}

// A goal's value with its unit; v defaults to the new value.
export const goalValue = (c, v = c.to) => (c.goal === 'weight' ? `${fmt(v, 1)} kg` : c.goal === 'energy' ? `${fmt(v)} kcal`
  : c.goal === 'water' ? `${fmt(v)} ml` : t('{n} steps', { n: fmt(v) }));

// The changes of the given goals (e.g. ['energy', 'weight']) inside the
// buckets as [i, label, changes]; several on one bucket share a mark.
export function goalMarks(buckets, kinds) {
  if (!buckets.length) return [];
  const byIndex = new Map();
  for (const [date, ch] of goalChangesIn(state.data.goal_changes, ...bucketSpan(buckets))) {
    const i = bucketIndex(buckets, date);
    const cs = kinds.map((k) => ch[k]).filter(Boolean);
    if (i >= 0 && cs.length) byIndex.set(i, [...(byIndex.get(i) || []), ...cs]);
  }
  return [...byIndex].map(([i, cs]) => [i, cs.map((c) => goalValue(c)).join(' · '), cs]);
}

// Tooltip rows for the goal changes at bucket i.
export const goalTipRows = (marks, i) => marks.filter(([j]) => j === i).flatMap(([, , cs]) => cs.map((c) => ({
  value: c.from ? `${goalValue(c, c.from)} → ${goalValue(c)}` : goalValue(c), label: t('goal changed'),
})));

// Goal changes as dotted vertical lines, each with a chip at the top (a flag
// and the new goals); [i, label] pairs from goalMarks. A chip that would run
// into the previous one moves down a row, one near the right edge ends at
// its line.
export function drawGoalMarks(g, marks, { x, top, bottom, right }) {
  const rows = [];
  const H = 17;
  for (const [i, label] of marks) {
    const gx = x(i), w = textWidth(label, 10.5) + 26;
    const from = gx + w > right ? gx - w : gx;
    let row = rows.findIndex((r) => from > r);
    if (row < 0) row = rows.length;
    rows[row] = from + w + 4;
    const cy = top + 1 + row * (H + 3);
    g.append(svg('line', { x1: gx, x2: gx, y1: cy + H, y2: bottom, class: 'goal-mark' }));
    const chip = svg('g', { class: 'goal-chip', transform: `translate(${from.toFixed(1)},${cy})` });
    chip.append(
      svg('rect', { width: w, height: H, rx: H / 2 }),
      // A small flag: pole and pennant.
      svg('path', { d: 'M9 4.5v9M9 4.5h6.5l-1.8 2.4 1.8 2.4H9', class: 'goal-chip-flag' }),
      svg('text', { x: 20, y: 12 }, label),
    );
    g.append(chip);
  }
}

export function barChart(host, s, cfg) {
  const { buckets, weekly } = s;
  const m = cfg.series;
  const max = Math.max(...m.values.map((v) => v || 0), cfg.goal?.value || 0);
  timeChart(host, {
    aria: cfg.aria,
    kind: 'band',
    empty: !max,
    n: buckets.length,
    dates: buckets.map((b) => b.start),
    y: [0, max * 1.05],
    goal: cfg.goal,
    noXLabels: cfg.noXLabels,
    draw(g, { x, y, bw }) { drawBars(g, m.values, m.cls, x, y, bw); },
    tip(i) {
      const head = m.values[i] != null
        ? { cls: 'rect ' + m.cls, value: `${fmt(m.values[i])}${m.unit ? ' ' + m.unit : ''}`, label: (weekly ? t('avg') + ' ' : '') + m.label }
        : { value: t('Nothing recorded'), label: '' };
      return { title: bucketTitle(buckets[i], weekly), rows: [head, ...(cfg.extra?.(i) || [])] };
    },
    onPick: weekly ? null : (i) => buckets[i].rows[0],
  });
}

export function drawBars(g, values, cls, x, y, bw) {
  const n = values.length;
  values.forEach((v, i) => {
    if (!(v > 0)) return;
    const y1 = y(v), hgt = y(0) - y1;
    const bar = svg('path', { d: barPath(x(i) - bw / 2, y1, bw, hgt, Math.min(4, bw / 2)), class: 'mark-bar ' + cls, 'data-i': i });
    bar.style.setProperty('--d', Math.round((i / n) * 220) + 'ms');
    g.append(bar);
  });
}

// Stacked bars: values[i] is an array of segment values (bottom first) or
// null; classes[k] colors segment k. A 2px gap separates the segments and
// only the top segment gets rounded corners.
export function drawStacks(g, values, classes, x, y, bw) {
  values.forEach((v, i) => {
    if (!v) return;
    let base = 0;
    const last = v.findLastIndex((n) => n > 0);
    v.forEach((n, k) => {
      if (!(n > 0)) return;
      const y0 = y(base), y1 = y(base + n);
      const hgt = Math.max(0, y0 - y1 - (base > 0 ? 2 : 0));
      g.append(svg('path', { d: barPath(x(i) - bw / 2, y1, bw, hgt, k === last ? Math.min(4, bw / 2) : 0), class: 'mark-bar ' + classes[k], 'data-i': i }));
      base += n;
    });
  });
}

// A line through the non-null values, broken where data is missing.
export function linePath(values, x, y) {
  let d = '', open = false;
  values.forEach((v, i) => {
    if (v == null) { open = false; return; }
    d += (open ? 'L' : 'M') + x(i).toFixed(1) + ',' + y(v).toFixed(1);
    open = true;
  });
  return d;
}

// Runs of consecutive non-null values as [[x, y], ...] points.
function runs(values, x, y) {
  const out = [];
  let run = null;
  values.forEach((v, i) => {
    if (v == null) { run = null; return; }
    if (!run) out.push(run = []);
    run.push([x(i), y(v)]);
  });
  return out;
}

// A smooth curve through points that never overshoots them (monotone cubic,
// Fritsch–Carlson), so a smoothed trend shows no dips the data doesn't have.
function curve(pts) {
  const n = pts.length;
  let d = 'M' + pts[0][0].toFixed(1) + ',' + pts[0][1].toFixed(1);
  if (n < 3) return d + pts.slice(1).map(([px, py]) => 'L' + px.toFixed(1) + ',' + py.toFixed(1)).join('');
  const dx = [], sl = [];
  for (let k = 0; k < n - 1; k++) {
    dx.push(pts[k + 1][0] - pts[k][0]);
    sl.push((pts[k + 1][1] - pts[k][1]) / dx[k]);
  }
  const m = [sl[0], ...sl.slice(1).map((s, k) => (s * sl[k] > 0 ? (s + sl[k]) / 2 : 0)), sl.at(-1)];
  for (let k = 0; k < n - 1; k++) {
    if (!sl[k]) { m[k] = m[k + 1] = 0; continue; }
    const a = m[k] / sl[k], b = m[k + 1] / sl[k], r = a * a + b * b;
    if (r > 9) { const t3 = 3 / Math.sqrt(r); m[k] = t3 * a * sl[k]; m[k + 1] = t3 * b * sl[k]; }
  }
  for (let k = 0; k < n - 1; k++) {
    const h3 = dx[k] / 3;
    d += 'C' + [pts[k][0] + h3, pts[k][1] + m[k] * h3, pts[k + 1][0] - h3, pts[k + 1][1] - m[k + 1] * h3, pts[k + 1][0], pts[k + 1][1]].map((v) => v.toFixed(1)).join(',');
  }
  return d;
}

export const smoothPath = (values, x, y) => runs(values, x, y).map(curve).join('');

// The area between a smooth line and y0, one shape per run.
export const smoothAreaPath = (values, x, y, y0) => runs(values, x, y)
  .map((pts) => curve(pts) + 'L' + pts.at(-1)[0].toFixed(1) + ',' + y0.toFixed(1) + 'L' + pts[0][0].toFixed(1) + ',' + y0.toFixed(1) + 'Z').join('');

// A smooth area under values fading out downwards, in the series color cls
// (s1, s2, ...); id names its gradient and must be unique on the page.
export function fadeArea(g, id, cls, values, { x, y, bottom }) {
  const grad = svg('linearGradient', { id, x1: 0, x2: 0, y1: 0, y2: 1, class: 'fade ' + cls });
  grad.append(svg('stop', { offset: 0, class: 'fade-top' }), svg('stop', { offset: 1, class: 'fade-bottom' }));
  const defs = svg('defs');
  defs.append(grad);
  g.append(defs, svg('path', { d: smoothAreaPath(values, x, y, bottom), class: 'fade-area', fill: `url(#${id})` }));
}

// A value label above (or below) a point, kept inside the plot; cls picks
// its color (a series class, or ghost for an outline).
export function valuePill(g, cx, cy, text, { top, bottom, right, x }, cls = '', below = false) {
  const w = textWidth(text, 11) + 14, H = 19;
  const left = Math.min(Math.max(cx - w / 2, x(0) - 4), right - w);
  const ty = below ? Math.min(bottom - H, cy + 9) : Math.max(top, cy - H - 9);
  const pill = svg('g', { class: 'value-pill ' + cls, transform: `translate(${left.toFixed(1)},${ty.toFixed(1)})` });
  pill.append(svg('rect', { width: w, height: H, rx: H / 2 }), svg('text', { x: w / 2, y: 13.5, 'text-anchor': 'middle' }, text));
  g.append(pill);
}

export function areaPath(values, x, y, y0) {
  let d = '', run = [];
  const flush = () => {
    if (run.length) {
      d += 'M' + x(run[0]).toFixed(1) + ',' + y0 + run.map((i) => 'L' + x(i).toFixed(1) + ',' + y(values[i]).toFixed(1)).join('') + 'L' + x(run.at(-1)).toFixed(1) + ',' + y0 + 'Z';
    }
    run = [];
  };
  values.forEach((v, i) => (v == null ? flush() : run.push(i)));
  flush();
  return d;
}

function barPath(x, y, w, hgt, r) {
  if (hgt <= 0) return '';
  r = Math.min(r, hgt);
  if (!r) return `M${x},${y + hgt}V${y}H${x + w}V${y + hgt}Z`;
  return `M${x},${y + hgt}V${y + r}Q${x},${y} ${x + r},${y}H${x + w - r}Q${x + w},${y} ${x + w},${y + r}V${y + hgt}Z`;
}

export function niceTicks(lo, hi, count) {
  if (!(hi > lo)) hi = lo + 1;
  const raw = (hi - lo) / count;
  const mag = 10 ** Math.floor(Math.log10(raw));
  const err = raw / mag;
  const step = (err >= 7.5 ? 10 : err >= 3.5 ? 5 : err >= 1.5 ? 2 : 1) * mag;
  const out = [];
  for (let v = Math.floor(lo / step) * step; v <= Math.ceil(hi / step) * step + step / 2; v += step) out.push(Number(v.toFixed(6)));
  return out;
}

// A small trend line in the de-emphasis color, latest point in the accent.
export function sparkline(host, values) {
  const pts = values.map((v, i) => [i, v]).filter(([, v]) => v != null);
  const draw = () => {
    const W = host.clientWidth, H = 34;
    if (!W || pts.length < 2) { host.replaceChildren(); return; }
    const ys = pts.map(([, v]) => v);
    const lo = Math.min(...ys), hi = Math.max(...ys);
    const x = (i) => 3 + (i / Math.max(1, values.length - 1)) * (W - 6);
    const y = (v) => (hi > lo ? 3 + (1 - (v - lo) / (hi - lo)) * (H - 6) : H / 2);
    // Smooth through the known values, bridging the gaps between them.
    const d = smoothPath(pts.map(([, v]) => v), (k) => x(pts[k][0]), y);
    const root = svg('svg', { viewBox: `0 0 ${W} ${H}`, 'aria-hidden': 'true' });
    root.append(svg('path', { d: d + `L${x(pts.at(-1)[0]).toFixed(1)},${H}L${x(pts[0][0]).toFixed(1)},${H}Z`, class: 'area' }));
    root.append(svg('path', { d, class: 'line' }));
    const [li, lv] = pts.at(-1);
    root.append(svg('circle', { cx: x(li), cy: y(lv), r: 3.5, class: 'end' }));
    host.replaceChildren(root);
  };
  host._draw = draw;
  resizer.observe(host);
  draw();
}

// Calorie ring: the fill is eaten / goal; past the goal it turns to the "over" color.
export function ring(host, eaten, goal) {
  const R = 96, C = 2 * Math.PI * R;
  if (!host.querySelector('svg')) {
    const root = svg('svg', { viewBox: '0 0 220 220', 'aria-hidden': 'true' });
    root.append(svg('circle', { cx: 110, cy: 110, r: R, 'stroke-width': 16, class: 'ring-track' }));
    root.append(svg('circle', { cx: 110, cy: 110, r: R, 'stroke-width': 16, class: 'ring-fill', 'stroke-dasharray': C, 'stroke-dashoffset': C }));
    host.replaceChildren(root, h('div', { class: 'ring-center' }));
  }
  const fill = host.querySelector('.ring-fill');
  const over = goal && eaten > goal;
  const frac = goal ? Math.min(1, eaten / goal) : 0;
  fill.classList.toggle('over', !!over);
  requestAnimationFrame(() => fill.setAttribute('stroke-dashoffset', C * (1 - frac)));

  const big = h('div', { class: 'big' });
  countUp(big, 'ring', goal ? Math.abs(goal - eaten) : eaten, (v) => fmt(v));
  host.querySelector('.ring-center').replaceChildren(
    big,
    h('div', { class: 'cap' + (over ? ' over' : '') }, !goal ? t('kcal eaten') : over ? t('kcal over goal') : t('kcal left')),
    h('div', { class: 'of' }, goal ? t('{eaten} of {goal} kcal', { eaten: fmt(eaten), goal: fmt(goal) }) : ''),
  );
  host.setAttribute('role', 'img');
  const vals = { eaten: fmt(eaten), goal: fmt(goal), rest: fmt(Math.abs(goal - eaten)) };
  host.setAttribute('aria-label', !goal ? t('{eaten} kcal eaten', vals)
    : over ? t('{eaten} of {goal} kcal eaten, {rest} kcal over goal', vals) : t('{eaten} of {goal} kcal eaten, {rest} kcal left', vals));
}

export function heatmap(s) {
  const { data, byDate } = state;
  const host = $('#heatmap');
  const first = parse(data.days[0].date);
  const draw = () => {
    const W = host.clientWidth;
    if (!W) return;
    // At least the range and ~6 months, more as far as the width allows
    // cells of about 16 px (a year on a wide screen); never before the
    // first synced day.
    const weeksFit = Math.max(26, Math.floor((W - 30) / 19));
    // A long range on a narrow screen shows its latest weeks, with cells of
    // at least 6 px, rather than running out of the card.
    const weeksMax = Math.floor((W - 30) / 9) - 1;
    const from = new Date(Math.max(first, addDays(s.end, -(weeksMax * 7 - 1)), Math.min(s.start, addDays(s.end, -(weeksFit * 7 - 1)))));
    const year = from.getUTCFullYear() !== s.end.getUTCFullYear() ? `, ${from.getUTCFullYear()}` : '';
    $('#heatmap-sub').textContent = t("Each day's intake compared with its goal, since {date}", { date: dfShort.format(from) + year });
    const start = addDays(from, -((from.getUTCDay() + 6) % 7));
    const weeks = Math.ceil((s.end - start) / 864e5 / 7 + 1 / 7);
    const left = 30, top = 18, gap = 3;
    const size = Math.max(4, Math.min(24, Math.floor((W - left) / weeks) - gap));
    const H = top + 7 * (size + gap);
    const root = svg('svg', { viewBox: `0 0 ${W} ${H}`, height: H, role: 'img', 'aria-label': t('Calendar of daily calories compared with goal') });
    const axis = svg('g', { class: 'axis' });
    [t('Mon'), t('Wed'), t('Fri')].forEach((l, k) => axis.append(svg('text', { x: 0, y: top + (k * 2) * (size + gap) + size - 1 }, l)));
    root.append(axis);

    let lastMonth = -1, lastLabelX = -99;
    for (let d = start; d <= s.end; d = addDays(d, 1)) {
      const col = Math.floor((d - start) / 864e5 / 7);
      const row = (d.getUTCDay() + 6) % 7;
      const x = left + col * (size + gap);
      if (d >= from && d.getUTCMonth() !== lastMonth && x - lastLabelX > 28) {
        axis.append(svg('text', { x, y: 11 }, dfMonth.format(d)));
        lastMonth = d.getUTCMonth();
        lastLabelX = x;
      }
      if (d < from) continue;
      const r = byDate.get(iso(d));
      const goal = r?.energy_goal || data.goals?.energy;
      const cell = svg('rect', { x, y: top + row * (size + gap), width: size, height: size, rx: Math.min(4, size / 4), class: 'cell' });
      if (r && logged(r) && goal) {
        cell.style.fill = divColor(r.energy / goal);
        cell.classList.add('logged');
      } else cell.classList.add('none');
      cell.dataset.date = iso(d);
      root.append(cell);
    }
    root.addEventListener('pointermove', (e) => {
      const date = e.target.dataset?.date;
      if (!date) return hideTip();
      const r = byDate.get(date);
      const goal = r?.energy_goal || data.goals?.energy;
      showTip(e.clientX, e.clientY, dfLong.format(parse(date)), r && logged(r)
        ? [{ value: fmt(r.energy) + ' kcal', label: goal ? t('{kcal} vs goal', { kcal: signed(r.energy - goal) }) : '' }]
        : [{ value: t('Not logged'), label: '' }], hasDetail(r) ? t('Click for day details') : '');
    });
    root.addEventListener('pointerleave', hideTip);
    root.addEventListener('click', (e) => {
      const date = e.target.dataset?.date;
      if (date && hasDetail(byDate.get(date))) { hideTip(); openDay(date); }
    });
    host.replaceChildren(root);
  };
  host._draw = draw;
  resizer.observe(host);
  draw();
}

function divColor(ratio) {
  const d = ratio - 1;
  if (Math.abs(d) < 0.05) return 'var(--div-mid)';
  const k = Math.min(1, (Math.abs(d) - 0.05) / 0.25);
  return `color-mix(in oklab, var(${d < 0 ? '--div-neg' : '--div-pos'}) ${Math.round(30 + 70 * k)}%, var(--div-mid))`;
}
