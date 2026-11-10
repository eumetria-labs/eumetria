// Search over the whole history (Ctrl/⌘+K or the magnifier in the header):
// foods, activities, personal bests (lifts, benchmark workouts), workout
// notes and days in one list. Arrow keys move,
// Enter opens the food's or activity's history window, the workout or the
// day. The input and list follow the WAI-ARIA combobox pattern.

import { actIcon, actLabel, dfLong, fmt, logged, parse } from './format.js';
import { liftBoard, wodBoard, workoutIcon, workoutName } from './health.js';
import { fmtKg, openLift } from './lifts.js';
import { fmtResult, openWod } from './wods.js';
import { $, h, emoji } from './dom.js';
import { state } from './state.js';
import { openDialog } from './dialog.js';
import { openDay } from './sheet.js';
import { openActivity, openFood } from './history.js';
import { openWorkout } from './workout.js';
import { dayActivities, matchRank, noteSnippet, searchAll, searchNotes } from './query.js';
import { t, tn } from './i18n.js';

const MAC = /Mac|iPhone|iPad/.test(navigator.platform);
let win = null;

export function initSearch() {
  const btn = $('#search');
  btn.title = t('Search ({keys})', { keys: MAC ? '⌘K' : t('Ctrl+K') });
  btn.addEventListener('click', () => openSearch());
  addEventListener('keydown', (e) => {
    if (e.key.toLowerCase() !== 'k' || !(e.ctrlKey || e.metaKey) || e.altKey || e.shiftKey) return;
    // Typing in a field keeps the browser's own Ctrl+K.
    if (e.target.closest?.('input, textarea, select, [contenteditable]') && !e.target.closest('.search-box')) return;
    if (!state.data || document.documentElement.dataset.locked) return;
    e.preventDefault();
    openSearch();
  });
}

// Shown once there is data to search.
export function enableSearch() {
  $('#search').hidden = false;
}

function openSearch() {
  if (win) return win.el.querySelector('input').select();
  const input = h('input', {
    class: 'field search-input', type: 'search', role: 'combobox', placeholder: t('Search foods, activities and days'),
    'aria-label': t('Search foods, activities and days'), 'aria-expanded': 'false', 'aria-controls': 'search-list',
    'aria-autocomplete': 'list', autocomplete: 'off', spellcheck: 'false',
  });
  const list = h('div', { class: 'search-list', id: 'search-list', role: 'listbox', 'aria-label': t('Results') });
  const status = h('p', { class: 'search-status muted', role: 'status' });
  const panel = h('div', { class: 'wo-panel search-box' });
  panel.append(input, list, status);
  win = openDialog({ cls: 'wo-overlay search-overlay', label: t('Search'), content: [panel], returnFocus: document.activeElement, onClose: () => { win = null; } });

  let options = [], active = -1;
  const setActive = (k) => {
    active = k;
    options.forEach((o, i) => o.el.setAttribute('aria-selected', String(i === k)));
    if (k < 0) return input.removeAttribute('aria-activedescendant');
    input.setAttribute('aria-activedescendant', options[k].el.id);
    options[k].el.scrollIntoView({ block: 'nearest' });
  };
  // The search window closes first, so the history or day opens on what
  // was there before (and focus returns there when it closes).
  const choose = (o) => {
    win?.close(true);
    o.open();
  };

  const draw = () => {
    const q = input.value;
    const r = searchAll(state.data.days, q);
    options = [];
    const groups = [
      ['foods', t('Foods'), r.foods.map((f) => ({ icon: '🍽️', label: f.name, note: `${fmt(f.n)}×`, open: () => openFood(f.name, document.activeElement) }))],
      ['activities', t('Activities'), r.activities.map((a) => ({ icon: actIcon(a.name), label: actLabel(a.name), note: `${fmt(a.n)}×`, open: () => openActivity(a.name, document.activeElement) }))],
      ['bests', t('Personal bests'), bestResults(q)],
      ['notes', t('Notes'), noteResults(q)],
      ['days', t('Days'), r.days.map((d) => ({ icon: '📅', label: dfLong.format(parse(d.date)), note: dayNote(d, q), open: () => openDay(d.date) }))],
    ];
    list.replaceChildren();
    for (const [key, title, items] of groups) {
      if (!items.length) continue;
      const id = 'search-g-' + key;
      const group = h('div', { role: 'group', 'aria-labelledby': id });
      group.append(h('div', { class: 'search-group', id, role: 'presentation' }, title));
      for (const it of items) {
        const k = options.length;
        const el = h('div', { class: 'search-opt', role: 'option', id: 'search-o-' + k, 'aria-selected': 'false' });
        const name = h('span', { class: 'name' });
        name.append(emoji(it.icon), h('span', { class: 'txt' }, it.label));
        el.append(name, h('span', { class: 'note' }, it.note));
        // mousedown would take focus from the input; the click still runs.
        el.addEventListener('mousedown', (e) => e.preventDefault());
        el.addEventListener('click', () => choose(it));
        el.addEventListener('mousemove', () => { if (active !== k) setActive(k); });
        options.push({ el, ...it });
        group.append(el);
      }
      list.append(group);
    }
    input.setAttribute('aria-expanded', String(options.length > 0));
    list.hidden = !options.length;
    status.textContent = !q.trim() ? t('Foods, activities and days, e.g. "banana", "running" or "oct 12".')
      : options.length ? tn(options.length, '{n} result', '{n} results') : t('Nothing found');
    status.classList.toggle('sr-only', options.length > 0);
    setActive(options.length ? 0 : -1);
  };

  input.addEventListener('input', draw);
  input.addEventListener('keydown', (e) => {
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault();
      if (options.length) setActive((active + (e.key === 'ArrowDown' ? 1 : -1) + options.length) % options.length);
    } else if (e.key === 'Enter' && active >= 0) {
      e.preventDefault();
      choose(options[active]);
    }
  });
  draw();
  input.focus();
}

// Lifts and benchmark workouts whose name fits the query, the closest
// first; each opens its window.
function bestResults(q) {
  if (!q.trim()) return [];
  const lifts = liftBoard(state.lifts, state.today).map((r) => ({
    name: r.lift, icon: '🏋️', label: r.reps > 1 ? `${r.lift} · ${r.reps} RM` : r.lift, note: fmtKg(r.best.kg), open: () => openLift(r.key, document.activeElement),
  }));
  // One entry per workout: its Rx best, else its scaled one.
  const seen = new Set();
  const wods = wodBoard(state.wods.workouts, state.wods.results, state.today).sort((a, b) => a.scaled - b.scaled).filter((r) => !seen.has(r.workout.id) && seen.add(r.workout.id)).map((r) => ({
    name: r.workout.name, icon: '⏱️', label: r.workout.name, note: r.best ? fmtResult(r.workout.scoring, r.best, true) : '', open: () => openWod(r.workout.id, document.activeElement),
  }));
  return [...lifts, ...wods].map((x) => ({ ...x, rank: matchRank(x.name, q) })).filter((x) => x.rank != null).sort((a, b) => a.rank - b.rank).slice(0, 8);
}

// The workouts whose notes fit the query.
function noteResults(q) {
  return searchNotes([...state.notes.values()], q).flatMap((n) => {
    const day = state.byDate.get(n.date);
    const w = day?.workouts?.find((x) => x.id === n.workout_id);
    if (!w) return [];
    return [{
      icon: workoutIcon(w.type), label: `${actLabel(workoutName(w.type))} · ${dfLong.format(parse(n.date))}`, note: noteSnippet(n.text, q),
      open: () => {
        openDay(n.date, 'activities');
        openWorkout(day, w.id, $('#modal .sheet-close'));
      },
    }];
  });
}

// What the day has that fits the query (foods, activities), or its calories.
function dayNote(d, q) {
  const names = [...(d.foods || []).map((f) => f.n), ...dayActivities(d).map((a) => actLabel(a.key))];
  const hits = [...new Set(names)].filter((n) => matchRank(n, q) != null);
  if (hits.length) return hits.slice(0, 2).join(', ') + (hits.length > 2 ? ', …' : '');
  return logged(d) ? `${fmt(d.energy)} kcal` : '';
}
