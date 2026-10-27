// Google Health (Fitbit) helpers: workout names, heart rate zones, sleep
// stages. No DOM access, so Node can test this file.

import { actIcon, actLabel, addDays, fmtDistance, fmtDuration, iso, nf1, parse } from './format.js';
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
