// Trainings (the server's sessions.go, PLAN phase 14): the hub of what was
// done in a workout. A training wraps the device workouts it was recorded
// in ({source, id}), and its sets, note (notes.js), lifts (lifts.js) and
// benchmark results (wods.js) belong to it (their session_id).
// state.trainings holds them all; state.sessions only those with sets, which
// the rest of the page counts (volume, strength over time). The Training
// form ("+ Log → Training", the workout window) takes the workouts, sets of
// reps × kg (a work set that beats a lift's best offered as a new best,
// ticked), a benchmark result and the note. view.health reads them,
// edit.lifts writes them.

import { actLabel, dfShort, fmt, fmtDuration, parse } from './format.js';
import { emoji, h } from './dom.js';
import { openDialog } from './dialog.js';
import { api, reason } from './account.js';
import { can, state } from './state.js';
import { COMMON_LIFTS, EVERYDAY, bestCandidates, liftKey, sessionVolumes, unclaimedWorkouts, workoutIcon, workoutName } from './health.js';
import { fmtKg, fmtSet, loadLifts, mayEditLifts, notifyBests, openLift, openLiftForm } from './lifts.js';
import { fmtResult, openResultForm, openWorkoutForm, renderWods, resultInputs } from './wods.js';
import { mayEditNotes, noteInput, saveNote } from './notes.js';
import { t, tn } from './i18n.js';

// loadSessions reads the sessions that go with data. Plain files (no
// server) read sessions.json, which -demo writes.
export async function loadSessions(data) {
  if (data.scopes && !data.scopes.includes('view.health')) {
    setTrainings([]);
    return;
  }
  let res = await api('training/sessions').catch(() => null);
  if (res?.status === 404) res = await fetch('sessions.json', { cache: 'no-cache' }).catch(() => null);
  if (res?.status === 404) {
    setTrainings([]);
    state.sessionsLoaded = true;
    return;
  }
  if (!res?.ok) {
    console.error('loading the sessions failed', res?.status);
    return;
  }
  setTrainings(await res.json());
  state.sessionsLoaded = true;
}

// setTrainings keeps the trainings, and the sessions among them.
export function setTrainings(list) {
  state.trainings = list;
  state.sessions = list.filter((x) => x.sets.length > 0);
  // "Not logged yet" counts again (unlogged.js).
  document.dispatchEvent(new Event('trainings-change'));
}

// trainingOf is the training of the Fitbit workout id, or null.
export const trainingOf = (id) => state.trainings.find((x) => x.workouts.some((r) => r.source === 'fitbit' && r.id === id)) || null;

// inWorkout reports whether a lift, benchmark result or note belongs to the
// Fitbit workout id: to its training, or (before the trainings are loaded
// again) to it as its first workout.
export const inWorkout = (item, id) => (item.session_id != null && item.session_id === trainingOf(id)?.id) || item.fitbit_workout === id;

// knowTraining loads the trainings again when the server put something in
// one the page doesn't know yet (a workout's first lift, result or note).
export async function knowTraining(id) {
  if (id != null && !state.trainings.some((x) => x.id === id)) await loadSessions(state.data);
}

// The training's own Fitbit workouts, without the one given.
const otherWorkouts = (x, id) => (x?.workouts || []).filter((r) => !(r.source === 'fitbit' && r.id === id));

// Whether there are trainings, which keep the Training card on show.
export const hasTrainings = () => state.trainings.length > 0;

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

// workoutSessions is the workout window's part: the sets of the training
// of the Fitbit workout fw, lift by lift. Null without any.
export function workoutSessions(fw) {
  const x = trainingOf(fw.id);
  if (!x?.sets.length) return null;
  const sec = h('section', { class: 'wo-sec' });
  const hd = h('div', { class: 'wo-sec-head' });
  hd.append(h('h3', {}, t('Sets')), h('span', { class: 'muted' }, tn(sessionVolumes(x).size, '{n} lift', '{n} lifts')));
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
  return sec;
}

// A device workout as a chip or list item says it: "Weights · 18:10 · 52 min".
export const workoutLine = (w) => [actLabel(workoutName(w.type)), w.start, w.dur ? fmtDuration(w.dur / 60) : ''].filter(Boolean).join(' · ');

// The Fitbit workout of a ref, from the day's data.
const deviceWorkout = (date, r) => (r.source === 'fitbit' ? state.byDate?.get(date)?.workouts?.find((w) => w.id === r.id) : null) || null;

// trainingSection heads the workout window's part about what was done: the
// training's name and effort, the other workouts it wraps, and for
// edit.lifts the button to the Training form (after runs once it is saved).
// Null when there is no training and nothing to add.
export function trainingSection(day, fw, after) {
  const x = trainingOf(fw.id);
  if (!x && !mayEditLifts()) return null;
  const sec = h('section', { class: 'wo-sec wo-training' });
  const hd = h('div', { class: 'wo-sec-head' });
  hd.append(h('h3', {}, t('Training')));
  const sub = [x?.title, x?.rpe ? t('effort {n}/10', { n: x.rpe }) : ''].filter(Boolean).join(' · ');
  if (sub) hd.append(h('span', { class: 'muted' }, sub));
  if (mayEditLifts()) {
    const edit = h('button', { class: 'pill-btn', type: 'button' }, x ? t('Edit training') : t('Log training'));
    edit.onclick = () => openTrainingForm({ training: x || undefined, link: x ? undefined : { id: fw.id, date: day.date }, returnFocus: edit, after });
    hd.append(edit);
  }
  sec.append(hd);
  const others = otherWorkouts(x, fw.id).map((r) => deviceWorkout(x.date, r)).filter(Boolean);
  if (others.length) sec.append(h('p', { class: 'muted wo-merged' }, t('Part of a training with {workouts}', { workouts: others.map((w) => `${actLabel(workoutName(w.type))} ${w.start}`).join(', ') })));
  if (!x) sec.append(h('p', { class: 'muted wod-hint' }, t('Log what you did here: sets, a benchmark result, a note.')));
  else if (!x.sets.length && !state.notes?.has(x.id) && !state.lifts.some((e) => e.session_id === x.id) && !state.wods.results.some((r) => r.session_id === x.id)) {
    sec.append(h('p', { class: 'muted wod-hint' }, t('Reviewed: nothing to add.')));
  }
  return sec;
}

/* ---------- the Training form ---------- */

let win = null;

// openTrainingForm opens a training to change, or a new one: for date, or
// wrapping the Fitbit workout link ({ id, date }). A workout with a training
// already opens that one. strength adds an empty exercise to start with;
// photos (Files, shared to the app) go into the note at once; after runs
// once it is saved or deleted.
export function openTrainingForm({ training, date, link, strength, photos, returnFocus, after } = {}) {
  win?.close(true);
  if (!training && link) training = trainingOf(link.id) || undefined;
  let saved = training || null; // what the server has, once saved
  let bestsSaved = false;
  const close = h('button', { class: 'sheet-close', type: 'button', 'aria-label': t('Close') }, '×');
  const head = h('div', { class: 'sheet-head wo-head' });
  const titles = h('div', { class: 'wo-titles' });
  titles.append(h('h2', { id: 'session-title' }, training ? t('Training') : t('New training')));
  head.append(titles, close);
  const form = h('form', { class: 'wo-body lift-form session-form' });
  form.noValidate = true;
  const field = (label, input, hint) => {
    const wrap = h('label', { class: 'lift-field' });
    wrap.append(h('span', { class: 'lbl' }, label), input);
    if (hint) wrap.append(h('span', { class: 'muted hint' }, hint));
    return wrap;
  };
  const section = (name, open, ...parts) => {
    const d = h('details', { class: 'ses-part' });
    d.open = open;
    d.append(h('summary', {}, name), ...parts);
    return d;
  };
  const v = training || { date: link?.date || date || state.today || '', title: '', rpe: null, sets: [], workouts: [] };

  const top = h('div', { class: 'ses-top' });
  const dateIn = h('input', { class: 'field', type: 'date', required: '', value: v.date, max: state.today || '' });
  const title = h('input', { class: 'field', type: 'text', maxlength: '48', value: v.title || '', placeholder: t('optional') });
  const rpe = h('select', { class: 'field' });
  rpe.append(h('option', { value: '' }, '–'), ...[...Array(10)].map((_, i) => h('option', { value: String(i + 1) }, String(i + 1))));
  rpe.value = v.rpe ? String(v.rpe) : '';
  top.append(field(t('Date'), dateIn), field(t('Name'), title), field(t('Effort (RPE)'), rpe, t('1 easy … 10 all out')));
  form.append(top);

  /* The device workouts it wraps: chips, and the day's unclaimed ones to add. */
  let refs = [...v.workouts];
  if (!training && link) refs = [{ source: 'fitbit', id: link.id }];
  else if (!training) {
    const free = unclaimedWorkouts(state.byDate?.get(v.date), state.trainings);
    if (free.length === 1) refs = [{ source: 'fitbit', id: free[0].id }];
  }
  const chips = h('div', { class: 'ses-workouts', role: 'list', 'aria-label': t('Workouts') });
  const addWorkout = h('select', { class: 'field ses-add-workout', 'aria-label': t('Add a workout') });
  const drawWorkouts = () => {
    const mine = new Set(refs.map((r) => `${r.source}:${r.id}`));
    chips.replaceChildren(...refs.map((r) => {
      const w = deviceWorkout(dateIn.value, r);
      const chip = h('span', { class: 'ses-wchip', role: 'listitem' });
      chip.append(emoji(w ? workoutIcon(w.type) : '⌚'), h('span', {}, w ? workoutLine(w) : t('A workout no longer synced')));
      const x = h('button', { type: 'button', class: 'icon-btn', 'aria-label': t('Take {name} out of the training', { name: w ? actLabel(workoutName(w.type)) : t('the workout') }) }, '×');
      x.onclick = () => { refs = refs.filter((y) => y !== r); drawWorkouts(); };
      chip.append(x);
      return chip;
    }));
    // The day's workouts no other training wraps, walks too (the last).
    const others = state.trainings.filter((x) => x.id !== saved?.id);
    const free = unclaimedWorkouts(state.byDate?.get(dateIn.value), others, true).filter((w) => !mine.has(`fitbit:${w.id}`))
      .sort((a, b) => Number(EVERYDAY.has(workoutName(a.type))) - Number(EVERYDAY.has(workoutName(b.type))));
    addWorkout.replaceChildren(h('option', { value: '' }, refs.length ? t('＋ Add a workout of that day') : t('＋ Add the recorded workout')),
      ...free.map((w) => h('option', { value: w.id }, workoutLine(w))));
    addWorkout.hidden = !free.length;
    chips.hidden = !refs.length;
    // With workouts, the day is theirs.
    dateIn.disabled = refs.length > 0;
    noWorkout.hidden = refs.length > 0 || free.length > 0;
  };
  addWorkout.onchange = () => {
    if (addWorkout.value) refs.push({ source: 'fitbit', id: addWorkout.value });
    drawWorkouts();
  };
  const noWorkout = h('p', { class: 'muted hint ses-noworkout' }, t('No recorded workout that day.'));
  const wbox = h('div', { class: 'ses-wbox' });
  wbox.append(h('span', { class: 'lbl' }, t('Recorded workouts')), chips, addWorkout, noWorkout);
  form.append(wbox);
  dateIn.addEventListener('change', drawWorkouts);

  /* Strength: exercises with their sets; a work set that beats a best is offered as one. */
  const listId = 'session-lift-names';
  const names = [...new Set([...state.lifts.map((e) => e.lift), ...state.sessions.flatMap((x) => x.sets.map((s) => s.lift)), ...COMMON_LIFTS])].sort((a, b) => a.localeCompare(b));
  const datalist = h('datalist', { id: listId });
  datalist.append(...names.map((n) => h('option', { value: n })));
  const exercises = h('div', { class: 'ses-exercises' });
  const addEx = h('button', { class: 'pill-btn', type: 'button' }, '＋ ' + t('Exercise'));
  const bestBox = h('div', { class: 'ses-bests', hidden: '' });
  // Single results noted in it (the lift form's).
  const entries = h('div', { class: 'ses-entries' });
  const drawEntries = () => {
    const mine = saved ? state.lifts.filter((e) => e.session_id === saved.id).sort((a, b) => a.id - b.id) : [];
    entries.replaceChildren(...mine.map((e) => {
      const row = h('div', { class: 'day-item lift-entry' });
      const name = h('span', { class: 'name' });
      name.append(emoji('🏋️', 'lift-emoji'), e.lift);
      const edit = h('button', { class: 'icon-btn lift-edit', type: 'button', 'aria-label': t('Change the {name} result', { name: e.lift }), title: t('Edit') }, '✎');
      edit.onclick = () => openLiftForm({ entry: e, returnFocus: edit, after: drawEntries });
      row.append(name, h('span', { class: 'amt' }, e.note || ''), h('span', { class: 'val' }, fmtSet(e)), edit);
      return row;
    }));
    entries.hidden = !mine.length;
  };
  drawEntries();
  const strengthPart = section(t('Strength'), v.sets.length > 0 || !!strength, entries, exercises, addEx, datalist, bestBox);
  form.append(strengthPart);

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
      return state.sessions.filter((x) => x.id !== saved?.id && x.date <= (dateIn.value || '9') && x.sets.some((s) => liftKey(s) === key))
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
  // The training's sets in order, grouped by exercise as entered.
  const groups = [];
  for (const s of v.sets) {
    if (groups.at(-1)?.lift === s.lift) groups.at(-1).sets.push(s);
    else groups.push({ lift: s.lift, sets: [s] });
  }
  for (const g of groups) addExercise(g.lift, g.sets);
  if (!groups.length && strength) addExercise();

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

  /* Benchmark: the results noted in it, and one to add. */
  const results = h('div', { class: 'ses-entries' });
  const drawResults = () => {
    const byId = new Map(state.wods.workouts.map((w) => [w.id, w]));
    const mine = saved ? state.wods.results.filter((r) => r.session_id === saved.id && byId.has(r.workout)) : [];
    results.replaceChildren(...mine.map((r) => {
      const w = byId.get(r.workout);
      const row = h('div', { class: 'day-item lift-entry' });
      const name = h('span', { class: 'name' });
      name.append(emoji('⏱️', 'lift-emoji'), w.name);
      if (r.scaled) name.append(h('span', { class: 'lift-rm' }, t('Scaled')));
      const edit = h('button', { class: 'icon-btn lift-edit', type: 'button', 'aria-label': t('Change the {name} result', { name: w.name }), title: t('Edit') }, '✎');
      edit.onclick = () => openResultForm({ workout: w, result: r, returnFocus: edit, after: drawResults });
      row.append(name, h('span', { class: 'amt' }, r.note || ''), h('span', { class: 'val' }, fmtResult(w.scoring, r, true)), edit);
      return row;
    }));
    results.hidden = !mine.length;
    return mine.length;
  };
  const hadResults = drawResults();
  const pick = h('select', { class: 'field', 'aria-label': t('Benchmark workout') });
  const fillPick = (value = '') => {
    pick.replaceChildren(h('option', { value: '' }, t('Add a result: choose a workout…')),
      ...[...state.wods.workouts].sort((a, b) => a.name.localeCompare(b.name)).map((w) => h('option', { value: String(w.id) }, w.name)),
      h('option', { value: 'new' }, t('New benchmark workout…')));
    pick.value = value;
  };
  fillPick();
  const resultBox = h('div', { class: 'ses-result' });
  let result = null; // { workout, read, note } of the result being added
  const showResult = (w) => {
    result = null;
    resultBox.replaceChildren();
    if (!w) return;
    const fieldIn = (label, input, hint) => {
      resultBox.append(field(label, input, hint));
      return input;
    };
    const { first, read: readResult } = resultInputs(resultBox, fieldIn, w, { value: '', reps: 0, scaled: false });
    const note = fieldIn(t('Note on the result'), h('input', { class: 'field', type: 'text', maxlength: '200', placeholder: t('optional') }));
    result = { workout: w, read: readResult, note };
    first.focus();
  };
  pick.onchange = () => {
    if (pick.value === 'new') {
      fillPick();
      showResult(null);
      openWorkoutForm({ returnFocus: pick, onCreated: (w) => { fillPick(String(w.id)); showResult(w); } });
      return;
    }
    showResult(state.wods.workouts.find((w) => String(w.id) === pick.value));
  };
  form.append(section(t('Benchmark workout'), hadResults > 0, results, pick, resultBox));

  /* The note: Markdown with photos, read off the whiteboard on request. */
  let note = null;
  if (mayEditNotes() && can('view.notes')) {
    const before = saved ? state.notes.get(saved.id)?.markdown ?? '' : '';
    note = noteInput({ key: () => (saved ? 'training-' + saved.id : 'new'), initial: before, label: t('Note on the training, in Markdown'), rows: 6 });
    const photoRow = h('div', { class: 'sec-row note-actions' });
    photoRow.append(...note.addImage);
    form.append(section(t('Note'), !!before || !training || !!photos?.length, h('p', { class: 'muted hint' }, t('The WOD from the whiteboard, how it went: a photo can be written out by Claude.')), ...note.parts, photoRow));
  }

  const error = h('p', { class: 'lock-error', role: 'alert', hidden: '' });
  const save = h('button', { class: 'pill-btn', type: 'submit' }, t('Save'));
  const cancel = h('button', { class: 'pill-btn', type: 'button' }, t('Cancel'));
  const row = h('div', { class: 'sec-row lift-actions' });
  row.append(save, cancel);
  let del = null;
  if (training) {
    del = h('button', { class: 'pill-btn danger', type: 'button' }, t('Delete'));
    row.append(del);
  }
  form.append(error, row);
  drawWorkouts();

  const panel = h('div', { class: 'wo-panel lift-form-panel session-panel' });
  panel.append(head, form);
  win = openDialog({ cls: 'wo-overlay', labelledBy: 'session-title', content: [panel], returnFocus, onClose: () => { win = null; } });
  close.onclick = cancel.onclick = () => win?.close();
  (exs[0] ? (exs[0].name.value ? exs[0].sets[0]?.reps : exs[0].name) : title).focus();
  // Shared photos: uploaded into the note, ready for "Read the whiteboard".
  if (photos?.length) {
    if (note) note.addFiles(photos);
    else {
      error.textContent = t('Your role can’t write notes, so the shared photo can’t be added.');
      error.hidden = false;
    }
  }

  const busy = (on) => { for (const b of [save, cancel, del]) if (b) b.disabled = on; };
  const fail = (text, focus = save) => {
    error.textContent = text;
    error.hidden = false;
    busy(false);
    focus.focus();
  };
  const why = async (res) => {
    const r = res ? await reason(res) : '';
    return r === 'bad_session' ? t('Check the sets: each needs a lift, 1 to 100 reps and 0 to 500 kg.')
      : r === 'workout_taken' ? t('One of the workouts has its own training already: open it from the workout.')
      : r === 'second_factor_required' ? t('Set up a second factor under Settings → Security first.')
      : res ? t('That didn’t work. Try again.') : t('Server not reachable.');
  };
  // Everything that shows training: bests, the calendar, the windows.
  const finish = () => {
    win?.close();
    notifyBests();
    document.dispatchEvent(new Event('phases-change'));
    after?.();
  };

  form.onsubmit = async (e) => {
    e.preventDefault();
    error.hidden = true;
    const all = flat();
    const sets = all.filter((s) => s.lift && s.reps > 0);
    let resultBody = null;
    if (result) {
      try {
        resultBody = { ...result.read(), note: result.note.value };
      } catch (err) {
        return fail(err.message.startsWith('check:') ? err.message.slice(6) : t('Check the result.'));
      }
      if (!(resultBody.value >= 0) || (result.workout.scoring !== 'time' && result.workout.scoring !== 'rounds' && !resultBody.value)) return fail(t('Check the result.'));
    }
    const text = note?.value() ?? '';
    if (note?.busy()) return fail(t('Wait for the images to finish uploading.'));
    // An empty training says "nothing to add" to its workout; without one
    // it says nothing.
    const empty = !sets.length && !resultBody && !text.trim();
    if (empty && !refs.length && !saved) return fail(t('Add a workout, sets, a result or a note.'));
    if (!dateIn.value) return fail(t('Pick the day.'), dateIn);
    busy(true);
    // The ticked bests, as indexes into the sets sent (empty rows left out).
    const kept = all.map((s, i) => (s.lift && s.reps > 0 ? i : -1)).filter((i) => i >= 0);
    const bests = [...ticks.values()].filter((x) => x.box.checked).map((x) => kept.indexOf(x.index)).filter((i) => i >= 0);
    const body = { date: dateIn.value, workouts: refs, title: title.value, rpe: rpe.value ? Number(rpe.value) : null, sets, bests };
    // Saved again after a part below failed: the bests are in already.
    if (bestsSaved) body.bests = [];
    const res = await (saved ? api('training/sessions/' + saved.id, 'PUT', body) : api('training/sessions', 'POST', body)).catch(() => null);
    if (!res?.ok) return fail(await why(res));
    const was = saved;
    saved = await res.json();
    bestsSaved = true;
    setTrainings(was ? state.trainings.map((x) => (x.id === saved.id ? saved : x)) : [...state.trainings, saved]);
    if (bests.length) await loadLifts(state.data);
    // Then the result and the note, into it.
    if (resultBody) {
      const r = await api(`wods/${result.workout.id}/results`, 'POST', { ...resultBody, date: saved.date, fitbit_workout: '', session_id: saved.id }).catch(() => null);
      if (!r?.ok) return fail(t('The training is saved, the result not: {why}', { why: await why(r) }));
      const added = await r.json();
      state.wods = { ...state.wods, results: [...state.wods.results, added] };
      result = null;
      fillPick();
      resultBox.replaceChildren();
      drawResults();
      renderWods();
    }
    if (note) {
      const problem = await saveNote(saved.id, text);
      if (problem) return fail(t('The training is saved, the note not: {why}', { why: problem }));
    }
    finish();
  };
  if (del) {
    del.onclick = async () => {
      if (!confirm(t('Delete this training with its sets and note? Its lifts and benchmark results stay, and its workouts can be logged again.'))) return;
      busy(true);
      error.hidden = true;
      const res = await api('training/sessions/' + saved.id, 'DELETE').catch(() => null);
      if (!res?.ok && res?.status !== 404) return fail(await why(res), del);
      const id = saved.id;
      setTrainings(state.trainings.filter((x) => x.id !== id));
      state.notes?.delete(id);
      state.lifts = state.lifts.map((x) => (x.session_id === id ? { ...x, session_id: null, fitbit_workout: '' } : x));
      state.wods = { ...state.wods, results: state.wods.results.map((x) => (x.session_id === id ? { ...x, session_id: null, fitbit_workout: '' } : x)) };
      finish();
    };
  }
}
