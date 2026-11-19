// The daily check-in (how the day feels: energy, soreness, stress and mood
// from 1 to 5, and a short note) and the day's tags (sick, travel, …) that
// explain an unusual day. The server keeps them (checkins.go): view.health
// reads them, edit.checkin writes them. Days with 10 g of alcohol or more
// are tagged "alcohol" by themselves unless someone removed that tag; days
// tagged sick or travel stay out of the scores' baselines on the server.
//
// Shown on the Today card (a line, or a button to check in), in the day
// window, as bands on the charts like phases, and in Patterns.

import { addDays, dfLong, iso, parse } from './format.js';
import { $, h, svg } from './dom.js';
import { openDialog } from './dialog.js';
import { account, api, reason } from './account.js';
import { state } from './state.js';
import { ALCOHOL_DAY } from './nutrients.js';
import { freeNightTest } from './sleeptiming.js';
import { t } from './i18n.js';

// The tags, in the server's order (checkins.Tags).
export const TAGS = {
  alcohol: { icon: '🍺', label: () => t('Alcohol') },
  sick: { icon: '🤒', label: () => t('Sick') },
  travel: { icon: '✈️', label: () => t('Travel') },
  // A day without work: the night before it is a free night (sleeptiming.js).
  day_off: { icon: '🛋️', label: () => t('Day off') },
  late_meal: { icon: '🌙', label: () => t('Late meal') },
  caffeine_late: { icon: '☕', label: () => t('Late caffeine') },
  work_stress: { icon: '💼', label: () => t('Work stress') },
  bad_room: { icon: '🌡️', label: () => t('Warm or noisy room') },
  injury: { icon: '🩹', label: () => t('Injury') },
};
export const tagName = (tag) => `${TAGS[tag]?.icon || ''} ${TAGS[tag]?.label() || tag}`.trim();

// The questions, each from 1 to 5 (stored as numbers), with a word for
// each answer. good: whether more is better (energy, mood) or less
// (soreness, stress); it colours the answers.
export const QUESTIONS = [
  { key: 'energy', label: () => t('Energy'), good: 'up', words: () => [t('drained'), t('low|level'), t('okay'), t('good'), t('great')] },
  { key: 'soreness', label: () => t('Soreness'), good: 'down', words: () => [t('none'), t('light'), t('some'), t('sore'), t('very sore')] },
  { key: 'stress', label: () => t('Stress'), good: 'down', words: () => [t('calm'), t('relaxed'), t('some'), t('high|level'), t('very high')] },
  { key: 'mood', label: () => t('Mood'), good: 'up', words: () => [t('down|mood'), t('meh'), t('okay'), t('good'), t('great')] },
];

// The symbol of each answer: much less, less, as usual, more, much more.
const LEVEL_PATHS = [
  'M7 6l5 5 5-5M7 13l5 5 5-5',
  'M12 5v14M6 13l6 6 6-6',
  'M3 12c2-3 4-3 6 0s4 3 6 0 4-3 6 0',
  'M12 19V5M6 11l6-6 6 6',
  'M7 11l5-5 5 5M7 18l5-5 5 5',
];
const levelIcon = (v) => {
  const root = svg('svg', { viewBox: '0 0 24 24', width: 18, height: 18, 'aria-hidden': 'true', fill: 'none', stroke: 'currentColor', 'stroke-width': 2.2, 'stroke-linecap': 'round', 'stroke-linejoin': 'round' });
  root.append(svg('path', { d: LEVEL_PATHS[v - 1] }));
  return root;
};
// tone: "good", "bad" or "" (as usual) for answer v of q.
const tone = (q, v) => (v === 3 ? '' : (v > 3) === (q.good === 'up') ? 'good' : 'bad');
export const answerWord = (q, v) => q.words()[v - 1];

// answerChip is one answer as the line shows it: the symbol in its colour
// and the question's name, the word as its tooltip.
function answerChip(q, v) {
  const el = h('span', { class: 'ci-ans ' + (tone(q, v) ? 'lv-' + tone(q, v) : 'lv-even'), title: `${q.label()}: ${answerWord(q, v)}` });
  el.append(levelIcon(v), h('span', {}, q.label()));
  return el;
}

const readable = (scopes) => !scopes || scopes.includes('view.health');

// loadCheckins reads the check-ins that go with data. Plain files (no
// server) read checkins.json, which -demo writes. Offline the ones already
// here stay.
export async function loadCheckins(data) {
  if (!readable(data.scopes)) {
    state.checkins = new Map();
    return;
  }
  let res = await api('checkins').catch(() => null);
  if (res?.status === 404) res = await fetch('checkins.json', { cache: 'no-cache' }).catch(() => null);
  if (res?.status === 404) {
    state.checkins = new Map();
    return;
  }
  if (!res?.ok) {
    if (res) console.error('loading the check-ins failed', res.status);
    return;
  }
  const body = await res.json();
  state.checkins = new Map((body.checkins || []).map((c) => [c.date, c]));
}

export function mayCheckin() {
  const u = account.user;
  return !!u?.scopes?.includes('edit.checkin') && (!u.needs_second_factor || !!u.demo);
}

const alcoholOf = (date) => state.byDate?.get(date)?.nut?.alcohol?.[0] || 0;

// dayTags are a day's tags, in the list's order: those people set, and
// "alcohol" for a day with 10 g or more unless removed.
export function dayTags(date) {
  const c = state.checkins?.get(date);
  const set = new Set(c?.tags || []);
  if (alcoholOf(date) >= ALCOHOL_DAY && !c?.dismissed?.includes('alcohol')) set.add('alcohol');
  return Object.keys(TAGS).filter((k) => set.has(k));
}

const hasAnswers = (c) => !!c && (QUESTIONS.some((q) => c[q.key]) || !!c.note);

// checkinLine is a day's check-in in words ("Energy good · Soreness light
// · 🤒 Sick"), or "" without one: for screen readers and tooltips.
export function checkinLine(date) {
  const c = state.checkins?.get(date);
  const parts = QUESTIONS.filter((q) => c?.[q.key]).map((q) => `${q.label()} ${answerWord(q, c[q.key])}`);
  parts.push(...dayTags(date).map(tagName));
  return parts.join(' · ');
}

// checkinNodes is the same as checkinLine shows it: each answer's symbol
// with its name, then the tags.
function checkinNodes(date) {
  const c = state.checkins?.get(date);
  const out = QUESTIONS.filter((q) => c?.[q.key]).map((q) => answerChip(q, c[q.key]));
  out.push(...dayTags(date).map((tag) => h('span', { class: 'ci-tagged' }, tagName(tag))));
  return out;
}

/* ---------- the Today card ---------- */

// The day a check-in from the Today card is for: today, and before noon
// yesterday too (the form switches between them).
const yesterdayOpen = () => new Date().getHours() < 12;

// renderCheckin draws the Today card's check-in line: what was noted
// today, or (to those who may) a button to check in.
export function renderCheckin() {
  const host = $('#today-checkin');
  if (!host) return;
  const date = state.today;
  const may = mayCheckin();
  const line = date && state.checkins ? checkinLine(date) : '';
  host.hidden = !line && !may;
  if (host.hidden) return;
  const b = h('button', { class: 'checkin-line' + (line ? '' : ' ask'), type: 'button' });
  if (line) {
    const text = h('span', { class: 'ci-text', 'aria-hidden': 'true' });
    text.append(...checkinNodes(date));
    b.append(h('span', { class: 'ci-icon', 'aria-hidden': 'true' }, '☀️'), text);
    b.title = line;
    b.setAttribute('aria-label', t('Today’s check-in: {line}', { line }) + (may ? '. ' + t('Change') : ''));
  } else {
    b.append(h('span', { class: 'ci-icon', 'aria-hidden': 'true' }, '☀️'), h('span', { class: 'ci-text' }, t('How do you feel today?')), h('span', { class: 'ci-go' }, t('Check in') + ' ›'));
  }
  b.disabled = !may;
  b.onclick = () => openCheckin({ date, returnFocus: b });
  host.replaceChildren(b);
}

/* ---------- the form ---------- */

// openCheckin opens the check-in of date (today when not given).
export function openCheckin({ date = state.today, returnFocus } = {}) {
  const today = state.today;
  const yesterday = today && iso(addDays(parse(today), -1));
  const close = h('button', { class: 'sheet-close', type: 'button', 'aria-label': t('Close') }, '×');
  const head = h('div', { class: 'sheet-head wo-head' });
  const titles = h('div', { class: 'wo-titles' });
  const sub = h('p', { class: 'muted' });
  titles.append(h('h2', { id: 'checkin-title' }, t('Check-in')), sub);
  head.append(titles, close);
  const form = h('form', { class: 'wo-body checkin-form' });

  // Before noon the card's check-in can still be yesterday's.
  let day = date;
  const switcher = h('div', { class: 'ci-day', role: 'group', 'aria-label': t('Day') });
  const dayBtns = [[today, t('Today')], [yesterday, t('Yesterday')]].map(([d, label]) => {
    const b = h('button', { type: 'button', class: 'ci-dbtn' }, label);
    b.onclick = () => { day = d; fill(); };
    b.dataset.day = d;
    return b;
  });
  switcher.append(...dayBtns);
  if ((date === today || date === yesterday) && yesterdayOpen()) form.append(switcher);

  const scales = {};
  for (const q of QUESTIONS) {
    const row = h('div', { class: 'ci-q', role: 'radiogroup', 'aria-label': q.label() });
    const label = h('div', { class: 'ci-q-head' });
    label.append(h('span', { class: 'lbl' }, q.label()));
    const btns = h('div', { class: 'ci-scale' });
    const buttons = [1, 2, 3, 4, 5].map((v) => {
      const b = h('button', { type: 'button', class: 'ci-n ' + (tone(q, v) ? 'lv-' + tone(q, v) : 'lv-even'), role: 'radio', 'aria-checked': 'false', 'aria-label': `${q.label()}: ${answerWord(q, v)}` });
      b.append(levelIcon(v), h('span', { class: 'ci-word' }, answerWord(q, v)));
      // Tapping the picked one again takes the answer back.
      b.onclick = () => { scales[q.key].value = scales[q.key].value === v ? null : v; paint(); };
      return b;
    });
    btns.append(...buttons);
    row.append(label, btns);
    form.append(row);
    scales[q.key] = { value: null, buttons };
  }

  const tagBox = h('div', { class: 'ci-tags', role: 'group', 'aria-label': t('Tags') });
  const tagState = new Map();
  for (const key of Object.keys(TAGS)) {
    const b = h('button', { type: 'button', class: 'ci-tag', 'aria-pressed': 'false' }, tagName(key));
    b.onclick = () => { tagState.set(key, !tagState.get(key)); paint(); };
    tagBox.append(b);
    tagState.set(key, false);
    b.dataset.tag = key;
  }
  const tagHead = h('div', { class: 'ci-q-head' });
  tagHead.append(h('span', { class: 'lbl' }, t('Tags')), h('span', { class: 'muted hint' }, t('What made the day unusual. Sick and travel days don’t count towards what’s usual for your scores.')));
  // Tags go on the day it happened: last evening's drinks or late dinner
  // belong to yesterday, whose night Patterns then looks at.
  const when = h('p', { class: 'muted hint ci-when', hidden: '' }, form.contains(switcher)
    ? t('Tags go on the day it happened: for last evening’s drinks or late meal, switch to Yesterday.')
    : t('Tags go on the day it happened: last evening’s drinks or late meal belong to yesterday (open it in the day window).'));
  form.append(tagHead, when, tagBox);
  const auto = h('p', { class: 'muted hint ci-auto', hidden: '' }, t('Alcohol is tagged by itself from the drinks logged; untick it if that’s wrong.'));
  form.append(auto);

  const noteWrap = h('label', { class: 'lift-field' });
  const note = h('input', { class: 'field', type: 'text', maxlength: '200', placeholder: t('optional') });
  noteWrap.append(h('span', { class: 'lbl' }, t('Note')), note);
  form.append(noteWrap);

  const error = h('p', { class: 'lock-error', role: 'alert', hidden: '' });
  const save = h('button', { class: 'pill-btn', type: 'submit' }, t('Save'));
  const cancel = h('button', { class: 'pill-btn', type: 'button' }, t('Cancel'));
  const del = h('button', { class: 'pill-btn danger', type: 'button' }, t('Delete'));
  const row = h('div', { class: 'sec-row lift-actions' });
  row.append(save, cancel, del);
  form.append(error, row);

  function paint() {
    for (const q of QUESTIONS) {
      scales[q.key].buttons.forEach((b, i) => b.setAttribute('aria-checked', String(scales[q.key].value === i + 1)));
    }
    for (const b of tagBox.children) b.setAttribute('aria-pressed', String(!!tagState.get(b.dataset.tag)));
    for (const b of dayBtns) b.setAttribute('aria-pressed', String(b.dataset.day === day));
  }
  function fill() {
    const c = state.checkins?.get(day);
    sub.textContent = dfLong.format(parse(day));
    for (const q of QUESTIONS) scales[q.key].value = c?.[q.key] || null;
    const tags = dayTags(day);
    for (const key of tagState.keys()) tagState.set(key, tags.includes(key));
    auto.hidden = !(alcoholOf(day) >= ALCOHOL_DAY);
    when.hidden = day !== today;
    note.value = c?.note || '';
    del.hidden = !c;
    error.hidden = true;
    paint();
  }
  fill();

  const panel = h('div', { class: 'wo-panel checkin-panel' });
  panel.append(head, form);
  const dlg = openDialog({ cls: 'wo-overlay', labelledBy: 'checkin-title', content: [panel], returnFocus });
  close.onclick = cancel.onclick = () => dlg.close();
  scales.energy.buttons[2].focus();

  const send = async (button, run, update) => {
    for (const b of [save, cancel, del]) b.disabled = true;
    error.hidden = true;
    const res = await run().catch(() => null);
    if (res?.ok || (res?.status === 404 && button === del)) {
      const before = skipOf(day);
      await update(res);
      dlg.close();
      // Sick and travel days change the scores: the stats come again.
      document.dispatchEvent(new CustomEvent('checkins-change', { detail: { scores: before !== skipOf(day) } }));
      return;
    }
    const why = res ? await reason(res) : '';
    error.textContent = why === 'second_factor_required' ? t('Set up a second factor under Settings → Security first.') : t('That didn’t work. Try again.');
    error.hidden = false;
    for (const b of [save, cancel, del]) b.disabled = false;
    button.focus();
  };

  form.onsubmit = (e) => {
    e.preventDefault();
    const tags = [...tagState].filter(([, on]) => on).map(([k]) => k);
    // An automatic alcohol tag unticked is remembered as removed.
    const dismissed = alcoholOf(day) >= ALCOHOL_DAY && !tagState.get('alcohol') ? ['alcohol'] : [];
    const body = { note: note.value, tags, dismissed };
    for (const q of QUESTIONS) body[q.key] = scales[q.key].value;
    send(save, () => api('checkins/' + day, 'PUT', body), async (res) => {
      if (!state.checkins) state.checkins = new Map();
      state.checkins.set(day, await res.json());
    });
  };
  del.onclick = () => {
    if (!confirm(t('Delete this check-in?'))) return;
    send(del, () => api('checkins/' + day, 'DELETE'), () => state.checkins.delete(day));
  };
}

const skipOf = (date) => {
  const tags = state.checkins?.get(date)?.tags || [];
  return tags.filter((x) => x === 'sick' || x === 'travel').join();
};

/* ---------- elsewhere ---------- */

// dayCheckin is the day window's check-in line, with a button to change it
// for those who may; null when the day has none and they may not.
export function dayCheckin(date) {
  const line = checkinLine(date);
  const note = state.checkins?.get(date)?.note;
  const may = mayCheckin();
  if (!line && !note && !may) return null;
  const box = h('div', { class: 'sheet-checkin' });
  const text = h('span', { class: 'ci-text' });
  const answers = h('span', { class: 'ci-answers', title: line });
  answers.append(...(line ? checkinNodes(date) : [t('No check-in')]));
  text.append(answers);
  if (note) text.append(h('small', {}, note));
  box.append(h('span', { class: 'ci-icon', 'aria-hidden': 'true' }, '☀️'), text);
  if (may) {
    const b = h('button', { class: 'pill-btn', type: 'button' }, hasAnswers(state.checkins?.get(date)) || line ? t('Change') : t('Check in'));
    b.onclick = () => openCheckin({ date, returnFocus: b });
    box.append(b);
  }
  return box;
}

// tagSpans are the runs of days tagged sick or travel, shaped like phases
// for the charts' bands (phaseSlots).
export function tagSpans() {
  if (!state.checkins?.size) return [];
  const out = [];
  for (const kind of ['sick', 'travel']) {
    const days = [...state.checkins.values()].filter((c) => c.tags?.includes(kind)).map((c) => c.date).sort();
    for (const d of days) {
      const last = out.at(-1);
      if (last?.kind === kind && iso(addDays(parse(last.end), 1)) === d) last.end = d;
      else out.push({ id: -1, kind, start: d, end: d, label: TAGS[kind].label(), icon: TAGS[kind].icon });
    }
  }
  return out;
}

// checkinRows are the days with a check-in or a tag, each with its tags
// resolved (the automatic alcohol one too), for the export.
export function checkinRows() {
  const dates = new Set([...(state.checkins?.keys() || []), ...(state.data?.days || []).filter((d) => dayTags(d.date).length).map((d) => d.date)]);
  return [...dates].map((date) => ({ ...(state.checkins?.get(date) || { date }), tags: dayTags(date) }));
}

// pageFreeNight is the test whether the night ending on a date's morning
// is a free one: a weekday off of the owner's (the stats' sleep_free_days), a public
// holiday, a vacation day (Phases & Events) or a day tagged "day off".
export function pageFreeNight() {
  const off = new Set();
  for (const p of state.phases || []) {
    if (p.kind !== 'vacation') continue;
    for (let d = parse(p.start); iso(d) <= (p.end || state.today); d = addDays(d, 1)) off.add(iso(d));
  }
  for (const [date, c] of state.checkins || []) if (c.tags?.includes('day_off')) off.add(date);
  return freeNightTest({ freeDays: state.data?.sleep_free_days || [0, 6], holidays: state.data?.holidays || {}, off });
}
