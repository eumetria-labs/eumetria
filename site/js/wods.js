// Benchmark workouts (Fran, Cindy, a WOD the box repeats, …) in the Training
// card's "Personal bests" tab, below the lifts. People create a workout once,
// with how it is scored, and note a result each time they do it; the best
// result is the personal best, Rx and scaled apart. The server keeps them
// (wods.go) under the lifts' scopes: view.health reads, edit.lifts writes.
// A result can be linked to the Fitbit workout it was done in: the workout's
// window lists it and adds new ones (wodSection).

import { actLabel, dfLong, dfShort, dfMonthYear, fmt, parse } from './format.js';
import { WOD_PRESETS, betterResult, fmtClock, parseClock, wodBestResults, wodBoard, workoutIcon, workoutName } from './health.js';
import { openWorkout } from './workout.js';
import { $, emoji, h, nameCell } from './dom.js';
import { drawProgress, progressChart } from './charts.js';
import { chart, list, shareTools, textLines, tiles } from './share.js';
import { openDialog } from './dialog.js';
import { api, reason } from './account.js';
import { fmtKg, mayEditLifts, notifyBests, openLift } from './lifts.js';
import { state } from './state.js';
import { t } from './i18n.js';

const SCORING = {
  time: () => t('For time'),
  rounds: () => t('Rounds and reps'),
  reps: () => t('Reps'),
  load: () => t('Weight'),
};

// A result as people say it: "4:12", "12 rounds + 5 reps", "150 reps", "80 kg";
// short, rounds and reps are "12 + 5", as on the whiteboard.
export function fmtResult(scoring, r, short = false) {
  if (r.capped) return short ? t('{n} reps (cap)', { n: fmt(r.value) }) : t('capped, {n} reps', { n: fmt(r.value) });
  if (scoring === 'time') return fmtClock(r.value);
  if (scoring === 'rounds' && short) return `${fmt(r.value)} + ${fmt(r.reps || 0)}`;
  if (scoring === 'rounds') return r.reps ? t('{rounds} rounds + {reps} reps', { rounds: fmt(r.value), reps: fmt(r.reps) }) : t('{rounds} rounds', { rounds: fmt(r.value) });
  if (scoring === 'reps') return t('{n} reps', { n: fmt(r.value) });
  return fmtKg(r.value);
}

const shortDate = (d) => (d.slice(0, 4) === (state.today || '').slice(0, 4) ? dfShort.format(parse(d)) : dfMonthYear.format(parse(d)));

// The workouts and results that go with data. Plain files (no server) read
// wods.json, which -demo writes. Offline the ones already here stay.
export async function loadWods(data) {
  if (data.scopes && !data.scopes.includes('view.health')) {
    state.wods = { workouts: [], results: [] };
    return;
  }
  let res = await api('wods').catch(() => null);
  if (res?.status === 404) res = await fetch('wods.json', { cache: 'no-cache' }).catch(() => null);
  if (res?.status === 404) {
    state.wods = { workouts: [], results: [] };
    state.wodsLoaded = true;
    return;
  }
  if (!res?.ok) {
    console.error('loading the benchmark workouts failed', res?.status);
    return;
  }
  state.wods = await res.json();
  state.wodsLoaded = true;
}

export const hasWods = () => state.wods.workouts.length > 0 || (mayEditLifts() && state.wodsLoaded);

// After a change here: the list, and what shows bests elsewhere.
function changed() {
  renderWods();
  notifyBests();
}

/* ---------- the list in the card ---------- */

export function renderWods() {
  const host = $('#wods');
  if (!host) return;
  const rows = wodBoard(state.wods.workouts, state.wods.results, state.today);
  const edit = mayEditLifts();
  // Viewers see the part only when there is something in it.
  $('#wods-part').hidden = !rows.length && !edit;
  const add = $('#wod-add');
  add.hidden = !edit;
  add.onclick = () => openWorkoutForm({ returnFocus: add });
  $('#wods-sub').textContent = rows.length ? t('best result per workout') : '';
  if (!rows.length) {
    host.replaceChildren(h('p', { class: 'empty-tab' }, !state.wodsLoaded ? t('The benchmark workouts could not be loaded.')
      : t('No benchmark workouts yet. Add Fran, Cindy or one your box repeats.')));
    return;
  }
  host.replaceChildren(...rows.map((r) => {
    const name = r.workout.name + (r.scaled ? ` · ${t('Scaled')}` : '');
    const detail = (short) => (r.best ? [shortDate(r.best.date), r.prev ? t('was {result}', { result: fmtResult(r.workout.scoring, r.prev, short) }) : ''].filter(Boolean).join(' · ') : t('No result yet'));
    const value = (short) => (r.best ? fmtResult(r.workout.scoring, r.best, short) : '–');
    const row = h('button', { class: 'day-item workout-row lift-row', type: 'button', 'aria-label': t('{label}: {value}, {detail}. Show history', { label: name, value: value(false), detail: detail(false) }) });
    const cell = nameCell(r.workout.name, false, '⏱️');
    if (r.scaled) cell.append(h('span', { class: 'lift-rm' }, t('Scaled')));
    if (r.recent && r.prev) cell.append(h('span', { class: 'lift-new', title: t('Set in the last 30 days') }, t('New')));
    row.append(cell, h('span', { class: 'amt' }, detail(true)), h('span', { class: 'val' }, value(true)), h('span', { class: 'chev', 'aria-hidden': 'true' }, '›'));
    row.addEventListener('click', () => openWod(r.workout.id, row));
    return row;
  }));
}

/* ---------- in the day window ---------- */

// The results noted on a day, lifts first, each with whether it was a new
// best and what it beat: { kind, id, key, icon, name, tag, value, pb, gain,
// note } (gain: "+2,5 kg" or "was 5:40"). The day window lists them, and
// they are the day's share card (cards.js).
export function notedOn(date) {
  const out = [];
  const byKey = new Map();
  for (const e of [...state.lifts].sort((a, b) => a.date.localeCompare(b.date) || a.id - b.id)) {
    const key = e.lift.toLowerCase() + '|' + e.reps;
    const before = byKey.get(key);
    if (e.date === date) {
      const pb = before != null && e.kg > before;
      out.push({ kind: 'lift', id: e.id, key, icon: '🏋️', name: e.lift, tag: e.reps > 1 ? `${e.reps} RM` : '', value: fmtKg(e.kg), pb, gain: pb ? `+${fmtKg(e.kg - before)}` : '', note: e.note || '', progress: () => liftProgress(key) });
    }
    if (before == null || e.kg > before) byKey.set(key, e.kg);
  }
  const byId = new Map(state.wods.workouts.map((w) => [w.id, w]));
  const best = new Map();
  for (const r of [...state.wods.results].sort((a, b) => a.date.localeCompare(b.date) || a.id - b.id)) {
    const w = byId.get(r.workout);
    if (!w) continue;
    const key = `${w.id}|${r.scaled ? 'scaled' : 'rx'}`;
    const before = best.get(key);
    const better = !before || betterResult(w.scoring, r, before);
    if (r.date === date) {
      const pb = !!before && better;
      out.push({ kind: 'wod', id: w.id, key, icon: '⏱️', name: w.name, tag: r.scaled ? t('Scaled') : '', value: fmtResult(w.scoring, r, true), pb, gain: pb ? t('was {result}', { result: fmtResult(w.scoring, before, true) }) : '', note: r.note || '', progress: () => wodProgress(w, !!r.scaled) });
    }
    if (better) best.set(key, r);
  }
  return out;
}

// A lift's results over time for drawProgress (charts.js): { series, opts }.
function liftProgress(key) {
  let best = 0;
  const points = state.lifts.filter((e) => e.lift.toLowerCase() + '|' + e.reps === key).sort((a, b) => a.date.localeCompare(b.date) || a.id - b.id).map((e) => {
    const pb = best > 0 && e.kg > best;
    best = Math.max(best, e.kg);
    return { date: e.date, value: e.kg, pb };
  });
  return { series: [{ points }], opts: { yFmt: (v) => fmtKg(v).replace(' kg', '') } };
}

// A workout's finished results of one kind (Rx or scaled) over time.
function wodProgress(w, scaled) {
  let best = null;
  const points = state.wods.results.filter((r) => r.workout === w.id && !!r.scaled === scaled && !r.capped).sort((a, b) => a.date.localeCompare(b.date) || a.id - b.id).map((r) => {
    const pb = !!best && betterResult(w.scoring, r, best);
    if (!best || pb) best = r;
    return { date: r.date, value: w.scoring === 'rounds' ? r.value + (r.reps || 0) / 1000 : r.value, pb };
  });
  return { series: [{ points, cls: scaled ? 'scaled' : '' }], opts: { invert: w.scoring === 'time', yFmt: w.scoring === 'time' ? fmtClock : (v) => fmt(v) } };
}

// Those results as rows that open their lift's or workout's window.
export function notedRows(date) {
  return notedOn(date).map((x) => {
    const detail = [x.gain, x.note].filter(Boolean).join(' · ');
    const b = h('button', { class: 'day-item workout-row lift-row', type: 'button', 'aria-label': t('{label}: {value}, {detail}. Show history', { label: x.name, value: x.value, detail }) });
    const cell = nameCell(x.name, false, x.icon);
    if (x.tag) cell.append(h('span', { class: 'lift-rm' }, x.tag));
    if (x.pb) cell.append(h('span', { class: 'lift-new pb' }, 'PB'));
    b.append(cell, h('span', { class: 'amt' }, detail), h('span', { class: 'val' }, x.value), h('span', { class: 'chev', 'aria-hidden': 'true' }, '›'));
    b.onclick = () => (x.kind === 'lift' ? openLift(x.key, b) : openWod(x.id, b));
    return b;
  });
}

/* ---------- in a Fitbit workout's window ---------- */

// The Fitbit workout a result is linked to, if the page has it.
function fitbitWorkout(r) {
  if (!r.fitbit_workout) return null;
  const day = state.byDate?.get(r.date);
  const w = day?.workouts?.find((x) => x.id === r.fitbit_workout);
  return w ? { day, w, label: actLabel(workoutName(w.type)) } : null;
}

// The benchmark workouts done in a Fitbit workout, by name (the day window's
// activity rows show them).
export const linkedNames = (workoutId) => {
  const byId = new Map(state.wods.workouts.map((w) => [w.id, w]));
  return [...new Set(state.wods.results.filter((r) => r.fitbit_workout === workoutId && byId.has(r.workout)).map((r) => byId.get(r.workout).name))];
};

// The workout window's part about what was done: the benchmark results
// linked to it, and for edit.lifts a pick of the saved workouts (or a new
// one) to add a result to, dated and linked to this session. Null when
// there is nothing to show or add.
export function wodSection(day, fw) {
  const mine = () => state.wods.results.filter((r) => r.fitbit_workout === fw.id);
  if (!mayEditLifts() && !mine().length) return null;
  const link = { id: fw.id, date: day.date, label: actLabel(workoutName(fw.type)) };
  const sec = h('section', { class: 'wo-sec wo-wods' });
  const draw = () => {
    const byId = new Map(state.wods.workouts.map((w) => [w.id, w]));
    const pbs = new Set(wodBestResults(state.wods.workouts, state.wods.results).map((r) => r.id));
    const hd = h('div', { class: 'wo-sec-head' });
    hd.append(h('h3', {}, t('Benchmark workout')), h('span', { class: 'muted' }, t('what you did in this session')));
    sec.replaceChildren(hd);
    for (const r of mine()) {
      const w = byId.get(r.workout);
      if (!w) continue;
      const row = h('div', { class: 'day-item lift-entry' });
      const name = h('span', { class: 'name' });
      const open = h('button', { class: 'link-btn', type: 'button' }, w.name);
      open.onclick = () => openWod(w.id, open);
      name.append(emoji('⏱️', 'lift-emoji'), open);
      if (r.scaled) name.append(h('span', { class: 'lift-rm' }, t('Scaled')));
      if (pbs.has(r.id)) name.append(h('span', { class: 'lift-new pb' }, 'PB'));
      row.append(name, h('span', { class: 'amt' }, r.note || ''), h('span', { class: 'val' }, fmtResult(w.scoring, r, true)));
      if (mayEditLifts()) {
        const edit = h('button', { class: 'icon-btn lift-edit', type: 'button', 'aria-label': t('Change the {name} result', { name: w.name }), title: t('Edit') }, '✎');
        edit.onclick = () => openResultForm({ workout: w, result: r, returnFocus: edit, after: draw });
        row.append(edit);
      }
      sec.append(row);
    }
    if (!mayEditLifts()) return;
    const pick = h('select', { class: 'field', 'aria-label': t('Benchmark workout') });
    pick.append(h('option', { value: '' }, t('Choose a workout…')), ...[...state.wods.workouts].sort((a, b) => a.name.localeCompare(b.name)).map((w) => h('option', { value: String(w.id) }, w.name)), h('option', { value: 'new' }, t('New workout…')));
    const add = h('button', { class: 'pill-btn', type: 'button' }, t('Add result'));
    add.onclick = () => {
      if (!pick.value) return pick.focus();
      if (pick.value === 'new') {
        openWorkoutForm({ returnFocus: add, onCreated: (saved) => openResultForm({ workout: saved, link, returnFocus: add, after: draw }) });
        return;
      }
      const w = byId.get(Number(pick.value));
      if (w) openResultForm({ workout: w, link, returnFocus: add, after: draw });
    };
    const controls = h('div', { class: 'wod-pick' });
    controls.append(pick, add);
    sec.append(controls);
    if (!mine().length) sec.append(h('p', { class: 'muted wod-hint' }, t('Pick the benchmark workout you did, or create it, and note your result.')));
  };
  draw();
  return sec;
}

/* ---------- one workout's window ---------- */

let win = null;

// A workout's window: the workout as written, its bests (Rx and scaled)
// and every result.
export function openWod(id, returnFocus) {
  win?.close(true);
  const close = h('button', { class: 'sheet-close', type: 'button', 'aria-label': t('Close') }, '×');
  const head = h('div', { class: 'sheet-head wo-head' });
  const titles = h('div', { class: 'wo-titles' });
  const title = h('h2', { id: 'wod-title' });
  const sub = h('p', { class: 'muted' });
  titles.append(title, sub);
  head.append(titles, ...shareTools(() => [{ key: 'wod', label: '', name: `wod-${id}`, build: () => wodCard(state.wods.workouts.find((x) => x.id === id)) }]), close);
  const body = h('div', { class: 'wo-body' });
  const panel = h('div', { class: 'wo-panel lift-panel' });
  panel.append(head, body);
  win = openDialog({ cls: 'wo-overlay', labelledBy: 'wod-title', content: [panel], returnFocus });
  close.onclick = () => win.close();
  close.focus(); // not the first button, which saves the picture

  const draw = () => {
    const w = state.wods.workouts.find((x) => x.id === id);
    if (!w) return win.close(); // deleted
    const rows = wodBoard([w], state.wods.results, state.today).filter((r) => r.best);
    title.replaceChildren(emoji('⏱️', 'lift-emoji'), w.name);
    sub.textContent = [SCORING[w.scoring]() + (w.time_cap ? ` · ${t('time cap {time}', { time: fmtClock(w.time_cap) })}` : ''), ...rows.map((r) => (r.scaled ? t('Scaled best {value}', { value: fmtResult(w.scoring, r.best) }) : t('Best {value} on {date}', { value: fmtResult(w.scoring, r.best), date: dfLong.format(parse(r.best.date)) })))].join(' · ');
    body.replaceChildren(...[workoutSection(w, draw), progressSection(w), resultsSection(w, draw)].filter(Boolean));
  };
  draw();
}

// The share picture of a workout: its bests (Rx and scaled), the workout
// as written, the progress and the latest results.
function wodCard(w) {
  const rows = wodBoard([w], state.wods.results, state.today).filter((r) => r.best);
  const rx = rows.find((r) => !r.scaled), sc = rows.find((r) => r.scaled);
  const mine = state.wods.results.filter((r) => r.workout === w.id).sort((a, b) => b.date.localeCompare(a.date) || b.id - a.id);
  const prog = [false, true].map((scaled) => wodProgress(w, scaled)).find((p) => p.series[0].points.length > 1);
  return {
    icon: '⏱️', title: w.name, accent: 's3',
    sub: SCORING[w.scoring]() + (w.time_cap ? ` · ${t('time cap {time}', { time: fmtClock(w.time_cap) })}` : ''),
    blocks: [
      tiles([
        rx && [t('Best'), fmtResult(w.scoring, rx.best, true), ''],
        sc && [t('Scaled'), fmtResult(w.scoring, sc.best, true), ''],
        [t('Results'), fmt(mine.length), ''],
      ]),
      w.description && textLines(w.description.split('\n').map((text) => ({ text })), t('The workout')),
      prog && chart((g, box) => drawProgress(g, prog.series, box.w, box.h, prog.opts), { h: 170, min: 110, max: 240, title: t('Progress') }),
      mine.length && list(mine.map((r) => ({
        name: dfLong.format(parse(r.date)), note: [r.scaled ? t('Scaled') : '', r.note].filter(Boolean).join(' · '), value: fmtResult(w.scoring, r, true),
      })), t('History'), { rowH: 28, minRows: 2 }),
    ].filter(Boolean),
  };
}

function workoutSection(w, redraw) {
  const sec = h('section', { class: 'wo-sec' });
  const hd = h('div', { class: 'wo-sec-head' });
  hd.append(h('h3', {}, t('The workout')));
  if (mayEditLifts()) {
    const edit = h('button', { class: 'pill-btn', type: 'button' }, t('Edit'));
    edit.onclick = () => openWorkoutForm({ workout: w, returnFocus: edit, after: redraw });
    hd.append(edit);
  }
  sec.append(hd, w.description ? h('p', { class: 'wod-desc' }, w.description) : h('p', { class: 'muted' }, t('No description.')));
  return sec;
}

// The results over time, Rx and scaled as two lines; better is up, also for
// times. Capped results count reps, not seconds, and stay out. From two
// results of one kind (Rx or scaled) on.
function progressSection(w) {
  const mine = state.wods.results.filter((r) => r.workout === w.id && !r.capped).sort((a, b) => a.date.localeCompare(b.date) || a.id - b.id);
  if (mine.length < 2) return null;
  const series = [false, true].map((scaled) => {
    let best = null;
    const points = mine.filter((r) => !!r.scaled === scaled).map((r) => {
      const pb = !!best && betterResult(w.scoring, r, best);
      if (!best || pb) best = r;
      // Rounds plus their reps: the reps only order equal rounds.
      return { date: r.date, value: w.scoring === 'rounds' ? r.value + (r.reps || 0) / 1000 : r.value, pb };
    });
    return { points, cls: scaled ? 'scaled' : '' };
  }).filter((x) => x.points.length);
  // A line needs two results of one kind.
  if (!series.some((x) => x.points.length > 1)) return null;
  const sec = h('section', { class: 'wo-sec' });
  const hd = h('div', { class: 'wo-sec-head' });
  hd.append(h('h3', {}, t('Progress')), h('span', { class: 'muted' }, w.scoring === 'time' ? t('faster is up') : t('big dots: new bests')));
  const host = h('div', { class: 'prog-chart' });
  sec.append(hd, host);
  if (series.length > 1) {
    const legend = h('ul', { class: 'prog-legend' });
    for (const [cls, label] of [['', 'Rx'], ['scaled', t('Scaled')]]) {
      const li = h('li');
      li.append(h('span', { class: 'key ' + cls }), label);
      legend.append(li);
    }
    sec.append(legend);
  }
  const yFmt = w.scoring === 'time' ? fmtClock : w.scoring === 'load' ? (v) => fmtKg(v) : (v) => fmt(v);
  requestAnimationFrame(() => progressChart(host, series, { invert: w.scoring === 'time', yFmt, aria: t('{name}: {n} results over time', { name: w.name, n: mine.length }) }));
  return sec;
}

// Every result, the newest first; a PB beat every earlier one of its kind
// (Rx or scaled).
function resultsSection(w, redraw) {
  const sec = h('section', { class: 'wo-sec' });
  const hd = h('div', { class: 'wo-sec-head' });
  hd.append(h('h3', {}, t('History')));
  if (mayEditLifts()) {
    const add = h('button', { class: 'pill-btn', type: 'button' }, t('Add result'));
    add.onclick = () => openResultForm({ workout: w, returnFocus: add, after: redraw });
    hd.append(add);
  }
  sec.append(hd);
  const mine = state.wods.results.filter((r) => r.workout === w.id).sort((a, b) => a.date.localeCompare(b.date) || a.id - b.id);
  if (!mine.length) {
    sec.append(h('p', { class: 'muted' }, t('No result yet')));
    return sec;
  }
  const best = { rx: null, scaled: null };
  const marks = mine.map((r) => {
    const k = r.scaled ? 'scaled' : 'rx';
    const pb = !!best[k] && betterResult(w.scoring, r, best[k]);
    const was = pb ? best[k] : null;
    if (!best[k] || pb) best[k] = r;
    return { r, pb, was };
  });
  for (const { r, pb, was } of marks.reverse()) {
    const row = h('div', { class: 'day-item lift-entry' });
    const name = h('span', { class: 'name' });
    name.append(h('span', { class: 'txt' }, dfLong.format(parse(r.date))));
    if (r.scaled) name.append(h('span', { class: 'lift-rm' }, t('Scaled')));
    if (pb) name.append(h('span', { class: 'lift-new pb' }, 'PB'));
    const linked = fitbitWorkout(r);
    if (linked) {
      const go = h('button', { class: 'wod-link', type: 'button', 'aria-label': t('Open the {name} of {date}', { name: linked.label, date: dfLong.format(parse(r.date)) }), title: t('Open the {name} of {date}', { name: linked.label, date: dfLong.format(parse(r.date)) }) });
      go.append(emoji(workoutIcon(linked.w.type)));
      go.onclick = () => openWorkout(linked.day, linked.w.id, go);
      name.append(go);
    }
    const ready = state.byDate?.get(r.date)?.scores?.readiness?.value;
    const note = [was ? t('was {result}', { result: fmtResult(w.scoring, was, true) }) : '', ready != null ? t('Readiness {value}', { value: ready }) : '', r.note].filter(Boolean).join(' · ');
    row.append(name, h('span', { class: 'amt' }, note), h('span', { class: 'val', title: fmtResult(w.scoring, r) }, fmtResult(w.scoring, r, true)));
    if (mayEditLifts()) {
      const edit = h('button', { class: 'icon-btn lift-edit', type: 'button', 'aria-label': t('Change the result of {date}', { date: dfLong.format(parse(r.date)) }), title: t('Edit') }, '✎');
      edit.onclick = () => openResultForm({ workout: w, result: r, returnFocus: edit, after: redraw });
      row.append(edit);
    }
    sec.append(row);
  }
  return sec;
}

/* ---------- the forms ---------- */

const ERRORS = {
  bad_wod: () => t('Check the workout: a name of up to 48 characters and how it is scored.'),
  bad_result: () => t('Check the result and the date: it must have passed.'),
  name_taken: () => t('There is already a workout of that name.'),
  has_results: () => t('The workout has results: how it is scored can no longer change.'),
  not_found: () => t('This workout or result is no longer there.'),
};

async function refused(res) {
  if (!res) return t('Server not reachable.');
  if (res.status === 401) return t('You are signed out. Reload the page.');
  return ERRORS[await reason(res)]?.() || t('That didn’t work. Try again.');
}

// A dialog with a form: its fields come from build(form, field); save
// runs on submit and returns the response, remove (if given) on Delete.
function formDialog({ id, title, build, save, remove, removeAsk, returnFocus, done }) {
  const close = h('button', { class: 'sheet-close', type: 'button', 'aria-label': t('Close') }, '×');
  const head = h('div', { class: 'sheet-head wo-head' });
  const titles = h('div', { class: 'wo-titles' });
  titles.append(h('h2', { id }, title));
  head.append(titles, close);
  const form = h('form', { class: 'wo-body lift-form' });
  const field = (label, input, hint) => {
    const wrap = h('label', { class: 'lift-field' });
    wrap.append(h('span', { class: 'lbl' }, label), input);
    if (hint) wrap.append(h('span', { class: 'muted hint' }, hint));
    form.append(wrap);
    return input;
  };
  const first = build(form, field);
  const error = h('p', { class: 'lock-error', role: 'alert', hidden: '' });
  const ok = h('button', { class: 'pill-btn', type: 'submit' }, t('Save'));
  const cancel = h('button', { class: 'pill-btn', type: 'button' }, t('Cancel'));
  const row = h('div', { class: 'sec-row lift-actions' });
  row.append(ok, cancel);
  const del = remove ? h('button', { class: 'pill-btn danger', type: 'button' }, t('Delete')) : null;
  if (del) row.append(del);
  form.append(error, row);
  const panel = h('div', { class: 'wo-panel lift-form-panel' });
  panel.append(head, form);
  const dlg = openDialog({ cls: 'wo-overlay', labelledBy: id, content: [panel], returnFocus });
  close.onclick = cancel.onclick = () => dlg.close();
  first.focus();

  const send = async (button, run, gone) => {
    for (const b of [ok, cancel, del]) if (b) b.disabled = true;
    error.hidden = true;
    let res;
    try {
      res = await run();
    } catch (err) {
      res = err instanceof Error && err.message.startsWith('check:') ? { local: err.message.slice(6) } : null;
    }
    if (res?.ok || (res?.status === 404 && gone)) {
      await done(res, gone);
      dlg.close();
      return;
    }
    error.textContent = res?.local || await refused(res);
    error.hidden = false;
    for (const b of [ok, cancel, del]) if (b) b.disabled = false;
    button.focus();
  };
  form.onsubmit = (e) => {
    e.preventDefault();
    send(ok, save, false);
  };
  if (del) del.onclick = () => confirm(removeAsk) && send(del, remove, true);
}

// A new workout or a change to one. Typing a famous one's name fills in the
// rest while it is empty.
// onCreated(saved) runs for a new workout instead of opening its window.
export function openWorkoutForm({ workout, returnFocus, after, onCreated }) {
  const results = workout ? state.wods.results.filter((r) => r.workout === workout.id).length : 0;
  let name, scoring, desc, cap;
  formDialog({
    id: 'wod-form-title',
    title: workout ? t('Change workout') : t('New workout'),
    returnFocus,
    build: (form, field) => {
      const listId = 'wod-names';
      const datalist = h('datalist', { id: listId });
      datalist.append(...WOD_PRESETS.filter((p) => !state.wods.workouts.some((w) => w.name.toLowerCase() === p.name.toLowerCase())).map((p) => h('option', { value: p.name })));
      name = field(t('Name'), h('input', { class: 'field', type: 'text', list: listId, required: '', maxlength: '48', autocomplete: 'off', value: workout?.name || '', placeholder: 'Fran' }));
      const select = h('select', { class: 'field' });
      select.append(...Object.entries(SCORING).map(([k, label]) => h('option', { value: k }, label())));
      scoring = field(t('Scored by'), select,
        results ? t('Fixed once the workout has results.') : t('For time: the less the better; the others: the more.'));
      scoring.value = workout?.scoring || 'time';
      scoring.disabled = results > 0;
      cap = field(t('Time cap (min)'), h('input', { class: 'field', type: 'number', min: '0', max: '1440', step: '1', inputmode: 'numeric', value: workout?.time_cap ? workout.time_cap / 60 : '', placeholder: t('none') }), t('only for workouts for time'));
      const capField = cap.closest('label');
      const syncCap = () => { capField.hidden = scoring.value !== 'time'; };
      scoring.addEventListener('change', syncCap);
      syncCap();
      desc = field(t('The workout'), h('textarea', { class: 'field', rows: '5', maxlength: '1000', placeholder: '21-15-9\nThrusters 43 kg\nPull-ups' }), t('optional'));
      desc.value = workout?.description || '';
      form.append(datalist);
      name.addEventListener('input', () => {
        const p = WOD_PRESETS.find((x) => x.name.toLowerCase() === name.value.trim().toLowerCase());
        if (!p || desc.value.trim() || results) return;
        scoring.value = p.scoring;
        desc.value = p.description;
      });
      return name;
    },
    save: () => {
      const body = { name: name.value, scoring: scoring.value, time_cap: scoring.value === 'time' ? Math.round((Number(cap.value) || 0) * 60) : 0, description: desc.value };
      return workout ? api('wods/' + workout.id, 'PUT', body) : api('wods', 'POST', body);
    },
    remove: workout && (() => api('wods/' + workout.id, 'DELETE')),
    removeAsk: results ? t('Delete this workout and its {n} results?', { n: results }) : t('Delete this workout?'),
    done: async (res, gone) => {
      if (gone) {
        state.wods = { workouts: state.wods.workouts.filter((w) => w.id !== workout.id), results: state.wods.results.filter((r) => r.workout !== workout.id) };
      } else {
        const saved = await res.json();
        const others = state.wods.workouts.filter((w) => w.id !== saved.id);
        state.wods = { ...state.wods, workouts: [...others, saved].sort((a, b) => a.name.localeCompare(b.name)) };
        // A new one opens, ready for its first result.
        if (!workout) setTimeout(() => (onCreated ? onCreated(saved) : openWod(saved.id, returnFocus)));
      }
      changed();
      after?.();
    },
  });
}

// A new result of workout, or a change to one (result).
// link: { id, date, label } of a Fitbit workout a new result is done in; a
// result linked already keeps its link unless it is unticked.
export function openResultForm({ workout, result, link, returnFocus, after }) {
  const v = result || { value: '', reps: 0, scaled: false, date: link?.date || state.today || '', note: '' };
  const linkTo = link || (result && fitbitWorkout(result) && { id: result.fitbit_workout, date: result.date, label: fitbitWorkout(result).label }) || null;
  let linked = null;
  let read, scaled, date, note;
  formDialog({
    id: 'wod-result-title',
    title: `${result ? t('Change result') : t('New result')} · ${workout.name}`,
    returnFocus,
    build: (form, field) => {
      let first;
      if (workout.scoring === 'time') {
        const time = first = field(t('Time'), h('input', { class: 'field', type: 'text', inputmode: 'text', autocomplete: 'off', placeholder: '4:12', value: v.value && !v.capped ? fmtClock(v.value) : '' }), t('minutes:seconds, or h:mm:ss'));
        // Not finished in the time cap: the reps done instead of a time.
        const capWrap = h('label', { class: 'lift-check' });
        const capped = h('input', { type: 'checkbox' });
        capped.checked = !!v.capped;
        capWrap.append(capped, h('span', {}, workout.time_cap ? t('Not finished in the {time} cap', { time: fmtClock(workout.time_cap) }) : t('Not finished in the time cap')));
        form.append(capWrap);
        const reps = field(t('Reps done'), h('input', { class: 'field', type: 'number', min: '0', max: '99999', step: '1', inputmode: 'numeric', value: v.capped ? v.value : '' }));
        const timeField = time.closest('label'), repsField = reps.closest('label');
        const sync = () => {
          timeField.hidden = capped.checked;
          repsField.hidden = !capped.checked;
          time.required = !capped.checked;
          reps.required = capped.checked;
        };
        capped.onchange = sync;
        sync();
        read = () => {
          if (capped.checked) return { value: Number(reps.value), capped: true };
          const s = parseClock(time.value);
          if (!s) throw new Error('check:' + t('Write the time like 4:12 or 1:02:05.'));
          return { value: s };
        };
      } else if (workout.scoring === 'rounds') {
        const rounds = first = field(t('Rounds'), h('input', { class: 'field', type: 'number', required: '', min: '0', max: '999', step: '1', inputmode: 'numeric', value: result ? v.value : '' }));
        const reps = field(t('Reps after them'), h('input', { class: 'field', type: 'number', min: '0', max: '999', step: '1', inputmode: 'numeric', value: v.reps || 0 }));
        read = () => ({ value: Number(rounds.value), reps: Number(reps.value) || 0 });
      } else {
        const load = workout.scoring === 'load';
        const n = first = field(load ? t('Weight (kg)') : t('Reps'), h('input', { class: 'field', type: 'number', required: '', min: load ? '0.5' : '1', max: load ? '500' : '99999', step: load ? '0.25' : '1', inputmode: load ? 'decimal' : 'numeric', value: v.value }));
        read = () => ({ value: Number(n.value) });
      }
      const wrap = h('label', { class: 'lift-check' });
      scaled = h('input', { type: 'checkbox' });
      scaled.checked = !!v.scaled;
      wrap.append(scaled, h('span', {}, t('Scaled')), h('span', { class: 'muted hint' }, t('lighter or easier than written; it has its own best')));
      form.append(wrap);
      if (linkTo) {
        const wrap = h('label', { class: 'lift-check' });
        linked = h('input', { type: 'checkbox' });
        linked.checked = true;
        wrap.append(linked, h('span', {}, t('Done in the {name} of {date}', { name: linkTo.label, date: dfLong.format(parse(linkTo.date)) })),
          h('span', { class: 'muted hint' }, t('the date is the workout’s')));
        form.append(wrap);
      }
      date = field(t('Date'), h('input', { class: 'field', type: 'date', required: '', value: v.date, max: state.today || '' }));
      if (linked) {
        const sync = () => { date.disabled = linked.checked; if (linked.checked) date.value = linkTo.date; };
        linked.onchange = sync;
        sync();
      }
      note = field(t('Note'), h('input', { class: 'field', type: 'text', maxlength: '200', value: v.note || '', placeholder: t('optional') }));
      return first;
    },
    save: () => {
      const body = { ...read(), scaled: scaled.checked, date: date.value, note: note.value, fitbit_workout: linked?.checked ? linkTo.id : '' };
      return result ? api('wod-results/' + result.id, 'PUT', body) : api(`wods/${workout.id}/results`, 'POST', body);
    },
    remove: result && (() => api('wod-results/' + result.id, 'DELETE')),
    removeAsk: t('Delete this result?'),
    done: async (res, gone) => {
      if (gone) {
        state.wods = { ...state.wods, results: state.wods.results.filter((r) => r.id !== result.id) };
      } else {
        const saved = await res.json();
        state.wods = { ...state.wods, results: [...state.wods.results.filter((r) => r.id !== saved.id), saved] };
      }
      changed();
      after?.();
    },
  });
}
