// Personal bests in lifts (Power Clean, Strict Press, Split Jerk, …), in the
// Training card's "Personal bests" tab. People note them themselves: the
// server keeps them (lifts.go), everyone with view.health reads them, and
// edit.lifts adds, changes and deletes them. Each entry is one result on one
// day; a lift's best is its heaviest, the rest is its history.

import { NUM_LOCALE, dfLong, dfShort, dfMonthYear, parse } from './format.js';
import { COMMON_LIFTS, liftBoard, loadable, oneRepMax, percentages } from './health.js';
import { $, emoji, h, nameCell } from './dom.js';
import { openDialog } from './dialog.js';
import { account, api, reason } from './account.js';
import { state } from './state.js';
import { t } from './i18n.js';

const nfKg = new Intl.NumberFormat(NUM_LOCALE, { maximumFractionDigits: 2 });
export const fmtKg = (kg) => `${nfKg.format(kg)} kg`;
const gainKg = (kg) => (kg > 0 ? '+' : '−') + fmtKg(Math.abs(kg));

// A day in the list: with its year only when that isn't this year's.
const shortDate = (d) => (d.slice(0, 4) === (state.today || '').slice(0, 4) ? dfShort.format(parse(d)) : dfMonthYear.format(parse(d)));

// "1 RM" for a one-rep max, "5 RM" for five reps.
const rm = (reps) => `${reps} RM`;

// What a row is called: the lift, with the reps when it isn't a single.
const rowName = (r) => (r.reps > 1 ? `${r.lift} · ${rm(r.reps)}` : r.lift);

// The entries that go with data. Plain files (no server) read lifts.json,
// which -demo writes. Offline the ones already here stay.
export async function loadLifts(data) {
  if (data.scopes && !data.scopes.includes('view.health')) {
    state.lifts = [];
    return;
  }
  let res = await api('lifts').catch(() => null);
  if (res?.status === 404) res = await fetch('lifts.json', { cache: 'no-cache' }).catch(() => null);
  if (res?.status === 404) {
    state.lifts = [];
    state.liftsLoaded = true;
    return;
  }
  if (!res?.ok) {
    console.error('loading the lifts failed', res?.status);
    return;
  }
  state.lifts = await res.json();
  state.liftsLoaded = true;
}

// Without a second factor the server refuses a role that needs one.
export function mayEditLifts() {
  const u = account.user;
  return !!u?.scopes?.includes('edit.lifts') && !u.needs_second_factor;
}

// Whether the tab has anything to show or to add.
export const hasLifts = () => state.lifts.length > 0 || (mayEditLifts() && state.liftsLoaded);

/* ---------- the list in the card ---------- */

export function renderLifts() {
  const host = $('#lifts');
  if (!host) return;
  const rows = liftBoard(state.lifts, state.today);
  const add = $('#lift-add');
  add.hidden = !mayEditLifts();
  add.onclick = () => openLiftForm({ returnFocus: add });
  $('#lifts-sub').textContent = rows.length ? t('heaviest result per lift') : '';
  if (!rows.length) {
    host.replaceChildren(h('p', { class: 'empty-tab' }, !state.liftsLoaded ? t('The lifts could not be loaded.')
      : mayEditLifts() ? t('No lifts yet. Add your first personal best.') : t('No lifts noted yet.')));
    return;
  }
  host.replaceChildren(...rows.map((r) => {
    const detail = [shortDate(r.best.date), r.gain ? gainKg(r.gain) : ''].filter(Boolean).join(' · ');
    const row = h('button', { class: 'day-item workout-row lift-row', type: 'button', 'aria-label': t('{label}: {value}, {detail}. Show history', { label: rowName(r), value: fmtKg(r.best.kg), detail }) });
    const name = nameCell(r.lift, false, '🏋️');
    if (r.reps > 1) name.append(h('span', { class: 'lift-rm' }, rm(r.reps)));
    if (r.recent) name.append(h('span', { class: 'lift-new', title: t('Set in the last 30 days') }, t('New')));
    row.append(name, h('span', { class: 'amt' }, detail), h('span', { class: 'val' }, fmtKg(r.best.kg)), h('span', { class: 'chev', 'aria-hidden': 'true' }, '›'));
    row.addEventListener('click', () => openLift(r.key, row));
    return row;
  }));
}

/* ---------- one lift's window ---------- */

let win = null;

// The window of one lift and rep count: its best, the percentages to train
// with, and every result.
function openLift(key, returnFocus) {
  win?.close(true);
  const close = h('button', { class: 'sheet-close', type: 'button', 'aria-label': t('Close') }, '×');
  const head = h('div', { class: 'sheet-head wo-head' });
  const titles = h('div', { class: 'wo-titles' });
  const title = h('h2', { id: 'lift-title' });
  const sub = h('p', { class: 'muted' });
  titles.append(title, sub);
  head.append(titles, close);
  const body = h('div', { class: 'wo-body' });
  const panel = h('div', { class: 'wo-panel lift-panel' });
  panel.append(head, body);
  win = openDialog({ cls: 'wo-overlay', labelledBy: 'lift-title', content: [panel], returnFocus });
  close.onclick = () => win.close();

  const draw = () => {
    const r = liftBoard(state.lifts, state.today).find((x) => x.key === key);
    if (!r) return win.close(); // its last entry was deleted
    title.replaceChildren(emoji('🏋️', 'lift-emoji'), rowName(r));
    sub.textContent = t('Best {value} on {date}', { value: fmtKg(r.best.kg), date: dfLong.format(parse(r.best.date)) });
    body.replaceChildren(...[percentSection(r), historySection(r, draw)].filter(Boolean));
  };
  draw();
}

// The weights at a share of the one-rep max, estimated from a rep max.
function percentSection(r) {
  const max = oneRepMax(r.best.kg, r.reps);
  const sec = h('section', { class: 'wo-sec' });
  const hd = h('div', { class: 'wo-sec-head' });
  hd.append(h('h3', {}, t('Training weights')), h('span', { class: 'muted' }, r.reps > 1
    ? t('from an estimated 1 RM of {value}', { value: fmtKg(loadable(max)) })
    : t('share of your 1 RM')));
  const grid = h('div', { class: 'lift-pct' });
  for (const p of percentages(max)) {
    const cell = h('div', { class: 'lift-pct-cell' });
    cell.append(h('span', { class: 'k' }, `${p.pct} %`), h('span', { class: 'v' }, fmtKg(p.kg)));
    grid.append(cell);
  }
  sec.append(hd, grid);
  return sec;
}

// Every result, the newest first, each with the change to the best before.
function historySection(r, redraw) {
  const sec = h('section', { class: 'wo-sec' });
  const hd = h('div', { class: 'wo-sec-head' });
  hd.append(h('h3', {}, t('History')));
  if (mayEditLifts()) {
    const add = h('button', { class: 'pill-btn', type: 'button' }, t('Add result'));
    add.onclick = () => openLiftForm({ preset: { lift: r.lift, reps: r.reps }, returnFocus: add, after: redraw });
    hd.append(add);
  }
  sec.append(hd);
  let best = 0;
  const marks = r.entries.map((e) => {
    const gain = best ? e.kg - best : null;
    const pb = e.kg > best;
    if (pb) best = e.kg;
    return { e, pb, gain };
  });
  for (const { e, pb, gain } of marks.reverse()) {
    const row = h('div', { class: 'day-item lift-entry' });
    const name = h('span', { class: 'name' });
    name.append(h('span', { class: 'txt' }, dfLong.format(parse(e.date))));
    if (pb) name.append(h('span', { class: 'lift-new pb' }, 'PB'));
    const note = [gain != null && pb ? gainKg(gain) : '', e.note].filter(Boolean).join(' · ');
    row.append(name, h('span', { class: 'amt' }, note), h('span', { class: 'val' }, fmtKg(e.kg)));
    if (mayEditLifts()) {
      const edit = h('button', { class: 'icon-btn lift-edit', type: 'button', 'aria-label': t('Change the result of {date}', { date: dfLong.format(parse(e.date)) }), title: t('Edit') }, '✎');
      edit.onclick = () => openLiftForm({ entry: e, returnFocus: edit, after: redraw });
      row.append(edit);
    }
    sec.append(row);
  }
  return sec;
}

/* ---------- the form ---------- */

const ERRORS = {
  bad_lift: () => t('Check the entry: a name, a weight up to 500 kg, 1 to 20 reps and a date that has passed.'),
  not_found: () => t('This result is no longer there.'),
};

async function refused(res) {
  if (!res) return t('Server not reachable.');
  if (res.status === 401) return t('You are signed out. Reload the page.');
  return ERRORS[await reason(res)]?.() || t('That didn’t work. Try again.');
}

// The form for a new result (preset: lift and reps to start with) or for
// changing one (entry). after runs once the lifts changed.
export function openLiftForm({ entry, preset, returnFocus, after }) {
  const close = h('button', { class: 'sheet-close', type: 'button', 'aria-label': t('Close') }, '×');
  const head = h('div', { class: 'sheet-head wo-head' });
  const titles = h('div', { class: 'wo-titles' });
  titles.append(h('h2', { id: 'lift-form-title' }, entry ? t('Change result') : t('New result')));
  head.append(titles, close);

  const form = h('form', { class: 'wo-body lift-form' });
  const listId = 'lift-names';
  const names = [...new Set([...state.lifts.map((e) => e.lift), ...COMMON_LIFTS])].sort((a, b) => a.localeCompare(b));
  const datalist = h('datalist', { id: listId });
  datalist.append(...names.map((n) => h('option', { value: n })));
  const field = (label, input, hint) => {
    const wrap = h('label', { class: 'lift-field' });
    wrap.append(h('span', { class: 'lbl' }, label), input);
    if (hint) wrap.append(h('span', { class: 'muted hint' }, hint));
    form.append(wrap);
    return input;
  };
  const v = entry || { lift: preset?.lift || '', kg: '', reps: preset?.reps || 1, date: state.today || '', note: '' };
  const lift = field(t('Lift'), h('input', { class: 'field', type: 'text', list: listId, required: '', maxlength: '48', autocomplete: 'off', value: v.lift, placeholder: 'Power Clean' }));
  const kg = field(t('Weight (kg)'), h('input', { class: 'field', type: 'number', required: '', min: '0.5', max: '500', step: '0.25', inputmode: 'decimal', value: v.kg }));
  const reps = field(t('Reps'), h('input', { class: 'field', type: 'number', required: '', min: '1', max: '20', step: '1', inputmode: 'numeric', value: v.reps }), t('1 for a one-rep max'));
  const date = field(t('Date'), h('input', { class: 'field', type: 'date', required: '', value: v.date, max: state.today || '' }));
  const note = field(t('Note'), h('input', { class: 'field', type: 'text', maxlength: '200', value: v.note || '', placeholder: t('optional') }));
  form.append(datalist);

  const error = h('p', { class: 'lock-error', role: 'alert', hidden: '' });
  const save = h('button', { class: 'pill-btn', type: 'submit' }, t('Save'));
  const cancel = h('button', { class: 'pill-btn', type: 'button' }, t('Cancel'));
  const row = h('div', { class: 'sec-row lift-actions' });
  row.append(save, cancel);
  let del = null;
  if (entry) {
    del = h('button', { class: 'pill-btn danger', type: 'button' }, t('Delete'));
    row.append(del);
  }
  form.append(error, row);

  const panel = h('div', { class: 'wo-panel lift-form-panel' });
  panel.append(head, form);
  const dlg = openDialog({ cls: 'wo-overlay', labelledBy: 'lift-form-title', content: [panel], returnFocus });
  close.onclick = cancel.onclick = () => dlg.close();
  (entry ? kg : lift.value ? kg : lift).focus();

  const send = async (button, run, update) => {
    for (const b of [save, cancel, del]) if (b) b.disabled = true;
    error.hidden = true;
    const res = await run().catch(() => null);
    // A result deleted elsewhere is gone either way.
    if (res?.ok || (res?.status === 404 && button === del)) {
      await update(res);
      dlg.close();
      renderLifts();
      after?.();
      return;
    }
    error.textContent = await refused(res);
    error.hidden = false;
    for (const b of [save, cancel, del]) if (b) b.disabled = false;
    button.focus();
  };

  form.onsubmit = (e) => {
    e.preventDefault();
    const body = { lift: lift.value, kg: Number(kg.value), reps: Number(reps.value) || 1, date: date.value, note: note.value };
    send(save,
      () => (entry ? api('lifts/' + entry.id, 'PUT', body) : api('lifts', 'POST', body)),
      async (res) => {
        const saved = await res.json();
        state.lifts = entry ? state.lifts.map((x) => (x.id === saved.id ? saved : x)) : [...state.lifts, saved];
      });
  };
  if (del) {
    del.onclick = () => {
      if (!confirm(t('Delete this result?'))) return;
      send(del, () => api('lifts/' + entry.id, 'DELETE'), () => {
        state.lifts = state.lifts.filter((x) => x.id !== entry.id);
      });
    };
  }
}
