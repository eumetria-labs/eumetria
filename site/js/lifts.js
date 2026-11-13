// Personal bests in lifts (Power Clean, Strict Press, Split Jerk, …), in the
// Training card's "Personal bests" tab. People note them themselves: the
// server keeps them (lifts.go), everyone with view.health reads them, and
// edit.lifts adds, changes and deletes them. Each entry is one result on one
// day; a lift's best is its heaviest, the rest is its history.

import { NUM_LOCALE, dfLong, dfShort, dfMonthYear, parse } from './format.js';
import { COMMON_LIFTS, bestDays, bestsReadiness, liftBoard, loadable, oneRepMax, percentages, platesFor, strengthTrend, wodBestDays } from './health.js';
import { $, emoji, h, nameCell } from './dom.js';
import { drawProgress, progressChart } from './charts.js';
import { chart, list, shareTools, tiles } from './share.js';
import { weightTrendAt } from './insights.js';
import { openDialog } from './dialog.js';
import { account, api, reason } from './account.js';
import { state, store } from './state.js';
import { t } from './i18n.js';

const nfKg = new Intl.NumberFormat(NUM_LOCALE, { maximumFractionDigits: 2 });
const nfX = new Intl.NumberFormat(NUM_LOCALE, { minimumFractionDigits: 2, maximumFractionDigits: 2 });
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

// Without a second factor the server refuses a role that needs one, except
// with -demo.
export function mayEditLifts() {
  const u = account.user;
  return !!u?.scopes?.includes('edit.lifts') && (!u.needs_second_factor || !!u.demo);
}

// Run when lifts or benchmark workouts changed here, for what shows bests
// elsewhere (the Today card's highlights).
const listeners = new Set();
export const onBestsChange = (fn) => listeners.add(fn);
export function notifyBests() {
  renderReadiness();
  for (const fn of listeners) fn();
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
  renderReadiness();
  // Viewers see the part only when there is something in it, or nothing
  // else in the tab.
  $('#lifts-part').hidden = !rows.length && !mayEditLifts() && state.wods.workouts.length > 0;
  if (!rows.length) {
    host.replaceChildren(h('p', { class: 'empty-tab' }, !state.liftsLoaded ? t('The lifts could not be loaded.')
      : mayEditLifts() ? t('No lifts yet. Add your first personal best.') : t('No lifts noted yet.')));
    return;
  }
  host.replaceChildren(...rows.map((r) => {
    const bw = bodyweight(r.best);
    const detail = [shortDate(r.best.date), r.gain ? gainKg(r.gain) : '', bw ? t('{x}× bodyweight', { x: nfX.format(bw) }) : ''].filter(Boolean).join(' · ');
    const row = h('button', { class: 'day-item workout-row lift-row', type: 'button', 'aria-label': t('{label}: {value}, {detail}. Show history', { label: rowName(r), value: fmtKg(r.best.kg), detail }) });
    const name = nameCell(r.lift, false, '🏋️');
    if (r.reps > 1) name.append(h('span', { class: 'lift-rm' }, rm(r.reps)));
    if (r.recent) name.append(h('span', { class: 'lift-new', title: t('Set in the last 30 days') }, t('New')));
    row.append(name, h('span', { class: 'amt' }, detail), h('span', { class: 'val' }, fmtKg(r.best.kg)), h('span', { class: 'chev', 'aria-hidden': 'true' }, '›'));
    row.addEventListener('click', () => openLift(r.key, row));
    return row;
  }));
}

// Whether new bests (lifts and benchmark workouts) came on ready mornings:
// their average Readiness against every morning in the same time.
function renderReadiness() {
  const host = $('#bests-ready');
  if (!host) return;
  const dates = [...bestDays(state.lifts), ...wodBestDays(state.wods.workouts, state.wods.results)];
  const r = bestsReadiness(dates, state.byDate || new Map());
  host.hidden = !r;
  if (!r) return;
  host.replaceChildren(emoji('🔋'), h('span', {}, t('Your {n} new bests came on mornings with a Readiness of {avg} on average; all mornings in that time: {usual}.', r)));
}

// The morning Readiness of a day, if it has one.
const readinessOn = (date) => state.byDate?.get(date)?.scores?.readiness?.value ?? null;

/* ---------- one lift's window ---------- */

let win = null;

// The window of one lift and rep count: its best, the percentages to train
// with, and every result.
export function openLift(key, returnFocus) {
  win?.close(true);
  const close = h('button', { class: 'sheet-close', type: 'button', 'aria-label': t('Close') }, '×');
  const head = h('div', { class: 'sheet-head wo-head' });
  const titles = h('div', { class: 'wo-titles' });
  const title = h('h2', { id: 'lift-title' });
  const sub = h('p', { class: 'muted' });
  titles.append(title, sub);
  // The picture of the lift as it is now, also after a change here.
  const row = () => liftBoard(state.lifts, state.today).find((x) => x.key === key);
  head.append(titles, ...shareTools(() => [{ key: 'lift', label: '', name: `lift-${key.replace(/[^a-z0-9]+/g, '-')}`, build: () => liftCard(row()) }]), close);
  const body = h('div', { class: 'wo-body' });
  const panel = h('div', { class: 'wo-panel lift-panel' });
  panel.append(head, body);
  win = openDialog({ cls: 'wo-overlay', labelledBy: 'lift-title', content: [panel], returnFocus });
  close.onclick = () => win.close();
  close.focus(); // not the first button, which saves the picture

  const draw = () => {
    const r = liftBoard(state.lifts, state.today).find((x) => x.key === key);
    if (!r) return win.close(); // its last entry was deleted
    title.replaceChildren(emoji('🏋️', 'lift-emoji'), rowName(r));
    const bw = bodyweight(r.best);
    sub.textContent = [t('Best {value} on {date}', { value: fmtKg(r.best.kg), date: dfLong.format(parse(r.best.date)) }), bw ? t('{x}× bodyweight', { x: nfX.format(bw) }) : ''].filter(Boolean).join(' · ');
    const plates = plateSection(r.best.kg);
    body.replaceChildren(...[progressSection(r), strengthSection(r), percentSection(r, plates.show), plates.sec, historySection(r, draw)].filter(Boolean));
  };
  draw();
}

// A result as a multiple of the bodyweight that week (the average of the
// weigh-ins of the 7 days up to it), or null without any.
export function bodyweight(e) {
  const w = state.byDate ? weightTrendAt(state.byDate, e.date) : null;
  return w ? e.kg / w : null;
}

// A lift's results for the progress chart, the new bests marked.
function liftPoints(r) {
  let best = 0;
  return r.entries.map((e) => {
    const pb = best > 0 && e.kg > best;
    best = Math.max(best, e.kg);
    return { date: e.date, value: e.kg, pb };
  });
}

// The share picture of a lift: its best, gain, bodyweight multiple and
// estimated 1 RM, the progress and the latest results.
function liftCard(r) {
  const bw = bodyweight(r.best);
  const points = liftPoints(r);
  const pbs = new Set(points.map((p, i) => (p.pb ? r.entries[i].id : null)));
  return {
    icon: '🏋️', title: rowName(r), sub: t('Best {value} on {date}', { value: fmtKg(r.best.kg), date: dfLong.format(parse(r.best.date)) }), accent: 's3',
    blocks: [
      tiles([
        [t('Best'), nfKg.format(r.best.kg), 'kg'],
        r.gain != null && [t('Gain'), '+' + nfKg.format(r.gain), 'kg'],
        bw && [t('Bodyweight'), nfX.format(bw) + '×', ''],
        r.reps > 1 && [t('Est. 1 RM'), nfKg.format(loadable(oneRepMax(r.best.kg, r.reps))), 'kg'],
      ]),
      points.length > 1 && chart((g, box) => drawProgress(g, [{ points }], box.w, box.h, { yFmt: (v) => nfKg.format(v) }), { h: 180, min: 120, max: 260, title: t('Progress') }),
      list([...r.entries].reverse().map((e) => ({
        name: dfLong.format(parse(e.date)), note: [pbs.has(e.id) ? 'PB' : '', e.note].filter(Boolean).join(' · '), value: fmtKg(e.kg),
      })), t('History'), { rowH: 28, minRows: 2 }),
    ].filter(Boolean),
  };
}

// Every result over time, the new bests bigger; from two results on.
function progressSection(r) {
  if (r.entries.length < 2) return null;
  const sec = h('section', { class: 'wo-sec' });
  const hd = h('div', { class: 'wo-sec-head' });
  hd.append(h('h3', {}, t('Progress')), h('span', { class: 'muted' }, t('big dots: new bests')));
  const host = h('div', { class: 'prog-chart' });
  sec.append(hd, host);
  const points = liftPoints(r);
  // Drawn once the window is laid out.
  requestAnimationFrame(() => progressChart(host, [{ points }], {
    yFmt: (v) => nfKg.format(v),
    aria: t('{lift}: {n} results from {from} to {to}, best {value}', { lift: rowName(r), n: points.length, from: fmtKg(points[0].value), to: fmtKg(points.at(-1).value), value: fmtKg(r.best.kg) }),
  }));
  return sec;
}

// The lift's strength over time with every rep count in it, as estimated
// 1 RM or as a multiple of the bodyweight. Only when it says more than the
// progress above: other rep counts, or weigh-ins to compare with.
function strengthSection(r) {
  const bwAt = (date) => (state.byDate ? weightTrendAt(state.byDate, date) : null);
  const points = strengthTrend(state.lifts, r.lift, bwAt);
  const otherReps = state.lifts.some((e) => e.lift.toLowerCase() === r.lift.toLowerCase() && e.reps !== r.reps);
  const ratios = points.filter((p) => p.ratio != null);
  if (points.length < 2 || (!otherReps && ratios.length < 2)) return null;
  const views = [
    ['kg', t('Est. 1 RM'), points.map((p) => ({ date: p.date, value: p.est, pb: p.pb })), (v) => nfKg.format(v)],
    ratios.length >= 2 && ['bw', t('× bodyweight'), ratios.map((p) => ({ date: p.date, value: p.ratio, pb: p.pb })), (v) => nfX.format(v) + '×'],
  ].filter(Boolean);
  let view = views.some(([k]) => k === store.get('strengthView')) ? store.get('strengthView') : views[0][0];
  const sec = h('section', { class: 'wo-sec' });
  const hd = h('div', { class: 'wo-sec-head' });
  hd.append(h('h3', {}, t('Strength over time')), h('span', { class: 'muted' }, otherReps
    ? t('all rep counts, as estimated 1 RM')
    : t('estimated 1 RM')));
  const pills = h('div', { class: 'strength-view', role: 'group', 'aria-label': t('Show as') });
  const host = h('div', { class: 'prog-chart' });
  const note = h('p', { class: 'muted strength-note' });
  const draw = () => {
    const [, label, pts, yFmt] = views.find(([k]) => k === view);
    for (const b of pills.children) b.setAttribute('aria-pressed', String(b.dataset.view === view));
    progressChart(host, [{ points: pts }], {
      yFmt, aria: t('{lift}, {what}: from {from} to {to}', { lift: r.lift, what: label, from: yFmt(pts[0].value), to: yFmt(pts.at(-1).value) }),
    });
    const a = pts[0], b = pts.at(-1);
    note.textContent = t('{from} on {d0}, {to} on {d1}', { from: yFmt(a.value), d0: shortDate(a.date), to: yFmt(b.value), d1: shortDate(b.date) });
  };
  if (views.length > 1) {
    for (const [k, label] of views) {
      const b = h('button', { class: 'pill-btn', type: 'button', 'data-view': k }, label);
      b.onclick = () => { view = k; store.set('strengthView', k); draw(); };
      pills.append(b);
    }
    hd.append(pills);
  }
  sec.append(hd, host, note);
  requestAnimationFrame(draw);
  return sec;
}

// The weights at a share of the one-rep max, estimated from a rep max. A
// weight picked here shows its plates (pick).
function percentSection(r, pick) {
  const max = oneRepMax(r.best.kg, r.reps);
  const sec = h('section', { class: 'wo-sec' });
  const hd = h('div', { class: 'wo-sec-head' });
  hd.append(h('h3', {}, t('Training weights')), h('span', { class: 'muted' }, r.reps > 1
    ? t('from an estimated 1 RM of {value}', { value: fmtKg(loadable(max)) })
    : t('share of your 1 RM')));
  const grid = h('div', { class: 'lift-pct' });
  for (const p of percentages(max)) {
    const cell = h('button', { class: 'lift-pct-cell', type: 'button', 'aria-label': t('{pct} %: {value}. Show the plates', { pct: p.pct, value: fmtKg(p.kg) }) });
    cell.append(h('span', { class: 'k' }, `${p.pct} %`), h('span', { class: 'v' }, fmtKg(p.kg)));
    cell.onclick = () => pick(p.kg);
    grid.append(cell);
  }
  sec.append(hd, grid);
  return sec;
}

const BARS = [20, 15, 10];

// Which plates go on each side of the bar for a weight: the bar (remembered)
// and the weight can change; the picture shows one side, the heaviest plate
// next to the collar.
function plateSection(start) {
  const sec = h('section', { class: 'wo-sec' });
  const hd = h('div', { class: 'wo-sec-head' });
  hd.append(h('h3', {}, t('Plates')), h('span', { class: 'muted' }, t('per side')));
  let bar = BARS.includes(store.get('bar')) ? store.get('bar') : 20;
  const controls = h('div', { class: 'plate-controls' });
  const weight = h('input', { class: 'field', type: 'number', min: '0', max: '500', step: '0.5', inputmode: 'decimal', value: start, 'aria-label': t('Weight (kg)') });
  const wrap = h('label', { class: 'plate-weight' });
  wrap.append(weight, h('span', {}, 'kg'));
  const bars = h('div', { class: 'seg plate-bars', role: 'radiogroup', 'aria-label': t('Bar') });
  for (const b of BARS) {
    const btn = h('button', { class: 'seg-btn', type: 'button', role: 'radio', 'data-bar': b }, t('{kg} kg bar', { kg: b }));
    btn.onclick = () => {
      bar = b;
      store.set('bar', b);
      draw();
    };
    bars.append(btn);
  }
  controls.append(wrap, bars);
  const out = h('div', { class: 'plate-out' });
  sec.append(hd, controls, out);

  const draw = () => {
    for (const b of bars.children) b.setAttribute('aria-checked', String(Number(b.dataset.bar) === bar));
    const kg = Number(weight.value);
    if (!(kg > 0)) {
      out.replaceChildren();
      return;
    }
    const { side, loaded } = platesFor(kg, bar);
    const pic = h('div', { class: 'plate-pic', 'aria-hidden': 'true' });
    pic.append(h('span', { class: 'plate-sleeve' }));
    for (const p of side) {
      const plate = h('span', { class: 'plate', 'data-kg': p });
      plate.style.setProperty('--h', `${plateHeight(p)}%`); // not a style attribute: the CSP refuses those
      pic.append(plate);
    }
    const words = h('p', { class: 'plate-words' });
    words.textContent = kg < bar ? t('Less than the bar.')
      : side.length ? side.map((p) => nfKg.format(p)).join(' + ') + ' kg' : t('Just the bar.');
    if (Math.abs(loaded - kg) > 1e-9 && kg >= bar) words.append(h('span', { class: 'muted' }, ' · ' + t('loads {value}', { value: fmtKg(loaded) })));
    out.replaceChildren(pic, words);
  };
  weight.oninput = draw;
  draw();
  return {
    sec,
    show: (kg) => {
      weight.value = kg;
      draw();
      sec.scrollIntoView({ block: 'nearest' });
    },
  };
}

// Bumpers are all one size; change plates get smaller with their weight.
const plateHeight = (kg) => (kg >= 10 ? 100 : kg >= 5 ? 78 : kg >= 2.5 ? 62 : 50);

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
    const ready = readinessOn(e.date);
    const bw = bodyweight(e);
    const note = [gain != null && pb ? gainKg(gain) : '', bw ? t('{x}× BW', { x: nfX.format(bw) }) : '', ready != null ? t('Readiness {value}', { value: ready }) : '', e.note].filter(Boolean).join(' · ');
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
      notifyBests();
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
