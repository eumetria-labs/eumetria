// Training sessions, set by set (the server's sessions.go): optional, next
// to the personal bests and benchmark workouts, which stay as they are. The
// Training card's Sessions tab lists them by week with the volume per lift;
// the form (quick log "+ Session") takes exercises with sets of reps × kg,
// and offers a work set that beats a lift's best as a new best, ticked.
// view.health reads them, edit.lifts writes them.

import { actLabel, dfLong, dfShort, fmt, parse } from './format.js';
import { $, emoji, h } from './dom.js';
import { openDialog } from './dialog.js';
import { api, reason } from './account.js';
import { state } from './state.js';
import { COMMON_LIFTS, bestCandidates, linkableWorkouts, liftKey, sessionVolumes, sessionWeeks, workoutName } from './health.js';
import { fmtKg, fmtSet, loadLifts, mayEditLifts, notifyBests, openLift } from './lifts.js';
import { openDay } from './sheet.js';
import { t, tn } from './i18n.js';

// loadSessions reads the sessions that go with data. Plain files (no
// server) read sessions.json, which -demo writes.
export async function loadSessions(data) {
  if (data.scopes && !data.scopes.includes('view.health')) {
    state.sessions = [];
    return;
  }
  let res = await api('training/sessions').catch(() => null);
  if (res?.status === 404) res = await fetch('sessions.json', { cache: 'no-cache' }).catch(() => null);
  if (res?.status === 404) {
    state.sessions = [];
    state.sessionsLoaded = true;
    return;
  }
  if (!res?.ok) {
    console.error('loading the sessions failed', res?.status);
    return;
  }
  state.sessions = await res.json();
  state.sessionsLoaded = true;
}

export const hasSessions = () => state.sessions.length > 0 || (mayEditLifts() && !!state.sessionsLoaded);

// fmtSets writes work sets compactly: runs of the same reps and weight as
// "5×5 @ 100 kg", single sets as "3 @ 105 kg", bodyweight as "BW".
export function fmtSets(sets) {
  const runs = [];
  for (const s of sets) {
    const last = runs.at(-1);
    if (last && last.reps === s.reps && last.kg === s.kg) last.n++;
    else runs.push({ reps: s.reps, kg: s.kg, n: 1 });
  }
  return runs.map((r) => `${r.n > 1 ? `${r.n}×${r.reps}` : r.reps} @ ${r.kg ? fmtKg(r.kg) : t('BW|bodyweight')}`).join(', ');
}

const volumeText = (v) => (v.kg ? `${fmt(v.kg)} kg` : tn(v.reps, '{n} rep', '{n} reps'));

// The lifts of a session in a line: "Back Squat 5×5 @ 100 kg · Pull-up …".
const sessionLine = (x) => [...new Set(x.sets.map((s) => s.lift))]
  .map((lift) => `${lift} ${fmtSets(x.sets.filter((s) => s.lift === lift && s.type !== 'warmup'))}`).join(' · ');

/* ---------- the Training card's Sessions tab ---------- */

let shownWeeks = 6;

export function renderSessions() {
  const host = $('#train-sessions');
  if (!host) return;
  const add = $('#session-add');
  add.hidden = !mayEditLifts();
  add.onclick = () => openSessionForm({ returnFocus: add });
  const weeks = sessionWeeks(state.sessions);
  $('#sessions-sub').textContent = weeks.length ? tn(state.sessions.length, '{n} session', '{n} sessions') : '';
  if (!weeks.length) {
    host.replaceChildren(h('p', { class: 'empty-tab' }, !state.sessionsLoaded ? t('The sessions could not be loaded.')
      : mayEditLifts() ? t('No sessions yet. Log one with + Session: sets of reps × kg, a new best offered as you go.') : t('No sessions noted yet.')));
    return;
  }
  const parts = weeks.slice(0, shownWeeks).map((w) => {
    const sec = h('section', { class: 'ses-week' });
    const hd = h('div', { class: 'ses-week-head' });
    hd.append(h('h4', {}, t('Week of {date}', { date: dfShort.format(parse(w.monday)) })),
      h('span', { class: 'muted' }, [tn(w.sessions.length, '{n} session', '{n} sessions'), tn(w.sets, '{n} work set', '{n} work sets')].join(' · ')));
    // Volume per lift: work sets and kg × reps, warm-ups left out.
    const vol = h('div', { class: 'ses-volume' });
    const cap = h('div', { class: 'ses-vol-row ses-vol-cap' });
    cap.append(h('span', {}, t('Work sets')), h('span', {}), h('span', {}, t('Volume (kg × reps)')));
    vol.append(cap);
    for (const [lift, v] of [...w.volumes].sort((a, b) => b[1].kg - a[1].kg || b[1].sets - a[1].sets)) {
      const row = h('div', { class: 'ses-vol-row' });
      row.append(h('span', { class: 'name' }, lift), h('span', { class: 'muted' }, tn(v.sets, '{n} set', '{n} sets')), h('b', {}, volumeText(v)));
      vol.append(row);
    }
    sec.append(hd, vol);
    for (const x of [...w.sessions].sort((a, b) => b.date.localeCompare(a.date) || b.id - a.id)) {
      const row = h('button', { class: 'day-item workout-row lift-row ses-row', type: 'button' });
      const name = h('span', { class: 'name' });
      name.append(emoji('🏋️'), x.title || dfLong.format(parse(x.date)));
      row.append(name, h('span', { class: 'amt' }, sessionLine(x)), h('span', { class: 'val' }, dfShort.format(parse(x.date))), h('span', { class: 'chev', 'aria-hidden': 'true' }, '›'));
      row.setAttribute('aria-label', `${x.title || ''} ${dfLong.format(parse(x.date))}: ${sessionLine(x)}`);
      row.onclick = () => (mayEditLifts() ? openSessionForm({ session: x, returnFocus: row }) : openDay(x.date));
      sec.append(row);
    }
    return sec;
  });
  if (weeks.length > shownWeeks) {
    const more = h('button', { class: 'pill-btn ses-more', type: 'button' }, t('Show older weeks'));
    more.onclick = () => { shownWeeks += 8; renderSessions(); };
    parts.push(more);
  }
  host.replaceChildren(...parts);
}

/* ---------- in other windows ---------- */

// liftSessions is the lift window's part: the last sessions with the lift,
// their work sets. Null without any.
export function liftSessions(key) {
  const mine = state.sessions.filter((x) => x.sets.some((s) => liftKey(s) === key)).sort((a, b) => b.date.localeCompare(a.date)).slice(0, 6);
  if (!mine.length) return null;
  const sec = h('section', { class: 'wo-sec' });
  const hd = h('div', { class: 'wo-sec-head' });
  hd.append(h('h3', {}, t('Last sessions')), h('span', { class: 'muted' }, t('work sets')));
  sec.append(hd);
  for (const x of mine) {
    const sets = x.sets.filter((s) => liftKey(s) === key && s.type !== 'warmup');
    const v = sessionVolumes({ sets }).values().next().value;
    const row = h('div', { class: 'day-item ses-lift-row' });
    row.append(h('span', { class: 'name' }, dfShort.format(parse(x.date))), h('span', { class: 'amt' }, fmtSets(sets)), h('span', { class: 'val' }, v ? volumeText(v) : ''));
    sec.append(row);
  }
  return sec;
}

// workoutSessions is the workout window's part: the sessions linked to the
// Fitbit workout fw, their lifts set by set. Null without any.
export function workoutSessions(fw) {
  const mine = state.sessions.filter((x) => x.fitbit_workout === fw.id);
  if (!mine.length) return null;
  const sec = h('section', { class: 'wo-sec' });
  for (const x of mine) {
    const hd = h('div', { class: 'wo-sec-head' });
    hd.append(h('h3', {}, x.title || t('Session')), h('span', { class: 'muted' }, tn(sessionVolumes(x).size, '{n} lift', '{n} lifts')));
    if (mayEditLifts()) {
      const edit = h('button', { class: 'pill-btn', type: 'button' }, t('Edit'));
      edit.onclick = () => openSessionForm({ session: x, returnFocus: edit });
      hd.append(edit);
    }
    sec.append(hd);
    for (const lift of [...new Set(x.sets.map((s) => s.lift))]) {
      const sets = x.sets.filter((s) => s.lift === lift);
      const work = sets.filter((s) => s.type !== 'warmup');
      const row = h('div', { class: 'day-item' });
      const name = h('span', { class: 'name' });
      const open = h('button', { class: 'link-btn', type: 'button' }, lift);
      open.onclick = () => openLift(lift.toLowerCase(), open);
      name.append(emoji('🏋️', 'lift-emoji'), open);
      row.append(name, h('span', { class: 'amt' }, fmtSets(work)), h('span', { class: 'val' }, sets.length > work.length ? tn(sets.length - work.length, '+{n} warm-up', '+{n} warm-ups') : ''));
      sec.append(row);
    }
    if (x.note) sec.append(h('p', { class: 'muted' }, x.note));
  }
  return sec;
}

/* ---------- the form ---------- */

let win = null;

// openSessionForm opens a new session (for date, linked to the Fitbit
// workout link when given) or session to change.
export function openSessionForm({ session, date, link, returnFocus } = {}) {
  win?.close(true);
  const close = h('button', { class: 'sheet-close', type: 'button', 'aria-label': t('Close') }, '×');
  const head = h('div', { class: 'sheet-head wo-head' });
  const titles = h('div', { class: 'wo-titles' });
  titles.append(h('h2', { id: 'session-title' }, session ? t('Change session') : t('New session')));
  head.append(titles, close);
  const form = h('form', { class: 'wo-body lift-form session-form' });
  form.noValidate = true;
  const field = (label, input, hint) => {
    const wrap = h('label', { class: 'lift-field' });
    wrap.append(h('span', { class: 'lbl' }, label), input);
    if (hint) wrap.append(h('span', { class: 'muted hint' }, hint));
    return wrap;
  };
  const v = session || { date: link?.date || date || state.today || '', title: '', note: '', rpe: null, sets: [], fitbit_workout: link?.id || '' };

  const top = h('div', { class: 'ses-top' });
  const dateIn = h('input', { class: 'field', type: 'date', required: '', value: v.date, max: state.today || '' });
  const title = h('input', { class: 'field', type: 'text', maxlength: '48', value: v.title || '', placeholder: t('optional') });
  const rpe = h('select', { class: 'field' });
  rpe.append(h('option', { value: '' }, '–'), ...[...Array(10)].map((_, i) => h('option', { value: String(i + 1) }, String(i + 1))));
  rpe.value = v.rpe ? String(v.rpe) : '';
  top.append(field(t('Date'), dateIn), field(t('Name'), title), field(t('Effort (RPE)'), rpe, t('1 easy … 10 all out')));
  form.append(top);

  // The Fitbit workout of the day: ticked when the day has just one.
  const linkWrap = h('label', { class: 'lift-check' });
  const linked = h('input', { type: 'checkbox' });
  const linkText = h('span', {});
  linkWrap.append(linked, linkText);
  form.append(linkWrap);
  let linkTo = null;
  const offer = (keep) => {
    const all = dateIn.value ? linkableWorkouts(state.byDate?.get(dateIn.value)) : [];
    const w = all.find((x) => x.id === v.fitbit_workout) || all.reduce((a, x) => (!a || (x.dur || 0) > (a.dur || 0) ? x : a), null);
    linkTo = w && { id: w.id, label: actLabel(workoutName(w.type)) };
    linked.checked = keep ? !!v.fitbit_workout && linkTo?.id === v.fitbit_workout : all.length === 1;
    linkWrap.hidden = !linkTo;
    if (linkTo) linkText.textContent = t('Done in the {name} of that day', { name: linkTo.label });
  };
  dateIn.addEventListener('change', () => offer(false));
  offer(!!session || !!link);

  // Exercises: a lift and its sets, in order.
  const listId = 'session-lift-names';
  const names = [...new Set([...state.lifts.map((e) => e.lift), ...state.sessions.flatMap((x) => x.sets.map((s) => s.lift)), ...COMMON_LIFTS])].sort((a, b) => a.localeCompare(b));
  const datalist = h('datalist', { id: listId });
  datalist.append(...names.map((n) => h('option', { value: n })));
  const exercises = h('div', { class: 'ses-exercises' });
  const addEx = h('button', { class: 'pill-btn', type: 'button' }, '＋ ' + t('Exercise'));
  form.append(exercises, addEx, datalist);

  const bestBox = h('div', { class: 'ses-bests', hidden: '' });
  const note = h('textarea', { class: 'field', rows: '2', maxlength: '500', placeholder: t('optional') });
  note.value = v.note || '';
  form.append(bestBox, field(t('Note'), note));

  const exs = []; // { lift input, rows: [{ type, reps, kg, el }] }
  function addSetRow(ex, s) {
    const row = h('div', { class: 'ses-set' });
    const type = h('button', { type: 'button', class: 'ses-type', 'aria-pressed': String(s.type === 'warmup') }, t('W|warm-up'));
    type.title = t('Warm-up set: not counted in the volume, never a best');
    type.onclick = () => { type.setAttribute('aria-pressed', String(type.getAttribute('aria-pressed') !== 'true')); changed(); };
    const reps = h('input', { class: 'field', type: 'number', min: '1', max: '100', step: '1', inputmode: 'numeric', value: s.reps ?? '', 'aria-label': t('Reps') });
    const kg = h('input', { class: 'field', type: 'number', min: '0', max: '500', step: '0.25', inputmode: 'decimal', value: s.kg ?? '', 'aria-label': t('Weight (kg)'), placeholder: t('BW|bodyweight') });
    const del = h('button', { type: 'button', class: 'icon-btn', 'aria-label': t('Remove the set') }, '×');
    const item = { row, type, reps, kg };
    del.onclick = () => { ex.sets.splice(ex.sets.indexOf(item), 1); row.remove(); number(ex); changed(); };
    reps.oninput = kg.oninput = changed;
    row.append(h('span', { class: 'ses-n muted' }), type, reps, h('span', { class: 'muted' }, '×'), kg, h('span', { class: 'muted' }, 'kg'), del);
    ex.list.append(row);
    ex.sets.push(item);
    number(ex);
  }
  const number = (ex) => ex.sets.forEach((s, i) => { s.row.firstChild.textContent = String(i + 1); });
  const read = (s) => ({ type: s.type.getAttribute('aria-pressed') === 'true' ? 'warmup' : 'work', reps: Number(s.reps.value) || 0, kg: Number(s.kg.value) || 0 });

  function addExercise(lift = '', sets = []) {
    const box = h('div', { class: 'ses-ex' });
    const name = h('input', { class: 'field ses-lift', type: 'text', list: listId, maxlength: '48', autocomplete: 'off', value: lift, placeholder: 'Back Squat', 'aria-label': t('Lift') });
    const repeat = h('button', { class: 'pill-btn small', type: 'button' }, t('Repeat last time'));
    const remove = h('button', { class: 'icon-btn', type: 'button', 'aria-label': t('Remove the exercise') }, '×');
    const hd = h('div', { class: 'ses-ex-head' });
    hd.append(name, repeat, remove);
    const list = h('div', { class: 'ses-sets' });
    const addSet = h('button', { class: 'pill-btn small', type: 'button' }, '＋ ' + t('Set'));
    box.append(hd, list, addSet);
    const ex = { box, name, list, sets: [] };
    exs.push(ex);
    exercises.append(box);
    // The last session with this lift, before this one.
    const previous = () => {
      const key = name.value.trim().toLowerCase();
      return state.sessions.filter((x) => x.id !== session?.id && x.date <= (dateIn.value || '9') && x.sets.some((s) => liftKey(s) === key))
        .sort((a, b) => b.date.localeCompare(a.date) || b.id - a.id)[0];
    };
    const showRepeat = () => { repeat.hidden = !name.value.trim() || !previous(); };
    name.oninput = () => { showRepeat(); changed(); };
    repeat.onclick = () => {
      const p = previous();
      if (!p) return;
      for (const s of [...ex.sets]) s.row.remove();
      ex.sets.length = 0;
      for (const s of p.sets.filter((x) => liftKey(x) === name.value.trim().toLowerCase())) addSetRow(ex, s);
      changed();
    };
    // "+ Set" copies the last one.
    addSet.onclick = () => {
      const last = ex.sets.at(-1);
      addSetRow(ex, last ? read(last) : { type: 'work', reps: 5, kg: '' });
      ex.sets.at(-1).reps.focus();
      changed();
    };
    remove.onclick = () => { exs.splice(exs.indexOf(ex), 1); box.remove(); changed(); };
    for (const s of sets) addSetRow(ex, s);
    if (!sets.length) addSetRow(ex, { type: 'work', reps: 5, kg: '' });
    showRepeat();
    return ex;
  }
  addEx.onclick = () => { addExercise().name.focus(); };
  // The session's sets in order, grouped by exercise as entered.
  const groups = [];
  for (const s of v.sets) {
    if (groups.at(-1)?.lift === s.lift) groups.at(-1).sets.push(s);
    else groups.push({ lift: s.lift, sets: [s] });
  }
  for (const g of groups) addExercise(g.lift, g.sets);
  if (!groups.length) addExercise();

  const flat = () => exs.flatMap((ex) => ex.sets.map((s) => ({ lift: ex.name.value.trim(), ...read(s) })));
  // A work set that beats a lift's best: offered as a new best, ticked.
  let ticks = new Map();
  function changed() {
    const sets = flat();
    const cands = bestCandidates(sets, state.lifts);
    bestBox.hidden = !cands.length;
    const before = ticks;
    ticks = new Map();
    bestBox.replaceChildren(...(cands.length ? [h('p', { class: 'lbl' }, t('New personal best'))] : []), ...cands.map((c) => {
      const key = `${c.set.lift.toLowerCase()}|${c.set.kg}|${c.set.reps}`;
      const box = h('input', { type: 'checkbox' });
      box.checked = before.has(key) ? before.get(key).box.checked : true;
      ticks.set(key, { box, index: c.index });
      const wrap = h('label', { class: 'lift-check ses-best' });
      wrap.append(box, h('span', {}, t('Save as PB? {lift} {set}', { lift: c.set.lift, set: fmtSet(c.set) })),
        h('span', { class: 'muted hint' }, c.prev ? t('was {set}', { set: fmtSet(c.prev) }) : t('the first for this lift')));
      return wrap;
    }));
  }
  changed();

  const error = h('p', { class: 'lock-error', role: 'alert', hidden: '' });
  const save = h('button', { class: 'pill-btn', type: 'submit' }, t('Save'));
  const cancel = h('button', { class: 'pill-btn', type: 'button' }, t('Cancel'));
  const row = h('div', { class: 'sec-row lift-actions' });
  row.append(save, cancel);
  let del = null;
  if (session) {
    del = h('button', { class: 'pill-btn danger', type: 'button' }, t('Delete'));
    row.append(del);
  }
  form.append(error, row);

  const panel = h('div', { class: 'wo-panel lift-form-panel session-panel' });
  panel.append(head, form);
  win = openDialog({ cls: 'wo-overlay', labelledBy: 'session-title', content: [panel], returnFocus, onClose: () => { win = null; } });
  close.onclick = cancel.onclick = () => win?.close();
  (exs[0].name.value ? exs[0].sets[0]?.reps : exs[0].name).focus();

  const send = async (button, run, update) => {
    for (const b of [save, cancel, del]) if (b) b.disabled = true;
    error.hidden = true;
    const res = await run().catch(() => null);
    if (res?.ok || (res?.status === 404 && button === del)) {
      await update(res);
      win?.close();
      // New bests, the calendar, the windows: everything that shows training.
      notifyBests();
      document.dispatchEvent(new Event('phases-change'));
      return;
    }
    const why = res ? await reason(res) : '';
    error.textContent = why === 'bad_session' ? t('Check the sets: each needs a lift, 1 to 100 reps and 0 to 500 kg.')
      : why === 'second_factor_required' ? t('Set up a second factor under Settings → Security first.') : t('That didn’t work. Try again.');
    error.hidden = false;
    for (const b of [save, cancel, del]) if (b) b.disabled = false;
    button.focus();
  };
  form.onsubmit = (e) => {
    e.preventDefault();
    const sets = flat().filter((s) => s.lift && s.reps > 0);
    if (!sets.length) {
      error.textContent = t('Add at least one set with a lift and reps.');
      error.hidden = false;
      return;
    }
    // The ticked bests, as indexes into the sets sent (empty rows left out).
    const all = flat();
    const kept = all.map((s, i) => (s.lift && s.reps > 0 ? i : -1));
    const bests = [...ticks.values()].filter((x) => x.box.checked).map((x) => kept.filter((i) => i >= 0).indexOf(x.index)).filter((i) => i >= 0);
    const body = { date: dateIn.value, fitbit_workout: linked.checked && linkTo ? linkTo.id : '', title: title.value, note: note.value, rpe: rpe.value ? Number(rpe.value) : null, sets, bests };
    send(save, () => (session ? api('training/sessions/' + session.id, 'PUT', body) : api('training/sessions', 'POST', body)), async (res) => {
      const saved = await res.json();
      state.sessions = session ? state.sessions.map((x) => (x.id === saved.id ? saved : x)) : [...state.sessions, saved];
      if (bests.length) await loadLifts(state.data);
    });
  };
  if (del) {
    del.onclick = () => {
      if (!confirm(t('Delete this session? Bests saved from it stay.'))) return;
      send(del, () => api('training/sessions/' + session.id, 'DELETE'), () => {
        state.sessions = state.sessions.filter((x) => x.id !== session.id);
      });
    };
  }
}
