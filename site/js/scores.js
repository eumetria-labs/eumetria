// Eumetria's own scores (computed on the server, package scores): the
// Today card's score chips, the score chart in the Sleep & recovery card
// and the breakdown in the day window's Sleep tab. They are Eumetria's
// formulas, not Fitbit's, and the page says so.

import { fmt, fmtRatio, signed } from './format.js';
import { $, h, svg } from './dom.js';
import { state } from './state.js';
import { timeChart, bucketTitle, smoothPath, valuePill } from './charts.js';
import { fmtSleep } from './health.js';
import { openDay } from './sheet.js';
import { t } from './i18n.js';

// Readiness first: it sums up the other two.
export const SCORES = [
  { key: 'readiness', label: t('Readiness'), cls: 's3' },
  { key: 'sleep', label: t('Sleep|score'), cls: 's2' },
  { key: 'recovery', label: t('Recovery'), cls: 's1' },
];

// The words for a score; the cut-offs follow the formulas (a usual morning
// has a Recovery of about 80 and a Readiness of about 85; its bands are
// scores.ReadyHard and scores.ReadyEasy).
export function scoreWord(key, v) {
  if (key === 'readiness') return v >= 90 ? t('Ready for a hard session') : v >= 75 ? t('Normal training') : t('Take it easy');
  if (key === 'sleep') return v >= 85 ? t('Great night') : v >= 70 ? t('Good night') : v >= 50 ? t('Fair night') : t('Short or restless night');
  return v >= 90 ? t('Better than usual') : v >= 70 ? t('Your usual') : v >= 60 ? t('Below usual') : t('Clearly below usual');
}

const FLAGS = {
  temp: () => t('Skin temperature clearly above your baseline: maybe a cold coming, alcohol or a warm room.'),
  spo2: () => t('Blood oxygen lower than usual.'),
};

export const hasScores = (d) => !!(d?.scores && (d.scores.sleep || d.scores.recovery || d.scores.readiness));

/* ---------- Today card ---------- */

// A small ring per score of the latest day; each opens the day's breakdown.
export function renderTodayScores(day) {
  const host = $('#today-scores');
  const shown = hasScores(day) ? SCORES.filter((s) => day.scores[s.key]) : [];
  host.hidden = !shown.length;
  if (!shown.length) return;
  host.replaceChildren(...shown.map((s) => {
    const v = day.scores[s.key].value;
    const b = h('button', { class: 'score-chip', type: 'button', 'aria-label': t('{score} {value} of 100, {word}. Show the breakdown', { score: s.label, value: v, word: scoreWord(s.key, v) }) });
    b.append(miniRing(v, s.cls));
    const txt = h('span', { class: 'sc-text' });
    txt.append(h('span', { class: 'sc-label' }, s.label), h('span', { class: 'sc-word' }, scoreWord(s.key, v)));
    if (day.scores[s.key].flags?.length) txt.append(h('span', { class: 'sc-flag' }, '⚠ ' + t('see details')));
    b.append(txt);
    b.onclick = () => openDay(day.date, 'sleep');
    return b;
  }));
}

function miniRing(v, cls) {
  const R = 19, C = 2 * Math.PI * R;
  const wrap = h('span', { class: 'sc-ring', 'aria-hidden': 'true' });
  const root = svg('svg', { viewBox: '0 0 48 48' });
  root.append(svg('circle', { cx: 24, cy: 24, r: R, 'stroke-width': 5, class: 'ring-track' }));
  root.append(svg('circle', { cx: 24, cy: 24, r: R, 'stroke-width': 5, class: 'sc-fill ' + cls, 'stroke-dasharray': C, 'stroke-dashoffset': C * (1 - v / 100), transform: 'rotate(-90 24 24)' }));
  wrap.append(root, h('b', {}, String(v)));
  return wrap;
}

/* ---------- Sleep & recovery card ---------- */

// The three scores per day (or weekly averages) as lines from 0 to 100.
export function scoresChart(s) {
  const { buckets, weekly } = s;
  const part = $('#scores-part');
  part.hidden = !state.data.days.some(hasScores);
  if (part.hidden) return;
  const series = SCORES.map((sc) => ({
    ...sc,
    vals: buckets.map((b) => {
      const vs = b.rows.map((r) => r.scores?.[sc.key]?.value).filter((v) => v != null);
      return vs.length ? vs.reduce((a, v) => a + v, 0) / vs.length : null;
    }),
  }));
  const present = series.flatMap((x) => x.vals).filter((v) => v != null);
  timeChart($('#chart-scores'), {
    aria: t('Eumetria scores per day: readiness, sleep and recovery'),
    kind: 'line',
    empty: !present.length,
    n: buckets.length,
    dates: buckets.map((b) => b.start),
    y: [Math.max(0, Math.min(...present) - 5), 100],
    draw(g, ctx) {
      const { x, y } = ctx;
      for (const sr of [...series].reverse()) {
        if (!sr.vals.some((v) => v != null)) continue;
        g.append(svg('path', { d: smoothPath(sr.vals, x, y), class: 'mark-line ' + sr.cls, pathLength: 1 }));
      }
      const ready = series[0].vals;
      const li = ready.findLastIndex((v) => v != null);
      if (li >= 0) valuePill(g, x(li), y(ready[li]), fmt(ready[li]), ctx, 's3');
    },
    tip(i) {
      const rows = series.filter((sr) => sr.vals[i] != null).map((sr) => ({ cls: sr.cls, value: fmt(sr.vals[i]), label: (weekly ? t('avg') + ' ' : '') + sr.label }));
      if (!rows.length) return { title: bucketTitle(buckets[i], weekly), rows: [{ value: t('No scores'), label: '' }] };
      return { title: bucketTitle(buckets[i], weekly), rows, y: series.find((sr) => sr.vals[i] != null).vals[i] };
    },
    onPick: weekly ? null : (i) => buckets[i].rows[0],
    pickTab: 'sleep',
  });
}

/* ---------- day window: Sleep tab ---------- */

const pct = (v) => `${fmt(v * 100)} %`;

// Label and detail of each part, by score.
function partText(key, p) {
  const goal = state.data.sleep_goal || 480;
  switch (`${key}.${p.key}`) {
    case 'sleep.duration': return [t('Duration'), t('{asleep} of {goal}', { asleep: fmtSleep(p.value), goal: fmtSleep(goal) })];
    case 'sleep.efficiency': return [t('Efficiency'), t('{pct} of the time in bed asleep', { pct: pct(p.value) })];
    case 'sleep.deep_rem': return [t('Deep + REM'), t('{pct} of sleep', { pct: pct(p.value) })];
    case 'sleep.regularity': return [t('Regularity'), t('{min} min from your usual mid-sleep', { min: fmt(p.value) })];
    case 'recovery.hrv': return ['HRV', t('{value} ms · usual {base}', { value: fmt(p.value, 1), base: fmt(p.baseline, 1) })];
    case 'recovery.resting_hr': return [t('Resting heart rate'), t('{value} bpm · usual {base}', { value: fmt(p.value), base: fmt(p.baseline) })];
    case 'recovery.sleeping_hr': return [t('Sleeping heart rate'), t('{value} bpm · usual {base}', { value: fmt(p.value), base: fmt(p.baseline) })];
    case 'recovery.breathing': return [t('Breathing rate'), t('{value}/min · usual {base}', { value: fmt(p.value, 1), base: fmt(p.baseline, 1) })];
    case 'readiness.recovery': return [t('Recovery'), t('{value} of 100', { value: fmt(p.value) })];
    case 'readiness.sleep': return [t('Sleep|score'), t('{value} of 100', { value: fmt(p.value) })];
    case 'readiness.load': return [t('Training load'), t('ratio {value}', { value: fmtRatio(p.value) })];
  }
  return [p.key, ''];
}

const NOTES = {
  readiness: () => t('Half Recovery, 30 % Sleep, 20 % training load.'),
  sleep: () => t('Duration against your sleep goal, time asleep in bed, deep and REM sleep, and how regular the night was.'),
  recovery: () => t('Last night’s measurements against your last 30 days: a usual morning scores about 80, each usual swing better or worse about 10 points.'),
};

// One block per score: value, word, flags and the parts.
export function scoresPanel(day) {
  if (!hasScores(day)) return null;
  const wrap = h('div', { class: 'score-panel' });
  const head = h('div', { class: 'mini-head' });
  head.append(h('h3', { class: 'mini-title' }, t('Eumetria scores')), h('span', { class: 'muted sc-own' }, t('Our own formulas, not Fitbit’s')));
  wrap.append(head);
  const grid = h('div', { class: 'score-grid' });
  for (const sc of SCORES) {
    const s = day.scores[sc.key];
    if (!s) continue;
    const box = h('div', { class: 'score-box' });
    const top = h('div', { class: 'sb-head' });
    top.append(miniRing(s.value, sc.cls));
    const txt = h('div');
    txt.append(h('b', {}, sc.label), h('span', { class: 'muted' }, scoreWord(sc.key, s.value)));
    top.append(txt);
    box.append(top);
    for (const f of s.flags || []) box.append(h('p', { class: 'sb-flag' }, '⚠ ' + (FLAGS[f]?.() || f)));
    for (const p of s.parts) {
      const [label, detail] = partText(sc.key, p);
      // Recovery parts move the score away from 80; the others earn points.
      const pts = sc.key === 'recovery' ? signed(p.points, 1) : `${fmt(p.points, p.points % 1 ? 1 : 0)} / ${fmt(p.max)}`;
      const row = h('div', { class: 'day-item metric-row' });
      row.append(h('span', { class: 'name' }, label), h('span', { class: 'amt' }, detail), h('span', { class: 'val' }, pts));
      box.append(row);
    }
    box.append(h('p', { class: 'note muted sb-note' }, NOTES[sc.key]()));
    grid.append(box);
  }
  wrap.append(grid);
  return wrap;
}
