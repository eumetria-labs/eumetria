// The page's frame around the cards: on wide screens a sidebar with the
// brand, the parts of the page, the watch and the account; on phones a tab
// bar that shows one part at a time ("#view=training") and a "More" card.
// Also the greeting, the "+ Log" menu, and card tabs that switch to their
// icons when their words don't fit.

import { $, h, hashParam, popupMenu, setHashParam } from './dom.js';
import { account, onUserChange } from './account.js';
import { state } from './state.js';
import { isBirthday, todayISO } from './format.js';
import { mayCheckin, openCheckin } from './checkin.js';
import { t } from './i18n.js';
import { mayEditLifts, openLiftForm } from './lifts.js';
import { openResultForm, openWorkoutForm } from './wods.js';
import { openPhaseForm } from './phases.js';
import { openWorkout } from './workout.js';
import { openDialog } from './dialog.js';
import { openPrefs } from './prefs.js';
import { openYear } from './yearreview.js';
import { GROUP } from './layout.js';

const VIEWS = ['today', 'nutrition', 'body', 'training', 'more'];
const wide = matchMedia('(min-width: 900px)');

export function initShell() {
  for (const [id, g] of Object.entries(GROUP)) document.getElementById(id)?.setAttribute('data-group', g);
  initSide();
  initTabbar();
  initQuick();
  initMore();
  fitTabs();
  onUserChange(showUser);
}

/* ---------- greeting ---------- */

// greeting is "Good afternoon, Anna": the first name from the profile, else
// the account name; without accounts (plain files) the site's title.
export function greeting(title) {
  const u = account.user;
  if (!u) return title;
  const hour = new Date().getHours();
  const name = u.first_name || u.name;
  // On their birthday (the site's "today", from the profile's date of birth).
  if (isBirthday(u.birth_date, state.today || todayISO())) return t('Happy birthday, {name}! 🎂', { name });
  return hour < 5 || hour >= 18 ? t('Good evening, {name}', { name }) : hour < 12 ? t('Good morning, {name}', { name }) : t('Good afternoon, {name}', { name });
}

// showGreeting puts the greeting in the header; on a birthday it is set off.
export function showGreeting(title) {
  const el = $('#greeting');
  const text = greeting(title);
  const birthday = !!account.user && isBirthday(account.user.birth_date, state.today || todayISO());
  el.classList.toggle('birthday', birthday);
  if (!birthday) return (el.textContent = text);
  // The words in colour; the cake keeps its own.
  const [words, cake] = text.split(/ (?=🎂)/);
  el.replaceChildren(h('span', { class: 'bday' }, words), ...(cake ? [' ', cake] : []));
}

/* ---------- sidebar ---------- */

// The watch and the account sit in the sidebar's foot on wide screens and
// in the header on phones: moved, not copied, so there is one of each.
function initSide() {
  // In the sidebar the account button is the whole row: picture, name and role.
  const me = h('div', { class: 'side-me' });
  $('#side-foot').append(me);
  const place = () => {
    if (wide.matches) {
      $('#side-foot').prepend($('#device'));
      me.prepend($('#account'));
      $('#search').before($('#quick'));
    } else {
      // Phones: the watch and you at the top right, next to the greeting.
      $('#top-me').append($('#device'), $('#account'));
      // Out of the header: its backdrop filter would pin a fixed button to it.
      document.body.append($('#quick'));
    }
  };
  place();
  wide.addEventListener('change', place);
}

function showUser(user) {
  $('#side-name').textContent = user.first_name || user.name;
  $('#side-role').textContent = $('#account-role').textContent;
  showGreeting(state.data?.title || 'Eumetria');
  showQuick();
}

/* ---------- phones: the tab bar ---------- */

function initTabbar() {
  const bar = $('#tabbar');
  bar.hidden = false;
  bar.addEventListener('click', (e) => {
    const a = e.target.closest('a[data-view]');
    if (!a) return;
    e.preventDefault();
    setView(a.dataset.view);
    scrollTo({ top: 0 });
  });
  setView(hashParam('view') || 'today', false);
}

// setView shows one part of the page on phones; wide screens show all.
export function setView(view, remember = true) {
  if (!VIEWS.includes(view)) view = 'today';
  document.documentElement.dataset.view = view;
  for (const a of $('#tabbar').querySelectorAll('a')) {
    if (a.dataset.view === view) a.setAttribute('aria-current', 'page');
    else a.removeAttribute('aria-current');
  }
  if (remember) setHashParam('view', view === 'today' ? null : view);
  // Charts drawn while hidden have no size: they draw on resize (charts.js).
  dispatchEvent(new Event('resize'));
}

// The "More" part on phones: what lives elsewhere on wide screens.
function initMore() {
  const card = h('section', { class: 'card more-card', id: 'more-card', 'data-group': 'more' });
  const item = (label, note, run) => {
    const b = h('button', { class: 'more-item', type: 'button' });
    b.append(h('b', {}, label), h('span', {}, note));
    b.onclick = () => run(b);
    return b;
  };
  card.append(h('h2', {}, t('More')),
    item(t('Search'), t('Foods, activities and days'), () => $('#search').click()),
    item(t('Year in review'), t('The year’s numbers, bests and favourite foods'), (b) => openYear((state.today || '').slice(0, 4), b)),
    item(t('Profile'), t('Account, Customize Dashboard, Phases & Events'), (b) => openPrefs('profile', '', { returnFocus: b })),
    item(t('Settings'), t('Sign-in, devices and the site'), (b) => openPrefs('settings', '', { returnFocus: b })));
  $('#cards').after(card);
}

/* ---------- "+ Log" ---------- */

function initQuick() {
  popupMenu($('#quick-btn'), $('#quick-menu'), (item) => {
    const back = $('#quick-btn');
    if (item.dataset.log === 'lift') return openLiftForm({ returnFocus: back });
    if (item.dataset.log === 'wod') return pickWod(back);
    if (item.dataset.log === 'phase') return openPhaseForm({ kind: 'cut', returnFocus: back });
    if (item.dataset.log === 'checkin') return openCheckin({ returnFocus: back });
    if (item.dataset.log === 'note') {
      const day = state.byDate?.get(state.today);
      const w = day?.workouts?.at(-1);
      if (w) openWorkout(day, w.id, back);
    }
  });
}

// showQuick offers what this user may log; the button goes when nothing.
export function showQuick() {
  const lifts = mayEditLifts();
  const u = account.user;
  const notes = !!u?.scopes?.includes('edit.notes') && (!u.needs_second_factor || !!u.demo);
  const workout = state.byDate?.get(state.today)?.workouts?.length;
  const menu = $('#quick-menu');
  for (const b of menu.querySelectorAll('[data-log]')) b.hidden = b.dataset.log === 'note' ? !(notes && workout) : b.dataset.log === 'checkin' ? !mayCheckin() : !lifts;
  $('#quick').hidden = ![...menu.querySelectorAll('[data-log]')].some((b) => !b.hidden);
}

// pickWod asks which benchmark workout the result is for, or a new one.
function pickWod(returnFocus) {
  const panel = h('div', { class: 'wo-panel pick-panel' });
  const list = h('div', { class: 'pick-list' });
  const done = (fn) => () => { win.close(true); fn(); };
  for (const w of [...state.wods.workouts].sort((a, b) => a.name.localeCompare(b.name))) {
    const b = h('button', { class: 'pick-item', type: 'button' }, w.name);
    b.onclick = done(() => openResultForm({ workout: w, returnFocus }));
    list.append(b);
  }
  const add = h('button', { class: 'pill-btn', type: 'button' }, '＋ ' + t('New benchmark workout'));
  add.onclick = done(() => openWorkoutForm({ returnFocus, onCreated: (w) => openResultForm({ workout: w, returnFocus }) }));
  const head = h('div', { class: 'sheet-head wo-head' });
  const close = h('button', { class: 'sheet-close', type: 'button', 'aria-label': t('Close') }, '×');
  head.append(h('h2', { id: 'pick-title' }, t('Which workout?')), close);
  const body = h('div', { class: 'wo-body' });
  body.append(list, add);
  panel.append(head, body);
  const win = openDialog({ cls: 'wo-overlay', labelledBy: 'pick-title', content: [panel], returnFocus });
  close.onclick = () => win.close();
  (list.firstChild || add).focus();
}

/* ---------- card tabs that don't fit ---------- */

// A row of tabs whose words don't fit shows the tabs' icons instead, with
// the words still there for screen readers and as a tooltip.
// Measured in the next frame: switching to icons changes the row's size,
// which inside the observer's callback would set it off again.
const tabRows = new ResizeObserver((entries) => {
  const rows = entries.map((e) => e.target);
  requestAnimationFrame(() => rows.forEach(fit));
});
function fit(row) {
  for (const b of row.querySelectorAll('[role="tab"]')) b.title ||= b.querySelector('.tab-label')?.textContent.trim() || '';
  row.classList.remove('icons');
  if (row.scrollWidth > row.clientWidth + 1 && row.querySelector('.tab-icon, .tab-thumb')) row.classList.add('icons');
}
function fitTabs() {
  const watch = (root) => root.querySelectorAll?.('.tabs').forEach((row) => {
    if (row._fitted) return;
    row._fitted = true;
    tabRows.observe(row);
  });
  watch(document);
  new MutationObserver((list) => {
    for (const m of list) {
      for (const n of m.addedNodes) if (n.nodeType === 1) watch(n.matches?.('.tabs') ? n.parentElement : n);
      if (m.target.matches?.('.tabs')) fit(m.target);
    }
  }).observe(document.body, { childList: true, subtree: true });
}
