// A calendar year in numbers, for the year in review (yearreview.js): plain
// sums and averages of what the year's days hold. A part the data doesn't
// have (no Fitbit, a role without foods) comes back empty, and the window
// leaves it out.

import { logged, mean } from './format.js';
import { bestEntries, liftBetter, liftKey, prompts, trainingDays, wodBestResults, workoutName } from './health.js';

// The years with data, newest first.
export const yearsOf = (days) => [...new Set(days.map((d) => d.date.slice(0, 4)))].sort().reverse();

// trainings are all of them (sessions.js state.trainings): each one is a day
// trained, and those with sets count their work sets and volume. types are
// the person's training types (Profile → Training; null: all but walks):
// only those workouts make a day trained or a kind done most often.
export function yearSummary(allDays, year, { lifts = [], wods = { workouts: [], results: [] }, trainings = [], types = null } = {}) {
  const days = allDays.filter((d) => d.date.startsWith(year));
  const inYear = (date) => date.startsWith(year);
  const eaten = days.filter(logged);

  const weighIns = days.filter((d) => d.weight != null);
  const weight = weighIns.length ? {
    first: weighIns[0].weight, firstDate: weighIns[0].date,
    last: weighIns.at(-1).weight, lastDate: weighIns.at(-1).date,
    min: Math.min(...weighIns.map((d) => d.weight)),
    n: weighIns.length,
  } : null;

  const stepDays = days.filter((d) => d.steps > 0);
  const topSteps = stepDays.reduce((a, d) => (!a || d.steps > a.steps ? d : a), null);

  // Training: the Fitbit workouts, and days with a training or a noted result.
  const workouts = days.flatMap((d) => (d.workouts || []).map((w) => ({ ...w, date: d.date })));
  const myTrainings = trainings.filter((x) => inYear(x.date));
  const noted = [...lifts.map((e) => e.date), ...wods.results.map((r) => r.date)].filter(inYear).concat(myTrainings.map((x) => x.date));
  // Trainings noted set by set: their work sets and volume (kg × reps).
  const mySessions = myTrainings.filter((x) => x.sets?.length);
  const work = mySessions.flatMap((x) => x.sets.filter((s) => s.type !== 'warmup'));
  // The kinds of workout done most often (of the training types), at most three.
  const isTraining = prompts(types);
  const kinds = new Map();
  for (const w of workouts.filter(isTraining)) {
    const name = workoutName(w.type);
    kinds.set(name, (kinds.get(name) || 0) + 1);
  }
  const top = [...kinds.entries()].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0])).slice(0, 3);
  const trained = [...trainingDays(days, noted, [], types).entries()].filter(([date]) => inYear(date));
  const longest = workouts.reduce((a, w) => (!a || (w.dur || 0) > (a.dur || 0) ? w : a), null);

  // The bests set this year, one row per lift (any reps) or benchmark
  // workout (Rx or scaled): where it stood when the year began, where it
  // ended and how many times it got better.
  const liftBests = groupBests(bestEntries(lifts).filter((e) => inYear(e.date)), liftKey)
    .map((list) => {
      const key = liftKey(list[0]);
      // A new best always has an earlier result to beat: the best before it.
      const before = lifts.filter((e) => liftKey(e) === key && e.date < list[0].date).reduce((a, e) => (!a || liftBetter(e, a) ? e : a), null);
      const last = list.at(-1);
      return { date: last.date, name: last.lift, reps: last.reps, kg: last.kg, n: list.length, from: before?.kg ?? null, fromReps: before?.reps ?? null };
    });
  const wodById = new Map(wods.workouts.map((w) => [w.id, w]));
  const wodBests = groupBests(wodBestResults(wods.workouts, wods.results).filter((r) => inYear(r.date)), (r) => r.workout + '|' + !!r.scaled)
    .map((list) => ({ date: list.at(-1).date, name: wodById.get(list[0].workout)?.name || '', workout: wodById.get(list[0].workout), result: list.at(-1), n: list.length }));

  const nights = days.filter((d) => d.sleep?.asleep);
  const ready = days.map((d) => d.scores?.readiness?.value).filter((v) => v != null);

  // Month by month: days trained, and the average sleep score.
  const months = Array.from({ length: 12 }, (_, m) => {
    const key = `${year}-${String(m + 1).padStart(2, '0')}`;
    const sleep = days.filter((d) => d.date.startsWith(key)).map((d) => d.scores?.sleep?.value).filter((v) => v != null);
    return {
      month: m,
      trained: trained.filter(([date]) => date.startsWith(key)).length,
      sleep: sleep.length ? mean(sleep) : null,
      days: days.filter((d) => d.date.startsWith(key)).length,
    };
  });
  const sleepMonths = months.filter((m) => m.sleep != null);
  const bestSleep = sleepMonths.length >= 2 ? sleepMonths.reduce((a, m) => (m.sleep > a.sleep ? m : a)) : null;

  // The foods eaten most often (times logged).
  const counts = new Map();
  for (const d of days) for (const f of d.foods || []) counts.set(f.n, (counts.get(f.n) || 0) + 1);
  const foods = [...counts.entries()].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0])).slice(0, 5);

  return {
    year,
    first: days[0]?.date, last: days.at(-1)?.date, n: days.length,
    nutrition: eaten.length ? {
      logged: eaten.length,
      kcal: mean(eaten.map((d) => d.energy)),
      protein: mean(eaten.map((d) => d.protein).filter((v) => v != null)),
    } : null,
    weight,
    // Logged days without alcohol, when the year had any (nutrients.js).
    alcohol: eaten.some((d) => d.nut?.alcohol?.[0] > 0) ? { free: eaten.filter((d) => !(d.nut?.alcohol?.[0] > 0)).length, days: eaten.length } : null,
    steps: stepDays.length ? {
      total: stepDays.reduce((a, d) => a + d.steps, 0),
      avg: mean(stepDays.map((d) => d.steps)),
      top: { date: topSteps.date, steps: topSteps.steps },
    } : null,
    training: workouts.length || trained.length ? {
      days: trained.length,
      workouts: workouts.length,
      hours: workouts.reduce((a, w) => a + (w.dur || 0), 0) / 3600,
      km: workouts.reduce((a, w) => a + (w.dist || 0), 0) / 1000,
      longest: longest ? { date: longest.date, type: longest.type, dur: longest.dur, dist: longest.dist } : null,
      top,
      trainings: myTrainings.length,
      sessions: mySessions.length, sets: work.length, volume: work.reduce((a, s) => a + s.kg * s.reps, 0),
    } : null,
    bests: liftBests.length || wodBests.length ? { lifts: liftBests, wods: wodBests, n: liftBests.reduce((a, b) => a + b.n, 0) + wodBests.reduce((a, b) => a + b.n, 0) } : null,
    sleep: nights.length ? {
      asleep: mean(nights.map((d) => d.sleep.asleep)),
      readiness: ready.length ? mean(ready) : null,
      bestMonth: bestSleep ? { month: bestSleep.month, score: bestSleep.sleep } : null,
    } : null,
    months,
    foods,
  };
}

// Entries (in date order) grouped by key, the groups in the order of their
// latest entry.
function groupBests(entries, keyOf) {
  const groups = new Map();
  for (const e of entries) {
    const k = keyOf(e);
    if (!groups.has(k)) groups.set(k, []);
    groups.get(k).push(e);
  }
  return [...groups.values()].sort((a, b) => b.at(-1).date.localeCompare(a.at(-1).date));
}
