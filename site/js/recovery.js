// Sleep and recovery: the "Sleep & recovery" card on the page and the Sleep
// tab of the day sheet. Only measured values from Fitbit are shown; no
// scores are derived from them.

import { addDays, avgOf, fmt, iso, logged, parse, signed } from './format.js';
import { median, nightsAfter } from './insights.js';
import { RESTLESS, STAGES, fmtSleep, nightBreaks, stageRow } from './health.js';
import { $, h, svg, showTip, hideTip } from './dom.js';
import { state } from './state.js';
import { timeChart, bucketTitle, drawStacks, fadeArea, smoothPath, valuePill } from './charts.js';
import { t, tn } from './i18n.js';

export const hasRecovery = (d) => !!(d && (d.sleep || d.rhr || d.hrv));

/* ---------- page card ---------- */

export function renderRecovery(s) {
  const card = $('#recovery-card');
  card.hidden = !state.data.days.some(hasRecovery);
  if (card.hidden) return;
  const { buckets, weekly, complete } = s;
  const nights = complete.filter((r) => r.sleep);
  const avgAsleep = avgOf(nights.map((r) => r.sleep), 'asleep');
  const parts = [];
  if (avgAsleep) parts.push(t('{time} asleep on average', { time: fmtSleep(avgAsleep) }));
  const rhr = avgOf(complete, 'rhr'), hrv = avgOf(complete, 'hrv');
  if (rhr) parts.push(t('resting heart rate {bpm} bpm', { bpm: fmt(rhr) }));
  if (hrv) parts.push(`HRV ${fmt(hrv)} ms`);
  $('#recovery-sub').textContent = (parts.join(' · ') || t('No sleep data in this range')) + (weekly ? ' · ' + t('weekly averages') : '');

  sleepChart(s);
  // Second lines: the heart rate while asleep (outside REM) and the HRV of
  // deep sleep, both measured by Fitbit like the first.
  metricChart($('#chart-rhr'), s, { key: 'rhr', label: t('resting heart rate'), key2: 'sleep_hr', label2: t('sleeping (non-REM)'), unit: 'bpm', aria: t('Resting and sleeping heart rate per day') });
  metricChart($('#chart-hrv'), s, { key: 'hrv', label: 'HRV', key2: 'hrv_deep', label2: t('deep sleep HRV'), unit: 'ms', digits: 1, aria: t('Heart rate variability per night, overall and in deep sleep') });
  $('#leg-sleep-hr').hidden = !s.complete.some((r) => r.sleep_hr);
  $('#leg-hrv-deep').hidden = !s.complete.some((r) => r.hrv_deep);
  renderNightsAfter(s);
}

// The night after high- vs. lower-calorie days and after workout vs. rest
// days. Plain averages of measured values: a pattern to look at, not a cause.
function renderNightsAfter(s) {
  const rows = s.complete;
  const eaten = rows.filter(logged).map((r) => r.energy);
  const mid = median(eaten);
  const pairs = [];
  if (mid) {
    pairs.push([[t('Above {kcal} kcal', { kcal: fmt(mid) }), t('{kcal} kcal or less', { kcal: fmt(mid) })], nightsAfter(rows, state.byDate, (r) => (!logged(r) ? null : r.energy > mid ? 0 : 1))]);
  }
  const active = (r) => r.workouts?.length || r.activities?.length;
  pairs.push([[t('Workout days'), t('Rest days')], nightsAfter(rows, state.byDate, (r) => (active(r) ? 0 : logged(r) || r.sleep ? 1 : null))]);

  const host = $('#nights-after');
  const shown = pairs.filter(([, c]) => c.ok);
  host.hidden = !shown.length;
  if (!shown.length) return;
  const table = h('table', { class: 'compare' });
  const head = h('tr');
  [t('The night after'), t('Nights'), 'HRV', t('Sleeping HR'), t('Asleep')].forEach((c) => head.append(h('th', { scope: 'col' }, c)));
  table.append(h('thead'), h('tbody'));
  table.tHead.append(head);
  for (const [labels, { groups }] of shown) {
    groups.forEach((g, i) => {
      const tr = h('tr', { class: i ? '' : 'group-start' });
      tr.append(
        h('th', { scope: 'row' }, labels[i]),
        h('td', {}, fmt(g.n)),
        h('td', {}, g.hrv ? `${fmt(g.hrv, 1)} ms` : '–'),
        h('td', {}, g.sleepHR ? `${fmt(g.sleepHR)} bpm` : '–'),
        h('td', {}, fmtSleep(g.asleep)),
      );
      table.tBodies[0].append(tr);
    });
  }
  $('#nights-after-table').replaceChildren(table);
}

// Stacked bars: deep at the bottom, then light, REM and awake time on top.
const STACK = ['deep', 'light', 'rem', 'awake'];
const STACK_CLS = { deep: 'st-deep', light: 'st-light', rem: 'st-rem', awake: 'st-awake' };
const STACK_LABEL = { deep: t('Deep'), light: t('Light'), rem: 'REM', awake: t('Awake') };

function sleepChart(s) {
  const { buckets, weekly } = s;
  // Per bucket: average minutes per stage over the nights in it.
  const vals = buckets.map((b) => {
    const nights = b.rows.filter((r) => r.sleep).map((r) => r.sleep);
    if (!nights.length) return null;
    const out = { asleep: avgOf(nights, 'asleep', () => true) };
    // Nights without stages count fully as light sleep in the stack.
    for (const k of STACK) out[k] = avgOf(nights, k, () => true) || 0;
    const staged = out.deep + out.light + out.rem;
    if (!staged) out.light = out.asleep;
    return out;
  });
  const max = Math.max(...vals.map((v) => (v ? STACK.reduce((a, k) => a + v[k], 0) : 0)));
  timeChart($('#chart-sleep'), {
    aria: t('Sleep per night by stage'),
    kind: 'band',
    empty: !max,
    n: buckets.length,
    dates: buckets.map((b) => b.start),
    // The axis runs in hours so its ticks fall on whole hours.
    y: [0, Math.max(8, (max / 60) * 1.05)],
    yFmt: (hrs) => `${hrs} h`,
    draw(g, { x, y: yh, bw }) {
      drawStacks(g, vals.map((v) => v && STACK.map((k) => v[k])), STACK.map((k) => STACK_CLS[k]), x, (min) => yh(min / 60), bw);
    },
    tip(i) {
      const v = vals[i];
      if (!v) return { title: bucketTitle(buckets[i], weekly), rows: [{ value: t('No sleep recorded'), label: '' }] };
      const night = !weekly && buckets[i].rows[0].sleep;
      return {
        title: bucketTitle(buckets[i], weekly) + (night ? ` · ${night.start}–${night.end}` : ''),
        rows: [
          { value: fmtSleep(v.asleep), label: weekly ? t('avg asleep') : t('asleep') },
          ...[...STACK].reverse().filter((k) => v[k] > 0).map((k) => ({ cls: 'rect ' + STACK_CLS[k], value: fmtSleep(v[k]), label: STACK_LABEL[k] })),
        ],
      };
    },
    onPick: weekly ? null : (i) => buckets[i].rows[0],
    pickTab: 'sleep',
  });
}

function metricChart(host, s, m) {
  const { buckets, weekly } = s;
  const vals = buckets.map((b) => avgOf(b.rows, m.key));
  const vals2 = m.key2 ? buckets.map((b) => avgOf(b.rows, m.key2)) : [];
  const present = [...vals, ...vals2].filter((v) => v != null);
  const lo = Math.min(...present), hi = Math.max(...present);
  const pad = Math.max(1, (hi - lo) * 0.15);
  timeChart(host, {
    aria: m.aria,
    kind: 'line',
    empty: !present.length,
    n: buckets.length,
    dates: buckets.map((b) => b.start),
    y: [lo - pad, hi + pad],
    draw(g, ctx) {
      const { x, y } = ctx;
      // The main measurement over a fading fill, with its latest value.
      if (!vals2.some((v) => v != null)) fadeArea(g, 'fade-' + host.id, 's1', vals, ctx);
      [[vals, 's1'], [vals2, 's2']].forEach(([vs, cls]) => {
        if (!vs.some((v) => v != null)) return;
        g.append(svg('path', { d: smoothPath(vs, x, y), class: 'mark-line ' + cls, pathLength: 1 }));
        if (buckets.length <= 45) vs.forEach((v, i) => { if (v != null) g.append(svg('circle', { cx: x(i), cy: y(v), r: 3, class: 'mark-dot ' + cls })); });
      });
      const li = vals.findLastIndex((v) => v != null);
      if (li >= 0) valuePill(g, x(li), y(vals[li]), `${fmt(vals[li], m.digits || 0)} ${m.unit}`, ctx);
    },
    tip(i) {
      if (vals[i] == null && vals2[i] == null) return { title: bucketTitle(buckets[i], weekly), rows: [{ value: t('Not measured'), label: '' }] };
      const avg = weekly ? t('avg') + ' ' : '';
      const rows = [[vals[i], 's1', m.label], [vals2[i], 's2', m.label2]]
        .filter(([v]) => v != null)
        .map(([v, cls, label]) => ({ cls, value: `${fmt(v, m.digits || 0)} ${m.unit}`, label: avg + label }));
      return { title: bucketTitle(buckets[i], weekly), rows, y: vals[i] ?? vals2[i] };
    },
    onPick: weekly ? null : (i) => buckets[i].rows[0],
    pickTab: 'sleep',
  });
}

/* ---------- day sheet tab ---------- */

export function sleepPanel(day) {
  // Two columns in the wide day window: the night, then the measurements.
  const wrap = h('div', { class: 'sleep-panel' });
  const night = h('div', { class: 'sleep-col' }), body = h('div', { class: 'sleep-col' });
  wrap.append(night, body);
  const sl = day.sleep;
  if (sl) {
    const sum = h('div', { class: 'wo-stats sleep-sum' });
    [[t('Asleep'), fmtSleep(sl.asleep)], [t('Awake'), fmtSleep(sl.awake)], [t('In bed'), `${sl.start}–${sl.end}`]].forEach(([k, v]) => {
      const d = h('div');
      d.append(h('span', {}, k), h('b', {}, v));
      sum.append(d);
    });
    night.append(sum);
    if (sl.stages?.length) {
      const chart = h('div', { class: 'chart hypno' });
      night.append(chart);
      requestAnimationFrame(() => hypnogram(chart, sl));
    }
    const staged = (sl.deep || 0) + (sl.light || 0) + (sl.rem || 0);
    if (staged) {
      const total = staged + (sl.awake || 0);
      for (const st of STAGES) {
        const min = sl[st.key] || 0;
        const row = h('div', { class: 'day-item stage-row' });
        const name = h('span', { class: 'name' });
        name.append(h('i', { class: 'key ' + st.cls }), h('span', { class: 'txt' }, st.label));
        row.append(name, h('span', { class: 'amt' }, `${Math.round((min / total) * 100)} %`), h('span', { class: 'val' }, fmtSleep(min)));
        night.append(row);
      }
    }
    if (sl.restless?.length) {
      // Brief stirrings during sleep; they overlap the stages above.
      const row = h('div', { class: 'day-item stage-row' });
      const name = h('span', { class: 'name' });
      name.append(h('i', { class: 'key ' + RESTLESS.cls }), h('span', { class: 'txt' }, RESTLESS.label));
      const n = sl.restless.length;
      row.append(name, h('span', { class: 'amt' }, tn(n, '{n} moment', '{n} moments')), h('span', { class: 'val' }, `${fmt(sl.restless_min)} min`));
      night.append(row);
    }
    const br = nightBreaks(sl.stages);
    if (br) {
      const n = br.awakeCount;
      night.append(metricRow(t('Awake during the night'), br.awakeMin ? fmtSleep(br.awakeMin) : '0 min', n ? tn(n, '{n} time', '{n} times') : ''));
      if (br.toDeep != null) night.append(metricRow(t('Time to first deep sleep'), `${fmt(br.toDeep)} min`, t('from going to bed')));
    }
    if (sl.to_sleep || sl.after_wake) {
      if (sl.to_sleep) night.append(metricRow(t('Time to fall asleep'), `${fmt(sl.to_sleep)} min`, ''));
      if (sl.after_wake) night.append(metricRow(t('Awake after waking up'), `${fmt(sl.after_wake)} min`, ''));
    }
  } else {
    night.append(h('p', { class: 'empty-tab' }, t('No sleep recorded for this night')));
  }

  // The night's measurements, each next to its 30-day average for context.
  const metrics = [
    [t('Resting heart rate'), 'rhr', 'bpm', 0],
    [t('Sleeping heart rate (non-REM)'), 'sleep_hr', 'bpm', 0],
    [t('Heart rate variability'), 'hrv', 'ms', 1],
    [t('HRV in deep sleep'), 'hrv_deep', 'ms', 1],
    [t('HRV entropy'), 'hrv_ent', '', 1],
    [t('Breathing rate'), 'resp', t('breaths/min'), 1],
    [t('Blood oxygen'), 'spo2', '%', 1],
  ].filter(([, k]) => day[k] != null);
  if (metrics.length || day.temp != null) {
    body.append(h('h3', { class: 'mini-title sleep-metrics-title' }, t('Overnight measurements')));
    const before = previous(day.date, 30);
    for (const [label, k, unit, digits] of metrics) {
      const avg = avgOf(before, k);
      body.append(metricRow(label, `${fmt(day[k], digits)} ${unit}`.trim(), avg != null ? t('30-day avg {value}', { value: fmt(avg, digits) }) : ''));
    }
    if (day.temp != null) body.append(metricRow(t('Skin temperature'), `${signed(day.temp, 1)} °C`, t('vs your baseline')));
  }
  return wrap;
}

function metricRow(label, value, note) {
  const row = h('div', { class: 'day-item metric-row' });
  row.append(h('span', { class: 'name' }, label), h('span', { class: 'amt' }, note), h('span', { class: 'val' }, value));
  return row;
}

// The days before date, up to n of them, that have data.
function previous(date, n) {
  const out = [];
  for (let d = addDays(parse(date), -1), k = 0; k < n; d = addDays(d, -1), k++) {
    const r = state.byDate.get(iso(d));
    if (r) out.push(r);
  }
  return out;
}

// Stages as steps from awake (top) to deep (bottom) across the night.
function hypnogram(host, sl) {
  const W = host.clientWidth, H = host.clientHeight;
  if (!W) return;
  const root = svg('svg', { viewBox: `0 0 ${W} ${H}`, role: 'img', 'aria-label': t('Sleep stages from {start} to {end}', { start: sl.start, end: sl.end }) });
  drawHypnogram(root, sl, W, H, (rect, start, dur, st) => {
    rect.addEventListener('pointerenter', () => {
      const r = rect.getBoundingClientRect();
      showTip(r.left + r.width / 2, r.top, clockAt(sl.start, start) + '–' + clockAt(sl.start, start + dur), [{ cls: 'rect ' + st.cls, value: `${dur} min`, label: st.label }]);
    });
    rect.addEventListener('pointerleave', hideTip);
  });
  host.replaceChildren(root);
}

// The stage rows, segments and hour marks into root (an <svg> or <g>) at
// W × H; onSeg(rect, start, dur, stage) for each segment drawn. Shared with
// the share card (cards.js).
export function drawHypnogram(root, sl, W, H, onSeg) {
  const m = { t: 6, r: 6, b: 22, l: 50 };
  const pw = W - m.l - m.r, ph = H - m.t - m.b;
  const end = Math.max(...sl.stages.map((s) => s[0] + s[1]));
  const rowH = ph / STAGES.length;
  const x = (min) => m.l + (min / end) * pw;
  STAGES.forEach((st, i) => {
    root.append(svg('text', { x: m.l - 8, y: m.t + rowH * i + rowH / 2 + 4, 'text-anchor': 'end', class: 'hypno-label' }, st.label));
    root.append(svg('line', { x1: m.l, x2: W - m.r, y1: m.t + rowH * (i + 1), y2: m.t + rowH * (i + 1), class: 'gridline' }));
  });
  const byCode = Object.fromEntries([...STAGES, RESTLESS].map((st) => [st.code, st]));
  for (const [start, dur, code] of sl.stages) {
    const row = stageRow(code);
    const st = byCode[code] || STAGES[row];
    const rect = svg('rect', { x: x(start), width: Math.max(1, x(start + dur) - x(start) - 0.5), y: m.t + rowH * row + 2, height: rowH - 4, rx: 2, class: 'hypno-seg ' + st.cls });
    onSeg?.(rect, start, dur, st);
    root.append(rect);
  }
  // Restless moments in the top row, in their own color.
  for (const [start, dur] of sl.restless || []) {
    const rect = svg('rect', { x: x(start), width: Math.max(2, x(start + dur) - x(start)), y: m.t + 2, height: rowH - 4, rx: 1, class: 'hypno-seg ' + RESTLESS.cls });
    onSeg?.(rect, start, dur, RESTLESS);
    root.append(rect);
  }
  // Hour marks along the bottom.
  const startMin = toMin(sl.start);
  for (let t = (60 - (startMin % 60)) % 60; t <= end; t += 60) {
    if (pw / (end / 60) < 34 && ((startMin + t) / 60) % 2) continue;
    root.append(svg('text', { x: x(t), y: H - 6, 'text-anchor': 'middle', class: 'hypno-label' }, clockAt(sl.start, t).slice(0, 2)));
  }
}

const toMin = (hhmm) => Number(hhmm.slice(0, 2)) * 60 + Number(hhmm.slice(3, 5));
function clockAt(startHHMM, plus) {
  const t = (toMin(startHHMM) + plus) % 1440;
  return `${String(Math.floor(t / 60)).padStart(2, '0')}:${String(t % 60).padStart(2, '0')}`;
}
