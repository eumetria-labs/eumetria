// Insights in numbers (the Insights card, insights-card.js): how two daily
// measures move together, and how often the goals were met. Pure: the card
// and the tests call it with the days (Day rows of stats.json).
//
// Correlation is Spearman's rank correlation: it asks whether more of one
// goes with more (or less) of the other, without assuming a straight line,
// and a few extreme days don't swing it. A pattern, not proof.

import { addDays, iso, parse } from './format.js';
import { macroGoal, weightTrendAt } from './insights.js';
import { phaseOn } from './phase.js';
import { midMin, onsetMin, wakeMin } from './sleeptiming.js';

// A measure: key, group, unit, how to read it from a day (null: not known
// that day) and the digits to show. label is filled in by the card (t()).
// A nutrient counts when its foods' data cover at least half the kcal.
const nut = (key) => (d) => (d.nut?.[key] && d.nut[key][1] >= 0.5 ? d.nut[key][0] : null);
const pos = (v) => (v > 0 ? v : null);
const ckin = (key) => (d, ctx) => ctx.checkins?.get(d.date)?.[key] ?? null;
export const MEASURES = [
  { key: 'energy', group: 'nutrition', unit: 'kcal', get: (d) => pos(d.energy) },
  { key: 'protein', group: 'nutrition', unit: 'g', get: (d) => (d.energy > 0 ? d.protein : null) },
  { key: 'carb', group: 'nutrition', unit: 'g', get: (d) => (d.energy > 0 ? d.carb : null) },
  { key: 'fat', group: 'nutrition', unit: 'g', get: (d) => (d.energy > 0 ? d.fat : null) },
  { key: 'alcohol', group: 'nutrition', unit: 'g', digits: 1, get: (d) => (d.energy > 0 ? d.nut?.alcohol?.[0] || 0 : null) },
  { key: 'fiber', group: 'nutrition', unit: 'g', digits: 1, get: nut('fiber') },
  { key: 'sodium', group: 'nutrition', unit: 'mg', get: nut('sodium') },
  { key: 'magnesium', group: 'nutrition', unit: 'mg', get: nut('magnesium') },
  { key: 'steps', group: 'movement', unit: '', get: (d) => pos(d.steps) },
  { key: 'burned', group: 'movement', unit: 'kcal', get: (d) => pos(d.activity_energy) },
  { key: 'azm', group: 'movement', unit: 'min', get: (d) => (d.azm ? d.azm[0] + d.azm[1] + d.azm[2] : null) },
  { key: 'load', group: 'movement', unit: '', digits: 2, get: (d) => d.scores?.load?.ratio || null },
  { key: 'asleep', group: 'sleep', unit: 'min', get: (d) => pos(d.sleep?.asleep) },
  { key: 'deep', group: 'sleep', unit: 'min', get: (d) => pos(d.sleep?.deep) },
  { key: 'rem', group: 'sleep', unit: 'min', get: (d) => pos(d.sleep?.rem) },
  { key: 'bedtime', group: 'sleep', unit: 'clock', get: (d) => (d.sleep?.start ? onsetMin(d.sleep) : null) },
  { key: 'waketime', group: 'sleep', unit: 'clock-wake', get: (d) => (d.sleep?.end ? wakeMin(d.sleep) : null) },
  { key: 'midsleep', group: 'sleep', unit: 'clock', get: (d) => (d.sleep?.start && d.sleep?.end ? midMin(d.sleep) : null) },
  { key: 'rhr', group: 'recovery', unit: 'bpm', get: (d) => pos(d.rhr) },
  { key: 'hrv', group: 'recovery', unit: 'ms', digits: 1, get: (d) => pos(d.hrv) },
  { key: 'sleep_hr', group: 'recovery', unit: 'bpm', get: (d) => pos(d.sleep_hr) },
  { key: 'readiness', group: 'scores', unit: '', get: (d) => d.scores?.readiness?.value ?? null },
  { key: 'sleep_score', group: 'scores', unit: '', get: (d) => d.scores?.sleep?.value ?? null },
  { key: 'recovery', group: 'scores', unit: '', get: (d) => d.scores?.recovery?.value ?? null },
  { key: 'ci_energy', group: 'checkin', unit: '1–5', get: ckin('energy') },
  { key: 'ci_soreness', group: 'checkin', unit: '1–5', get: ckin('soreness') },
  { key: 'ci_stress', group: 'checkin', unit: '1–5', get: ckin('stress') },
  { key: 'ci_mood', group: 'checkin', unit: '1–5', get: ckin('mood') },
  { key: 'weight', group: 'body', unit: 'kg', digits: 1, get: (d, ctx) => (ctx.byDate ? weightTrendAt(ctx.byDate, d.date) ?? null : d.weight ?? null) },
];
export const measure = (key) => MEASURES.find((m) => m.key === key);

// The lags: how many days later the second measure is read. Sleep belongs
// to the morning it ended, so "the day after" of a day's alcohol is the
// night that followed it.
export const LAGS = [0, 1, 2];

// pairsOf are the days of rows with both measures known, y read lag days
// after x: [{ date (of x), x, y }]. skip(date) leaves a day out (either of
// the two dates). ctx: { byDate, checkins } (a Map each).
export function pairsOf(rows, xKey, yKey, lag, ctx = {}, skip = () => false) {
  const X = measure(xKey), Y = measure(yKey);
  const byDate = ctx.byDate || new Map(rows.map((r) => [r.date, r]));
  const out = [];
  for (const r of rows) {
    const later = lag ? byDate.get(iso(addDays(parse(r.date), lag))) : r;
    if (!later || skip(r.date) || skip(later.date)) continue;
    const x = X.get(r, ctx), y = Y.get(later, ctx);
    if (x == null || y == null || Number.isNaN(x) || Number.isNaN(y)) continue;
    out.push({ date: r.date, x, y });
  }
  return out;
}

// ranks of xs, ties given their average rank (1-based).
export function ranks(xs) {
  const order = xs.map((v, i) => [v, i]).sort((a, b) => a[0] - b[0]);
  const out = new Array(xs.length);
  for (let i = 0; i < order.length;) {
    let j = i;
    while (j + 1 < order.length && order[j + 1][0] === order[i][0]) j++;
    const r = (i + j) / 2 + 1;
    for (let k = i; k <= j; k++) out[order[k][1]] = r;
    i = j + 1;
  }
  return out;
}

function pearson(xs, ys) {
  const n = xs.length;
  const mx = xs.reduce((a, b) => a + b, 0) / n, my = ys.reduce((a, b) => a + b, 0) / n;
  let sxy = 0, sxx = 0, syy = 0;
  for (let i = 0; i < n; i++) {
    sxy += (xs[i] - mx) * (ys[i] - my);
    sxx += (xs[i] - mx) ** 2;
    syy += (ys[i] - my) ** 2;
  }
  return sxx && syy ? sxy / Math.sqrt(sxx * syy) : null;
}

// spearman is Spearman's rank correlation of xs and ys (−1…1), null when
// either doesn't vary.
export const spearman = (xs, ys) => pearson(ranks(xs), ranks(ys));

// fit is the least-squares line through the points: y = a + b·x.
export function fit(points) {
  const n = points.length;
  const mx = points.reduce((s, p) => s + p.x, 0) / n, my = points.reduce((s, p) => s + p.y, 0) / n;
  let sxy = 0, sxx = 0;
  for (const p of points) {
    sxy += (p.x - mx) * (p.y - my);
    sxx += (p.x - mx) ** 2;
  }
  const b = sxx ? sxy / sxx : 0;
  return { a: my - b * mx, b };
}

// Fewer days than this say nothing.
export const MIN_PAIRS = 10;

// strength names |r|: Cohen's rough steps.
export const strength = (r) => {
  const a = Math.abs(r);
  return a < 0.1 ? 'none' : a < 0.3 ? 'weak' : a < 0.5 ? 'moderate' : 'strong';
};

// correlate is the correlation of the pairs: { n, r, strength, line }, r
// null below MIN_PAIRS or without variation.
export function correlate(points) {
  const n = points.length;
  if (n < MIN_PAIRS) return { n, r: null };
  const r = spearman(points.map((p) => p.x), points.map((p) => p.y));
  if (r == null) return { n, r: null };
  return { n, r, strength: strength(r), line: fit(points) };
}

/* ---------- goal hit rates ---------- */

// CALORIE_BAND is how far from the goal a maintenance day may be.
export const CALORIE_BAND = 0.1;

// calorieHit tells whether a logged day's calories meet its goal, given the
// phase it lies in: a cut at most the goal, a bulk at least, else within
// ±10 %. null without a goal.
export function calorieHit(d, kind) {
  const goal = d.energy_goal || d.energy_base;
  if (!goal || !(d.energy > 0)) return null;
  if (kind === 'cut') return d.energy <= goal;
  if (kind === 'bulk') return d.energy >= goal;
  return Math.abs(d.energy - goal) <= goal * CALORIE_BAND;
}

// goalHits counts, over the rows, the days each goal was met: [{ key, hit,
// of, kind? }] for protein, calories (by phase), steps, sleep and days
// without alcohol; a goal without days to judge is left out. Days without
// data don't count. ctx: { goals, sleepGoal, phases, today }.
export function goalHits(rows, ctx = {}) {
  const g = ctx.goals || {};
  const out = [];
  const add = (key, judged, extra = {}) => {
    const days = judged.filter((v) => v != null);
    if (days.length) out.push({ key, hit: days.filter(Boolean).length, of: days.length, ...extra });
  };
  const logged = rows.filter((d) => d.energy > 0);
  add('protein', logged.map((d) => {
    const goal = macroGoal(d, 'protein', g);
    return goal ? d.protein >= goal : null;
  }));
  const kinds = new Set();
  add('energy', logged.map((d) => {
    const p = ctx.phases ? phaseOn(ctx.phases, d.date, ctx.today) : null;
    const kind = p && (p.kind === 'cut' || p.kind === 'bulk') ? p.kind : 'maintain';
    kinds.add(kind);
    return calorieHit(d, kind);
  }), { kinds: [...kinds] });
  if (g.steps) add('steps', rows.map((d) => (d.steps > 0 ? d.steps >= g.steps : null)));
  if (ctx.sleepGoal) add('sleep', rows.map((d) => (d.sleep?.asleep > 0 ? d.sleep.asleep >= ctx.sleepGoal : null)));
  // Only once alcohol was ever logged: else every day is "without".
  if (ctx.alcoholKnown) add('alcohol_free', logged.map((d) => !(d.nut?.alcohol?.[0] > 0)));
  return out;
}
