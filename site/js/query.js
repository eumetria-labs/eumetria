// Searching, filtering and sorting days and foods, and the JSON export. No
// DOM access, so Node can test this file.

import { actLabel, actName, addDays, dfLong, iso, logged, parse } from './format.js';
import { t } from './i18n.js';
import { workoutName } from './health.js';

// Two foldings, so German names are found however they are typed: "Müsli"
// by "müsli", "musli" and "muesli" (accents and case never matter).
const plain = (s) => s.toLowerCase().normalize('NFD').replace(/\p{M}/gu, '');
const spelled = (s) => plain(s.toLowerCase().replace(/ä/g, 'ae').replace(/ö/g, 'oe').replace(/ü/g, 'ue').replace(/ß/g, 'ss'));
const FOLDS = [plain, spelled];
const words = (q) => q.trim().split(/\s+/).filter(Boolean);

// Everything a day can be found by: its date in both spellings and the
// names of what was eaten and done.
function dayText(r) {
  const parts = [r.date, dfLong.format(parse(r.date))];
  for (const f of r.foods || []) parts.push(f.n);
  // Activities by what was typed, their English key and the shown name.
  for (const a of r.activities || []) parts.push(a.name, actName(a.name), actLabel(actName(a.name)));
  for (const w of r.workouts || []) parts.push(workoutName(w.type), actLabel(workoutName(w.type)), w.name || '');
  return parts.join('\n');
}

// matchesQuery is true when every word of q appears in the day, so
// "pizza oct" finds the October days with pizza.
export function matchesQuery(r, q) {
  const ws = words(q);
  if (!ws.length) return true;
  const text = dayText(r);
  return FOLDS.some((fold) => {
    const t = fold(text);
    return ws.every((w) => t.includes(fold(w)));
  });
}

// How well name matches q: 0 when it starts with q, 1 when one of its words
// does, 2 when it merely contains every word of q; null when it doesn't match.
export function matchRank(name, q) {
  let best = null;
  for (const fold of FOLDS) {
    const n = fold(name), fq = fold(q.trim());
    const rank = n.startsWith(fq) ? 0 : n.split(/[\s,()/-]+/).some((w) => w.startsWith(fq)) ? 1
      : words(fq).every((w) => n.includes(w)) ? 2 : null;
    if (rank != null && (best == null || rank < best)) best = rank;
  }
  return best;
}

// Quick filters for the daily data table.
export const DAY_FILTERS = [
  ['all', t('All days'), () => true],
  ['logged', t('Food logged'), logged],
  ['over', t('Over goal'), (r) => logged(r) && r.energy_goal > 0 && r.energy > r.energy_goal],
  ['under', t('Under goal'), (r) => logged(r) && r.energy_goal > 0 && r.energy <= r.energy_goal],
  ['weight', t('Weigh-ins'), (r) => r.weight != null],
  ['training', t('Workouts'), (r) => !!(r.workouts?.length || r.activities?.length)],
  ['sleep', t('Sleep tracked'), (r) => !!r.sleep],
];

export function filterDays(rows, filter, q) {
  const pred = DAY_FILTERS.find(([k]) => k === filter)?.[2] || (() => true);
  return rows.filter((r) => pred(r) && matchesQuery(r, q));
}

// sortBy returns rows ordered by value(row); missing values always go last,
// whichever the direction, and ties keep the newest day first.
export function sortBy(rows, value, desc) {
  const missing = (v) => v == null || Number.isNaN(v);
  return [...rows].sort((a, b) => {
    const va = value(a), vb = value(b);
    if (missing(va) || missing(vb)) return missing(va) - missing(vb) || b.date.localeCompare(a.date);
    const c = typeof va === 'string' ? va.localeCompare(vb) : va - vb;
    return (desc ? -c : c) || b.date.localeCompare(a.date);
  });
}

// How the foods list can be ordered; each entry is [name, {n, e, ...}].
export const FOOD_SORTS = [
  ['count', t('Most logged'), (c) => c.n],
  ['kcal', t('Most calories'), (c) => c.e],
  ['avg', t('Highest avg kcal'), (c) => c.e / c.n],
  ['name', t('A–Z'), null],
];

export function sortFoods(entries, key, q = '') {
  const words = q.toLowerCase().split(/\s+/).filter(Boolean);
  const hit = ([name]) => words.every((w) => name.toLowerCase().includes(w));
  const metric = FOOD_SORTS.find(([k]) => k === key)?.[2];
  return entries.filter(hit).sort(metric
    ? (a, b) => metric(b[1]) - metric(a[1]) || b[1].n - a[1].n || a[0].localeCompare(b[0])
    : (a, b) => a[0].localeCompare(b[0], undefined, { sensitivity: 'base' }));
}

// Where the macros of rows come from, per food name: entries (n), kcal (e)
// and grams of protein (pr), carbs (c), fat (f). Fibre (fi), sugar (su) and
// saturated fat (sf) count only entries that have them, with the kcal,
// carbs and fat of those entries alongside (fiE, fiC, suE, sfF), so ratios
// compare like with like. totals sums every food the same way.
export function macroSources(rows) {
  const foods = new Map();
  const totals = { n: 0, e: 0, pr: 0, c: 0, f: 0, fi: 0, fiE: 0, fiC: 0, su: 0, suE: 0, sf: 0, sfF: 0 };
  for (const r of rows) for (const x of r.foods || []) {
    if (!foods.has(x.n)) foods.set(x.n, { n: 0, e: 0, pr: 0, c: 0, f: 0, fi: 0, fiE: 0, fiC: 0, su: 0, suE: 0, sf: 0, sfF: 0 });
    for (const t of [foods.get(x.n), totals]) {
      t.n++; t.e += x.e || 0; t.pr += x.pr || 0; t.c += x.c || 0; t.f += x.f || 0;
      if (x.fi != null) { t.fi += x.fi; t.fiE += x.e || 0; t.fiC += x.c || 0; }
      if (x.su != null) { t.su += x.su; t.suE += x.e || 0; }
      if (x.sf != null) { t.sf += x.sf; t.sfF += x.f || 0; }
    }
  }
  return { foods: [...foods], totals };
}

// The macro sources card's tabs. top is the grams a food contributed;
// best ranks foods eaten at least minN times by quality: grams per 100 kcal
// for protein and fibre, fibre per 10 g of carbs, the share of saturated fat
// (lower is better); for sugar the most per 100 kcal, the foods to watch.
// partial names the kcal the known values cover, for tabs Yazio lacks data
// for on some entries (AI ones). Foods need enough of the macro per entry to count
// (min, in g), and the data the ratio is made of.
export const MACRO_TABS = [
  { key: 'pr', label: t('Protein'), cls: 's1', min: 5, best: (c) => (c.e ? (c.pr / c.e) * 100 : null) },
  { key: 'c', label: t('Carbs'), cls: 's2', min: 10, best: (c) => (c.fiC ? (c.fi / c.fiC) * 10 : null) },
  { key: 'f', label: t('Fat'), cls: 's3', min: 4, best: (c) => (c.sfF ? c.sf / c.sfF : null), asc: true },
  { key: 'fi', label: t('Fibre'), cls: 'z3', min: 1, partial: 'fiE', best: (c) => (c.fiE ? (c.fi / c.fiE) * 100 : null) },
  { key: 'su', label: t('Sugar'), cls: 'z4', min: 2, partial: 'suE', best: (c) => (c.suE ? (c.su / c.suE) * 100 : null) },
];

export function rankSources(foods, tab, { minN = 3, limit = 8 } = {}) {
  const top = foods.filter(([, c]) => c[tab.key] > 0).sort((a, b) => b[1][tab.key] - a[1][tab.key]).slice(0, limit);
  const best = foods
    .filter(([, c]) => c.n >= minN && c[tab.key] / c.n >= tab.min && tab.best(c) != null)
    .sort((a, b) => (tab.asc ? tab.best(a[1]) - tab.best(b[1]) : tab.best(b[1]) - tab.best(a[1])) || b[1].n - a[1].n)
    .slice(0, limit);
  return { top, best };
}

const nextMonth = (m) => {
  const [y, mo] = m.split('-').map(Number);
  return mo === 12 ? `${y + 1}-01` : `${y}-${String(mo + 1).padStart(2, '0')}`;
};

// Everything about one food over the whole history: its entries (newest
// first), how often it was logged each month from the first synced month to
// the last, the meals it was logged at and, when every entry used the same
// unit, the average amount.
export function foodHistory(days, name) {
  const entries = [];
  for (let i = days.length - 1; i >= 0; i--) {
    for (const f of days[i].foods || []) if (f.n === name) entries.push({ date: days[i].date, ...f });
  }
  const months = new Map();
  if (days.length) {
    for (let m = days[0].date.slice(0, 7); m <= days.at(-1).date.slice(0, 7); m = nextMonth(m)) months.set(m, 0);
  }
  const meals = new Map(), units = new Set(), barcodes = new Map();
  let energy = 0, amount = 0;
  for (const f of entries) {
    months.set(f.date.slice(0, 7), (months.get(f.date.slice(0, 7)) || 0) + 1);
    meals.set(f.m, (meals.get(f.m) || 0) + 1);
    units.add(f.u || '');
    energy += f.e || 0;
    amount += f.a || 0;
    if (f.x) barcodes.set(f.x, (barcodes.get(f.x) || 0) + 1);
  }
  const n = entries.length;
  const unit = units.size === 1 ? [...units][0] : '';
  return {
    name, entries, n,
    days: new Set(entries.map((f) => f.date)).size,
    energy, avgEnergy: n ? energy / n : 0,
    unit, avgAmount: unit && amount ? amount / n : null,
    months: [...months], meals,
    // The barcode logged most often, for the Nutri-Score and NOVA badges.
    barcode: [...barcodes].sort((a, b) => b[1] - a[1])[0]?.[0] || null,
    ai: entries.some((f) => f.ai),
    first: entries.at(-1)?.date ?? null, last: entries[0]?.date ?? null,
  };
}

// The activities of a day as "Top activities" counts them: every YAZIO
// activity, named after the type of its linked Fitbit workout when there is
// one, and every Fitbit workout without a YAZIO activity. Durations are in
// minutes (workouts store seconds), distances in metres; w is the workout id.
export function dayActivities(r) {
  const workouts = new Map((r.workouts || []).map((w) => [w.id, w]));
  const used = new Set();
  const named = (w) => w?.type && w.type !== 'OTHER';
  const out = [];
  for (const a of r.activities || []) {
    const w = a.w ? workouts.get(a.w) : null;
    if (w) used.add(w.id);
    out.push({
      key: named(w) ? workoutName(w.type) : actName(a.name),
      energy: a.energy || 0, dur: a.dur || (w?.dur ? w.dur / 60 : 0), dist: a.dist || w?.dist || 0, w: w?.id ?? null,
    });
  }
  for (const w of r.workouts || []) {
    if (used.has(w.id)) continue;
    out.push({
      key: named(w) ? workoutName(w.type) : w.name ? actName(w.name) : workoutName(w.type),
      energy: w.energy || 0, dur: w.dur ? w.dur / 60 : 0, dist: w.dist || 0, w: w.id,
    });
  }
  return out;
}

// Everything about one activity (a dayActivities key) over the whole
// history, like foodHistory; weekdays counts Monday first.
export function activityHistory(days, key) {
  const entries = [];
  for (let i = days.length - 1; i >= 0; i--) {
    for (const a of dayActivities(days[i])) if (a.key === key) entries.push({ date: days[i].date, ...a });
  }
  const months = new Map();
  if (days.length) {
    for (let m = days[0].date.slice(0, 7); m <= days.at(-1).date.slice(0, 7); m = nextMonth(m)) months.set(m, 0);
  }
  const weekdays = [0, 0, 0, 0, 0, 0, 0];
  let energy = 0, dur = 0, durN = 0, dist = 0;
  for (const a of entries) {
    months.set(a.date.slice(0, 7), (months.get(a.date.slice(0, 7)) || 0) + 1);
    weekdays[(parse(a.date).getUTCDay() + 6) % 7]++;
    energy += a.energy;
    if (a.dur) { dur += a.dur; durN++; }
    dist += a.dist;
  }
  const n = entries.length;
  return {
    key, entries, n,
    days: new Set(entries.map((a) => a.date)).size,
    energy, avgEnergy: n ? energy / n : 0,
    dur, avgDur: durN ? dur / durN : null, dist,
    months: [...months], weekdays,
    first: entries.at(-1)?.date ?? null, last: entries[0]?.date ?? null,
  };
}

// Every published file the data points to (photos, routes, the older days),
// wherever it sits in stats.json. The service worker keeps these offline and
// drops the cached files nothing points to any more.
const FILE = /^(photos|routes|days)\//;
export function referencedFiles(data) {
  const out = new Set();
  const walk = (v) => {
    if (typeof v === 'string') { if (FILE.test(v)) out.add(v); }
    else if (Array.isArray(v)) v.forEach(walk);
    else if (v && typeof v === 'object') Object.values(v).forEach(walk);
  };
  walk(data);
  return [...out];
}

// Activities per calendar week (Monday first) from the week of start to the
// week of end, never before the first synced day: [{ start, n, keys }],
// keys counting each activity name that week.
export function sessionsPerWeek(byDate, start, end, first) {
  const from = start < first ? first : start;
  const weeks = [];
  for (let d = addDays(from, -((from.getUTCDay() + 6) % 7)); d <= end; d = addDays(d, 7)) {
    const keys = new Map();
    let n = 0;
    for (let k = 0; k < 7; k++) {
      const r = byDate.get(iso(addDays(d, k)));
      for (const a of r ? dayActivities(r) : []) { n++; keys.set(a.key, (keys.get(a.key) || 0) + 1); }
    }
    weeks.push({ start: d, n, keys });
  }
  return weeks;
}

// Search over the whole history: foods and activities (better matches
// first, then the most logged) and days (newest first), at most limit each.
const counted = new WeakMap(); // days → { foods, activities }, counted once per data
export function searchAll(days, q, limit = 8) {
  if (!words(q).length) return { foods: [], activities: [], days: [] };
  if (!counted.has(days)) {
    const foods = new Map(), activities = new Map();
    for (const r of days) {
      for (const f of r.foods || []) foods.set(f.n, (foods.get(f.n) || 0) + 1);
      for (const a of dayActivities(r)) activities.set(a.key, (activities.get(a.key) || 0) + 1);
    }
    counted.set(days, { foods, activities });
  }
  // Activities match by their key and by their shown (translated) name.
  const better = (a, b) => (a == null ? b : b == null ? a : Math.min(a, b));
  const rank = (counts, label) => [...counts]
    .map(([name, n]) => ({ name, n, rank: label ? better(matchRank(name, q), matchRank(label(name), q)) : matchRank(name, q) }))
    .filter((x) => x.rank != null)
    .sort((a, b) => a.rank - b.rank || b.n - a.n || a.name.localeCompare(b.name))
    .slice(0, limit);
  const { foods, activities } = counted.get(days);
  const found = [];
  for (let i = days.length - 1; i >= 0 && found.length < limit; i--) if (matchesQuery(days[i], q)) found.push(days[i]);
  return { foods: rank(foods), activities: rank(activities, actLabel), days: found };
}

// The first and last day of the selected range, as dates: the last N days
// up to the last synced day, everything, or a custom from–to (either order).
// Never outside the synced days, so charts have no empty lead-in.
export function rangeDays(range, first, last, from, to) {
  const clamp = (d) => new Date(Math.min(Math.max(d, first), last));
  if (range === 'custom' && from && to) {
    const [a, b] = [parse(from), parse(to)].sort((x, y) => x - y);
    return { start: clamp(a), end: clamp(b) };
  }
  if (range === 'all') return { start: first, end: last };
  const n = Number(range) || 30; // also a custom range without dates
  return { start: clamp(addDays(last, -(n - 1))), end: last };
}
