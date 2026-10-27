// The Macronutrients card: protein, carbs and fat as 7-day averages, and
// protein per kg body weight.

import { MACROS, avgOf, dfLong, fmt, logged, parse } from './format.js';
import { $, h, svg } from './dom.js';
import { state } from './state.js';
import { smoothPath, timeChart } from './charts.js';
import { macroGoal, proteinPerKg, trailingMean } from './insights.js';
import { t, tn } from './i18n.js';

export function renderMacros(s) {
  const { rows, complete } = s;
  const { byDate } = state;
  renderProteinPerKg(complete, byDate);
  const g = state.data.goals || {};
  // With goals, every macro is shown as % of that day's goal so all three share one axis.
  const pct = MACROS.every((m) => macroGoal(null, m.key, g));
  const loggedRows = complete.filter(logged);
  const avgs = MACROS.map((m) => avgOf(loggedRows, m.key, logged) || 0);
  const kcal = avgs.map((v, i) => v * MACROS[i].kcal);
  const total = kcal.reduce((a, b) => a + b, 0);

  $('#macro-sub').textContent = pct ? t("7-day average, as % of each day's goal") : t('7-day average in grams');
  $('#macro-legend').replaceChildren(...MACROS.map((m, i) => {
    const li = h('li');
    li.append(h('span', { class: 'key line ' + m.cls }), document.createTextNode(m.label + ' '));
    li.append(h('b', {}, `${fmt(avgs[i])} g`));
    li.append(document.createTextNode(total ? ' · ' + t('{pct}% of kcal', { pct: fmt((kcal[i] / total) * 100) }) : ''));
    return li;
  }));

  // Trailing 7-day mean over complete logged days, reaching back before the range start.
  const lines = MACROS.map((m) => rows.map((r) => trailingMean(byDate, r.date, (day) => (pct ? (day[m.key] / macroGoal(day, m.key, g)) * 100 : day[m.key]), state.today)));
  const all = lines.flat().filter((v) => v != null);
  const hi = Math.max(pct ? 110 : 0, ...all);

  timeChart($('#chart-macros'), {
    aria: t('Protein, carbs and fat, 7-day averages'),
    kind: 'line',
    empty: !all.length,
    n: rows.length,
    dates: rows.map((r) => parse(r.date)),
    y: [0, hi * 1.05],
    yFmt: pct ? (t) => fmt(t) + '%' : null,
    goal: pct ? { value: 100, label: t('Goal') } : null,
    draw(gr, { x, y }) {
      MACROS.forEach((m, k) => gr.append(svg('path', { d: smoothPath(lines[k], x, y), class: 'mark-line ' + m.cls, pathLength: 1 })));
    },
    tip(i) {
      if (lines.every((l) => l[i] == null)) return null;
      const r = rows[i];
      return {
        title: dfLong.format(parse(r.date)),
        rows: MACROS.map((m, k) => lines[k][i] != null && {
          cls: m.cls,
          value: pct ? `${fmt(lines[k][i])}%` : `${fmt(lines[k][i])} g`,
          label: m.label + (logged(r) ? ' · ' + t('{g} g of {goal} g that day', { g: fmt(r[m.key]), goal: fmt(macroGoal(r, m.key, g)) }) : ''),
        }),
      };
    },
    onPick: (i) => rows[i],
  });
}

// Protein per kg body weight, the unit sports nutrition advice uses.
function renderProteinPerKg(rows, byDate) {
  const p = proteinPerKg(rows, byDate);
  const host = $('#protein-kg');
  host.hidden = !p.all.n;
  if (host.hidden) return;
  const stat = (label, v, note) => {
    const el = h('div', { class: 'stat' });
    el.append(h('div', { class: 'k' }, label));
    const val = h('div', { class: 'v' }, v.avg != null ? fmt(v.avg, 1) : '–');
    if (v.avg != null) val.append(h('small', {}, 'g/kg'));
    el.append(val, h('p', { class: 'note' }, note(v.n)));
    return el;
  };
  const days = (n) => tn(n, '{n} day', '{n} days');
  host.replaceChildren(
    stat(t('Protein per kg'), p.all, (n) => t('Body weight from the 7-day average') + ' · ' + days(n)),
    stat(t('Workout days'), p.workout, days),
    stat(t('Rest days'), p.rest, days),
  );
}
