// The Insights card: Goals (how often each goal was met in the range) and
// Explore (how two daily measures move together, as a scatter plot with
// Spearman's r), and Streaks (goals met days in a row, and when each was
// last missed, over the whole history). The numbers come from
// correlate.js; the range is the dashboard's. A pattern, not proof: the card says so.

import { LOCALE, actLabel, dfShort, fmt, fmtDuration, parse } from './format.js';
import { $, h, svg, showTip, hideTip, panelTabs } from './dom.js';
import { state, store } from './state.js';
import { niceTicks, resizer } from './charts.js';
import { openDay } from './sheet.js';
import { dayTags } from './checkin.js';
import { clockOf } from './sleeptiming.js';
import { LAGS, MEASURES, RECENT_DAYS, correlate, goalHits, goalStreaks, lastTimes, measure, pairsOf } from './correlate.js';
import { promptTypes } from './unlogged.js';
import { t, tn } from './i18n.js';

// The measures' names and groups as the page says them.
const LABELS = {
  energy: () => t('Calories'), protein: () => t('Protein'), carb: () => t('Carbs'), fat: () => t('Fat'), alcohol: () => t('Alcohol'),
  fiber: () => t('Fibre'), sodium: () => t('Sodium'), magnesium: () => t('Magnesium'),
  steps: () => t('Steps'), burned: () => t('Active calories'), azm: () => t('Active zone minutes'), load: () => t('Training load'),
  asleep: () => t('Time asleep'), deep: () => t('Deep sleep'), rem: () => t('REM sleep'),
  bedtime: () => t('Bedtime'), waketime: () => t('Wake time'), midsleep: () => t('Mid-sleep'),
  rhr: () => t('Resting heart rate'), hrv: () => 'HRV', sleep_hr: () => t('Sleeping heart rate'),
  readiness: () => t('Readiness'), sleep_score: () => t('Sleep score'), recovery: () => t('Recovery'),
  ci_energy: () => t('Energy (check-in)'), ci_soreness: () => t('Soreness (check-in)'), ci_stress: () => t('Stress (check-in)'), ci_mood: () => t('Mood (check-in)'),
  weight: () => t('Weight (7-day average)'),
};
const GROUPS = {
  nutrition: () => t('Nutrition'), movement: () => t('Movement & training'), sleep: () => t('Sleep'), recovery: () => t('Recovery'),
  scores: () => t('Scores'), checkin: () => t('Check-in'), body: () => t('Body'),
};
const label = (key) => LABELS[key]?.() || key;
const LAG_LABELS = { 0: () => t('the same day'), 1: () => t('the day after'), 2: () => t('two days after') };

// A measure's value as text: clock times as "HH:MM", minutes of sleep as
// hours and minutes.
function fmtValue(m, v) {
  if (m.unit === 'clock') return clockOf(v);
  if (m.unit === 'clock-wake') return clockOf(v + 360); // wake time counts from 18:00
  if (m.unit === 'min' && m.group === 'sleep') return fmtDuration(v);
  const n = fmt(v, m.digits || 0);
  return m.unit && m.unit !== '1–5' ? `${n} ${m.unit}` : n;
}

export function renderInsightsCard(s) {
  const card = $('#insights-card');
  card.hidden = !state.data.days.length;
  if (card.hidden) return;
  renderGoals(s);
  renderStreaks();
  renderExplore(s);
  panelTabs($('#insights-tabs'), [['goals', t('Goals'), '🎯'], ['streaks', t('Streaks'), '🔥'], ['explore', t('Explore'), '🔍']], store.get('insightsTab'), (key) => store.set('insightsTab', key));
}

// openExplore shows the Explore tab (from the Patterns tab's link).
export function openExplore() {
  const card = $('#insights-card');
  const tab = $('#insights-tabs [data-tab="explore"]');
  if (!tab || card.hidden || card.hasAttribute('data-off')) return;
  if (tab.getAttribute('aria-selected') !== 'true') tab.click();
  card.scrollIntoView({ block: 'start', behavior: 'smooth' });
  tab.focus({ preventScroll: true });
}

/* ---------- goals ---------- */

const GOAL_LABELS = {
  protein: () => t('Protein goal'), energy: () => t('Calories on goal'), steps: () => t('Steps goal'),
  sleep: () => t('Sleep goal'), alcohol_free: () => t('Days without alcohol'),
};

function renderGoals(s) {
  const host = $('#insights-goals');
  const alcoholKnown = state.data.days.some((d) => d.nut?.alcohol);
  const hits = goalHits(s.complete, { goals: state.data.goals, sleepGoal: state.data.sleep_goal, phases: state.phases, today: state.today, alcoholKnown });
  if (!hits.length) return host.replaceChildren(h('p', { class: 'empty-tab' }, t('No goals to count in this range.')));
  const list = h('div', { class: 'hbars goal-hits' });
  for (const x of hits) {
    const row = h('div', { class: 'hbar' });
    const pct = x.hit / x.of;
    const val = h('span', { class: 'val' }, t('{hit} of {of} days', { hit: fmt(x.hit), of: fmt(x.of) }));
    val.append(h('small', {}, `${fmt(pct * 100)} %`));
    row.append(h('span', { class: 'name' }, GOAL_LABELS[x.key]()), val);
    const track = h('span', { class: 'track' });
    const bar = h('i');
    bar.style.width = `${pct * 100}%`;
    track.append(bar);
    row.append(track);
    list.append(row);
  }
  const energy = hits.find((x) => x.key === 'energy');
  const notes = [t('Days without data don’t count; today is still running and left out.')];
  if (energy) {
    notes.unshift(energy.kinds.some((k) => k !== 'maintain')
      ? t('Calories follow your phase: in a cut at or below the goal, in a bulk at or above it, otherwise within ±10 %. The goal includes the day’s activity.')
      : t('Calories count within ±10 % of the goal, which includes the day’s activity; in a cut or bulk (Phases & Events) below or above it.'));
  }
  host.replaceChildren(list, h('p', { class: 'note muted' }, notes.join(' ')));
}

/* ---------- streaks ---------- */

const STREAK_LABELS = { logged: () => t('Food logged'), ...GOAL_LABELS };
const STREAK_ICONS = { logged: '📝', protein: '💪', energy: '🔥', steps: '👟', sleep: '😴', alcohol_free: '🚱' };

// A date as "Nov 14", with the year when it isn't this one.
const when = (d) => dfShort.format(parse(d)) + (d.slice(0, 4) !== state.today?.slice(0, 4) ? ` ${d.slice(0, 4)}` : '');
function ago(d) {
  const n = state.today ? Math.round((parse(state.today) - parse(d)) / 864e5) : null;
  return n == null ? '' : n === 0 ? t('today') : n === 1 ? t('yesterday') : tn(n, '{n} day ago', '{n} days ago');
}
// A day's date that opens the day.
function dayLink(d) {
  const b = h('button', { class: 'link-btn', type: 'button', title: t('Open {date}', { date: dfShort.format(parse(d)) }) }, when(d));
  b.onclick = () => openDay(d);
  return b;
}

// renderStreaks: per goal (and food logged) the streak running now, the
// longest so far and the last miss, then the last training and weigh-in;
// over everything synced, not the range.
function renderStreaks() {
  const host = $('#insights-streaks');
  const alcoholKnown = state.data.days.some((d) => d.nut?.alcohol);
  const rows = goalStreaks(state.data.days, { goals: state.data.goals, sleepGoal: state.data.sleep_goal, phases: state.phases, today: state.today, alcoholKnown });
  if (!rows.length) return host.replaceChildren(h('p', { class: 'empty-tab' }, t('No goals to count in this range.')));
  const list = h('div', { class: 'streaks' });
  for (const x of rows) {
    const row = h('div', { class: 'streak' + (x.n ? '' : ' off') });
    const main = h('div', { class: 'st-main' });
    main.append(h('span', { class: 'st-name' }, STREAK_LABELS[x.key]()));
    const sub = h('span', { class: 'st-sub muted' });
    const part = (...nodes) => { if (sub.childNodes.length) sub.append(' · '); sub.append(...nodes); };
    if (x.best && x.best.n > x.n) {
      const range = h('span', { class: 'st-range' });
      range.append('(', dayLink(x.best.from), ' – ', dayLink(x.best.to), ')');
      part(t('Longest: {days}', { days: tn(x.best.n, '{n} day', '{n} days') }), ' ', range);
    } else if (x.n > 1) part(t('Your longest yet'));
    if (x.miss) {
      if (x.key === 'alcohol_free') part(t('Last alcohol:'), ' ', dayLink(x.miss), `, ${ago(x.miss)} (${fmt(x.grams)}\u00a0g)`);
      else if (x.key === 'logged') part(t('Last day not logged:'), ' ', dayLink(x.miss), `, ${ago(x.miss)}`);
      else part(t('Last missed:'), ' ', dayLink(x.miss), `, ${ago(x.miss)}`);
    }
    main.append(sub);
    const now = h('b', { class: 'st-now', title: x.from ? t('Since {date}', { date: when(x.from) }) : '' }, tn(x.n, '{n} day', '{n} days'));
    row.append(h('span', { class: 'st-icon', 'aria-hidden': 'true' }, STREAK_ICONS[x.key]), main, now);
    list.append(row);
  }
  const note = t('Days in a row up to yesterday, over everything synced, not just the range. A day without data ends a streak; alcohol logged today counts at once, and so do today’s training and weigh-in.');
  host.replaceChildren(list, ...lastTimeRows(), h('p', { class: 'note muted' }, note));
}

// The last training and weigh-in, today included: when, and what.
function lastTimeRows() {
  const noted = [...state.lifts.map((e) => e.date), ...state.wods.results.map((r) => r.date), ...state.trainings.map((x) => x.date)];
  const items = lastTimes(state.data.days, { noted, types: promptTypes(), today: state.today });
  if (!items.length) return [];
  const list = h('div', { class: 'streaks st-last' });
  for (const x of items) {
    const row = h('div', { class: 'streak' });
    const main = h('div', { class: 'st-main' });
    const sub = h('span', { class: 'st-sub muted' });
    sub.append(dayLink(x.date));
    if (x.key === 'trained') {
      if (x.what.length) sub.append(` · ${x.what.map(actLabel).join(', ')}`);
      sub.append(' · ' + tn(x.recent, '{n} day trained in the last {weeks} weeks', '{n} days trained in the last {weeks} weeks', { weeks: fmt(RECENT_DAYS / 7) }));
    } else {
      sub.append(` · ${fmt(x.value, 1)}\u00a0kg`);
    }
    main.append(h('span', { class: 'st-name' }, x.key === 'trained' ? t('Last training') : t('Last weigh-in')), sub);
    const a = ago(x.date);
    row.append(h('span', { class: 'st-icon', 'aria-hidden': 'true' }, x.key === 'trained' ? '🏋️' : '⚖️'), main, h('b', { class: 'st-now' }, a.charAt(0).toUpperCase() + a.slice(1)));
    list.append(row);
  }
  return [h('h3', { class: 'mini-title' }, t('Last time')), list];
}

// A training added, changed or removed moves the last training.
document.addEventListener('trainings-change', () => {
  if (state.data?.days.length && !$('#insights-card').hidden) renderStreaks();
});

/* ---------- explore ---------- */

const PICK = 'explore';
function picked() {
  let p = null;
  try { p = JSON.parse(store.get(PICK) || 'null'); } catch { /* the default */ }
  const alcohol = state.data.days.some((d) => d.nut?.alcohol);
  const def = { x: alcohol ? 'alcohol' : 'steps', y: 'hrv', lag: 1, skip: true };
  p = { ...def, ...(p || {}) };
  if (!measure(p.x)) p.x = def.x;
  if (!measure(p.y)) p.y = def.y;
  if (!LAGS.includes(p.lag)) p.lag = def.lag;
  return p;
}

function renderExplore(s) {
  const host = $('#insights-explore');
  const p = picked();
  const rows = s.complete;
  const ctx = { byDate: state.byDate, checkins: state.checkins };
  // Only the measures this range has: a list without dead ends.
  const known = MEASURES.filter((m) => rows.some((d) => m.get(d, ctx) != null));
  const select = (value, aria, onPick) => {
    const sel = h('select', { class: 'field', 'aria-label': aria });
    for (const [g, name] of Object.entries(GROUPS)) {
      const ms = known.filter((m) => m.group === g);
      if (!ms.length) continue;
      const og = h('optgroup', { label: name() });
      og.append(...ms.map((m) => h('option', { value: m.key }, label(m.key))));
      sel.append(og);
    }
    sel.value = value;
    sel.onchange = () => onPick(sel.value);
    return sel;
  };
  const save = (change) => {
    store.set(PICK, JSON.stringify({ ...p, ...change }));
    renderExplore(s);
  };
  const xSel = select(p.x, t('First measure'), (x) => save({ x }));
  const ySel = select(p.y, t('Second measure'), (y) => save({ y }));
  const lagSel = h('select', { class: 'field', 'aria-label': t('When') });
  for (const lag of LAGS) lagSel.append(h('option', { value: String(lag) }, LAG_LABELS[lag]()));
  lagSel.value = String(p.lag);
  lagSel.onchange = () => save({ lag: Number(lagSel.value) });
  const skipBox = h('input', { type: 'checkbox' });
  skipBox.checked = p.skip;
  skipBox.onchange = () => save({ skip: skipBox.checked });
  const skipLabel = h('label', { class: 'ex-skip' });
  skipLabel.append(skipBox, ' ', t('Leave out sick and travel days'));
  const swap = h('button', { class: 'icon-btn ex-swap', type: 'button', 'aria-label': t('Swap the measures'), title: t('Swap the measures') }, '⇄');
  swap.onclick = () => save({ x: p.y, y: p.x });

  const controls = h('div', { class: 'ex-controls' });
  const sentence = h('div', { class: 'ex-sentence' });
  sentence.append(xSel, swap, h('span', { class: 'muted' }, t('against')), ySel, lagSel);
  controls.append(sentence, skipLabel);

  const skip = p.skip ? (date) => dayTags(date).some((x) => x === 'sick' || x === 'travel') : () => false;
  const points = pairsOf(rows, p.x, p.y, p.lag, ctx, skip);
  const res = correlate(points);
  const X = measure(p.x), Y = measure(p.y);
  const chart = h('div', { class: 'chart ex-chart' });
  const summary = h('p', { class: 'ex-summary' });
  if (p.x === p.y && p.lag === 0) {
    summary.textContent = t('Pick two different measures, or the same one a day later.');
  } else if (res.r == null) {
    summary.textContent = res.n < 10
      ? tn(res.n, 'Only {n} day has both: at least 10 are needed.', 'Only {n} days have both: at least 10 are needed.')
      : t('One of the two doesn’t vary in this range.');
  } else {
    const dir = direction(res, X, Y, p.lag);
    summary.append(h('b', { class: 'ex-r' }, `r = ${fmtR(res.r)}`), ' · ', STRENGTH[res.strength](), dir ? ' · ' + dir : '', ' · ',
      h('span', { class: 'muted' }, tn(res.n, '{n} day, {from} – {to}.', '{n} days, {from} – {to}.', { from: dfShort.format(parse(points[0].date)), to: dfShort.format(parse(points.at(-1).date)) })));
  }
  const note = h('p', { class: 'note muted' }, t('A pattern, not proof: two things can move together because of a third, or by chance. Spearman’s r runs from −1 to 1 and only asks whether more of one goes with more (or less) of the other. Sleep belongs to the morning it ended, so “the day after” a day is the night that followed it.'));
  host.replaceChildren(controls, summary, chart, note);
  if (points.length && !(p.x === p.y && p.lag === 0)) drawScatter(chart, points, X, Y, res, p.lag);
}

// r with two decimals and a real minus; nothing negative about a zero.
const fmtR = (r) => {
  const v = Math.abs(r) < 0.005 ? 0 : r;
  return (v < 0 ? '−' : '') + Math.abs(v).toLocaleString(LOCALE, { minimumFractionDigits: 2, maximumFractionDigits: 2 });
};

const STRENGTH = {
  none: () => t('no relation to speak of'), weak: () => t('a weak relation'), moderate: () => t('a moderate relation'), strong: () => t('a strong relation'),
};

function direction(res, X, Y, lag) {
  if (res.strength === 'none') return '';
  const more = res.r > 0;
  return t('More {x} goes with {more} {y} {when}.', { x: label(X.key), more: more ? t('more|amount') : t('less|amount'), y: label(Y.key), when: LAG_LABELS[lag]() });
}

// drawScatter: one dot per day (8 px with a ring in the surface colour),
// the least-squares line, recessive axes, and the nearest day's values on
// hover; a click opens that day.
function drawScatter(host, points, X, Y, res, lag) {
  const draw = () => {
    const W = host.clientWidth, H = host.clientHeight || 240;
    if (!W) return;
    const m = { t: 10, r: 12, b: 34, l: 52 };
    const pw = W - m.l - m.r, ph = H - m.t - m.b;
    const span = (vals) => {
      let lo = Math.min(...vals), hi = Math.max(...vals);
      if (lo === hi) { lo -= 1; hi += 1; }
      const pad = (hi - lo) * 0.06;
      return [lo - pad, hi + pad];
    };
    const [x0, x1] = span(points.map((p) => p.x)), [y0, y1] = span(points.map((p) => p.y));
    const x = (v) => m.l + ((v - x0) / (x1 - x0)) * pw;
    const y = (v) => m.t + (1 - (v - y0) / (y1 - y0)) * ph;
    const root = svg('svg', { viewBox: `0 0 ${W} ${H}`, role: 'img',
      'aria-label': t('{x} against {y} {when}, one dot per day', { x: label(X.key), y: label(Y.key), when: LAG_LABELS[lag]() }) });
    const axis = svg('g', { class: 'axis' });
    const tickText = (mm, v) => (mm.unit.startsWith('clock') ? fmtValue(mm, v) : mm.unit === 'min' && mm.group === 'sleep' ? `${fmt(v / 60, 1)} h` : fmt(v, mm.digits && Math.abs(x1 - x0) < 5 ? 1 : 0));
    for (const v of niceTicks(y0, y1, 4).filter((v) => v >= y0 && v <= y1)) {
      axis.append(svg('line', { x1: m.l, x2: W - m.r, y1: y(v), y2: y(v), class: 'gridline' }));
      axis.append(svg('text', { x: m.l - 6, y: y(v) + 4, 'text-anchor': 'end' }, tickText(Y, v)));
    }
    for (const v of niceTicks(x0, x1, Math.max(2, Math.floor(pw / 90))).filter((v) => v >= x0 && v <= x1)) {
      axis.append(svg('text', { x: x(v), y: H - 16, 'text-anchor': 'middle' }, tickText(X, v)));
    }
    axis.append(svg('text', { x: m.l + pw, y: H - 2, 'text-anchor': 'end', class: 'ex-axis-name' }, label(X.key) + ' →'));
    // The second measure's name up the left edge, reading upwards.
    axis.append(svg('text', { x: 10, y: m.t, 'text-anchor': 'end', class: 'ex-axis-name', transform: `rotate(-90 10 ${m.t})` }, label(Y.key) + ' →'));
    root.append(axis);
    if (res.line) {
      const { a, b } = res.line;
      root.append(svg('line', { x1: x(x0), y1: y(a + b * x0), x2: x(x1), y2: y(a + b * x1), class: 'ex-trend' }));
    }
    const dots = svg('g', { class: 'ex-dots' });
    const els = points.map((p) => {
      const c = svg('circle', { cx: x(p.x), cy: y(p.y), r: 4, class: 'ex-dot' });
      dots.append(c);
      return c;
    });
    root.append(dots);
    // The nearest day within 24 px answers the pointer.
    let on = null;
    const nearest = (e) => {
      const r = root.getBoundingClientRect();
      const px = ((e.clientX - r.left) / r.width) * W, py = ((e.clientY - r.top) / r.height) * H;
      let best = -1, bd = 24 * 24;
      points.forEach((p, i) => {
        const d = (x(p.x) - px) ** 2 + (y(p.y) - py) ** 2;
        if (d < bd) { bd = d; best = i; }
      });
      return best;
    };
    root.addEventListener('pointermove', (e) => {
      const i = nearest(e);
      if (on != null) els[on].classList.remove('on');
      on = i < 0 ? null : i;
      if (on == null) return hideTip();
      els[on].classList.add('on');
      const p = points[on];
      const r = els[on].getBoundingClientRect();
      showTip(r.left + r.width / 2, r.top, dfShort.format(parse(p.date)), [
        { value: fmtValue(X, p.x), label: label(X.key) },
        { value: fmtValue(Y, p.y), label: label(Y.key) + (lag ? ` (${LAG_LABELS[lag]()})` : '') },
      ], t('Click to open the day'));
    });
    root.addEventListener('pointerleave', () => {
      if (on != null) els[on].classList.remove('on');
      on = null;
      hideTip();
    });
    root.addEventListener('click', (e) => {
      const i = nearest(e);
      if (i >= 0) openDay(points[i].date);
    });
    host.replaceChildren(root);
  };
  host._draw = draw;
  resizer.observe(host);
  draw();
}
