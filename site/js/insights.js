// Derived numbers: goals per day, maintenance estimate and goal forecast.
// No DOM access, so Node can test this file.

import { addDays, avgOf, iso, mean, logged, parse } from './format.js';
import { dayActivities } from './query.js';
import { personalBests } from './health.js';

export const KCAL_PER_KG = 7700; // energy in 1 kg of body fat, the usual approximation

// Yazio reports macro goals as a fixed share of the base energy goal; the app
// scales them up on active days in proportion to the energy goal.
export function macroGoal(day, key, fallback = {}) {
  const base = day?.[key + '_goal'] ?? fallback[key];
  if (!base) return null;
  const scale = day?.energy_goal && day?.energy_base ? day.energy_goal / day.energy_base : 1;
  return base * scale;
}

// The goal changes from stats.json between two ISO dates (inclusive), one
// entry per day: [date, {energy: change, weight: change, ...}].
export function goalChangesIn(changes, from, to) {
  const byDate = new Map();
  for (const c of changes || []) {
    if (c.date < from || c.date > to) continue;
    if (!byDate.has(c.date)) byDate.set(c.date, {});
    byDate.get(c.date)[c.goal] = c;
  }
  return [...byDate];
}

// The weight goal in effect on an ISO date: the last change up to it, the
// goal before the first change, or the current goal when it never changed.
export function weightGoalAt(changes, date, current) {
  const ws = (changes || []).filter((c) => c.goal === 'weight');
  const last = ws.findLast((c) => c.date <= date);
  if (last) return last.to;
  return ws.length ? ws[0].from || null : current || null;
}

// The weight goals to draw over weigh-ins between lo and hi kg (null where
// no goal was set) and the chart's widened range: all of them, so the line
// never stops halfway, or none when one is more than maxGap kg away.
export function weightGoalLine(goals, lo, hi, maxGap = 10) {
  const set = goals.filter((v) => v);
  if (!set.length || set.some((v) => v < lo - maxGap || v > hi + maxGap)) return { goals: goals.map(() => null), lo, hi };
  return { goals: goals.map((v) => v || null), lo: Math.min(lo, ...set), hi: Math.max(hi, ...set) };
}

// Least-squares slope of points [x, y], in y per x.
export function slope(points) {
  if (points.length < 2) return null;
  const mx = mean(points.map(([x]) => x)), my = mean(points.map(([, y]) => y));
  const num = points.reduce((a, [x, y]) => a + (x - mx) * (y - my), 0);
  const den = points.reduce((a, [x]) => a + (x - mx) ** 2, 0);
  return den ? num / den : null;
}

// Maintenance calories from logged intake and the weight trend over the
// windowDays ending at endISO: intake minus the energy the weight change stands for.
export function energyBalance(byDate, endISO, { windowDays = 28, minLogged = 14, minWeighIns = 6 } = {}) {
  const end = parse(endISO);
  const intake = [], weights = [];
  for (let k = windowDays - 1; k >= 0; k--) {
    const day = byDate.get(iso(addDays(end, -k)));
    if (!day) continue;
    if (logged(day)) intake.push(day.energy);
    if (day.weight != null) weights.push([windowDays - 1 - k, day.weight]);
  }
  const base = { windowDays, logged: intake.length, weighIns: weights.length };
  if (intake.length < minLogged || weights.length < minWeighIns) return { ok: false, ...base };
  const kgPerDay = slope(weights);
  const avgIntake = mean(intake);
  const maintenance = avgIntake - kgPerDay * KCAL_PER_KG;
  return { ok: true, ...base, intake: avgIntake, kgPerWeek: kgPerDay * 7, maintenance, balance: avgIntake - maintenance };
}

// When the current trend reaches the goal weight, if it is heading there at all.
export function goalForecast(current, kgPerWeek, goal, fromISO, { maxWeeks = 156 } = {}) {
  if (current == null || !goal) return null;
  const diff = goal - current;
  if (Math.abs(diff) < 0.2) return { status: 'reached' };
  if (!kgPerWeek || Math.abs(kgPerWeek) < 0.02 || Math.sign(kgPerWeek) !== Math.sign(diff)) return { status: 'off-track' };
  const weeks = diff / kgPerWeek;
  if (weeks > maxWeeks) return { status: 'too-far', weeks };
  return { status: 'on-track', weeks, date: iso(addDays(parse(fromISO), Math.round(weeks * 7))) };
}

// The weight trend carried forward on the weight chart of n days: the
// forecast for each day after the last one, up to the day it reaches the
// goal (hit, in days; null when not within the days shown). It looks a
// quarter of the range ahead (7–180 days), or just past the goal when the
// goal is less than half a range away.
export function weightForecast(current, kgPerWeek, goal, n) {
  const perDay = kgPerWeek / 7;
  const toGoal = goal && perDay && Math.sign(goal - current) === Math.sign(perDay) ? (goal - current) / perDay : null;
  const days = toGoal != null && toGoal <= n / 2 ? Math.max(7, Math.ceil(toGoal * 1.15)) : Math.min(180, Math.max(7, Math.round(n / 4)));
  const hit = toGoal != null && toGoal <= days ? toGoal : null;
  const values = Array.from({ length: days }, (_, k) => (hit != null && k + 1 > Math.ceil(hit) ? null : hit != null && k + 1 >= hit ? goal : current + perDay * (k + 1)));
  return { days, values, hit };
}

// The mean of f(day) over the logged, complete days (today is still being
// logged) among the n days ending at date, reaching back before any range;
// null with fewer than min of them.
export function trailingMean(byDate, date, f, today, { n = 7, min = 1 } = {}) {
  const xs = [];
  for (let k = 0; k < n; k++) {
    const day = byDate.get(iso(addDays(parse(date), -k)));
    if (day && logged(day) && day.date !== today) xs.push(f(day));
  }
  return xs.length >= min ? mean(xs) : null;
}

// Mean weight over the 7 days ending at date (reaching back past any range).
export function weightTrendAt(byDate, date) {
  const ws = [];
  for (let k = 0; k < 7; k++) {
    const w = byDate.get(iso(addDays(parse(date), -k)))?.weight;
    if (w != null) ws.push(w);
  }
  return mean(ws);
}

// The night after each day, split into two groups by a property of the day
// (e.g. calories eaten): average HRV, sleeping heart rate and sleep time of
// the following night in each group. A night belongs to the day it ends on,
// so day D is paired with the row of D + 1. Measured values only; groups
// with fewer than minNights nights report ok: false.
export function nightsAfter(rows, byDate, groupOf, { minNights = 5 } = {}) {
  const groups = [[], []];
  for (const r of rows) {
    const g = groupOf(r);
    if (g !== 0 && g !== 1) continue;
    const next = byDate.get(iso(addDays(parse(r.date), 1)));
    if (next && (next.hrv || next.sleep_hr || next.sleep)) groups[g].push(next);
  }
  const avg = (nights, f) => mean(nights.map(f).filter((v) => v > 0));
  const out = groups.map((nights) => ({
    n: nights.length,
    hrv: avg(nights, (n) => n.hrv),
    sleepHR: avg(nights, (n) => n.sleep_hr),
    asleep: avg(nights, (n) => n.sleep?.asleep),
  }));
  return { groups: out, ok: out.every((g) => g.n >= minNights) };
}

// The middle value, or null for an empty list.
export function median(xs) {
  if (!xs.length) return null;
  const s = [...xs].sort((a, b) => a - b), m = s.length >> 1;
  return s.length % 2 ? s[m] : (s[m - 1] + s[m]) / 2;
}

// Average calories eaten per weekday (Monday first) over the logged days.
export function weekdayAverages(rows) {
  const sums = Array.from({ length: 7 }, () => []);
  for (const r of rows) if (logged(r)) sums[(parse(r.date).getUTCDay() + 6) % 7].push(r.energy);
  return sums.map((xs) => ({ avg: mean(xs), n: xs.length }));
}

// Protein per kg of body weight on logged days, overall and split into
// workout and rest days. The weight is the 7-day average, or the last one
// known within 60 days before.
export function proteinPerKg(rows, byDate) {
  const weightAt = (date) => {
    for (let k = 0; k < 60; k += 7) {
      const w = weightTrendAt(byDate, iso(addDays(parse(date), -k)));
      if (w != null) return w;
    }
    return null;
  };
  const groups = { all: [], workout: [], rest: [] };
  for (const r of rows) {
    if (!logged(r) || !(r.protein > 0)) continue;
    const kg = weightAt(r.date);
    if (!kg) continue;
    const v = r.protein / kg;
    groups.all.push(v);
    groups[r.workouts?.length || r.activities?.length ? 'workout' : 'rest'].push(v);
  }
  return Object.fromEntries(Object.entries(groups).map(([k, xs]) => [k, { avg: mean(xs), n: xs.length }]));
}

// Everything the printable report sums up about rows (the days of a range,
// missing ones as {date}; leave out today's partial totals): averages over
// the days with data, the weight trend, workouts, the week-by-week table and
// the most eaten foods and most done activities.
export function rangeSummary(rows, byDate) {
  const fed = rows.filter(logged);
  const nights = rows.filter((r) => r.sleep?.asleep > 0);
  const avg = (rs, f) => mean(rs.map(f).filter((v) => v > 0));
  const weighed = rows.map((r, i) => [i, r.weight]).filter(([, w]) => w != null);
  const perDay = weighed.length >= 3 ? slope(weighed) : null;
  const acts = rows.flatMap((r) => dayActivities(r).map((a) => ({ ...a, date: r.date })));

  const foods = new Map();
  let foodKcal = 0;
  for (const r of rows) for (const f of r.foods || []) {
    const c = foods.get(f.n) || { n: 0, e: 0 };
    c.n++; c.e += f.e || 0; foodKcal += f.e || 0;
    foods.set(f.n, c);
  }
  const activities = new Map();
  for (const a of acts) {
    const c = activities.get(a.key) || { n: 0, dur: 0, e: 0, dist: 0 };
    c.n++; c.dur += a.dur; c.e += a.energy; c.dist += a.dist;
    activities.set(a.key, c);
  }

  // Monday-based weeks; the first and last may be partial.
  const weeks = new Map();
  for (const r of rows) {
    const d = parse(r.date);
    const key = iso(addDays(d, -((d.getUTCDay() + 6) % 7)));
    if (!weeks.has(key)) weeks.set(key, []);
    weeks.get(key).push(r);
  }

  return {
    days: rows.length,
    logged: fed.length,
    energy: avgOf(fed, 'energy'),
    goal: avg(fed, (r) => r.energy_goal || r.energy_base),
    protein: avgOf(fed, 'protein'), carb: avgOf(fed, 'carb'), fat: avgOf(fed, 'fat'),
    meals: Object.fromEntries(['breakfast', 'lunch', 'dinner', 'snack'].map((m) => [m, mean(fed.map((r) => r.meals?.[m] || 0))])),
    proteinPerKg: proteinPerKg(fed, byDate).all.avg,
    weighIns: weighed.length,
    weightFrom: weightTrendAt(byDate, rows[0].date),
    weightTo: weightTrendAt(byDate, rows.at(-1).date),
    kgPerWeek: perDay != null ? perDay * 7 : null,
    steps: avgOf(rows, 'steps'),
    water: avgOf(rows, 'water'),
    burned: avgOf(rows, 'kcal_burned'),
    nights: nights.length,
    asleep: avg(nights, (r) => r.sleep.asleep),
    deep: avg(nights, (r) => r.sleep.deep),
    rem: avg(nights, (r) => r.sleep.rem),
    rhr: avgOf(rows, 'rhr'),
    hrv: avgOf(rows, 'hrv'),
    workouts: { n: acts.length, dur: acts.reduce((a, x) => a + x.dur, 0), e: acts.reduce((a, x) => a + x.energy, 0) },
    weeks: [...weeks].map(([start, rs]) => {
      const f = rs.filter(logged), sl = rs.filter((r) => r.sleep?.asleep > 0);
      return {
        start, first: rs[0].date, end: rs.at(-1).date, days: rs.length, logged: f.length,
        energy: avgOf(f, 'energy'), goal: avg(f, (r) => r.energy_goal || r.energy_base),
        protein: avgOf(f, 'protein'), steps: avgOf(rs, 'steps'),
        weight: weightTrendAt(byDate, rs.at(-1).date),
        asleep: avg(sl, (r) => r.sleep.asleep),
        workouts: rs.reduce((a, r) => a + dayActivities(r).length, 0),
      };
    }),
    foods: [...foods].sort((a, b) => b[1].n - a[1].n || b[1].e - a[1].e).map(([name, c]) => ({ name, ...c, share: foodKcal ? c.e / foodKcal : 0 })),
    activities: [...activities].sort((a, b) => b[1].n - a[1].n || b[1].dur - a[1].dur).map(([key, c]) => ({ key, ...c })),
  };
}

// Notable moments of the last 7 days for the Today card, most notable first:
// a workout record that beat an earlier one, the 7-day weight at its lowest
// (or highest, when the goal is above) for at least 30 days, the most steps
// in a day for at least 60 days, and the protein goal reached 5+ days in a
// row. since is the last earlier date as good, or null when never.
export function highlights(days, byDate, goals = {}, today = null) {
  if (!days.length) return [];
  const last = days.at(-1).date;
  const end = last === today ? iso(addDays(parse(last), -1)) : last;
  const weekStart = iso(addDays(parse(last), -6));
  const out = [];

  const before = days.filter((d) => d.date < weekStart);
  const old = new Map(personalBests(before).map((b) => [b.key, b]));
  for (const b of personalBests(days)) {
    if (b.date >= weekStart && old.has(b.key)) out.push({ kind: 'best', key: b.key, label: b.label, value: b.value, date: b.date, w: b.w });
  }

  const now = weightTrendAt(byDate, end);
  if (now != null) {
    const up = goals.weight > now;
    let since = null, d = addDays(parse(end), -1);
    for (; d >= parse(days[0].date); d = addDays(d, -1)) {
      const v = weightTrendAt(byDate, iso(d));
      if (v != null && (up ? v >= now : v <= now)) { since = iso(d); break; }
    }
    const first = days.find((x) => x.weight != null)?.date;
    const span = (since ?? first) && (parse(end) - parse(since ?? first)) / 864e5;
    if (span >= 30) out.push({ kind: up ? 'weight-high' : 'weight-low', value: now, date: end, since });
  }

  const week = days.filter((x) => x.date >= weekStart);
  const top = week.reduce((a, x) => (x.steps > (a?.steps || 0) ? x : a), null);
  if (top) {
    const prev = days.findLast((x) => x.date < top.date && x.steps >= top.steps)?.date ?? null;
    const span = (parse(top.date) - parse(prev ?? days[0].date)) / 864e5;
    if (span >= 60) out.push({ kind: 'steps', value: top.steps, date: top.date, since: prev });
  }

  let run = 0;
  for (let d = parse(end); ; d = addDays(d, -1)) {
    const x = byDate.get(iso(d));
    const goal = x && macroGoal(x, 'protein', goals);
    if (!x || !logged(x) || !goal || x.protein < goal) break;
    run++;
  }
  if (run >= 5) out.push({ kind: 'protein', value: run, date: end });
  return out.slice(0, 3);
}
