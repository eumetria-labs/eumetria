// The Weight card: weigh-ins with their 7-day trend, and the maintenance,
// balance and goal estimates below the chart.

import { addDays, avgOf, dfLong, dfMonthYear, fmt, iso, parse, signed } from './format.js';
import { $, h, svg } from './dom.js';
import { state } from './state.js';
import { drawGoalMarks, fadeArea, goalMarks, goalTipRows, linePath, smoothPath, stepPath, timeChart, valuePill } from './charts.js';
import { energyBalance, goalForecast, slope, weightForecast, weightGoalAt, weightGoalLine, weightTrendAt } from './insights.js';
import { t, tn } from './i18n.js';

// The rows the card shows, for its share button (cards.js).
let shownRows = [];
export const weightRows = () => shownRows;

export function renderWeight(s) {
  const { rows } = s;
  shownRows = rows;
  const g = state.data.goals || {};
  // Trailing 7-day mean of weigh-ins, looking back before the range start.
  const trend = rows.map((r) => weightTrendAt(state.byDate, r.date));
  const pts = rows.map((r) => r.weight ?? null);

  const weighed = rows.map((r, i) => [i, r.weight]).filter(([, w]) => w != null);
  const perDay = weighed.length >= 3 ? slope(weighed) : null;
  $('#weight-sub').textContent = tn(weighed.length, '{n} weigh-in', '{n} weigh-ins') + (perDay != null ? ' · ' + t('trend {kg} kg/week', { kg: signed(perDay * 7, 1) }) : '');

  const f = forecastFor(rows);
  const n = f?.n ?? rows.length;
  const ahead = f?.ahead ?? [];
  const dates = Array.from({ length: n }, (_, i) => addDays(parse(rows[0].date), i));
  const vals = [...pts, ...trend, ...ahead].filter((v) => v != null);

  // The weight goal as it was on each day: a step line when it changed.
  const goalAt = dates.map((d) => weightGoalAt(state.data.goal_changes, iso(d), g.weight));
  const { goals: shown, lo, hi } = weightGoalLine(goalAt, Math.min(...vals), Math.max(...vals));
  // One point per day, also on long ranges.
  const marks = goalMarks(rows.map((r) => ({ start: parse(r.date), rows: [r] })), ['energy', 'weight']);

  timeChart($('#chart-weight'), {
    aria: t('Weight over time'),
    kind: 'line',
    empty: !vals.length,
    n,
    dates,
    y: [lo - 0.5, hi + 0.5],
    last: rows.length - 1,
    draw(gr, ctx) {
      drawWeight(gr, ctx, { days: rows.length, trend, pts, goals: shown, marks, f, id: 'fade-weight', dot: { r: rows.length > 120 ? 3 : 4, cls: 'mark-dot s1-soft' } });
    },
    tip(i) {
      if (i >= rows.length) {
        return ahead[i] == null ? null : { title: dfLong.format(dates[i]), rows: [{ cls: 's1', value: fmt(ahead[i], 1) + ' kg', label: t('forecast') }], y: ahead[i] };
      }
      if (pts[i] == null && trend[i] == null) return null;
      return {
        title: dfLong.format(parse(rows[i].date)),
        rows: [
          pts[i] != null && { cls: 'dot s1-soft', value: fmt(pts[i], 1) + ' kg', label: t('weigh-in') },
          trend[i] != null && { cls: 's1', value: fmt(trend[i], 1) + ' kg', label: t('7-day average') },
          ...goalTipRows(marks, i),
        ],
        y: trend[i] ?? pts[i],
      };
    },
    onPick: (i) => rows[i],
  });
}

// The weight chart's marks, on the page and in the printed report: the
// forecast band, the goal as a step line, the 7-day trend over its fade,
// the weigh-ins, goal changes, the forecast and the current value. days is
// how many slots are real days; dot sets the weigh-in dots' size and class.
export function drawWeight(gr, ctx, { days, trend, pts, goals, marks, f, id, dot }) {
  const { x, y, step, top, bottom, right } = ctx;
  if (f && f.n > days) {
    // The days ahead, shaded and named.
    const fx = x(days - 1) + step / 2;
    gr.append(svg('rect', { x: fx, y: top, width: right - fx, height: bottom - top, class: 'future-band' }));
    gr.append(svg('text', { x: right - 6, y: top + 13, 'text-anchor': 'end', class: 'future-label' }, t('Forecast')));
  }
  if (goals.some((v) => v != null)) {
    gr.append(svg('path', { d: stepPath(goals, x, y, step), class: 'goal-line' }));
    // Labelled where the current goal's segment starts.
    let gi = goals.findLastIndex((v) => v != null);
    while (gi > 0 && goals[gi - 1] === goals[gi]) gi--;
    gr.append(svg('text', { x: Math.max(x(gi) - step / 2, x(0)) + 6, y: y(goals[gi]) - 6, class: 'goal-label' }, t('Goal {kg} kg', { kg: fmt(goals[gi], 1) })));
  }
  // The trend as a smooth line over a fading fill.
  fadeArea(gr, id, 's1', trend, ctx);
  pts.forEach((v, i) => { if (v != null) gr.append(svg('circle', { cx: x(i), cy: y(v), r: dot.r, class: dot.cls })); });
  gr.append(svg('path', { d: smoothPath(trend, x, y), class: 'mark-line s1', pathLength: 1 }));
  // Goal changes over the dots, so their chips stay readable.
  drawGoalMarks(gr, marks, ctx);
  if (f) drawForecast(gr, ctx, f);
  // The latest 7-day average at the end of the line.
  const li = trend.findLastIndex((v) => v != null);
  if (li >= 0) valuePill(gr, x(li), y(trend[li]), `Ø ${fmt(trend[li], 1)} kg`, ctx);
}

// When rows reach the latest complete day, the weight trend carried past
// them at the 4-week rate the estimates use: the chart's length n with the
// days ahead, and the forecast per index (ahead) from the last day (iEnd).
export function forecastFor(rows) {
  const ol = outlook();
  const iEnd = rows.findIndex((r) => r.date === ol.end);
  const reaches = iEnd >= 0 && rows.slice(iEnd + 1).every((r) => r.date === state.today);
  if (!reaches || !ol.eb.ok || ol.current == null) return null;
  const fc = weightForecast(ol.current, ol.eb.kgPerWeek, ol.goal, rows.length);
  const n = Math.max(rows.length, iEnd + fc.days + 1);
  const ahead = Array(n).fill(null);
  ahead[iEnd] = ol.current;
  fc.values.forEach((v, k) => { ahead[iEnd + k + 1] = v; });
  return { n, ahead, iEnd, fc, ol };
}

// The forecast as a dotted line from the last day, with a pin where it
// meets the goal, or the expected date at its end when that is further out.
export function drawForecast(gr, ctx, { ahead, iEnd, fc, ol }) {
  const { x, y } = ctx;
  gr.append(svg('path', { d: linePath(ahead, x, y), class: 'forecast-line' }));
  const when = ol.fc?.date ? dfMonthYear.format(parse(ol.fc.date)) : '';
  if (fc.hit != null) {
    const px = x(iEnd + fc.hit), py = y(ol.goal);
    gr.append(svg('circle', { cx: px, cy: py, r: 5, class: 'forecast-pin' }));
    // Below the pin when losing weight, clear of the current value above.
    if (when) valuePill(gr, px, py, `${fmt(ol.goal, 1)} kg · ${when}`, ctx, 'ghost', ol.goal < ol.current);
  } else if (when) {
    // On the goal's side of the line, clear of the value label above it.
    const last = ahead.findLastIndex((v) => v != null);
    valuePill(gr, x(last), y(ahead[last]), `${fmt(ol.goal, 1)} kg · ${when} →`, ctx, 'ghost', ol.goal < ahead[last]);
  }
}

// The 4-week energy balance and where its weight trend leads; the chart's
// forecast and the estimates below it share it. The last day counts only
// once it is complete.
function outlook() {
  const { data, byDate } = state;
  const lastDay = data.days.at(-1).date;
  const end = lastDay === state.today ? iso(addDays(parse(lastDay), -1)) : lastDay;
  const eb = energyBalance(byDate, end);
  const current = weightTrendAt(byDate, end);
  const goal = data.goals?.weight;
  return { end, eb, current, goal, fc: eb.ok ? goalForecast(current, eb.kgPerWeek, goal, end) : null };
}

// Fitbit's average total burn over the last 4 weeks, to compare with the
// maintenance estimate from weight and intake.
function fitbitBurned(byDate, end, stat) {
  const rows = [];
  for (let d = parse(end), k = 0; k < 28; d = addDays(d, -1), k++) {
    const r = byDate.get(iso(d));
    if (r?.kcal_burned) rows.push(r);
  }
  if (rows.length < 7) return [];
  return [stat(t('Fitbit burned'), fmt(avgOf(rows, 'kcal_burned')), t('kcal/day'), tn(rows.length, 'Fitbit estimate, {n} day', 'Fitbit estimate, {n} days'),
    t('Average of the total calories Fitbit estimates you burned per day (resting metabolism included) over the last 4 weeks.'))];
}

// Maintenance calories, energy balance and goal forecast over the last four
// complete weeks; independent of the selected range so they stay stable.
export function renderInsights() {
  const { byDate } = state;
  const { end, eb, current, goal, fc } = outlook();

  const stat = (label, value, unit, note, hint) => {
    const el = h('div', { class: 'stat', title: hint || '' });
    el.append(h('div', { class: 'k' }, label));
    const v = h('div', { class: 'v' }, value);
    if (unit) v.append(h('small', {}, unit));
    el.append(v, h('p', { class: 'note' }, note));
    return el;
  };
  const method = t('Average logged intake minus the energy your weight change stands for ({kcal} kcal per kg), over the last {n} days.', { kcal: fmt(7700), n: eb.windowDays });
  const missing = t('Needs 14 logged days and 6 weigh-ins in the last 4 weeks (now {logged} and {weighIns})', { logged: eb.logged, weighIns: eb.weighIns });
  const items = eb.ok
    ? [
      stat(t('Maintenance'), fmt(eb.maintenance), t('kcal/day'), t('Estimated from the last 4 weeks'), method),
      ...fitbitBurned(byDate, end, stat),
      stat(t('Balance'), signed(eb.balance), t('kcal/day'), t('Avg intake {kcal} kcal', { kcal: fmt(eb.intake) }) + ' · ' + t('{kg} kg/week', { kg: signed(eb.kgPerWeek, 1) }), method),
    ]
    : [stat(t('Maintenance'), '–', '', missing, method), stat(t('Balance'), '–', '', t('Shown once maintenance can be estimated'), method)];

  if (goal) {
    const label = t('Goal {kg} kg', { kg: fmt(goal, 1) });
    const rate = signed(eb.kgPerWeek, 1);
    if (!fc) items.push(stat(label, '–', '', t('Needs the 4-week trend')));
    else if (fc.status === 'reached') items.push(stat(label, t('Reached'), '', t('7-day average {kg} kg', { kg: fmt(current, 1) })));
    else if (fc.status === 'off-track') items.push(stat(label, t('Not yet'), '', t('The 4-week trend ({kg} kg/week) is not heading there', { kg: rate })));
    else if (fc.status === 'too-far') items.push(stat(label, t('3+ years'), '', t('At {kg} kg/week', { kg: rate })));
    else items.push(stat(label, dfMonthYear.format(parse(fc.date)), '', tn(Math.round(fc.weeks), 'In about {n} week at {kg} kg/week', 'In about {n} weeks at {kg} kg/week', { kg: rate })));
  }
  $('#weight-insights').replaceChildren(...items);
}
