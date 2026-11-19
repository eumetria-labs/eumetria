// Body composition from a Withings scale (stats: Day.body, weight_src): the
// Weight card's "Body composition" tab, a weigh-in chip on the Today card,
// and a phase's weight change split into fat and lean mass. Everything
// stays hidden while there are no measurements.

import { addDays, dfLong, fmt, iso, parse, signed } from './format.js';
import { $, h, emoji, svg, panelTabs } from './dom.js';
import { state, store } from './state.js';
import { fadeArea, smoothPath, timeChart, valuePill } from './charts.js';
import { openDay } from './sheet.js';
import { t } from './i18n.js';

export const hasBody = () => state.data.days.some((d) => d.body);

// The measures the tab lists, in the order shown: key, label, unit, digits.
const PARTS = [
  ['fat_pct', () => t('Body fat'), '%', 1],
  ['fat_kg', () => t('Fat mass'), 'kg', 1],
  ['lean_kg', () => t('Lean mass'), 'kg', 1],
  ['muscle_kg', () => t('Muscle mass'), 'kg', 1],
  ['water_kg', () => t('Water'), 'kg', 1],
  ['bone_kg', () => t('Bone mass'), 'kg', 1],
  ['visceral', () => t('Visceral fat'), '', 0],
];

// A measure's average over the weigh-ins of the 7 days up to date.
function trendAt(key, date) {
  const vals = [];
  for (let k = 0; k < 7; k++) {
    const v = state.byDate.get(isoMinus(date, k))?.body?.[key];
    if (v) vals.push(v);
  }
  return vals.length ? vals.reduce((a, b) => a + b, 0) / vals.length : null;
}
const isoMinus = (date, k) => iso(addDays(parse(date), -k));

// renderBody draws the tab for the range's rows: body fat over time with
// its 7-day average, and the measures with their change over the range.
export function renderBody(s) {
  const panel = $('#weight-body');
  if (!panel) return;
  // The tabs only once the scale brought something; the legend is the
  // weight chart's.
  const legend = $('#weight-card .card-head .legend');
  panelTabs($('#weight-tabs'), [['weight', t('Weight'), '⚖️'], ...(hasBody() ? [['body', t('Body composition'), '🧬']] : [])],
    store.get('weightTab'), (key) => { store.set('weightTab', key); legend.hidden = key === 'body'; });
  legend.hidden = !panel.hidden;
  if (panel.hidden && !hasBody()) return;
  const rows = s.rows;
  const weighed = rows.filter((r) => r.body);
  if (!weighed.length) {
    $('#body-stats').replaceChildren(h('p', { class: 'empty-tab' }, t('No weigh-ins with body composition in this range.')));
    $('#chart-body').replaceChildren();
    return;
  }
  const first = weighed[0], last = weighed.at(-1);
  const stats = $('#body-stats');
  stats.replaceChildren(...PARTS.filter(([k]) => last.body[k]).map(([k, label, unit, digits]) => {
    const el = h('div', { class: 'stat' });
    const v = h('div', { class: 'v' }, fmt(last.body[k], digits));
    if (unit) v.append(h('small', {}, unit));
    el.append(h('div', { class: 'k' }, label()), v);
    if (first !== last && first.body[k]) {
      const d = last.body[k] - first.body[k];
      el.append(h('div', { class: 'sub muted' }, `${signed(d, digits)}${unit ? ' ' + unit : ''}`));
    }
    return el;
  }));
  const pts = rows.map((r) => r.body?.fat_pct ?? null);
  const trend = rows.map((r) => trendAt('fat_pct', r.date));
  const vals = [...pts, ...trend].filter((v) => v != null);
  const lo = Math.min(...vals), hi = Math.max(...vals);
  timeChart($('#chart-body'), {
    aria: t('Body fat over time'),
    kind: 'line',
    empty: !vals.length,
    n: rows.length,
    dates: rows.map((r) => parse(r.date)),
    y: [Math.floor(lo - 0.5), Math.ceil(hi + 0.5)],
    yFmt: (v) => `${fmt(v, Number.isInteger(v) ? 0 : 1)} %`,
    phases: true,
    draw(gr, ctx) {
      const { x, y } = ctx;
      fadeArea(gr, 'fade-body', 's2', trend, ctx);
      pts.forEach((v, i) => { if (v != null) gr.append(svg('circle', { cx: x(i), cy: y(v), r: rows.length > 120 ? 3 : 4, class: 'mark-dot s2-soft' })); });
      gr.append(svg('path', { d: smoothPath(trend, x, y), class: 'mark-line s2', pathLength: 1 }));
      const li = trend.findLastIndex((v) => v != null);
      if (li >= 0) valuePill(gr, x(li), y(trend[li]), `Ø ${fmt(trend[li], 1)} %`, ctx);
    },
    tip(i) {
      const b = rows[i].body;
      if (!b && trend[i] == null) return null;
      return {
        title: dfLong.format(parse(rows[i].date)) + (b?.time ? ` · ${b.time}` : ''),
        rows: [
          b?.fat_pct && { cls: 'dot s2-soft', value: `${fmt(b.fat_pct, 1)} %`, label: t('body fat') },
          trend[i] != null && { cls: 's2', value: `${fmt(trend[i], 1)} %`, label: t('7-day average') },
          b?.lean_kg && { value: `${fmt(b.lean_kg, 1)} kg`, label: t('lean mass') },
          b?.muscle_kg && { value: `${fmt(b.muscle_kg, 1)} kg`, label: t('muscle mass') },
        ].filter(Boolean),
        y: trend[i] ?? pts[i],
      };
    },
    onPick: (i) => rows[i],
  });
}

// bodyChip is the Today card's chip for a weigh-in on the scale today, or
// null.
export function bodyChip() {
  const d = state.byDate?.get(state.today);
  if (!d?.body || d.weight_src !== 'withings') return null;
  const b = h('button', { class: 'highlight', type: 'button', role: 'listitem', title: t('Open {date}', { date: dfLong.format(parse(d.date)) }) });
  const parts = [d.body.fat_pct ? t('fat {pct} %', { pct: fmt(d.body.fat_pct, 1) }) : '', d.body.muscle_kg ? t('muscle {kg} kg', { kg: fmt(d.body.muscle_kg, 1) }) : ''].filter(Boolean);
  b.append(emoji('⚖️'), h('span', { class: 'hl-text' }, [d.body.time ? t('Weighed in at {time}', { time: d.body.time }) : t('Weighed in'), ...parts].join(' · ')), h('b', {}, `${fmt(d.weight, 1)} kg`));
  b.onclick = () => openDay(d.date);
  return b;
}

// compositionChange is a phase's change in fat and lean mass: the first
// and last weigh-in with body composition between from and to (the 7-day
// averages there), or null with fewer than two.
export function compositionChange(from, to) {
  const days = state.data.days.filter((d) => d.body?.fat_kg && d.date >= from && d.date <= to);
  if (days.length < 2) return null;
  const a = days[0].date, b = days.at(-1).date;
  const fat = trendAt('fat_kg', b) - trendAt('fat_kg', a);
  const lean = trendAt('lean_kg', b) - trendAt('lean_kg', a);
  if (Number.isNaN(fat) || Number.isNaN(lean)) return null;
  return { fat, lean };
}
