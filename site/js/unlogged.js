// "Nothing logged" (PLAN 14c): recorded workouts of the last 7 days that no
// training wraps yet, of the types the person picked (Profile → Training;
// all but walks unless set). A count on "+ Log", in the Training card's head
// and on the phone's Training tab, and a window that handles them one by
// one: log a training, add the workout to a training of its day (merge), or
// say there is nothing to add (an empty training, kept by the server, so
// every device agrees). Computed in the page from the day data and the
// trainings; for edit.lifts only, who can act on it.

import { actLabel, dfLong, fmt, parse } from './format.js';
import { $, emoji, h } from './dom.js';
import { openDialog } from './dialog.js';
import { account, api, reason } from './account.js';
import { state } from './state.js';
import { EVERYDAY, REVIEW_DAYS, prompts, unreviewedWorkouts, workoutIcon, workoutName } from './health.js';
import { mayEditLifts } from './lifts.js';
import { openTrainingForm, setTrainings, workoutLine } from './sessions.js';
import { t, tn } from './i18n.js';

// The person's types, or null for the default.
export const promptTypes = () => account.user?.layout?.prompt_types ?? null;

// The workouts not logged, the newest first: { date, w }. Nothing before
// the trainings are known, or every workout would count.
export function unlogged() {
  if (!mayEditLifts() || !state.sessionsLoaded || !state.byDate) return [];
  return unreviewedWorkouts(state.byDate, state.trainings, state.today, promptTypes());
}

/* ---------- the counts ---------- */

const badge = (n) => h('b', { class: 'log-badge', 'aria-hidden': 'true' }, String(n));

// renderUnlogged puts the count where it shows: "+ Log", its Training
// item, the Training card's head and the phone's Training tab.
export function renderUnlogged() {
  const n = unlogged().length;
  const say = tn(n, '{n} workout not logged', '{n} workouts not logged');
  const quick = $('#quick-btn');
  if (quick) {
    quick.querySelector('.log-badge')?.remove();
    if (n) quick.append(badge(n));
    quick.setAttribute('aria-label', n ? `${t('Log something')}, ${say}` : t('Log something'));
  }
  const item = $('#quick-menu [data-log="training"]');
  if (item) {
    item.querySelector('.log-badge')?.remove();
    if (n) item.append(badge(n));
  }
  const tab = $('#tabbar a[data-view="training"]');
  if (tab) {
    tab.querySelector('.log-badge')?.remove();
    if (n) tab.append(badge(n));
    tab.setAttribute('aria-label', n ? `${t('Training')}, ${say}` : t('Training'));
  }
  const head = $('#training-card .card-head');
  if (head) {
    let b = $('#training-unlogged');
    if (!b) {
      b = h('button', { class: 'pill-btn unlogged-btn', id: 'training-unlogged', type: 'button' });
      b.onclick = () => openUnlogged(b);
      head.append(b);
    }
    b.hidden = !n;
    b.replaceChildren(emoji('📝'), say);
  }
}

// The counts follow every change to the trainings.
document.addEventListener('trainings-change', () => {
  renderUnlogged();
  redraw?.();
});

/* ---------- the window ---------- */

let redraw = null;

// openUnlogged lists the workouts not logged, each with what can be done.
export function openUnlogged(returnFocus) {
  const close = h('button', { class: 'sheet-close', type: 'button', 'aria-label': t('Close') }, '×');
  const head = h('div', { class: 'sheet-head wo-head' });
  const titles = h('div', { class: 'wo-titles' });
  titles.append(h('h2', { id: 'unlogged-title' }, t('Not logged yet')),
    h('p', { class: 'muted' }, tn(REVIEW_DAYS, 'Recorded workouts of the last {n} day without a training', 'Recorded workouts of the last {n} days without a training')));
  head.append(titles, close);
  const list = h('div', { class: 'unlogged-list' });
  const error = h('p', { class: 'lock-error', role: 'alert', hidden: '' });
  const types = h('button', { class: 'pill-btn small unlogged-types', type: 'button' }, t('Which workout types ask: Profile → Training'));
  const body = h('div', { class: 'wo-body' });
  body.append(list, error, types);
  const panel = h('div', { class: 'wo-panel unlogged-panel' });
  panel.append(head, body);
  const win = openDialog({ cls: 'wo-overlay', labelledBy: 'unlogged-title', content: [panel], returnFocus, onClose: () => { redraw = null; } });
  close.onclick = () => win.close();
  types.onclick = async () => {
    win.close(true);
    const { openPrefs } = await import('./prefs.js');
    openPrefs('profile', 'training', { returnFocus });
  };

  const fail = async (res) => {
    const why = res ? await reason(res) : '';
    error.textContent = why === 'workout_taken' ? t('That workout was logged elsewhere in the meantime. Reload the page.')
      : why === 'second_factor_required' ? t('Set up a second factor under Settings → Security first.')
      : res ? t('That didn’t work. Try again.') : t('Server not reachable.');
    error.hidden = false;
  };

  const draw = () => {
    const items = unlogged();
    if (!items.length) {
      list.replaceChildren(h('p', { class: 'empty-tab' }, tn(REVIEW_DAYS, 'Every workout of the last {n} day is logged.', 'Every workout of the last {n} days is logged.')));
      return;
    }
    list.replaceChildren(...items.map(({ date, w }) => row(date, w)));
  };

  const row = (date, w) => {
    const ref = { source: 'fitbit', id: w.id };
    const item = h('div', { class: 'unlogged-row' });
    const what = h('div', { class: 'unlogged-what' });
    const name = h('b', {});
    name.append(emoji(workoutIcon(w.type)), ' ', actLabel(workoutName(w.type)));
    const facts = [dfLong.format(parse(date)), workoutLine(w).split(' · ').slice(1).join(' · '), w.hr_avg ? t('⌀ {n} bpm', { n: fmt(w.hr_avg) }) : ''].filter(Boolean).join(' · ');
    what.append(name, h('span', { class: 'muted' }, facts));
    const actions = h('div', { class: 'sec-row unlogged-actions' });
    const log = h('button', { class: 'pill-btn', type: 'button' }, t('Log training'));
    log.onclick = () => openTrainingForm({ link: { id: w.id, date }, returnFocus: log });
    actions.append(log);
    // A training of the same day can take it (merging).
    const same = state.trainings.filter((x) => x.date === date);
    if (same.length) {
      const add = h('select', { class: 'field unlogged-add', 'aria-label': t('Add {name} to a training of that day', { name: actLabel(workoutName(w.type)) }) });
      add.append(h('option', { value: '' }, t('Add to…')), ...same.map((x) => h('option', { value: String(x.id) }, trainingLabel(x))));
      add.onchange = async () => {
        const x = state.trainings.find((y) => String(y.id) === add.value);
        if (!x) return;
        add.disabled = true;
        error.hidden = true;
        const res = await api('training/sessions/' + x.id, 'PUT', { date: x.date, workouts: [...x.workouts, ref], title: x.title || '', rpe: x.rpe ?? null, sets: x.sets, bests: [] }).catch(() => null);
        if (!res?.ok) {
          add.disabled = false;
          add.value = '';
          return fail(res);
        }
        const saved = await res.json();
        setTrainings(state.trainings.map((y) => (y.id === saved.id ? saved : y)));
      };
      actions.append(add);
    }
    const none = h('button', { class: 'pill-btn', type: 'button' }, t('Nothing to add'));
    none.onclick = async () => {
      none.disabled = true;
      error.hidden = true;
      const res = await api('training/sessions', 'POST', { date, workouts: [ref], title: '', rpe: null, sets: [], bests: [] }).catch(() => null);
      if (!res?.ok) {
        none.disabled = false;
        return fail(res);
      }
      setTrainings([...state.trainings, await res.json()]);
    };
    actions.append(none);
    item.append(what, actions);
    return item;
  };

  redraw = draw;
  draw();
  (list.querySelector('button') || close).focus();
}

// A training as a choice: its name, else its workouts, else what it holds.
function trainingLabel(x) {
  if (x.title) return x.title;
  const day = state.byDate?.get(x.date);
  const ws = x.workouts.map((r) => day?.workouts?.find((w) => w.id === r.id)).filter(Boolean);
  if (ws.length) return ws.map((w) => `${actLabel(workoutName(w.type))} ${w.start}`).join(' + ');
  return x.sets.length ? tn(x.sets.length, 'Training with {n} set', 'Training with {n} sets') : t('Training without a workout');
}

/* ---------- Profile → Training ---------- */

// renderPromptTypes draws the ticks for the workout types that ask to be
// logged: every type recorded in the last 90 days, and any picked before.
export function renderPromptTypes(host) {
  // Opened before the day data is here (straight from the address): drawn
  // again once it and the trainings are.
  if (!state.data?.days?.length || !state.sessionsLoaded) {
    const again = () => {
      if (!state.data?.days?.length || !state.sessionsLoaded) return;
      for (const e of ['data-rendered', 'trainings-change']) document.removeEventListener(e, again);
      if (host.isConnected) renderPromptTypes(host);
    };
    for (const e of ['data-rendered', 'trainings-change']) document.addEventListener(e, again);
  }
  const sec = h('section', { class: 'wo-sec lo-panel' });
  const hd = h('div', { class: 'wo-sec-head' });
  hd.append(h('h3', {}, t('Not logged yet')),
    h('span', { class: 'muted' }, tn(REVIEW_DAYS, 'A recorded workout of these types counts as not logged for {n} day, until a training has it (“Nothing to add” counts).', 'A recorded workout of these types counts as not logged for {n} days, until a training has it (“Nothing to add” counts).')));
  const since = state.today ? new Date(parse(state.today).getTime() - 90 * 864e5).toISOString().slice(0, 10) : '';
  const seen = new Map();
  for (const d of state.data?.days || []) {
    if (d.date < since) continue;
    for (const w of d.workouts || []) seen.set(w.type, (seen.get(w.type) || 0) + 1);
  }
  for (const ty of promptTypes() || []) if (!seen.has(ty)) seen.set(ty, 0);
  const status = h('p', { class: 'muted lo-status', role: 'status' });
  const list = h('div', { class: 'prompt-types' });
  const boxes = [];
  const ask = prompts(promptTypes());
  for (const [type, n] of [...seen].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))) {
    const box = h('input', { type: 'checkbox', value: type });
    box.checked = ask({ type });
    const label = h('label', { class: 'lift-check prompt-type' });
    label.append(box, h('span', {}, `${actLabel(workoutName(type))}`), h('span', { class: 'muted hint' }, n ? tn(n, '{n} in 90 days', '{n} in 90 days') : t('none in 90 days')));
    boxes.push(box);
    list.append(label);
  }
  const reset = h('button', { class: 'pill-btn', type: 'button' }, t('Default: all but walks'));
  const foot = h('div', { class: 'lo-foot' });
  foot.append(reset, status);
  let saving = Promise.resolve();
  const save = (types) => {
    if (!account.user) return;
    const next = { ...(account.user.layout || { order: [], hidden: [] }) };
    if (types) next.prompt_types = types;
    else delete next.prompt_types;
    account.user.layout = next;
    document.dispatchEvent(new Event('trainings-change'));
    saving = saving.then(async () => {
      const res = await api('profile/layout', 'PUT', next).catch(() => null);
      status.textContent = res?.ok ? t('Saved.') : t('Not saved: the server could not be reached.');
    });
  };
  for (const box of boxes) box.onchange = () => save(boxes.filter((b) => b.checked).map((b) => b.value));
  reset.onclick = () => {
    for (const b of boxes) b.checked = !EVERYDAY.has(workoutName(b.value));
    save(null);
  };
  if (!boxes.length) list.append(h('p', { class: 'empty-tab' }, t('No workouts recorded in the last 90 days.')));
  sec.append(hd, list, foot);
  host.replaceChildren(sec);
}
