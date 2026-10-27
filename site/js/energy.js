// The Calories card: intake against the goal over time, the split by meal
// and the average per weekday.

import { MEALS, avgOf, fmt, logged, mean, signed } from './format.js';
import { $, h, svg, meter } from './dom.js';
import { state } from './state.js';
import { bucketTitle, drawBars, drawGoalMarks, fadeArea, goalMarks, goalTipRows, linePath, smoothPath, stepPath, timeChart, valuePill } from './charts.js';
import { trailingMean, weekdayAverages } from './insights.js';
import { t, tn } from './i18n.js';

export function renderEnergy(s) {
  const { buckets, weekly, complete } = s;
  // Yazio raises the goal by the calories burned through activities and steps,
  // so the chart shows the fixed base goal as a dashed line and each day's
  // full goal (base + activity) as a solid line.
  const fallback = state.data.goals?.energy;
  const vals = buckets.map((b) => avgOf(b.rows, 'energy'));
  const base = buckets.map((b) => avgOf(b.rows, 'energy_base') ?? fallback ?? null);
  const goals = buckets.map((b, i) => avgOf(b.rows, 'energy_goal') ?? base[i]);
  // Fitbit's estimate of everything burned that day, resting metabolism included.
  const burned = buckets.map((b) => avgOf(b.rows, 'kcal_burned'));
  const hasBurned = burned.some((v) => v != null);
  $('#leg-burned').hidden = !hasBurned;
  const avg = avgOf(complete, 'energy');
  const lastBase = base.findLast((v) => v != null);
  $('#cal-sub').textContent = t('Daily average {kcal} kcal', { kcal: fmt(avg) })
    + (lastBase ? ' · ' + t('base goal {kcal} kcal plus calories burned', { kcal: fmt(lastBase) }) : '') + (weekly ? ' · ' + t('weekly averages') : '');
  // A week or less: one bar per day reads better than a line.
  const bars = s.rows.length <= 7;
  const max = Math.max(...vals.map((v) => v || 0), ...goals.map((v) => v || 0), ...base.map((v) => v || 0), ...burned.map((v) => v || 0));
  const marks = goalMarks(buckets, ['energy']);
  // Day by day, a 7-day average over complete logged days (reaching back
  // before the range) shows the trend through the daily ups and downs.
  const avg7 = bars || weekly ? null : s.rows.map((r) => trailingMean(state.byDate, r.date, (day) => day.energy, state.today, { min: 3 }));
  const trend = avg7 || vals;
  $('#leg-avg').hidden = !avg7;
  $('#leg-avg').previousElementSibling.querySelector('.key').classList.toggle('soft', !!avg7);

  timeChart($('#chart-energy'), {
    aria: t('Calories eaten per day compared with the goal'),
    kind: bars ? 'band' : 'line',
    empty: !max,
    n: buckets.length,
    dates: buckets.map((b) => b.start),
    y: [0, max * 1.08],
    focusCls: 'focus-dot',
    draw(g, ctx) {
      const { x, y, bw, step } = ctx;
      if (bars) drawBars(g, vals, 's1', x, y, bw);
      else {
        fadeArea(g, 'fade-energy', 's1', trend, ctx);
        if (avg7) g.append(svg('path', { d: linePath(vals, x, y), class: 'mark-line s1 soft', pathLength: 1 }));
      }
      // Thinner next to the 7-day average, which carries the chart.
      const thin = avg7 ? ' thin' : '';
      g.append(svg('path', { d: linePath(goals, x, y), class: 'mark-line ink' + thin, pathLength: 1 }));
      if (hasBurned) g.append(svg('path', { d: linePath(burned, x, y), class: 'mark-line s2 burned' + thin, pathLength: 1 }));
      // Base goal as a step line, so a changed base goal shows where it changed.
      g.append(svg('path', { d: stepPath(base, x, y, step), class: 'goal-line' }));
      const first = base.find((v) => v != null);
      if (first != null) g.append(svg('text', { x: x(0) - step / 2 + 6, y: y(first) + 15, class: 'goal-label' }, t('Base {kcal}', { kcal: fmt(first) })));
      if (!bars) g.append(svg('path', { d: smoothPath(trend, x, y), class: 'mark-line s1', pathLength: 1 }));
      // Goal changes over the lines and bars, so their chips stay readable.
      drawGoalMarks(g, marks, ctx);
      if (!bars) {
        const li = trend.findLastIndex((v) => v != null);
        if (li >= 0) valuePill(g, x(li), y(trend[li]), `Ø ${fmt(trend[li])} kcal`, ctx);
      }
    },
    tip(i) {
      const out = [];
      out.push(vals[i] != null
        ? { cls: bars ? 'rect s1' : avg7 ? 's1-soft' : 's1', value: fmt(vals[i]) + ' kcal', label: weekly ? t('avg eaten') : t('eaten') }
        : { value: t('Not logged'), label: '' });
      if (avg7?.[i] != null) out.push({ cls: 's1', value: fmt(avg7[i]) + ' kcal', label: t('7-day average') });
      if (goals[i] != null) {
        const bonus = base[i] != null ? goals[i] - base[i] : 0;
        out.push({ cls: 'ink', value: fmt(goals[i]) + ' kcal', label: bonus >= 1 ? t('goal ({base} + {bonus} activity)', { base: fmt(base[i]), bonus: fmt(bonus) }) : t('goal') });
        if (vals[i]) out.push({ value: signed(vals[i] - goals[i]) + ' kcal', label: t('vs goal') });
      }
      if (burned[i] != null) {
        out.push({ cls: 's2', value: fmt(burned[i]) + ' kcal', label: weekly ? t('avg burned (Fitbit)') : t('burned (Fitbit)') });
        if (vals[i]) out.push({ value: signed(vals[i] - burned[i]) + ' kcal', label: t('eaten vs burned') });
      }
      out.push(...goalTipRows(marks, i));
      return { title: bucketTitle(buckets[i], weekly), rows: out, y: vals[i] ?? goals[i] };
    },
    onPick: weekly ? null : (i) => buckets[i].rows[0],
  });
}

export function renderMeals(s) {
  const loggedRows = s.complete.filter(logged);
  const avgs = MEALS.map(([k]) => mean(loggedRows.map((r) => r.meals?.[k] || 0)) || 0);
  const total = avgs.reduce((a, b) => a + b, 0);
  const max = Math.max(...avgs, 1);
  $('#meals').replaceChildren(...MEALS.map(([, label], i) => {
    const el = h('div', { class: 'stat' });
    const k = h('div', { class: 'k' });
    k.append(h('span', {}, label), h('span', {}, total ? `${fmt((avgs[i] / total) * 100)}%` : ''));
    const v = h('div', { class: 'v' }, fmt(avgs[i]));
    v.append(h('small', {}, t('kcal avg')));
    el.append(k, v, meter(avgs[i] / max));
    return el;
  }));
}

const WEEKDAYS = [t('Mon'), t('Tue'), t('Wed'), t('Thu'), t('Fri'), t('Sat'), t('Sun')];

export function renderWeekdays(s) {
  const days = weekdayAverages(s.complete);
  const all = mean(s.complete.filter(logged).map((r) => r.energy));
  const max = Math.max(...days.map((d) => d.avg || 0), 1);
  $('#weekdays').replaceChildren(...days.map((d, i) => {
    const el = h('div', { class: 'wd', role: 'listitem', title: d.n ? `${WEEKDAYS[i]}: ` + tn(d.n, '{kcal} kcal on average over {n} day', '{kcal} kcal on average over {n} days', { kcal: fmt(d.avg) }) : `${WEEKDAYS[i]}: ` + t('not logged') });
    const bar = h('div', { class: 'wd-bar' });
    const fill = h('div', { class: 'wd-fill' + (all && d.avg > all ? ' above' : '') });
    fill.style.height = `${((d.avg || 0) / max) * 100}%`;
    bar.append(fill);
    el.append(bar, h('b', {}, d.avg ? fmt(d.avg) : '–'), h('span', {}, WEEKDAYS[i]));
    return el;
  }));
}
