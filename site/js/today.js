// The Today card (the last synced day's ring, macros and meals) and the KPI
// tiles with averages over the selected range.

import { MACROS, MEALS, MEAL_ICONS, MEAL_TABS, addDays, avgOf, compact, dfDay, dfShort, fmt, fmtDistance, fmtDuration, iso, logged, mealThumb, mean, parse, signed } from './format.js';
import { $, h, emoji, meter, countUp, thumbImg } from './dom.js';
import { can, reduceMotion, state } from './state.js';
import { ring, sparkline } from './charts.js';
import { openDay } from './sheet.js';
import { highlights, macroGoal } from './insights.js';
import { t, tn } from './i18n.js';
import { renderTodayScores } from './scores.js';
import { liftBoard, wodBoard } from './health.js';
import { fmtSet, onBestsChange, openLift } from './lifts.js';
import { fmtResult, openWod } from './wods.js';
import { offeredYear, openYear } from './yearreview.js';
import { renderPhaseStatus } from './phases.js';
import { renderCheckin } from './checkin.js';

export function renderToday() {
  const { data } = state;
  const day = data.days.at(-1);
  // Without nutrition and steps there is nothing of the day to show here.
  const food = can('view.nutrition'), steps = can('view.activity');
  $('#today').hidden = !food && !steps;
  $('#today').classList.toggle('no-ring', !food);
  $('#today-ring').hidden = $('#today-meals').hidden = !food;
  const g = data.goals || {};
  const isYesterday = iso(addDays(parse(state.today), -1)) === day.date;
  $('#today-title').textContent = day.date === state.today ? t('Today') : isYesterday ? t('Yesterday') : dfShort.format(parse(day.date));
  const sub = [dfDay.format(parse(day.date))];
  if (day.activity_energy) sub.push(t('{kcal} kcal burned', { kcal: fmt(day.activity_energy) }));
  if (day.weight != null) sub.push(t('weighed {kg} kg', { kg: fmt(day.weight, 1) }));
  $('#today-sub').textContent = sub.join(' · ');

  const goal = day.energy_goal || day.energy_base || g.energy || 0;
  ring($('#today-ring'), day.energy || 0, goal);

  // Macro goals grow with the day's activity, like in the Yazio app.
  const stats = [
    ...(food ? MACROS.map((m) => ({ label: m.label, v: day[m.key], goal: macroGoal(day, m.key, g), unit: 'g' })) : []),
    ...(steps ? [{ label: t('Steps'), steps: true, v: day.steps, goal: g.steps, unit: '' }] : []),
  ];
  $('#today-macros').replaceChildren(...stats.map((st) => {
    const el = h('div', { class: 'stat' });
    const k = h('div', { class: 'k' });
    k.append(h('span', {}, st.label), h('span', {}, st.goal ? t('of {goal}', { goal: fmt(st.goal) + (st.unit ? ' ' + st.unit : '') }) : ''));
    const v = h('div', { class: 'v' }, fmt(st.v || 0));
    if (st.unit) v.append(h('small', {}, st.unit));
    el.append(k, v);
    // Steps are activity, green like everything about movement and training.
    if (st.goal) el.append(meter((st.v || 0) / st.goal, !st.steps && st.v > st.goal * 1.1, st.steps ? 's3' : ''));
    return el;
  }));

  $('#today-meals').replaceChildren(...MEALS.map(([key, label]) => {
    const e = day.meals?.[key] || 0;
    const thumb = mealThumb(day, key, data.recipes);
    const chip = h('button', { class: 'meal-chip' + (e ? '' : ' empty') + (thumb ? ' has-thumb' : ''), type: 'button', 'aria-label': thumb ? t('{meal}: {kcal} kcal, with photo. Show details', { meal: label, kcal: fmt(e) }) : t('{meal}: {kcal} kcal. Show details', { meal: label, kcal: fmt(e) }) });
    if (thumb) chip.append(thumbImg(thumb, 'chip-thumb'));
    const m = h('span', { class: 'm' });
    // The short names ("Abend"): the chip shares its width with a photo.
    m.append(emoji(MEAL_ICONS[key]), document.createTextNode(MEAL_TABS[key]));
    chip.append(m);
    const v = h('span', { class: 'e' }, e ? fmt(e) : t('Not logged'));
    if (e) v.append(h('small', {}, ' kcal'));
    chip.append(v);
    chip.addEventListener('click', () => openDay(day.date, key));
    return chip;
  }));
  renderTodayScores(day);
  renderPhaseStatus();
  renderCheckin();
  $('#today-open').onclick = () => openDay(day.date);
  renderHighlights();
}

// A date with its year only when that isn't this year.
const when = (d) => dfShort.format(parse(d)) + (d.slice(0, 4) !== state.today?.slice(0, 4) ? ` ${d.slice(0, 4)}` : '');

// Lifts and benchmark workouts whose best got better in the last 7 days:
// one chip that opens it, or for several one chip that names them and
// opens the Personal bests tab, so a good week doesn't fill the card.
function bestChips() {
  if (!state.today) return [];
  const lifts = liftBoard(state.lifts, state.today, 7).filter((r) => r.recent && r.prev).map((r) => ({
    date: r.best.date, icon: '🏋️', what: r.lift, short: r.lift,
    value: fmtSet(r.best), open: (b) => openLift(r.key, b),
  }));
  const wods = wodBoard(state.wods.workouts, state.wods.results, state.today, 7).filter((r) => r.recent && r.prev).map((r) => ({
    date: r.best.date, icon: '⏱️', what: r.scaled ? `${r.workout.name} · ${t('Scaled')}` : r.workout.name, short: r.workout.name,
    value: fmtResult(r.workout.scoring, r.best), open: (b) => openWod(r.workout.id, b),
  }));
  const all = [...lifts, ...wods].sort((a, b) => b.date.localeCompare(a.date));
  if (!all.length) return [];
  if (all.length === 1) {
    const [x] = all;
    const b = h('button', { class: 'highlight', type: 'button', role: 'listitem', title: t('Show the history') });
    b.append(emoji(x.icon), h('span', { class: 'hl-text' }, t('New PB: {what}', { what: x.what })), h('b', {}, x.value));
    b.onclick = () => x.open(b);
    return [b];
  }
  const names = [...new Set(all.map((x) => x.short))].join(', ');
  const b = h('button', { class: 'highlight', type: 'button', role: 'listitem', title: t('Show the personal bests') });
  b.append(emoji('🏆'), h('b', {}, tn(all.length, '{n} new PB', '{n} new PBs')), h('span', { class: 'hl-text' }, names), h('b', {}, '›'));
  b.onclick = () => showBests(() => all[0].open(b));
  return [b];
}
onBestsChange(() => {
  if (!state.data) return;
  renderTodayScores(state.data.days.at(-1)); // "Go for a PB" needs a noted result
  renderHighlights();
});

// The Training card's Personal bests tab, scrolled to; when the card is
// hidden or turned off, fallback instead.
function showBests(fallback) {
  const card = $('#training-card');
  const tab = $('#training-tabs [data-tab="bests"]');
  if (!tab || card.hidden || card.hasAttribute('data-off')) return fallback();
  if (tab.getAttribute('aria-selected') !== 'true') tab.click();
  card.scrollIntoView({ behavior: reduceMotion.matches ? 'auto' : 'smooth', block: 'start' });
  tab.focus({ preventScroll: true });
}

// At most this many chips; the rest behind a "+n" chip.
const SHOWN = 3;
let expanded = false;

// The Today card's chips: the year in review (December and January), new
// personal bests, then the records of the last 7 days, which open their
// day.
export function renderHighlights() {
  const { data, byDate } = state;
  const items = highlights(data.days, byDate, data.goals || {}, state.today);
  const chips = bestChips();
  const year = offeredYear(state.today);
  if (year && data.days.some((d) => d.date.startsWith(year))) {
    const b = h('button', { class: 'highlight yr-chip', type: 'button', role: 'listitem' });
    b.append(emoji('🎉'), h('span', { class: 'hl-text' }, t('Your {year} in review', { year })), h('b', {}, '›'));
    b.onclick = () => openYear(year, b);
    chips.unshift(b);
  }
  chips.push(...items.map((x) => {
    const [icon, text, value] = x.kind === 'best'
      ? ['🏅', t('New record: {what}', { what: x.label }),
        x.key === 'dur' ? fmtDuration(x.value / 60) : x.key === 'energy' ? `${fmt(x.value)} kcal` : x.key === 'azm' ? `${fmt(x.value)} min` : x.key === 'elev' ? `${fmt(x.value)} m` : fmtDistance(x.value)]
      : x.kind === 'steps' ? ['👟', x.since ? t('Most steps since {date}', { date: when(x.since) }) : t('Most steps in a day so far'), fmt(x.value)]
        : x.kind === 'protein' ? ['💪', t('Protein goal reached {n} days in a row', { n: fmt(x.value) }), '']
          : ['📉', x.since ? (x.kind === 'weight-low' ? t('Lowest weight since {date}', { date: when(x.since) }) : t('Highest weight since {date}', { date: when(x.since) }))
            : x.kind === 'weight-low' ? t('Lowest weight so far') : t('Highest weight so far'), `${fmt(x.value, 1)} kg`];
    const chip = h('button', { class: 'highlight', type: 'button', role: 'listitem', title: t('Open {date}', { date: dfShort.format(parse(x.date)) }) });
    chip.append(emoji(x.kind === 'weight-high' ? '📈' : icon), h('span', { class: 'hl-text' }, text));
    if (value) chip.append(h('b', {}, value));
    chip.onclick = () => openDay(x.date);
    return chip;
  }));
  const host = $('#today-highlights');
  host.hidden = !chips.length;
  // One more chip than the limit isn't worth a "+1".
  if (expanded || chips.length <= SHOWN + 1) return host.replaceChildren(...chips);
  const more = h('button', { class: 'highlight hl-more', type: 'button', role: 'listitem', 'aria-label': tn(chips.length - SHOWN, 'Show {n} more highlight', 'Show {n} more highlights') }, `+${chips.length - SHOWN}`);
  more.onclick = () => {
    expanded = true;
    more.replaceWith(...chips.slice(SHOWN));
    chips[SHOWN].focus();
  };
  host.replaceChildren(...chips.slice(0, SHOWN), more);
}

export function renderTiles(s) {
  const { rows, complete, prevRows } = s;
  const { data } = state;
  const g = data.goals || {};
  const loggedRows = complete.filter(logged);
  const prevLogged = prevRows.filter(logged);
  const vsPrev = (cur, prev) => (cur && prev ? (cur - prev) / prev : null);

  const avgE = avgOf(loggedRows, 'energy');
  const avgGoal = avgOf(loggedRows, 'energy_goal') || g.energy;
  const avgP = avgOf(loggedRows, 'protein');
  const avgPGoal = mean(loggedRows.map((r) => macroGoal(r, 'protein', g)).filter(Boolean));
  const avgSteps = avgOf(complete, 'steps');

  const allW = data.days.filter((d) => d.weight != null);
  const rangeW = rows.filter((r) => r.weight != null);
  const latest = allW.at(-1);
  let wCtx = latest ? t('Last weigh-in {date}', { date: dfShort.format(parse(latest.date)) }) : t('No weigh-ins yet');
  let wGood = false;
  if (rangeW.length >= 2) {
    const diff = rangeW.at(-1).weight - rangeW[0].weight;
    wCtx = `${diff < 0 ? '▼' : diff > 0 ? '▲' : ''} ` + t('{kg} kg since {date}', { kg: signed(diff, 1), date: dfShort.format(parse(rangeW[0].date)) });
    wGood = !!g.weight && Math.abs(rangeW.at(-1).weight - g.weight) < Math.abs(rangeW[0].weight - g.weight);
  }

  const daily = (key, pred = (r) => r[key] > 0) => complete.map((r) => (pred(r) ? r[key] : null));
  const food = can('view.nutrition');
  const tiles = [
    food && {
      label: t('Weight'), value: latest?.weight, digits: 1, unit: 'kg', ctx: wCtx, good: wGood,
      chip: g.weight && latest ? t('{kg} kg to goal', { kg: fmt(Math.abs(latest.weight - g.weight), 1) }) : '',
      spark: rows.map((r) => r.weight ?? null),
    },
    food && {
      label: t('Avg calories'), value: avgE, unit: 'kcal', ctx: avgE && avgGoal ? t('{kcal} kcal vs goal', { kcal: signed(avgE - avgGoal) }) : '',
      delta: vsPrev(avgE, avgOf(prevLogged, 'energy')), spark: daily('energy'),
    },
    food && {
      label: t('Avg protein'), value: avgP, unit: 'g', ctx: avgP && avgPGoal ? t('{pct}% of {goal} g goal', { pct: fmt((avgP / avgPGoal) * 100), goal: fmt(avgPGoal) }) : '',
      delta: vsPrev(avgP, avgOf(prevLogged, 'protein')), spark: daily('protein'),
    },
    can('view.activity') && {
      label: t('Avg steps'), value: avgSteps, unit: '', format: (v) => (v >= 10000 ? compact.format(v) : fmt(v)),
      ctx: avgSteps && g.steps ? t('{pct}% of {goal} goal', { pct: fmt((avgSteps / g.steps) * 100), goal: compact.format(g.steps) }) : '',
      delta: vsPrev(avgSteps, avgOf(prevRows, 'steps')), spark: daily('steps'),
    },
  ].filter(Boolean);
  $('#tiles').hidden = !tiles.length;
  $('#tiles').style.setProperty('--tiles', tiles.length);

  const prevLabel = state.range === 'all' ? '' : tn(rows.length, 'vs previous {n} day', 'vs previous {n} days');
  $('#tiles').replaceChildren(...tiles.map((tile) => {
    const card = h('article', { class: 'card tile' });
    card.append(h('h2', { class: 'label' }, tile.label));
    const v = h('div', { class: 'value' });
    const num = h('span');
    v.append(num);
    if (tile.unit) v.append(h('small', {}, tile.unit));
    if (tile.delta != null && Math.abs(tile.delta) >= 0.005) {
      v.append(h('span', { class: 'chip', title: prevLabel }, `${tile.delta > 0 ? '▲' : '▼'} ${fmt(Math.abs(tile.delta) * 100)}%`));
    } else if (tile.chip) {
      const chip = h('span', { class: 'chip' });
      chip.append(emoji('🎯'), tile.chip);
      v.append(chip);
    }
    card.append(v);
    countUp(num, 'tile:' + tile.label, tile.value, tile.format || ((x) => fmt(x, tile.digits || 0)));
    if (tile.ctx) card.append(h('p', { class: 'ctx' + (tile.good ? ' good' : '') }, tile.ctx));
    const sp = h('div', { class: 'spark' });
    card.append(sp);
    sparkline(sp, tile.spark);
    return card;
  }));
}
