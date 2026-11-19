// When you sleep, not only how long: regularity and chronotype, from the
// main sleep of each night (Day.sleep: local "HH:MM" start and end, so
// daylight saving time needs no care, and the hypnogram's stages).
// Pure functions; internal/stats/sleeptiming.go computes the same for the
// weekly review, and tests/fixtures/sleeptiming.json keeps the two equal.
//
// - Regularity: how much bedtime and wake time vary (their standard
//   deviation, "±38 min"), and the Sleep Regularity Index (SRI): the chance
//   of being in the same state, asleep or awake, at the same minute 24 h
//   apart, scaled to -100…100 (100: the same every day). Phillips et al.,
//   Sci Rep 2017.
// - Chronotype: the mid-sleep point on free nights (those ending on the
//   morning of a day off: a weekday off, by default Saturday and Sunday, a
//   public holiday, a vacation day or one tagged "day off"; travel days
//   left out), corrected for the
//   sleep caught up then (MSFsc), and social jetlag: how much later the
//   middle of the night lies on free nights than on work nights.
//   Roenneberg et al., Curr Biol 2012 (MCTQ).

import { addDays, iso, parse } from './format.js';

// Enough nights to say something.
export const MIN_NIGHTS = 4, MIN_PAIRS = 4, MIN_FREE = 4, MIN_WORK = 4;

const toMin = (hhmm) => Number(hhmm.slice(0, 2)) * 60 + Number(hhmm.slice(3, 5));
// Minutes since the noon before (bedtime) or the 18:00 before (waking), so
// a night past midnight doesn't wrap around.
export const onsetMin = (sl) => (toMin(sl.start) - 720 + 1440) % 1440;
export const wakeMin = (sl) => (toMin(sl.end) - 1080 + 1440) % 1440;
// The time from falling asleep to waking, in minutes.
export const spanMin = (sl) => (toMin(sl.end) - toMin(sl.start) + 1440) % 1440;
// The middle of the night, in minutes since the noon before.
export const midMin = (sl) => onsetMin(sl) + spanMin(sl) / 2;
// A time in minutes since noon as "HH:MM".
export function clockOf(sinceNoon) {
  const m = Math.round(sinceNoon + 720) % 1440;
  return `${String(Math.floor(m / 60)).padStart(2, '0')}:${String(m % 60).padStart(2, '0')}`;
}

const mean = (xs) => xs.reduce((a, b) => a + b, 0) / xs.length;
function sd(xs) {
  const m = mean(xs);
  return Math.sqrt(xs.reduce((a, x) => a + (x - m) ** 2, 0) / (xs.length - 1));
}

// asleepMask is the night ending on the morning of its date, minute by
// minute from the noon before to noon: true while asleep. Awake stages are
// awake; a night without stages is asleep from start to end.
export function asleepMask(sl) {
  const mask = new Uint8Array(1440);
  const o = onsetMin(sl), n = Math.min(spanMin(sl), 1440 - o);
  mask.fill(1, o, o + n);
  for (const [start, dur, code] of sl.stages || []) {
    if (code !== 0) continue;
    mask.fill(0, Math.min(1440, o + start), Math.min(1440, o + start + dur));
  }
  return mask;
}

// regularity of the rows' nights (rows: days, each with its sleep, in date
// order): bedtime and wake time SD in minutes (null below MIN_NIGHTS), and
// the SRI over pairs of following nights (null below MIN_PAIRS).
export function regularity(rows) {
  const nights = rows.filter((r) => r.sleep?.start && r.sleep?.end);
  const out = { nights: nights.length, onsetSD: null, wakeSD: null, sri: null, pairs: 0 };
  if (nights.length >= MIN_NIGHTS) {
    out.onsetSD = Math.round(sd(nights.map((r) => onsetMin(r.sleep))));
    out.wakeSD = Math.round(sd(nights.map((r) => wakeMin(r.sleep))));
  }
  const byDate = new Map(nights.map((r) => [r.date, r.sleep]));
  let same = 0, total = 0;
  for (const r of nights) {
    const next = byDate.get(iso(addDays(parse(r.date), 1)));
    if (!next) continue;
    const a = asleepMask(r.sleep), b = asleepMask(next);
    for (let i = 0; i < 1440; i++) if (a[i] === b[i]) same++;
    total += 1440;
    out.pairs++;
  }
  if (out.pairs >= MIN_PAIRS) out.sri = Math.round(200 * (same / total) - 100);
  return out;
}

// freeNight: the night ended on a Saturday or Sunday morning.
export const freeNight = (date) => [0, 6].includes(parse(date).getUTCDay());

// freeNightTest is the free-night test for the weekdays off (Sunday 0; the
// default Saturday and Sunday), holidays (date to name) and the dates of
// other days off (a Set: vacation, the day-off tag).
export function freeNightTest({ freeDays = [0, 6], holidays = {}, off = new Set() } = {}) {
  return (date) => freeDays.includes(parse(date).getUTCDay()) || !!holidays[date] || off.has(date);
}

// chronotype of the rows' nights; skip(date) leaves a night out (travel:
// the night's date or the evening before), isFree(date) tells the free
// ones (freeNightTest). null without MIN_FREE free and
// MIN_WORK work nights, else { free, work } counts and the minutes since
// noon of mid-sleep on free nights (msf), corrected (msfsc), on work nights
// (msw), and the social jetlag (msf − msw, minutes).
export function chronotype(rows, skip = () => false, isFree = freeNight) {
  const nights = rows.filter((r) => r.sleep?.start && r.sleep?.end && !skip(r.date) && !skip(iso(addDays(parse(r.date), -1))));
  const free = nights.filter((r) => isFree(r.date)).map((r) => r.sleep);
  const work = nights.filter((r) => !isFree(r.date)).map((r) => r.sleep);
  if (free.length < MIN_FREE || work.length < MIN_WORK) return { free: free.length, work: work.length, ok: false };
  const msf = mean(free.map(midMin)), msw = mean(work.map(midMin));
  const sdf = mean(free.map(spanMin)), sdw = mean(work.map(spanMin));
  const sdweek = (5 * sdw + 2 * sdf) / 7;
  // Sleeping longer on free days is paying back the week's debt: the
  // middle of that sleep is moved back by half the extra.
  const msfsc = sdf > sdw ? msf - (sdf - sdweek) / 2 : msf;
  return { free: free.length, work: work.length, ok: true, msf: Math.round(msf), msfsc: Math.round(msfsc), msw: Math.round(msw), jetlag: Math.round(msf - msw) };
}

// The chronotypes from the clear early bird to the clear night owl, by
// the corrected mid-sleep on free days in minutes since noon (until: the
// first minute of the next). A rough grouping: the research draws no sharp
// lines, and adults lie around 4 o'clock on average (MCTQ).
export const CHRONOTYPES = [
  { key: 'lark2', until: 14 * 60 + 30 }, // before 2:30
  { key: 'lark', until: 15 * 60 + 30 }, // 2:30–3:30
  { key: 'mid', until: 17 * 60 }, // 3:30–5:00
  { key: 'owl', until: 18 * 60 }, // 5:00–6:00
  { key: 'owl2', until: Infinity }, // from 6:00
];
export const AVERAGE_MSF = 16 * 60; // 4:00
export const chronoType = (msfsc) => CHRONOTYPES.find((c) => msfsc < c.until).key;
