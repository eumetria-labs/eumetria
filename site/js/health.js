// Google Health (Fitbit) helpers: workout names, heart rate zones, sleep
// stages. No DOM access, so Node can test this file.

import { actIcon, actLabel, addDays, fmtDistance, fmtDuration, iso, mean, nf1, parse } from './format.js';
import { t } from './i18n.js';

// Zones from easiest to hardest, as the Fitbit app names them.
export const ZONES = [
  { key: 'light', label: t('Light'), cls: 'z1' },
  { key: 'moderate', label: t('Moderate'), cls: 'z2' },
  { key: 'vigorous', label: t('Vigorous'), cls: 'z3' },
  { key: 'peak', label: t('Peak'), cls: 'z4' },
];

// The zone (0–3) of a heart rate, given the lower bounds of moderate,
// vigorous and peak.
export function zoneOf(bpm, limits) {
  if (!limits) return -1;
  let z = 0;
  limits.forEach((lo, i) => { if (bpm >= lo) z = i + 1; });
  return z;
}

// Minutes and share per zone; shares round to whole percents that add up
// to 100 (largest remainder), so the list never reads 99 % or 101 %.
export function zoneShares(secs) {
  const total = secs.reduce((a, b) => a + b, 0);
  if (!total) return secs.map(() => ({ min: 0, pct: 0 }));
  const raw = secs.map((s) => (s / total) * 100);
  const pct = raw.map(Math.floor);
  let left = 100 - pct.reduce((a, b) => a + b, 0);
  raw.map((r, i) => [r - Math.floor(r), i]).sort((a, b) => b[0] - a[0]).forEach(([, i]) => { if (left-- > 0) pct[i]++; });
  return secs.map((s, i) => ({ min: Math.round(s / 60), pct: pct[i] }));
}

// Google Health exercise types ("BIKING") in plain English; unknown ones are
// title-cased ("FRISBEE_PLAYING_GENERAL" → "Frisbee playing general"). The
// name is the key; actLabel translates it for display.
const TYPE_NAMES = {
  BIKING: 'Cycling', BIKING_STATIONARY: 'Indoor cycling', MOUNTAIN_BIKING: 'Mountain biking', RUNNING: 'Running',
  RUNNING_TREADMILL: 'Treadmill', WALKING: 'Walking', HIKING: 'Hiking', SWIMMING: 'Swimming',
  SWIMMING_POOL: 'Pool swimming', SWIMMING_OPEN_WATER: 'Open water swimming', STRENGTH_TRAINING: 'Strength training',
  WEIGHTLIFTING: 'Weightlifting', CROSSFIT: 'CrossFit', HIIT: 'HIIT', YOGA: 'Yoga', PILATES: 'Pilates',
  JUMPING_ROPE: 'Jump rope', FRISBEE_PLAYING_GENERAL: 'Frisbee', ROWING_MACHINE: 'Rowing machine', ROWING: 'Rowing',
  ELLIPTICAL: 'Elliptical', DANCING: 'Dancing', TENNIS: 'Tennis', SOCCER: 'Football', CLIMBING: 'Climbing',
  ROCK_CLIMBING: 'Climbing', STAIR_CLIMBING: 'Stair climbing', MARTIAL_ARTS: 'Martial arts', BOXING: 'Boxing',
  STRETCHING: 'Stretching', GOLF: 'Golf', SKIING: 'Skiing', SNOWBOARDING: 'Snowboarding', OTHER: 'Workout',
};

// Every workout key, for the translation test.
export const WORKOUT_KEYS = Object.values(TYPE_NAMES);

export function workoutName(type) {
  if (TYPE_NAMES[type]) return TYPE_NAMES[type];
  const words = String(type || 'Workout').toLowerCase().replace(/_+/g, ' ');
  return words.charAt(0).toUpperCase() + words.slice(1);
}

export const workoutIcon = (type) => actIcon(workoutName(type));

// Pace for walking and running ("6:12/km"), speed for everything else.
export function speedLabel(w) {
  if (!w.dist || !w.dur) return '';
  if (/RUN|WALK|HIK/.test(w.type)) {
    const s = Math.round(w.dur / (w.dist / 1000));
    return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}/km`;
  }
  return `${nf1.format((w.dist / 1000) / (w.dur / 3600))} km/h`;
}

// Splits the route into runs of the same heart rate zone, for drawing it in
// zone colors. Neighbouring runs share their end point so the line has no
// gaps. Points are [lat, lon, distance, altitude, bpm]; without heart rate
// or zone limits the whole route is one run with zone -1.
export function zoneRuns(route, limits) {
  const runs = [];
  for (const p of route) {
    const z = limits && p[4] > 0 ? zoneOf(p[4], limits) : -1;
    const last = runs.at(-1);
    if (last && last.zone === z) last.pts.push([p[0], p[1]]);
    else {
      if (last) last.pts.push([p[0], p[1]]);
      runs.push({ zone: z, pts: [[p[0], p[1]]] });
    }
  }
  return runs;
}

export function workoutDetail(w, sep = ' · ') {
  return [w.dur ? fmtDuration(w.dur / 60) : '', w.dist ? fmtDistance(w.dist) : ''].filter(Boolean).join(sep);
}

// The day's activity rows: Yazio activities, linked to their Fitbit workout
// when Yazio imported them, plus Fitbit workouts Yazio does not know about.
export function dayActivities(day) {
  const byId = new Map((day.workouts || []).map((w) => [w.id, w]));
  const used = new Set();
  const rows = (day.activities || []).map((a) => {
    const w = a.w ? byId.get(a.w) : null;
    if (w) used.add(w.id);
    return { a, w };
  });
  for (const w of day.workouts || []) if (!used.has(w.id)) rows.push({ a: null, w });
  return rows;
}

// Sleep stages: codes as in stats.json, listed top to bottom as a
// hypnogram draws them (awake on top, deep at the bottom).
export const STAGES = [
  { code: 0, key: 'awake', label: t('Awake'), cls: 'st-awake' },
  { code: 3, key: 'rem', label: 'REM', cls: 'st-rem' },
  { code: 1, key: 'light', label: t('Light'), cls: 'st-light' },
  { code: 2, key: 'deep', label: t('Deep'), cls: 'st-deep' },
];
// Restless moments are short stirrings that overlap the stages; nights
// without stage tracking have them as a stage of their own (code 5).
export const RESTLESS = { code: 5, key: 'restless', label: t('Restless'), cls: 'st-restless' };
// Nights without stage tracking only know asleep and restless.
export const stageRow = (code) => ({ 0: 0, 3: 1, 1: 2, 2: 3, 4: 2, 5: 0 })[code] ?? 2;

export const fmtSleep = (min) => (min > 0 ? fmtDuration(min) : '–');

// From a night's stages ([start min, duration min, code]): the awake time
// between falling asleep and the final wake-up (awake stages before the first
// and after the last sleep stage are left out), and the minutes from going to
// bed to the first deep sleep. Only measured stages are used.
export function nightBreaks(stages) {
  if (!stages?.length) return null;
  const sleepIdx = stages.map((s, i) => (s[2] === 0 ? -1 : i)).filter((i) => i >= 0);
  if (!sleepIdx.length) return null;
  const first = sleepIdx[0], last = sleepIdx[sleepIdx.length - 1];
  let awakeMin = 0, awakeCount = 0;
  for (let i = first + 1; i < last; i++) {
    if (stages[i][2] === 0) { awakeMin += stages[i][1]; awakeCount++; }
  }
  const deep = stages.find((s) => s[2] === 2);
  return { awakeMin, awakeCount, toDeep: deep ? deep[0] : null };
}

// Training volume of the last 7 days (today included) next to the weekly
// average of the 4 weeks before. Zone minutes are the day totals (fat burn +
// cardio + peak). prev is null until the data reaches back far enough.
export function trainingWeek(days, today) {
  const at = (n) => iso(addDays(parse(today), n));
  const sum = (from, to) => {
    const out = { sessions: 0, dur: 0, dist: 0, azm: 0 };
    for (const d of days) {
      if (d.date < from || d.date > to) continue;
      for (const w of d.workouts || []) { out.sessions++; out.dur += w.dur || 0; out.dist += w.dist || 0; }
      if (d.azm) out.azm += d.azm[0] + d.azm[1] + d.azm[2];
    }
    return out;
  };
  const cur = sum(at(-6), today);
  const first = days.find((d) => d.workouts?.length || d.azm)?.date;
  let prev = null;
  if (first && first <= at(-34)) {
    const p = sum(at(-34), at(-7));
    prev = Object.fromEntries(Object.entries(p).map(([k, v]) => [k, v / 4]));
  }
  return { cur, prev };
}

// "Longest run", "Longest ride"; other types name the distance.
const LONGEST = {
  Running: t('Longest run'), Cycling: t('Longest ride'), Walking: t('Longest walk'), Hiking: t('Longest hike'),
  Swimming: t('Longest swim'), 'Mountain biking': t('Longest mountain bike ride'),
};
const longestLabel = (name) => LONGEST[name] || t('Longest distance: {name}', { name: actLabel(name) });

// The best single workouts: longest, most calories, most zone minutes, most
// climbing, and the longest distance per workout type.
export function personalBests(days) {
  const best = new Map();
  const consider = (key, label, value, d, w) => {
    if (!(value > 0)) return;
    const b = best.get(key);
    if (!b || value > b.value) best.set(key, { key, label, value, date: d.date, w });
  };
  for (const d of days) for (const w of d.workouts || []) {
    consider('dur', t('Longest workout'), w.dur, d, w);
    consider('energy', t('Most calories'), w.energy, d, w);
    consider('azm', t('Most zone minutes'), w.azm, d, w);
    consider('elev', t('Most climbing'), w.elev, d, w);
    consider('dist:' + workoutName(w.type), longestLabel(workoutName(w.type)), w.dist, d, w);
  }
  return [...best.values()];
}

// The usual training load (package scores): the ratio of the last 7 days'
// zone minutes to the last 28 days'.
export const LOAD_ZONE = [0.8, 1.3];

// The words for a load ratio.
export function loadLabel(ratio) {
  if (ratio < 0.5) return t('much lighter than usual');
  if (ratio < LOAD_ZONE[0]) return t('lighter than usual');
  if (ratio <= LOAD_ZONE[1]) return t('balanced');
  if (ratio <= 1.5) return t('ramping up');
  return t('sharp increase');
}

/* ---------- lifts ---------- */

// Lifts the form suggests; any other name can be typed. CrossFit boxes use
// the English names in German too.
export const COMMON_LIFTS = [
  'Back Squat', 'Front Squat', 'Overhead Squat', 'Deadlift', 'Sumo Deadlift', 'Bench Press',
  'Strict Press', 'Push Press', 'Push Jerk', 'Split Jerk', 'Clean', 'Power Clean', 'Squat Clean',
  'Hang Power Clean', 'Clean & Jerk', 'Snatch', 'Power Snatch', 'Squat Snatch', 'Hang Power Snatch',
  'Thruster', 'Weighted Pull-up',
];

// Epley's estimate of the one-rep max from a set of reps.
export const oneRepMax = (kg, reps) => (reps > 1 ? kg * (1 + reps / 30) : kg);

// Round to what a barbell can be loaded to, in half kilos.
export const loadable = (kg) => Math.round(kg * 2) / 2;

// The personal bests: one row per lift and number of reps (a 5-rep max is a
// record of its own), the heaviest result first reached. prev is the best
// before it, for the gain; recent marks a best from the last `days` days.
// Rows come in the order of the most recent best first.
export function liftBoard(entries, today, days = 30) {
  const groups = new Map();
  for (const e of entries) {
    const key = e.lift.toLowerCase() + '|' + e.reps;
    if (!groups.has(key)) groups.set(key, []);
    groups.get(key).push(e);
  }
  const since = today ? iso(addDays(parse(today), -days)) : '';
  const rows = [...groups.entries()].map(([key, list]) => {
    list = [...list].sort((a, b) => a.date.localeCompare(b.date) || a.id - b.id);
    let best = list[0];
    for (const e of list) if (e.kg > best.kg) best = e;
    let prev = null;
    for (const e of list) if (e.date < best.date && (!prev || e.kg > prev.kg)) prev = e;
    return {
      key, lift: list.at(-1).lift, reps: best.reps, best, prev, entries: list,
      gain: prev ? best.kg - prev.kg : null,
      recent: !!since && best.date > since,
    };
  });
  return rows.sort((a, b) => b.best.date.localeCompare(a.best.date) || a.lift.localeCompare(b.lift));
}

// The results that raised a lift's best: heavier than every earlier result
// of the same lift and rep count. A lift's first result is no comparison.
export function bestEntries(entries) {
  const best = new Map(), out = [];
  const sorted = [...entries].sort((a, b) => a.date.localeCompare(b.date) || a.id - b.id);
  for (const e of sorted) {
    const key = e.lift.toLowerCase() + '|' + e.reps;
    const prev = best.get(key);
    if (prev != null && e.kg > prev) out.push(e);
    if (prev == null || e.kg > prev) best.set(key, e.kg);
  }
  return out;
}

// The days of those results.
export const bestDays = (entries) => bestEntries(entries).map((e) => e.date);

// The morning Readiness on the days with a new best (dates) against every
// morning from the first of them to the last: whether bests come on ready
// days. Null with fewer than three such mornings, which says nothing yet.
export function bestsReadiness(dates, byDate) {
  const ready = (date) => byDate.get(date)?.scores?.readiness?.value ?? null;
  dates = [...new Set(dates)].filter((d) => ready(d) != null).sort();
  if (dates.length < 3) return null;
  const span = [...byDate.keys()].filter((d) => d >= dates[0] && d <= dates.at(-1)).map(ready).filter((v) => v != null);
  return { n: dates.length, avg: Math.round(mean(dates.map(ready))), usual: Math.round(mean(span)) };
}

/* ---------- benchmark workouts ---------- */

// Famous benchmark workouts to start from: picking one by name fills in how
// it is scored and the workout (weights for men / women).
export const WOD_PRESETS = [
  { name: 'Fran', scoring: 'time', description: '21-15-9\nThrusters 43/29 kg\nPull-ups' },
  { name: 'Grace', scoring: 'time', description: '30 Clean & Jerks for time\n61/43 kg' },
  { name: 'Isabel', scoring: 'time', description: '30 Snatches for time\n61/43 kg' },
  { name: 'Diane', scoring: 'time', description: '21-15-9\nDeadlifts 102/70 kg\nHandstand Push-ups' },
  { name: 'Elizabeth', scoring: 'time', description: '21-15-9\nCleans 61/43 kg\nRing Dips' },
  { name: 'Helen', scoring: 'time', description: '3 rounds for time\n400 m Run\n21 Kettlebell Swings 24/16 kg\n12 Pull-ups' },
  { name: 'Karen', scoring: 'time', description: '150 Wall Balls for time\n9/6 kg' },
  { name: 'Annie', scoring: 'time', description: '50-40-30-20-10\nDouble-Unders\nSit-ups' },
  { name: 'Jackie', scoring: 'time', description: '1000 m Row\n50 Thrusters 20/15 kg\n30 Pull-ups' },
  { name: 'Murph', scoring: 'time', description: '1 Mile Run\n100 Pull-ups\n200 Push-ups\n300 Air Squats\n1 Mile Run\nWith a 9/6 kg vest' },
  { name: 'Cindy', scoring: 'rounds', description: '20 min AMRAP\n5 Pull-ups\n10 Push-ups\n15 Air Squats' },
  { name: 'Mary', scoring: 'rounds', description: '20 min AMRAP\n5 Handstand Push-ups\n10 Pistols\n15 Pull-ups' },
  { name: 'Fight Gone Bad', scoring: 'reps', description: '3 rounds, 1 min per station, 1 min rest\nWall Balls 9/6 kg\nSumo Deadlift High Pulls 34/25 kg\nBox Jumps 50 cm\nPush Presses 34/25 kg\nRow (calories)' },
];

// Whether result a beats b: less time, more rounds (then reps), more reps
// or more weight. A finished time beats every capped result (not done in
// the time cap); capped ones count their reps.
export function betterResult(scoring, a, b) {
  if (scoring === 'time') {
    if (!!a.capped !== !!b.capped) return !a.capped;
    return a.capped ? a.value > b.value : a.value < b.value;
  }
  if (scoring === 'rounds') return a.value > b.value || (a.value === b.value && (a.reps || 0) > (b.reps || 0));
  return a.value > b.value;
}

const byDateId = (a, b) => a.date.localeCompare(b.date) || a.id - b.id;

// One row per workout, and one more for its scaled results, which have their
// own best: the best with the one it beat (prev) and every result. A workout
// without results gets an empty row. Rows with a best come first, the
// newest best first.
export function wodBoard(workouts, results, today, days = 30) {
  const since = today ? iso(addDays(parse(today), -days)) : '';
  const rows = [];
  for (const w of workouts) {
    const mine = results.filter((r) => r.workout === w.id).sort(byDateId);
    const groups = [false, true].map((scaled) => [scaled, mine.filter((r) => !!r.scaled === scaled)]).filter(([, list]) => list.length);
    if (!groups.length) rows.push({ key: `${w.id}|rx`, workout: w, scaled: false, best: null, prev: null, entries: [], recent: false });
    for (const [scaled, list] of groups) {
      let best = null, prev = null;
      for (const r of list) if (!best || betterResult(w.scoring, r, best)) { prev = best; best = r; }
      rows.push({ key: `${w.id}|${scaled ? 'scaled' : 'rx'}`, workout: w, scaled, best, prev, entries: list, recent: !!since && best.date > since });
    }
  }
  return rows.sort((a, b) => (!a.best - !b.best) || (b.best && b.best.date.localeCompare(a.best.date)) || a.workout.name.localeCompare(b.workout.name) || a.scaled - b.scaled);
}

// The results that made a workout's best better, Rx and scaled apart; a
// first result is no comparison.
export function wodBestResults(workouts, results) {
  const out = [];
  for (const row of wodBoard(workouts, results)) {
    let best = null;
    for (const r of row.entries) {
      if (best && betterResult(row.workout.scoring, r, best)) out.push(r);
      if (!best || betterResult(row.workout.scoring, r, best)) best = r;
    }
  }
  return out.sort(byDateId);
}

// The days of those results.
export const wodBestDays = (workouts, results) => wodBestResults(workouts, results).map((r) => r.date);

/* ---------- the training calendar ---------- */

// Workouts that are everyday movement rather than training.
const EVERYDAY = new Set(['Walking']);

// Each day trained: the minutes of its Fitbit workouts (walks aside), how
// many, whether a result was noted (a lift or benchmark workout counts as
// training without a recording) and whether a best got better (bestDates).
export function trainingDays(days, notedDates = [], bestDates = []) {
  const out = new Map();
  const at = (date) => {
    if (!out.has(date)) out.set(date, { min: 0, sessions: 0, noted: false, pb: false });
    return out.get(date);
  };
  for (const d of days) {
    for (const w of d.workouts || []) {
      if (EVERYDAY.has(workoutName(w.type))) continue;
      const x = at(d.date);
      x.sessions++;
      x.min += (w.dur || 0) / 60;
    }
  }
  for (const date of notedDates) at(date).noted = true;
  for (const date of bestDates) at(date).pb = true;
  return out;
}

// The days trained from..to (inclusive), per week on average, and the
// longest run of weeks (Monday to Sunday) with at least one.
export function trainingSummary(trained, from, to) {
  const dates = [...trained.keys()].filter((d) => d >= from && d <= to);
  const weeks = (parse(to) - parse(from)) / 864e5 / 7 + 1 / 7;
  const monday = (d) => iso(addDays(parse(d), -((parse(d).getUTCDay() + 6) % 7)));
  const active = new Set(dates.map(monday));
  let run = 0, longest = 0;
  for (let m = parse(monday(from)); m <= parse(to); m = addDays(m, 7)) {
    run = active.has(iso(m)) ? run + 1 : 0;
    longest = Math.max(longest, run);
  }
  return { days: dates.length, perWeek: weeks > 0 ? dates.length / weeks : 0, pbs: dates.filter((d) => trained.get(d).pb).length, longestWeeks: longest };
}

// "4:12" or "1:02:05" from seconds.
export function fmtClock(sec) {
  const s = Math.round(sec), two = (n) => String(n).padStart(2, '0');
  return s >= 3600 ? `${Math.floor(s / 3600)}:${two(Math.floor(s / 60) % 60)}:${two(s % 60)}` : `${Math.floor(s / 60)}:${two(s % 60)}`;
}

// Seconds from "4:12", "1:02:05" or whole minutes ("12"); null for
// anything else.
export function parseClock(text) {
  const parts = String(text).trim().split(':');
  if (parts.length > 3 || parts.some((p) => !/^\d{1,3}$/.test(p))) return null;
  const n = parts.map(Number);
  if (n.length === 1) return n[0] * 60; // "12" is minutes
  if (n.at(-1) > 59 || parts.at(-1).length !== 2 || (n.length === 3 && (n[1] > 59 || parts[1].length !== 2))) return null;
  return n.length === 2 ? n[0] * 60 + n[1] : (n[0] * 60 + n[1]) * 60 + n[2];
}

// The plates of a usual box: bumpers and change plates, in kg.
export const PLATES = [25, 20, 15, 10, 5, 2.5, 2, 1.5, 1.25, 1, 0.5];

// The plates for one side of the bar to load total kg, the heaviest first,
// with what that loads (the rest can't be made with these plates).
export function platesFor(total, bar = 20, plates = PLATES) {
  let side = Math.max(0, (total - bar) / 2);
  const out = [];
  for (const p of plates) {
    while (side >= p - 1e-9) {
      out.push(p);
      side -= p;
    }
  }
  return { side: out, loaded: Math.max(bar, bar + 2 * out.reduce((a, b) => a + b, 0)) };
}

// The weights at 50 to 95 % of a one-rep max, for the day's sets.
export const percentages = (max) => [50, 60, 65, 70, 75, 80, 85, 90, 95].map((pct) => ({ pct, kg: loadable((max * pct) / 100) }));
