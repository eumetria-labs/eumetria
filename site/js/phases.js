// Phases (a cut, a bulk, maintenance, or one's own) and events (a
// competition, a race), under Profile → Phases & Events. People note
// them themselves: the server keeps them (phases.go), view.nutrition or
// view.health reads them, edit.lifts writes them. The weight, calorie,
// macro, score and load charts shade the phases and mark the events
// (charts.js); the Today card names the running phase and the next event.

import { addDays, dfShort, fmt, parse, iso, signed } from './format.js';
import { $, emoji, h } from './dom.js';
import { openDialog } from './dialog.js';
import { api, reason } from './account.js';
import { mayEditLifts } from './lifts.js';
import { state } from './state.js';
import { openPrefs } from './prefs.js';
import { KINDS, isEvent, phaseName, phaseOn, phaseSummary } from './phase.js';
import { t, tn } from './i18n.js';

const readable = (scopes) => !scopes || scopes.includes('view.nutrition') || scopes.includes('view.health');

// The phases that go with data. Plain files (no server) read phases.json,
// which -demo writes. Offline the ones already here stay.
export async function loadPhases(data) {
  const ok = readable(data.scopes);
  if (!ok) {
    state.phases = [];
    return;
  }
  let res = await api('phases').catch(() => null);
  if (res?.status === 404) res = await fetch('phases.json', { cache: 'no-cache' }).catch(() => null);
  if (res?.status === 404) {
    state.phases = [];
    state.phasesLoaded = true;
    loaded();
    return;
  }
  if (!res?.ok) {
    console.error('loading the phases failed', res?.status);
    loaded();
    return;
  }
  state.phases = await res.json();
  state.phasesLoaded = true;
  loaded();
}

// Profile → Phases & Events can be open before the phases are here (a link
// to it): it waits for this.
let tried = false;
const loaded = () => {
  tried = true;
  document.dispatchEvent(new Event('phases-loaded'));
};

// Charts and the Today card are drawn again after a change here.
const changed = () => document.dispatchEvent(new Event('phases-change'));

const sortPhases = () => state.phases.sort((a, b) => a.start.localeCompare(b.start) || a.id - b.id);

// A day with its year only when that isn't this year.
const day = (d) => (d.slice(0, 4) === (state.today || '').slice(0, 4) ? dfShort.format(parse(d)) : `${dfShort.format(parse(d))} ${d.slice(0, 4)}`);
const daysBetween = (a, b) => Math.round((parse(b) - parse(a)) / 864e5);

const notedDates = () => new Set([...state.lifts.map((e) => e.date), ...state.wods.results.map((r) => r.date)]);

/* ---------- the Today card ---------- */

// The running phase ("Maintain · day 71") and the next event within 90
// days ("Open 26.1 in 24 days"), as one line under the Today card's title:
// they hold for weeks, so they're status, not highlights. It opens
// Profile → Phases & Events.
export function renderPhaseStatus() {
  const host = $('#today-status');
  const today = state.today;
  const parts = [];
  const running = today && phaseOn(state.phases, today, today);
  if (running) parts.push(`${KINDS[running.kind].icon} ${phaseName(running)} · ${t('day {n}', { n: fmt(daysBetween(running.start, today) + 1) })}`);
  const next = today && state.phases.find((p) => isEvent(p) && p.start >= today && daysBetween(today, p.start) <= 90);
  if (next) {
    const n = daysBetween(today, next.start);
    parts.push(`🏁 ${phaseName(next)} ${n === 0 ? t('today') : tn(n, 'in {n} day', 'in {n} days')}`);
  }
  host.hidden = !parts.length;
  if (!parts.length) return;
  // Each part stays on one line; a narrow screen breaks between them.
  const spans = parts.map((p) => h('span', { class: 'st-part' }, p));
  spans.at(-1).append(' ', h('span', { 'aria-hidden': 'true' }, '›'));
  host.replaceChildren(...spans.flatMap((sp, i) => (i ? [' · ', sp] : [sp])));
  host.title = t('Phases & Events');
  host.onclick = () => openPrefs('profile', 'phases', { returnFocus: host });
}

/* ---------- Profile → Phases & Events ---------- */

// renderPhases draws the phases and events into body, with their numbers;
// people who may write lifts add and change them here.
export function renderPhases(body) {
  const card = (title, list) => {
    const sec = h('section', { class: 'wo-sec' });
    const head = h('div', { class: 'wo-sec-head' });
    head.append(h('h3', {}, title));
    sec.append(head, list);
    return sec;
  };
  const draw = () => {
    const edit = mayEditLifts();
    const today = state.today || iso(new Date());
    const phases = state.phases.filter((p) => !isEvent(p)).reverse();
    const events = state.phases.filter(isEvent).reverse();
    const parts = [];
    if (edit) {
      const add = h('div', { class: 'sec-row' });
      const addPhase = h('button', { class: 'pill-btn', type: 'button' }, '＋ ' + t('Phase'));
      const addEvent = h('button', { class: 'pill-btn', type: 'button' }, '＋ ' + t('Event'));
      addPhase.onclick = () => openPhaseForm({ kind: 'cut', returnFocus: addPhase, after: draw });
      addEvent.onclick = () => openPhaseForm({ kind: 'event', returnFocus: addEvent, after: draw });
      add.append(addPhase, addEvent);
      parts.push(add);
    }
    if (!state.phasesLoaded && !tried) {
      document.addEventListener('phases-loaded', () => { if (body.isConnected) draw(); }, { once: true });
      parts.push(h('p', { class: 'empty-tab' }, t('Loading…')));
    } else if (!state.phases.length) {
      parts.push(h('p', { class: 'empty-tab' }, !state.phasesLoaded ? t('The phases could not be loaded.')
        : edit ? t('No phases yet. Note a cut, a bulk or a competition to see it on the charts.') : t('No phases yet.')));
    }
    const byDate = state.byDate || new Map();
    const noted = notedDates();
    const item = (p, lines) => {
      const li = h('li', { class: 'phase-item ph-' + p.kind });
      const top = h('div', { class: 'phase-top' });
      const name = h('b', {});
      name.append(emoji(KINDS[p.kind]?.icon || '📌'), phaseName(p));
      top.append(name);
      if (edit) {
        const b = h('button', { class: 'pill-btn small', type: 'button', 'aria-label': t('Change {what}', { what: phaseName(p) }) }, t('Change'));
        b.onclick = () => openPhaseForm({ entry: p, returnFocus: b, after: draw });
        top.append(b);
      }
      li.append(top, ...lines.filter(Boolean).map((l) => h('p', { class: 'muted phase-line' }, l)));
      if (p.note) li.append(h('p', { class: 'phase-note' }, p.note));
      return li;
    };
    if (phases.length) {
      const ul = h('ul', { class: 'phase-list' });
      ul.append(...phases.map((p) => {
        const s = phaseSummary(p, byDate, today, noted);
        const kind = p.label ? KINDS[p.kind]?.label : '';
        const when = p.end ? `${day(p.start)} – ${day(p.end)}` : t('since {date}', { date: day(p.start) });
        const facts = [
          s.weight && `${signed(s.weight.change, 1)} kg`,
          s.energy != null && t('Ø {kcal} kcal', { kcal: fmt(s.energy) }),
          s.protein != null && t('Ø {g} g protein', { g: fmt(s.protein) }),
          s.perWeek != null && t('{n} training days a week', { n: fmt(s.perWeek, 1) }),
        ].filter(Boolean);
        return item(p, [[kind, when, tn(s.days, '{n} day', '{n} days')].filter(Boolean).join(' · '), facts.join(' · ')]);
      }));
      parts.push(card(t('Phases'), ul));
    }
    if (events.length) {
      const ul = h('ul', { class: 'phase-list' });
      ul.append(...events.map((p) => {
        const n = daysBetween(today, p.start);
        const rel = n > 0 ? tn(n, 'in {n} day', 'in {n} days') : n === 0 ? t('today') : tn(-n, '{n} day ago', '{n} days ago');
        return item(p, [`${day(p.start)} · ${rel}`]);
      }));
      parts.push(card(t('Events'), ul));
    }
    body.replaceChildren(...parts);
  };
  draw();
}

/* ---------- the form ---------- */

const ERRORS = {
  bad_phase: () => t('Check the entry: a phase starts by today and ends on or after its start; an event and "Other" need a name.'),
  not_found: () => t('This entry is no longer there.'),
};

async function refused(res) {
  if (!res) return t('Server not reachable.');
  if (res.status === 401) return t('You are signed out. Reload the page.');
  return ERRORS[await reason(res)]?.() || t('That didn’t work. Try again.');
}

// The form for a new phase or event (kind to start with) or for changing
// one (entry). after runs once the phases changed.
export function openPhaseForm({ entry, kind, returnFocus, after }) {
  const event = entry ? isEvent(entry) : kind === 'event';
  const close = h('button', { class: 'sheet-close', type: 'button', 'aria-label': t('Close') }, '×');
  const head = h('div', { class: 'sheet-head wo-head' });
  const titles = h('div', { class: 'wo-titles' });
  titles.append(h('h2', { id: 'phase-form-title' }, event ? (entry ? t('Change event') : t('New event')) : entry ? t('Change phase') : t('New phase')));
  head.append(titles, close);

  const form = h('form', { class: 'wo-body lift-form' });
  const field = (label, input, hint) => {
    const wrap = h('label', { class: 'lift-field' });
    wrap.append(h('span', { class: 'lbl' }, label), input);
    if (hint) wrap.append(h('span', { class: 'muted hint' }, hint));
    form.append(wrap);
    return input;
  };
  const today = state.today || iso(new Date());
  const v = entry || { kind, label: '', start: today, end: '', note: '' };
  let kindSel = null;
  if (!event) {
    kindSel = h('select', { class: 'field' });
    kindSel.append(...['cut', 'bulk', 'maintain', 'other', 'vacation'].map((k) => {
      const o = h('option', { value: k }, `${KINDS[k].icon} ${KINDS[k].label}`);
      o.selected = k === v.kind;
      return o;
    }));
    field(t('Kind'), kindSel);
  }
  const label = field(t('Name'), h('input', { class: 'field', type: 'text', maxlength: '48', autocomplete: 'off', value: v.label || '',
    placeholder: event ? 'Open 26.1' : t('optional') }), event ? '' : t('Shown instead of the kind, e.g. "Winter cut"'));
  if (event) label.required = true;
  const start = field(event ? t('Date') : t('Start'), h('input', { class: 'field', type: 'date', required: '', value: v.start,
    max: event ? iso(addDays(parse(today), 730)) : today }));
  let end = null;
  if (!event) end = field(t('End'), h('input', { class: 'field', type: 'date', value: v.end || '' }), t('Leave empty while it runs'));
  // A vacation is planned ahead, and ends.
  const vacation = () => {
    const on = kindSel?.value === 'vacation';
    start.max = on ? iso(addDays(parse(today), 730)) : today;
    if (end) end.required = on;
  };
  if (kindSel) kindSel.addEventListener('change', vacation);
  vacation();
  const note = field(t('Note'), h('input', { class: 'field', type: 'text', maxlength: '200', value: v.note || '', placeholder: t('optional') }));

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
  const dlg = openDialog({ cls: 'wo-overlay', labelledBy: 'phase-form-title', content: [panel], returnFocus });
  close.onclick = cancel.onclick = () => dlg.close();
  (kindSel || label).focus();

  const send = async (button, run, update) => {
    for (const b of [save, cancel, del]) if (b) b.disabled = true;
    error.hidden = true;
    const res = await run().catch(() => null);
    if (res?.ok || (res?.status === 404 && button === del)) {
      await update(res);
      sortPhases();
      dlg.close();
      changed();
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
    const body = { kind: event ? 'event' : kindSel.value, label: label.value, start: start.value, end: end ? end.value : '', note: note.value };
    send(save,
      () => (entry ? api('phases/' + entry.id, 'PUT', body) : api('phases', 'POST', body)),
      async (res) => {
        const saved = await res.json();
        state.phases = entry ? state.phases.map((x) => (x.id === saved.id ? saved : x)) : [...state.phases, saved];
      });
  };
  if (del) {
    del.onclick = () => {
      if (!confirm(event ? t('Delete this event?') : t('Delete this phase?'))) return;
      send(del, () => api('phases/' + entry.id, 'DELETE'), () => {
        state.phases = state.phases.filter((x) => x.id !== entry.id);
      });
    };
  }
}
