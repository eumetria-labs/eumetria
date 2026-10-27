// The history windows: opened from the most logged foods and the top
// activities. One food or activity over the whole history: how often it was
// logged per month, at which meals or on which weekdays, and every entry,
// each opening its day.

import { MEALS, MEAL_ICONS, actIcon, actLabel, activityDetail, dfLong, dfMonthYear, dfShort, fmt, fmtAmount, fmtDistance, fmtDuration, fmtAvgQty, parse } from './format.js';
import { $, h, emoji, qualityBadges, setHashParam } from './dom.js';
import { state } from './state.js';
import { openDialog } from './dialog.js';
import { openDay } from './sheet.js';
import { openWorkout } from './workout.js';
import { activityHistory, foodHistory } from './query.js';
import { t, tn } from './i18n.js';

let win = null;

const mealLabel = (m) => MEALS.find(([k]) => k === m)?.[1] || t('Other');
const WEEKDAYS = [t('Monday'), t('Tuesday'), t('Wednesday'), t('Thursday'), t('Friday'), t('Saturday'), t('Sunday')];
const month = (m) => dfMonthYear.format(parse(m + '-01'));
// The year only when it isn't the current one, so the date fits its tile.
const lastDate = (d) => dfShort.format(parse(d)) + (d.slice(0, 4) !== state.data.days.at(-1).date.slice(0, 4) ? `, ${d.slice(0, 4)}` : '');
const daysText = (x) => tn(x.days, '{times}× on {n} day', '{times}× on {n} days', { times: fmt(x.n) }) + ' · ' + t('since {month}', { month: month(x.first.slice(0, 7)) });

// Names logged as a food on any day; only those have a history to open.
let loggedNames = null, namesOf = null;
export function hasHistory(name) {
  if (namesOf !== state.data) {
    namesOf = state.data;
    loggedNames = new Set(state.data.days.flatMap((d) => (d.foods || []).map((f) => f.n)));
  }
  return loggedNames.has(name);
}

// Makes a bar of a list (most logged foods, top activities, ultra-processed
// foods) open name's history window: the whole row highlights and clicks.
export function historyLink(row, name, open, label = name) {
  row.classList.add('hbar-link');
  Object.assign(row, { tabIndex: 0, title: t('{name}: every time you logged it', { name: label }) });
  row.setAttribute('role', 'button');
  row.onclick = () => open(name, row);
  row.onkeydown = (e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); open(name, row); } };
  return row;
}

// A food name that opens its history window, wherever it is shown (day
// sheet, recipe ingredients, food quality); plain text for names never
// logged as a food, e.g. an ingredient only used in recipes.
export function foodName(name, cls = 'txt') {
  if (!hasHistory(name)) return h('span', { class: cls }, name);
  const b = h('button', { class: cls + ' food-link', type: 'button', title: t('{name}: every time you logged it', { name }) }, name);
  b.onclick = (e) => { e.stopPropagation(); openFood(name, b); };
  return b;
}

export function openFood(name, returnFocus) {
  const f = foodHistory(state.data.days, name);
  if (!f.n) return;
  const title = h('h2', { id: 'hs-title' });
  title.append(emoji('🍽️'), ' ', name);
  const badges = qualityBadges(f.barcode, state.data.products?.[f.barcode]);
  if (badges) title.append(' ', badges);
  if (f.ai) title.append(h('span', { class: 'ai-badge', title: t('Logged with Yazio AI recognition at least once') }, 'AI'));
  const meals = [...f.meals].sort((a, b) => b[1] - a[1]).map(([m, n]) => [MEAL_ICONS[m] || '🍽️', mealLabel(m), n]);
  openWindow('food', name, title, daysText(f), [
    [t('Times logged'), fmt(f.n)],
    ...(f.avgAmount ? [[t('Avg amount'), fmtAvgQty(f.avgAmount, f.unit)]] : []),
    [t('Avg calories'), fmt(f.avgEnergy), 'kcal'],
    [t('Total calories'), fmt(f.energy), 'kcal'],
    [t('Last logged'), lastDate(f.last)],
  ], [
    monthsSection(f.months),
    barsSection(t('Meals'), f.meals.size === 1 ? t('always the same meal') : '', meals, f.n),
    // Back on the list first, so closing the day returns there.
    entriesSection(f.n, f.entries.map((e) => [
      MEAL_ICONS[e.m] || '🍽️', e.date, [mealLabel(e.m), fmtAmount(e)].filter(Boolean).join(' · '), `${fmt(e.e)} kcal`,
      () => { closeHistory(true); openDay(e.date, e.m); },
    ])),
  ], returnFocus);
}

export function openActivity(key, returnFocus) {
  const a = activityHistory(state.data.days, key);
  if (!a.n) return;
  const title = h('h2', { id: 'hs-title' });
  title.append(emoji(actIcon(key)), ' ', actLabel(key));
  // All seven, so the days without it show too.
  const weekdays = a.weekdays.map((n, i) => ['', WEEKDAYS[i], n]);
  openWindow('activity', key, title, daysText(a), [
    [t('Times logged'), fmt(a.n)],
    ...(a.avgDur ? [[t('Avg duration'), fmtDuration(a.avgDur)], [t('Total time'), fmtDuration(a.dur)]] : []),
    ...(a.dist ? [[t('Total distance'), fmtDistance(a.dist)]] : []),
    [t('Avg calories'), fmt(a.avgEnergy), 'kcal'],
    [t('Total calories'), fmt(a.energy), 'kcal'],
    [t('Last logged'), lastDate(a.last)],
  ], [
    monthsSection(a.months),
    barsSection(t('Weekdays'), a.weekdays.filter(Boolean).length === 1 ? t('always the same weekday') : '', weekdays, a.n),
    // A Fitbit workout opens on top of its day, as from the day's activities.
    entriesSection(a.n, a.entries.map((e) => [
      actIcon(key), e.date, activityDetail(e), `${fmt(e.energy)} kcal`,
      () => {
        closeHistory(true);
        openDay(e.date, 'activities');
        const day = state.byDate.get(e.date);
        if (e.w && day?.workouts?.some((w) => w.id === e.w)) openWorkout(day, e.w, $('#modal .sheet-close'));
      },
    ])),
  ], returnFocus);
}

// stats are [label, value, unit?] tiles; sections are elements.
function openWindow(hashKey, value, title, sub, stats, sections, returnFocus) {
  closeHistory(true);
  setHashParam(hashKey, value);
  const close = h('button', { class: 'sheet-close', type: 'button', 'aria-label': t('Close') }, '×');
  const head = h('div', { class: 'sheet-head wo-head' });
  const titles = h('div', { class: 'wo-titles' });
  titles.append(title, h('p', { class: 'muted' }, sub));
  head.append(titles, close);

  const body = h('div', { class: 'wo-body' });
  const tiles = h('div', { class: 'wo-stats' });
  for (const [label, v, unit] of stats) {
    const d = h('div');
    const b = h('b', { title: v }, v);
    if (unit) b.append(h('small', {}, unit));
    d.append(h('span', {}, label), b);
    tiles.append(d);
  }
  body.append(tiles, ...sections);

  // hs-food or hs-activity: the bars take the colour of their kind of data.
  const panel = h('div', { class: 'wo-panel hs-' + hashKey });
  panel.append(head, body);
  win = openDialog({ cls: 'wo-overlay', labelledBy: 'hs-title', content: [panel], returnFocus, onClose: () => setHashParam(hashKey, null) });
  close.onclick = () => closeHistory();
  close.focus();
}

function section(title, note) {
  const sec = h('section', { class: 'wo-sec' });
  const hd = h('div', { class: 'wo-sec-head' });
  hd.append(h('h3', {}, title), h('span', { class: 'muted' }, note));
  sec.append(hd);
  return sec;
}

// Times logged per month, from the first synced month to the last.
function monthsSection(months) {
  const busiest = months.reduce((a, b) => (b[1] > a[1] ? b : a));
  const sec = section(t('Per month'), t('most in {month} ({n}×)', { month: month(busiest[0]), n: fmt(busiest[1]) }));
  const bars = h('div', { class: 'hs-months', role: 'img', 'aria-label': months.map(([m, n]) => `${month(m)}: ${n}`).join(', ') });
  for (const [m, n] of months) {
    const bar = h('i', { title: `${month(m)}: ${fmt(n)}×` });
    bar.style.height = n ? `${Math.max(4, (n / busiest[1]) * 100)}%` : '0';
    bars.append(bar);
  }
  const axis = h('div', { class: 'hs-axis muted', 'aria-hidden': 'true' });
  axis.append(h('span', {}, month(months[0][0])), h('span', {}, month(months.at(-1)[0])));
  sec.append(bars, axis);
  return sec;
}

// rows are [icon, label, count]; bars are relative to the largest count.
function barsSection(title, note, rows, total) {
  const sec = section(title, note);
  const list = h('div', { class: 'hbars' });
  const max = Math.max(...rows.map(([, , n]) => n), 1);
  for (const [icon, label, n] of rows) {
    const row = h('div', { class: 'hbar' });
    const name = h('span', { class: 'name' });
    if (icon) name.append(emoji(icon));
    name.append(h('span', { class: 'txt' }, label));
    const v = h('span', { class: 'val' }, `${fmt(n)}×`);
    v.append(h('small', {}, `${fmt((n / total) * 100)}%`));
    const track = h('div', { class: 'track' });
    const fill = h('i');
    fill.style.width = `${(n / max) * 100}%`;
    track.append(fill);
    row.append(name, v, track);
    list.append(row);
  }
  sec.append(list);
  return sec;
}

// Every entry, newest first; rows are [icon, date, detail, value, open].
function entriesSection(n, rows) {
  const sec = section(t('Every time'), tn(n, '{n} entry', '{n} entries'));
  for (const [icon, date, detail, value, open] of rows) {
    const row = h('button', { class: 'day-item workout-row hs-row', type: 'button', title: t('Open this day') });
    const name = h('span', { class: 'name' });
    name.append(emoji(icon), h('span', { class: 'txt' }, dfLong.format(parse(date))));
    row.append(name, h('span', { class: 'amt' }, detail), h('span', { class: 'val' }, value), h('span', { class: 'chev', 'aria-hidden': 'true' }, '›'));
    row.onclick = open;
    sec.append(row);
  }
  return sec;
}

export function closeHistory(instant) {
  win?.close(instant);
  win = null;
}
