// The year in review: a calendar year in numbers (year.js) in a window,
// with a picture to share. It opens from the Today card in December and
// January, from the Training card's calendar, and from "#year=2026".

import { compact, dfLong, dfMonth, fmt, fmtDistance, fmtDuration, parse } from './format.js';
import { $, emoji, h, setHashParam } from './dom.js';
import { openDialog } from './dialog.js';
import { state } from './state.js';
import { fmtSleep, workoutName } from './health.js';
import { fmtKg } from './lifts.js';
import { fmtResult } from './wods.js';
import { list, shareTools, tiles } from './share.js';
import { yearSummary, yearsOf } from './year.js';
import { t, tn } from './i18n.js';

// The year the Today card offers: this one in December, the last in January.
export function offeredYear(today) {
  if (!today) return null;
  const m = today.slice(5, 7), y = Number(today.slice(0, 4));
  return m === '12' ? String(y) : m === '01' ? String(y - 1) : null;
}

const summary = (year) => yearSummary(state.data.days, year, { lifts: state.lifts || [], wods: state.wods || { workouts: [], results: [] } });

const signedKg = (kg) => (kg > 0 ? '+' : kg < 0 ? '−' : '±') + fmt(Math.abs(kg), 1) + ' kg';

// The key numbers, for the window and the picture alike: [label, value, unit].
function keyNumbers(y) {
  return [
    y.training && [t('Days trained'), fmt(y.training.days), ''],
    y.training?.hours >= 1 && [t('Hours of workouts'), fmt(y.training.hours), 'h'],
    y.training?.km >= 1 && [t('Distance'), fmt(y.training.km), 'km'],
    y.bests && [t('New bests'), fmt(y.bests.n), ''],
    y.steps && [t('Steps a day'), fmt(y.steps.avg), ''],
    y.sleep && [t('Asleep a night'), fmtSleep(y.sleep.asleep), ''],
    y.nutrition && [t('Calories a day'), fmt(y.nutrition.kcal), 'kcal'],
    y.weight && y.weight.n > 1 && [t('Weight'), signedKg(y.weight.last - y.weight.first), ''],
  ].filter(Boolean);
}

// One row per lift or workout: where it ended, from where, how often better.
const bestRows = (y) => [
  ...y.bests.lifts.map((b) => ({
    date: b.date, icon: '🏋️', name: b.reps > 1 ? `${b.name} · ${b.reps} RM` : b.name, value: fmtKg(b.kg),
    note: [b.from != null ? t('from {kg}', { kg: fmtKg(b.from) }) : '', tn(b.n, '{n} new best', '{n} new bests')].filter(Boolean).join(' · '),
  })),
  ...y.bests.wods.map((b) => ({
    date: b.date, icon: '⏱️', name: b.result.scaled ? `${b.name} · ${t('Scaled')}` : b.name, value: b.workout ? fmtResult(b.workout.scoring, b.result) : '',
    note: tn(b.n, '{n} new best', '{n} new bests'),
  })),
];

let win = null;

export function openYear(year, returnFocus) {
  const years = yearsOf(state.data?.days || []);
  if (!years.includes(year)) return;
  win?.close(true);
  setHashParam('year', year);
  const close = h('button', { class: 'sheet-close', type: 'button', 'aria-label': t('Close') }, '×');
  const prev = h('button', { class: 'sheet-nav', type: 'button', 'aria-label': t('Previous year') }, '‹');
  const next = h('button', { class: 'sheet-nav', type: 'button', 'aria-label': t('Next year') }, '›');
  const head = h('div', { class: 'sheet-head wo-head' });
  const titles = h('div', { class: 'wo-titles' });
  const title = h('h2', { id: 'yr-title' });
  const sub = h('p', { class: 'muted' });
  titles.append(title, sub);
  head.append(titles, prev, next, ...shareTools(() => [{ key: 'year', label: '', name: `year-${year}`, build: () => yearCard(summary(year)) }]), close);
  const body = h('div', { class: 'wo-body' });
  const panel = h('div', { class: 'wo-panel yr-panel' });
  panel.append(head, body);
  win = openDialog({ cls: 'wo-overlay', labelledBy: 'yr-title', content: [panel], returnFocus, onClose: () => setHashParam('year', null) });
  close.onclick = () => win.close();
  const i = years.indexOf(year);
  prev.disabled = i === years.length - 1;
  next.disabled = i === 0;
  prev.onclick = () => openYear(years[i + 1], returnFocus);
  next.onclick = () => openYear(years[i - 1], returnFocus);
  close.focus();

  const y = summary(year);
  title.replaceChildren(emoji('🎉', 'lift-emoji'), t('Your {year}', { year }));
  const partial = y.first && y.last && (y.first.slice(5) !== '01-01' || y.last.slice(5) !== '12-31');
  sub.textContent = [
    partial ? `${dfLong.format(parse(y.first))} – ${dfLong.format(parse(y.last))}` : '',
    tn(y.n, '{n} day with data', '{n} days with data'),
  ].filter(Boolean).join(' · ');
  body.replaceChildren(...[numbersSection(y), monthsSection(y), bestsSection(y), moreSection(y), foodsSection(y)].filter(Boolean));
}

function section(name, note) {
  const sec = h('section', { class: 'wo-sec' });
  const hd = h('div', { class: 'wo-sec-head' });
  hd.append(h('h3', {}, name));
  if (note) hd.append(h('span', { class: 'muted' }, note));
  sec.append(hd);
  return sec;
}

function statGrid(items) {
  const grid = h('div', { class: 'wo-stats' });
  for (const [label, value, unit] of items) {
    const b = h('b', {}, value);
    if (unit) b.append(h('small', {}, unit));
    const cell = h('div');
    cell.append(h('span', {}, label), b);
    grid.append(cell);
  }
  return grid;
}

function numbersSection(y) {
  const items = keyNumbers(y);
  if (!items.length) return null;
  const sec = section(t('In numbers'));
  sec.append(statGrid(items));
  return sec;
}

// Days trained per month, as the weekday split under the calories chart.
function monthsSection(y) {
  if (!y.training) return null;
  const sec = section(t('Days trained per month'));
  const max = Math.max(1, ...y.months.map((m) => m.trained));
  const row = h('div', { class: 'weekday-split yr-months', role: 'list' });
  for (const m of y.months) {
    const name = dfMonth.format(new Date(Date.UTC(Number(y.year), m.month, 1)));
    const el = h('div', { class: 'wd', role: 'listitem', title: m.days ? `${name}: ${tn(m.trained, '{n} day trained', '{n} days trained')}` : `${name}: ${t('No data')}` });
    const bar = h('div', { class: 'wd-bar' });
    const fill = h('div', { class: 'wd-fill' + (m.trained === max && m.trained ? ' above' : '') });
    fill.style.height = `${(m.trained / max) * 100}%`;
    bar.append(fill);
    el.append(bar, h('b', {}, m.days ? fmt(m.trained) : '–'), h('span', {}, name));
    row.append(el);
  }
  sec.append(row);
  return sec;
}

function bestsSection(y) {
  if (!y.bests) return null;
  const rows = bestRows(y);
  const sec = section(t('New bests'), tn(y.bests.n, '{n} this year', '{n} this year'));
  const ul = h('div', { class: 'yr-list' });
  for (const r of rows) {
    const row = h('div', { class: 'yr-row' });
    row.append(emoji(r.icon), h('span', { class: 'yr-name' }, r.name), h('span', { class: 'muted' }, r.note), h('b', {}, r.value));
    ul.append(row);
  }
  sec.append(ul);
  return sec;
}

// The rest: the body, movement and sleep in a sentence each.
function moreSection(y) {
  const lines = [];
  if (y.weight) {
    lines.push(['⚖️', y.weight.n > 1
      ? t('From {first} on {d0} to {last} on {d1}, {min} at the lowest.', { first: `${fmt(y.weight.first, 1)} kg`, d0: dfLong.format(parse(y.weight.firstDate)), last: `${fmt(y.weight.last, 1)} kg`, d1: dfLong.format(parse(y.weight.lastDate)), min: `${fmt(y.weight.min, 1)} kg` })
      : t('One weigh-in: {kg}.', { kg: `${fmt(y.weight.first, 1)} kg` })]);
  }
  if (y.steps) {
    lines.push(['👟', t('{total} steps in all; the most on {date}: {top}.', { total: compact.format(y.steps.total), date: dfLong.format(parse(y.steps.top.date)), top: fmt(y.steps.top.steps) })]);
  }
  if (y.training?.longest?.dur) {
    const l = y.training.longest;
    lines.push(['⏱️', t('Longest workout: {what} on {date}, {dur}.', { what: workoutName(l.type), date: dfLong.format(parse(l.date)), dur: fmtDuration(l.dur / 60) + (l.dist ? ', ' + fmtDistance(l.dist) : '') })]);
  }
  if (y.sleep) {
    const parts = [t('{time} asleep a night on average', { time: fmtSleep(y.sleep.asleep) })];
    if (y.sleep.readiness != null) parts.push(t('Readiness {n} on average', { n: fmt(y.sleep.readiness) }));
    if (y.sleep.bestMonth) parts.push(t('the best sleep in {month}', { month: dfMonth.format(new Date(Date.UTC(Number(y.year), y.sleep.bestMonth.month, 1))) }));
    lines.push(['😴', parts.join(', ') + '.']);
  }
  if (y.nutrition) {
    lines.push(['🥗', t('{n} days logged, {kcal} kcal and {protein} g protein a day on average.', { n: fmt(y.nutrition.logged), kcal: fmt(y.nutrition.kcal), protein: fmt(y.nutrition.protein) })]);
  }
  if (!lines.length) return null;
  const sec = section(t('The year'));
  for (const [icon, text] of lines) {
    const p = h('p', { class: 'yr-line' });
    p.append(emoji(icon), h('span', {}, text));
    sec.append(p);
  }
  return sec;
}

function foodsSection(y) {
  if (!y.foods.length) return null;
  const sec = section(t('Eaten most often'));
  const ul = h('div', { class: 'yr-list' });
  y.foods.forEach(([name, n], i) => {
    const row = h('div', { class: 'yr-row' });
    row.append(h('span', { class: 'yr-rank' }, `${i + 1}.`), h('span', { class: 'yr-name' }, name), h('b', {}, `${fmt(n)}×`));
    ul.append(row);
  });
  sec.append(ul);
  return sec;
}

// The share picture: the key numbers, the bests and the foods.
function yearCard(y) {
  const bests = y.bests ? bestRows(y) : [];
  return {
    icon: '🎉', title: t('Your {year}', { year: y.year }), sub: tn(y.n, '{n} day with data', '{n} days with data'), accent: 's1',
    blocks: [
      tiles(keyNumbers(y)),
      bests.length && list(bests.map((b) => ({ icon: b.icon, name: b.name, note: b.note, value: b.value })), t('New bests'), { rowH: 28, minRows: 2 }),
      y.foods.length && list(y.foods.map(([name, n]) => ({ name, value: `${fmt(n)}×` })), t('Eaten most often'), { rowH: 28, minRows: 2 }),
    ].filter(Boolean),
  };
}

// "#year=2026" from a shared link.
export function openYearFromHash(year) {
  if (year && /^\d{4}$/.test(year)) openYear(year, $('#today'));
}
