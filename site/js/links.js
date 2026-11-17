// Linking results noted before they could be linked: lifts and benchmark
// results whose day has Fitbit workouts but that belong to none. The
// Personal bests tab says how many there are (to edit.lifts), and a window
// lists them: a day with one workout is ticked already, a day with several
// asks which. Linking changes each result as its own form would.

import { actLabel, dfLong, parse } from './format.js';
import { linkSuggestions, workoutIcon, workoutName } from './health.js';
import { $, emoji, h } from './dom.js';
import { openDialog } from './dialog.js';
import { api } from './account.js';
import { fmtSet, mayEditLifts, notifyBests, renderLifts } from './lifts.js';
import { fmtResult, renderWods } from './wods.js';
import { state, store } from './state.js';
import { t, tn } from './i18n.js';

// Suggestions put off with "Not now", by key, so the note stays away until
// another one comes. Only a convenience of this browser.
const DISMISSED = 'linksDismissed';
const keyOf = (s) => `${s.kind}:${s.item.id}`;
const dismissed = () => {
  try { return new Set(JSON.parse(store.get(DISMISSED) || '[]')); } catch { return new Set(); }
};

// The suggestions with what the page needs to show them.
function suggestions() {
  if (!state.byDate) return [];
  const byId = new Map(state.wods.workouts.map((w) => [w.id, w]));
  return linkSuggestions(state.lifts, state.wods.results, state.byDate).filter((s) => s.kind === 'lift' || byId.has(s.item.workout))
    .map((s) => (s.kind === 'lift'
      ? { ...s, name: s.item.lift, value: fmtSet(s.item) }
      : { ...s, wod: byId.get(s.item.workout), name: byId.get(s.item.workout).name, value: fmtResult(byId.get(s.item.workout).scoring, s.item, true) }));
}

// The note in the Personal bests tab, for those who may link.
export function renderLinkNotice() {
  const host = $('#bests-link');
  if (!host) return;
  const off = dismissed();
  const all = mayEditLifts() ? suggestions() : [];
  const fresh = all.filter((s) => !off.has(keyOf(s)));
  host.hidden = !fresh.length;
  if (!fresh.length) return host.replaceChildren();
  const review = h('button', { class: 'pill-btn', type: 'button' }, t('Review'));
  review.onclick = () => openLinks(review);
  host.replaceChildren(emoji('🔗'), h('span', {}, tn(all.length, '{n} result was noted on a day with a Fitbit workout but isn’t linked to it.', '{n} results were noted on days with Fitbit workouts but aren’t linked to them.')), review);
}

const workoutLabel = (w) => `${actLabel(workoutName(w.type))} · ${w.start}`;

// The window that links them.
export function openLinks(returnFocus) {
  const list = suggestions();
  if (!list.length) return;
  const close = h('button', { class: 'sheet-close', type: 'button', 'aria-label': t('Close') }, '×');
  const head = h('div', { class: 'sheet-head wo-head' });
  const titles = h('div', { class: 'wo-titles' });
  titles.append(h('h2', { id: 'links-title' }, t('Link results to workouts')), h('p', { class: 'muted' }, t('Days with one workout are ticked; for days with several, pick the one.')));
  head.append(titles, close);
  const body = h('div', { class: 'wo-body links-body' });
  const rows = list.map((s) => {
    const row = h('div', { class: 'day-item link-row' });
    const tick = h('input', { type: 'checkbox', 'aria-label': t('Link {name} of {date}', { name: s.name, date: dfLong.format(parse(s.item.date)) }) });
    tick.checked = !!s.pick;
    const what = h('span', { class: 'name' });
    what.append(emoji(s.kind === 'lift' ? '🏋️' : '⏱️'), h('span', { class: 'txt' }, s.name));
    const when = h('span', { class: 'amt' }, dfLong.format(parse(s.item.date)));
    let pick;
    if (s.candidates.length === 1) {
      const w = s.candidates[0];
      pick = h('span', { class: 'link-to' });
      pick.append(emoji(workoutIcon(w.type)), ' ', workoutLabel(w));
    } else {
      pick = h('select', { class: 'field link-to', 'aria-label': t('Workout') });
      pick.append(h('option', { value: '' }, t('Which workout?')), ...s.candidates.map((w) => h('option', { value: w.id }, workoutLabel(w))));
      pick.onchange = () => { tick.checked = !!pick.value; };
    }
    const label = h('label', { class: 'link-main' });
    label.append(tick, what);
    row.append(label, h('span', { class: 'val' }, s.value), when, pick);
    return { s, tick, workout: () => (s.candidates.length === 1 ? s.candidates[0].id : pick.value) };
  });
  body.append(...rows.map((r) => r.tick.closest('.link-row')));

  const error = h('p', { class: 'lock-error', role: 'alert', hidden: '' });
  const save = h('button', { class: 'pill-btn', type: 'button' }, t('Link'));
  const later = h('button', { class: 'pill-btn', type: 'button' }, t('Not now'));
  const actions = h('div', { class: 'sec-row lift-actions' });
  actions.append(save, later);
  body.append(error, actions);
  const panel = h('div', { class: 'wo-panel links-panel' });
  panel.append(head, body);
  const dlg = openDialog({ cls: 'wo-overlay', labelledBy: 'links-title', content: [panel], returnFocus });
  close.onclick = () => dlg.close();
  close.focus();

  // Not now: the ones still here don't bring the note back.
  later.onclick = () => {
    const off = dismissed();
    for (const { s } of rows) off.add(keyOf(s));
    store.set(DISMISSED, JSON.stringify([...off]));
    dlg.close();
    renderLinkNotice();
  };

  save.onclick = async () => {
    const chosen = rows.filter((r) => r.tick.checked && r.workout());
    if (!chosen.length) return;
    for (const b of [save, later]) b.disabled = true;
    error.hidden = true;
    let failed = 0;
    for (const { s, workout } of chosen) {
      const e = s.item;
      const res = s.kind === 'lift'
        ? await api('lifts/' + e.id, 'PUT', { lift: e.lift, kg: e.kg, reps: e.reps, date: e.date, note: e.note || '', fitbit_workout: workout() }).catch(() => null)
        : await api('wod-results/' + e.id, 'PUT', { value: e.value, reps: e.reps || 0, scaled: !!e.scaled, capped: !!e.capped, date: e.date, note: e.note || '', fitbit_workout: workout() }).catch(() => null);
      if (!res?.ok) {
        failed++;
        continue;
      }
      const saved = await res.json();
      if (s.kind === 'lift') state.lifts = state.lifts.map((x) => (x.id === saved.id ? saved : x));
      else state.wods = { ...state.wods, results: state.wods.results.map((x) => (x.id === saved.id ? saved : x)) };
    }
    renderLifts();
    renderWods();
    notifyBests();
    if (failed) {
      error.textContent = tn(failed, '{n} result could not be linked. Try again.', '{n} results could not be linked. Try again.');
      error.hidden = false;
      for (const b of [save, later]) b.disabled = false;
      return;
    }
    dlg.close();
  };
}
