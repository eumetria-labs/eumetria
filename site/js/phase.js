// Phases (a cut, a bulk, …) and events (a competition) in numbers: where
// they fall on a chart's slots and what a phase's days hold. The window
// and the chart layer are in phases.js and charts.js.

import { MACROS, addDays, iso, logged, mean, parse } from './format.js';
import { weightTrendAt } from './insights.js';
import { t } from './i18n.js';

export const KINDS = {
  cut: { label: t('Cut|phase'), icon: '🔻' },
  bulk: { label: t('Bulk|phase'), icon: '🔺' },
  maintain: { label: t('Maintain|phase'), icon: '⚖️' },
  other: { label: t('Other|phase'), icon: '📌' },
  event: { label: t('Event'), icon: '🏁' },
};

export const isEvent = (p) => p.kind === 'event';
export const phaseName = (p) => p.label || KINDS[p.kind]?.label || p.kind;
// The last day of a phase; one still running ends today.
export const lastDay = (p, today) => p.end || today;

// The phase (not event) that covers date, the latest-starting one if
// several overlap.
export const phaseOn = (phases, date, today) =>
  phases.filter((p) => !isEvent(p) && p.start <= date && date <= lastDay(p, today)).at(-1) || null;

// Where phases and events fall on a chart whose slot i starts on dates[i]
// (days or weeks: a slot runs to the next one's start, the last one as
// long as the one before it). Bands span the slots a phase touches; an
// event marks the slot its day lies in.
export function phaseSlots(dates, phases, today) {
  const starts = dates.map((d) => iso(d));
  const n = starts.length;
  if (!n) return { bands: [], events: [] };
  const span = n > 1 ? (dates[n - 1] - dates[n - 2]) / 864e5 : 1;
  const ends = starts.map((_, i) => (i < n - 1 ? starts[i + 1] : iso(addDays(dates[n - 1], span))));
  const slotOf = (date) => starts.findIndex((s, i) => s <= date && date < ends[i]);
  const bands = [], events = [];
  for (const p of phases) {
    if (isEvent(p)) {
      const i = slotOf(p.start);
      if (i >= 0) events.push({ i, phase: p });
      continue;
    }
    const last = lastDay(p, today);
    let from = -1, to = -1;
    for (let i = 0; i < n; i++) {
      if (p.start < ends[i] && last >= starts[i]) {
        if (from < 0) from = i;
        to = i;
      }
    }
    if (from >= 0) bands.push({ from, to, phase: p });
  }
  return { bands, events };
}

// A day counts as training with a Fitbit workout, a Yazio activity or a
// noted lift or benchmark workout (noted: a Set of dates).
export const trainedOn = (r, noted = new Set()) => !!(r.workouts?.length || r.activities?.length || noted.has(r.date));

// What a phase's days hold, up to today: the weight change (7-day average
// at the start and at the end), the average calories and macros on logged
// days, and the training days per week.
export function phaseSummary(p, byDate, today, noted = new Set()) {
  const last = lastDay(p, today) < today ? lastDay(p, today) : today;
  const days = [];
  for (let d = parse(p.start); iso(d) <= last; d = addDays(d, 1)) days.push(byDate.get(iso(d)) || { date: iso(d) });
  // Today's partial totals stay out of the averages.
  const fed = days.filter((r) => logged(r) && r.date !== today);
  const w0 = weightTrendAt(byDate, iso(addDays(parse(p.start), 6))), w1 = weightTrendAt(byDate, last);
  const trained = days.filter((r) => trainedOn(r, noted)).length;
  return {
    days: days.length,
    logged: fed.length,
    weight: w0 != null && w1 != null && days.length >= 14 ? { from: w0, to: w1, change: w1 - w0 } : null,
    energy: mean(fed.map((r) => r.energy)),
    protein: mean(fed.map((r) => r.protein).filter((v) => v > 0)),
    perWeek: days.length >= 7 ? (trained / days.length) * 7 : null,
  };
}

// Calories and macros on training days next to rest days, over the logged
// days of rows (today's partial totals left out by the caller). Each side
// has its number of days, the averages and the calories vs the day's goal.
export function trainingVsRest(rows, noted = new Set()) {
  const side = (rs) => ({
    n: rs.length,
    energy: mean(rs.map((r) => r.energy)),
    vsGoal: mean(rs.filter((r) => r.energy_goal > 0).map((r) => r.energy - r.energy_goal)),
    ...Object.fromEntries(MACROS.map((m) => [m.key, mean(rs.map((r) => r[m.key]).filter((v) => v > 0))])),
  });
  const fed = rows.filter(logged);
  return { training: side(fed.filter((r) => trainedOn(r, noted))), rest: side(fed.filter((r) => !trainedOn(r, noted))) };
}
