// The printable report of a date range: an A4 document with the key numbers,
// charts and tables, built into #report and printed (or saved as PDF) with
// the browser's print dialog. The page itself is hidden while it prints.

import { LOCALE, MACROS, MEALS, actLabel, addDays, dfShort, fmt, fmtDuration, iso, logged, mean, parse, signed } from './format.js';
import { h, svg, textWidth } from './dom.js';
import { state } from './state.js';
import { drawGoalMarks, goalMarks, goalValue, niceTicks, stepPath } from './charts.js';
import { drawWeight, forecastFor } from './weight.js';
import { goalChangesIn, rangeSummary, weightGoalAt, weightGoalLine, weightTrendAt } from './insights.js';
import { t, tn } from './i18n.js';

const W = 688; // the A4 text width at 96 dpi, so chart text prints at its real size
const dfDate = new Intl.DateTimeFormat(LOCALE, { day: 'numeric', month: 'short', year: 'numeric', timeZone: 'UTC' });

export function printReport(from, to) {
  document.getElementById('report')?.remove();
  const report = buildReport(from, to);
  document.body.append(report);
  const root = document.documentElement;
  root.classList.add('printing');
  const done = () => {
    root.classList.remove('printing');
    report.remove();
    removeEventListener('afterprint', done);
  };
  addEventListener('afterprint', done);
  // One frame for layout, so the print preview has the charts.
  requestAnimationFrame(() => requestAnimationFrame(() => print()));
}

export function buildReport(from, to) {
  // Today is still being logged, so its totals stay out like on the page,
  // unless the report is about today alone.
  let rows = [];
  for (let d = parse(from); d <= parse(to); d = addDays(d, 1)) rows.push(state.byDate.get(iso(d)) || { date: iso(d) });
  if (rows.length > 1) rows = rows.filter((r) => r.date !== state.today);
  const sum = rangeSummary(rows, state.byDate);
  // Long ranges chart weekly averages, so the bars stay readable on paper.
  const weekly = rows.length > 92;
  const buckets = weekly ? sum.weeks.map((w) => ({ start: parse(w.start), rows: rows.filter((r) => r.date >= w.start && r.date <= w.end) }))
    : rows.map((r) => ({ start: parse(r.date), rows: [r] }));

  const el = h('article', { id: 'report', class: 'rp', lang: document.documentElement.lang });
  // The range as covered: without today, when it is the last day.
  el.append(header(rows[0].date, rows.at(-1).date, sum), kpis(sum));
  const charts = [energySection(buckets, weekly, sum), weightSection(rows, sum)];
  el.append(...charts.filter(Boolean));
  const split = macroSection(sum);
  if (split) el.append(split);
  for (const sec of [sleepSection(buckets, weekly, sum), stepsSection(buckets, weekly, sum), weeksSection(sum), listsSection(sum), goalsSection(from, to)]) {
    if (sec) el.append(sec);
  }
  el.append(h('p', { class: 'rp-foot' }, t('Created with Eumetria from YAZIO and Fitbit data. Calorie and macro averages count the days with food logged; weights are 7-day averages.')));
  return el;
}

/* ---------- header and key numbers ---------- */

function header(from, to, sum) {
  const head = h('header', { class: 'rp-head' });
  const brand = h('div', { class: 'rp-brand' });
  brand.append(h('img', { src: 'icons/mark.svg', alt: '', width: 22, height: 22 }), h('span', {}, state.data.title || 'Eumetria'));
  const created = h('span', { class: 'rp-created' }, t('Created {date}', { date: dfDate.format(parse(state.today || iso(new Date()))) }));
  const top = h('div', { class: 'rp-top' });
  top.append(brand, created);
  const range = from === to ? dfDate.format(parse(from)) : `${dfDate.format(parse(from))} – ${dfDate.format(parse(to))}`;
  const sub = [tn(sum.days, '{n} day', '{n} days'), tn(sum.logged, '{n} day with food logged', '{n} days with food logged')];
  if (sum.nights) sub.push(tn(sum.nights, '{n} night of sleep', '{n} nights of sleep'));
  head.append(top, h('h1', {}, t('Health report')), h('p', { class: 'rp-range' }, range), h('p', { class: 'rp-sub' }, sub.join(' · ')));
  return head;
}

function kpi(label, value, unit, note, cls = '') {
  const el = h('div', { class: 'rp-kpi ' + cls });
  const v = h('div', { class: 'rp-kpi-v' }, value);
  if (unit) v.append(h('small', {}, unit));
  el.append(h('div', { class: 'rp-kpi-k' }, label), v);
  if (note) el.append(h('div', { class: 'rp-kpi-n' }, note));
  return el;
}

function kpis(sum) {
  const items = [];
  if (sum.energy) {
    items.push(kpi(t('Calories'), fmt(sum.energy), t('kcal/day'), sum.goal ? t('Goal {kcal} kcal · {diff}', { kcal: fmt(sum.goal), diff: signed(sum.energy - sum.goal) }) : '', 's1'));
    items.push(kpi(t('Protein'), fmt(sum.protein), t('g/day'), sum.proteinPerKg ? t('{g} g per kg body weight', { g: fmt(sum.proteinPerKg, 1) }) : '', 's1'));
  }
  if (sum.weightTo != null) {
    const change = sum.weightFrom != null ? sum.weightTo - sum.weightFrom : null;
    items.push(kpi(t('Weight'), change != null ? signed(change, 1) : fmt(sum.weightTo, 1), 'kg',
      change != null ? `${fmt(sum.weightFrom, 1)} → ${fmt(sum.weightTo, 1)} kg` + (sum.kgPerWeek != null ? ' · ' + t('{kg} kg/week', { kg: signed(sum.kgPerWeek, 1) }) : '') : tn(sum.weighIns, '{n} weigh-in', '{n} weigh-ins'), 's2'));
  }
  if (sum.steps) items.push(kpi(t('Steps'), fmt(sum.steps), t('per day'), state.data.goals?.steps ? t('Goal {n}', { n: fmt(state.data.goals.steps) }) : '', 's3'));
  if (sum.asleep) items.push(kpi(t('Sleep'), fmtDuration(sum.asleep), '', [sum.deep && t('deep {d}', { d: fmtDuration(sum.deep) }), sum.rem && t('REM {d}', { d: fmtDuration(sum.rem) })].filter(Boolean).join(' · '), 'st'));
  if (sum.rhr) items.push(kpi(t('Resting heart rate'), fmt(sum.rhr), 'bpm', sum.hrv ? t('HRV {ms} ms', { ms: fmt(sum.hrv) }) : '', 'z4'));
  if (sum.workouts.n) items.push(kpi(t('Activities'), fmt(sum.workouts.n), '', [sum.workouts.dur && fmtDuration(sum.workouts.dur), sum.workouts.e && `${fmt(sum.workouts.e)} kcal`].filter(Boolean).join(' · '), 's3'));
  if (sum.burned) items.push(kpi(t('Burned (Fitbit)'), fmt(sum.burned), t('kcal/day'), sum.energy ? t('{diff} kcal/day vs eaten', { diff: signed(sum.energy - sum.burned) }) : '', 's2'));
  else if (sum.water) items.push(kpi(t('Water'), fmt(sum.water), t('ml/day'), '', 's1'));
  const grid = h('section', { class: 'rp-kpis' });
  grid.append(...items.slice(0, 8));
  return grid;
}

/* ---------- charts ---------- */

function section(title, note, ...content) {
  const sec = h('section', { class: 'rp-sec' });
  const head = h('div', { class: 'rp-sec-head' });
  head.append(h('h2', {}, title));
  if (note) head.append(note instanceof Node ? note : h('span', { class: 'rp-note' }, note));
  sec.append(head, ...content);
  return sec;
}

function legend(items) {
  const ul = h('ul', { class: 'rp-legend' });
  for (const [cls, label] of items) {
    const li = h('li');
    li.append(h('span', { class: 'rp-key ' + cls }), label);
    ul.append(li);
  }
  return ul;
}

// A static time chart: y grid with labels, about seven date labels, and
// draw(g, ctx) for the marks, with the same ctx as the page's charts.
function chart({ n, dates, lo = 0, hi, height = 170, yFmt = (v) => fmt(v), aria, draw }) {
  const ticks = niceTicks(lo, hi, 4);
  const labels = ticks.map(yFmt);
  const m = { t: 8, r: 4, b: 20, l: Math.max(...labels.map((l) => textWidth(l, 10))) + 10 };
  const pw = W - m.l - m.r, ph = height - m.t - m.b;
  const y = (v) => m.t + ph - ((v - ticks[0]) / (ticks.at(-1) - ticks[0])) * ph;
  const step = pw / n;
  const x = (i) => m.l + (i + 0.5) * step;
  const root = svg('svg', { viewBox: `0 0 ${W} ${height}`, class: 'rp-chart', role: 'img', 'aria-label': aria });
  const axis = svg('g', { class: 'axis' });
  ticks.forEach((v, k) => {
    axis.append(svg('line', { x1: m.l, x2: W - m.r, y1: y(v), y2: y(v), class: k ? 'gridline' : 'baseline' }));
    axis.append(svg('text', { x: m.l - 6, y: y(v) + 3.5, 'text-anchor': 'end' }, labels[k]));
  });
  const every = Math.max(1, Math.ceil(n / 7));
  for (let i = n - 1; i >= 0; i -= every) {
    const anchor = i === n - 1 ? 'end' : 'middle';
    axis.append(svg('text', { x: anchor === 'end' ? Math.min(W - m.r, x(i) + step / 2) : x(i), y: height - 5, 'text-anchor': anchor }, dfShort.format(dates[i])));
  }
  root.append(axis);
  const g = svg('g');
  draw(g, { x, y, step, bw: Math.max(1, Math.min(18, step * 0.72)), top: m.t, bottom: m.t + ph, right: W - m.r });
  root.append(g);
  return root;
}

const bucketAvg = (b, f) => mean(b.rows.map(f).filter((v) => v > 0));
const bar = (g, x, y, i, v, bw, cls, base = 0) => {
  if (!(v > 0)) return;
  g.append(svg('rect', { x: x(i) - bw / 2, y: y(base + v), width: bw, height: Math.max(0.5, y(base) - y(base + v)), rx: Math.min(2, bw / 4), class: 'rp-bar ' + cls }));
};
function energySection(buckets, weekly, sum) {
  if (!sum.logged) return null;
  const eaten = buckets.map((b) => bucketAvg(b, (r) => (logged(r) ? r.energy : 0)));
  const goal = buckets.map((b) => bucketAvg(b, (r) => r.energy_goal || r.energy_base) ?? null);
  const base = buckets.map((b) => bucketAvg(b, (r) => r.energy_base) ?? null);
  const hi = Math.max(...eaten.map((v) => v || 0), ...goal.map((v) => v || 0)) * 1.08;
  const marks = goalMarks(buckets, ['energy']);
  const svgEl = chart({
    n: buckets.length, dates: buckets.map((b) => b.start), hi, aria: t('Calories eaten per day compared with the goal'),
    draw(g, ctx) {
      const { x, y, bw, step } = ctx;
      eaten.forEach((v, i) => bar(g, x, y, i, v, bw, goal[i] && v > goal[i] * 1.05 ? 'over' : 's1'));
      g.append(svg('path', { d: stepPath(base, x, y, step), class: 'goal-line' }));
      g.append(svg('path', { d: stepPath(goal, x, y, step), class: 'rp-goal' }));
      // Goal changes over the bars, so their chips stay readable.
      drawGoalMarks(g, marks, ctx);
    },
  });
  const note = weekly ? t('Weekly averages') : t('Per day');
  return section(t('Calories'), note, legend([['s1-bar', t('Eaten')], ['over', t('More than 5 % over the goal')], ['goal', t('Goal incl. activity')], ['base', t('Base goal')]]), svgEl);
}

function weightSection(rows, sum) {
  if (!sum.weighIns) return null;
  const trend = rows.map((r) => weightTrendAt(state.byDate, r.date));
  const pts = rows.map((r) => r.weight ?? null);
  // The forecast like on the page, when the report reaches the latest day.
  const f = forecastFor(rows);
  const n = f?.n ?? rows.length;
  const ahead = f?.ahead ?? [];
  const dates = Array.from({ length: n }, (_, i) => addDays(parse(rows[0].date), i));
  const goal = dates.map((d) => weightGoalAt(state.data.goal_changes, iso(d), state.data.goals?.weight));
  const vals = [...pts, ...trend, ...ahead].filter((v) => v != null);
  const { goals: shown, lo, hi } = weightGoalLine(goal, Math.min(...vals), Math.max(...vals));
  const marks = goalMarks(rows.map((r) => ({ start: parse(r.date), rows: [r] })), ['energy', 'weight']);
  const svgEl = chart({
    n, dates, lo: lo - 0.5, hi: hi + 0.5, height: 170, yFmt: (v) => fmt(v, Number.isInteger(v) ? 0 : 1), aria: t('Weight over time'),
    draw(g, ctx) {
      drawWeight(g, ctx, { days: rows.length, trend, pts, goals: shown, marks, f, id: 'fade-rp-weight', dot: { r: rows.length > 120 ? 1.8 : 2.6, cls: 'rp-dot' } });
    },
  });
  const items = [['dot', t('Weigh-in')], ['s1', t('7-day average')]];
  if (shown.some((v) => v != null)) items.push(['base', t('Weight goal')]);
  if (f) items.push(['forecast', t('Forecast')]);
  return section(t('Weight'), tn(sum.weighIns, '{n} weigh-in', '{n} weigh-ins'), legend(items), svgEl);
}

// Average macros and meals as two stacked bars with their numbers.
function macroSection(sum) {
  if (!sum.logged) return null;
  const stack = (title, parts) => {
    const total = parts.reduce((a, p) => a + p.kcal, 0) || 1;
    const box = h('div', { class: 'rp-split' });
    box.append(h('h3', {}, title));
    const barEl = h('div', { class: 'rp-stack' });
    const list = h('div', { class: 'rp-split-list' });
    for (const p of parts) {
      const seg = h('i', { class: p.cls });
      seg.style.width = `${(p.kcal / total) * 100}%`;
      barEl.append(seg);
      const row = h('div');
      row.append(h('span', { class: 'rp-key ' + p.cls }), h('span', { class: 'rp-split-name' }, p.label), h('b', {}, p.value), h('span', { class: 'rp-split-pct' }, `${fmt((p.kcal / total) * 100)} %`));
      list.append(row);
    }
    box.append(barEl, list);
    return box;
  };
  const macros = stack(t('Macronutrients'), MACROS.map((m) => ({ cls: m.cls, label: m.label, kcal: (sum[m.key] || 0) * m.kcal, value: `${fmt(sum[m.key])} g` })));
  const meals = stack(t('Meals'), MEALS.map(([k, label], i) => ({ cls: ['m1', 'm2', 'm3', 'm4'][i], label, kcal: sum.meals[k] || 0, value: `${fmt(sum.meals[k])} kcal` })));
  const grid = h('div', { class: 'rp-splits' });
  grid.append(macros, meals);
  return section(t('Nutrition'), t('Average per logged day'), grid);
}

function sleepSection(buckets, weekly, sum) {
  if (!sum.nights) return null;
  const part = (k) => buckets.map((b) => bucketAvg(b, (r) => r.sleep?.[k]) || 0);
  const asleep = part('asleep'), deep = part('deep'), rem = part('rem');
  const hi = Math.max(...asleep, sum.asleep) * 1.1 / 60;
  const svgEl = chart({
    n: buckets.length, dates: buckets.map((b) => b.start), hi, height: 150, yFmt: (v) => `${fmt(v)} h`, aria: t('Hours asleep per night'),
    draw(g, { x, y, bw, right }) {
      asleep.forEach((v, i) => {
        if (!v) return;
        // Deep and REM at the bottom; the rest of the time asleep is light sleep.
        const light = Math.max(0, v - deep[i] - rem[i]);
        bar(g, x, y, i, deep[i] / 60, bw, 'st-deep');
        bar(g, x, y, i, rem[i] / 60, bw, 'st-rem', deep[i] / 60);
        bar(g, x, y, i, light / 60, bw, 'st-light', (deep[i] + rem[i]) / 60);
      });
      const ay = y(sum.asleep / 60);
      g.append(svg('line', { x1: x(0) - bw, x2: right, y1: ay, y2: ay, class: 'goal-line' }));
      g.append(svg('text', { x: right - 2, y: ay - 5, 'text-anchor': 'end', class: 'goal-label' }, t('Avg {d}', { d: fmtDuration(sum.asleep) })));
    },
  });
  return section(t('Sleep'), weekly ? t('Weekly averages') : t('Per night'), legend([['st-deep', t('Deep')], ['st-rem', 'REM'], ['st-light', t('Light')]]), svgEl);
}

function stepsSection(buckets, weekly, sum) {
  if (!sum.steps) return null;
  const steps = buckets.map((b) => bucketAvg(b, (r) => r.steps) || 0);
  const goal = state.data.goals?.steps;
  const hi = Math.max(...steps, goal || 0) * 1.08;
  const svgEl = chart({
    n: buckets.length, dates: buckets.map((b) => b.start), hi, height: 130, yFmt: (v) => (v >= 1000 ? `${fmt(v / 1000)}k` : fmt(v)), aria: t('Steps per day'),
    draw(g, { x, y, bw, right }) {
      steps.forEach((v, i) => bar(g, x, y, i, v, bw, goal && v >= goal ? 's3' : 's3-soft'));
      if (goal) {
        g.append(svg('line', { x1: x(0) - bw, x2: right, y1: y(goal), y2: y(goal), class: 'goal-line' }));
        g.append(svg('text', { x: right - 2, y: y(goal) - 5, 'text-anchor': 'end', class: 'goal-label' }, t('Goal {n}', { n: fmt(goal) })));
      }
    },
  });
  const items = goal ? [['s3', t('Goal reached')], ['s3-soft', t('Below the goal')]] : [['s3', t('Steps')]];
  return section(t('Steps'), weekly ? t('Weekly averages') : t('Per day'), legend(items), svgEl);
}

/* ---------- tables ---------- */

function table(cols, rows, cls = '') {
  const tb = h('table', { class: 'rp-table ' + cls });
  const head = h('tr');
  for (const [label, , num] of cols) head.append(h('th', num ? { class: 'num' } : {}, label));
  const thead = h('thead');
  thead.append(head);
  const body = h('tbody');
  for (const r of rows) {
    const tr = h('tr');
    for (const [, cell, num] of cols) tr.append(h('td', num ? { class: 'num' } : {}, cell(r) ?? '–'));
    body.append(tr);
  }
  tb.append(thead, body);
  return tb;
}

function weeksSection(sum) {
  if (sum.weeks.length < 2) return null;
  const dash = (v, f) => (v ? f(v) : '–');
  const cols = [
    [t('Week'), (w) => (w.first === w.end ? dfShort.format(parse(w.end)) : `${dfShort.format(parse(w.first))} – ${dfShort.format(parse(w.end))}`)],
    [t('Calories'), (w) => dash(w.energy, fmt), true],
    [t('vs goal'), (w) => (w.energy && w.goal ? signed(w.energy - w.goal) : '–'), true],
    [t('Protein'), (w) => dash(w.protein, (v) => `${fmt(v)} g`), true],
    [t('Steps'), (w) => dash(w.steps, fmt), true],
    [t('Weight'), (w) => dash(w.weight, (v) => `${fmt(v, 1)} kg`), true],
    [t('Sleep'), (w) => dash(w.asleep, fmtDuration), true],
    [t('Activities'), (w) => (w.workouts ? fmt(w.workouts) : '–'), true],
  ];
  const sec = section(t('Week by week'), t('Averages per week; weight at the end of the week'), table(cols, sum.weeks));
  sec.classList.add('rp-long');
  return sec;
}

function listsSection(sum) {
  const parts = [];
  if (sum.foods.length) {
    const foods = table([
      [t('Food'), (f) => f.name],
      [t('Times'), (f) => `${fmt(f.n)}×`, true],
      [t('Avg kcal'), (f) => fmt(f.e / f.n), true],
      [t('Share'), (f) => `${fmt(f.share * 100, f.share < 0.1 ? 1 : 0)} %`, true],
    ], sum.foods.slice(0, 12), 'rp-list');
    const box = h('div');
    box.append(h('h3', {}, t('Most eaten foods')), foods);
    parts.push(box);
  }
  if (sum.activities.length) {
    const acts = table([
      [t('Activity'), (a) => actLabel(a.key)],
      [t('Times'), (a) => `${fmt(a.n)}×`, true],
      [t('Time'), (a) => (a.dur ? fmtDuration(a.dur) : '–'), true],
      ['kcal', (a) => (a.e ? fmt(a.e) : '–'), true],
    ], sum.activities.slice(0, 12), 'rp-list');
    const box = h('div');
    box.append(h('h3', {}, t('Activities')), acts);
    parts.push(box);
  }
  if (!parts.length) return null;
  const grid = h('div', { class: 'rp-lists' });
  grid.append(...parts);
  return section(t('Foods and activities'), t('Share: of all calories eaten in the range'), grid);
}

function goalsSection(from, to) {
  const changes = goalChangesIn(state.data.goal_changes, from, to);
  if (!changes.length) return null;
  const names = { energy: t('Calorie goal'), weight: t('Weight goal'), steps: t('Step goal'), water: t('Water goal') };
  const rows = changes.flatMap(([date, ch]) => Object.values(ch).map((c) => ({ date, c })));
  return section(t('Goal changes'), t('As set in YAZIO'), table([
    [t('Date'), (r) => dfDate.format(parse(r.date))],
    [t('Goal'), (r) => names[r.c.goal] || r.c.goal],
    [t('Before'), (r) => (r.c.from ? goalValue(r.c, r.c.from) : '–'), true],
    [t('After'), (r) => goalValue(r.c), true],
  ], rows, 'rp-goals'));
}
