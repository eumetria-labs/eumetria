// The Profile / Settings view (account menu → Profile or Settings): one page
// over the dashboard with a switch between your profile and the settings, a
// list of sections (a sidebar on wide screens, a list to tap through on
// phones) and the chosen section. The sections are drawn by the modules
// that own them (profile.js, security.js, manage.js, settings.js,
// phases.js, layout.js).
//
// The address says what is open: "#profile=account", "#settings=google"
// ("#settings=" alone: the list, on phones). Opening the view adds one entry
// to the browser's history, so Back closes it; on phones opening a section
// from the list adds another, so Back goes to the list.
//
// Forms that keep settings mark themselves data-save: a change shows the save
// bar ("Unsaved changes · Discard · Save"), and leaving with unsaved changes
// asks first.

import { h, hashParam, setHashParam } from './dom.js';
import { state } from './state.js';
import { openDialog } from './dialog.js';
import { account, api } from './account.js';
import { icon } from './kit.js';
import { t, tn } from './i18n.js';
import { renderAccount, renderDevices } from './profile.js';
import { renderSignIn } from './security.js';
import { renderManage } from './manage.js';
import { renderSettings, stopSettings } from './settings.js';
import { renderPhases } from './phases.js';
import { renderLayout } from './layout.js';

const can = (scope) => !!account.user?.scopes?.includes(scope);
const admin = () => can('admin');
const seesPhases = () => can('view.nutrition') || can('view.health');

// Every section: where it lives, what it shows and who sees it. color is
// the icon's tile in the phone list.
const SECTIONS = [
  { side: 'profile', group: () => t('You'), key: 'account', label: () => t('Account'), icon: 'user', color: '#2a78d6',
    desc: () => t('How you appear in Eumetria, and where security alerts go.'), draw: renderAccount },
  { side: 'profile', group: () => t('Dashboard'), key: 'layout', label: () => t('Customize Dashboard'), icon: 'layout', color: '#1baf7a',
    desc: () => t('The order of the cards and which ones you see, on all your devices.'), draw: renderLayout },
  { side: 'profile', group: () => t('Dashboard'), key: 'phases', label: () => t('Phases & Events'), icon: 'flag', color: '#eb6834', when: seesPhases,
    desc: () => t('Cuts, bulks and competitions, shaded and marked on the charts.'), draw: renderPhases },

  { side: 'settings', group: () => t('Security'), key: 'sign-in', label: () => t('Sign-in'), icon: 'lock', color: '#5e6ad2',
    desc: () => t('Your password, passkeys and security keys, authenticator app and recovery codes.'), draw: renderSignIn },
  { side: 'settings', group: () => t('Security'), key: 'devices', label: () => t('Devices & API tokens'), icon: 'key', color: '#8a63d2',
    desc: () => t('Where you are signed in, and tokens for scripts.'), draw: renderDevices },
  ...[['users', () => t('Users'), 'users', '#2a78d6', () => t('Who has an account, and with which role.')],
    ['roles', () => t('Roles'), 'shield', '#1d8f6b', () => t('What each role may see and do.')],
    ['sessions', () => t('Sessions'), 'monitor', '#6b7280', () => t('Everyone’s signed-in devices.')],
    ['notes', () => t('Notes'), 'note', '#c27c0e', () => t('Notes whose workout is no longer there.')],
    ['audit', () => t('Audit log'), 'list', '#6b7280', () => t('Sign-ins, changes and refused attempts.')],
  ].map(([key, label, ic, color, desc]) => ({ side: 'settings', group: () => t('Users & Roles'), key, label, icon: ic, color, desc, when: admin,
    draw: (host) => renderManage(host, key) })),
  ...[['yazio', () => 'Yazio', 'food', '#1baf7a', () => t('Calories, meals, foods, weight, steps and activities.')],
    ['google', () => 'Google', 'pulse', '#e34948', () => t('Workouts, sleep and overnight measurements from a Fitbit or Pixel Watch, through the Google Health API.')],
    ['sync', () => t('Sync'), 'sync', '#2a78d6', () => t('When and how far back the data is fetched.')],
  ].map(([key, label, ic, color, desc]) => ({ side: 'settings', group: () => t('Data sources'), key, label, icon: ic, color, desc, when: admin,
    draw: (host, result) => renderSettings(host, key, result) })),
  ...[['general', () => t('General'), 'sliders', '#6b7280', () => t('The site’s name, address, language and time zone, and the scores.')],
    ['review', () => t('Weekly review'), 'spark', '#a24bc4', () => t('A review of each week, written by Claude.')],
    ['mail', () => t('E-mail'), 'mail', '#eb6834', () => t('Security alerts and links by e-mail.')],
    ['backups', () => t('Backups'), 'archive', '#4b5563', () => t('A daily archive on this server, and on Google Drive if you like.')],
  ].map(([key, label, ic, color, desc]) => ({ side: 'settings', group: () => t('Site'), key, label, icon: ic, color, desc, when: admin,
    draw: (host, result) => renderSettings(host, key, result) })),
];

const visible = (side) => SECTIONS.filter((s) => s.side === side && (!s.when || s.when()));
const narrow = matchMedia('(max-width: 899px)');

// Old addresses still lead somewhere: "#security", "#admin=roles", the
// settings tab "email".
const ALIASES = { email: 'mail', security: 'sign-in' };

let view = null; // the open view: { win, side, key, pushed, … }

// openPrefs opens the view on side ("profile" or "settings") and section key;
// without a key, phones show the list and wider screens the first section.
// result is how Connect Google ended, when it brought the browser back.
export function openPrefs(side = 'profile', key = '', { returnFocus, result } = {}) {
  key = ALIASES[key] ?? key;
  if (view) return go(side, key, { result, push: false });
  const box = h('div', { class: 'pf' });
  view = { side: '', key: null, pushed: 0, box, returnFocus };
  view.win = openDialog({ cls: 'pf-overlay', labelledBy: 'pf-section-title', content: [box], returnFocus, onClose: closed });
  // Escape closes, after asking when something isn't saved.
  view.win.el.addEventListener('keydown', (e) => {
    if (e.key === 'Escape' && view?.dirty && !confirm(t('Leave without saving your changes?'))) e.stopImmediatePropagation();
  }, true);
  // One history entry for the view: Back closes it. Opened from a link
  // ("#settings=google"), the entry before is the page without it.
  setHashParam('profile', null);
  setHashParam('settings', null);
  history.pushState({ prefs: true }, '', location.href);
  view.pushed = 1;
  go(side, key, { result, push: false });
}

// close leaves the view, back to where it was opened from.
function close() {
  if (!view) return;
  if (view.dirty && !confirm(t('Leave without saving your changes?'))) return;
  view.dirty = false;
  // Back past the entries the view added: popstate then closes it.
  if (view.pushed) return history.go(-view.pushed);
  view.win.close();
}

function closed() {
  stopSettings();
  setHashParam('profile', null);
  setHashParam('settings', null);
  view = null;
}

addEventListener('popstate', () => {
  if (!view) return;
  const side = hashParam('profile') != null ? 'profile' : hashParam('settings') != null ? 'settings' : '';
  view.pushed = Math.max(0, view.pushed - 1);
  if (!side) {
    view.dirty = false;
    view.pushed = 0;
    return view.win.close();
  }
  go(side, hashParam(side) || '', { push: false, fromHistory: true });
});
narrow.addEventListener('change', () => view && go(view.side, view.key || '', { push: false }));

// go shows section key of side; '' is the list on phones and the first
// section elsewhere. push adds a history entry (opening a section from the
// phone list).
function go(side, key, { result, push = false, fromHistory = false } = {}) {
  if (!view) return;
  if (view.dirty && !fromHistory && !confirm(t('Leave without saving your changes?'))) return;
  view.dirty = false;
  const list = visible(side);
  let sec = list.find((s) => s.key === key);
  if (!sec && (key || !narrow.matches)) sec = list[0];
  if (view.side !== side) view.box.replaceChildren(topBar(side), body(side));
  view.side = side;
  view.key = sec?.key || '';
  const hash = { profile: null, settings: null, [side]: view.key };
  if (push) {
    history.pushState({ prefs: true }, '', location.href);
    view.pushed++;
  }
  for (const [k, v] of Object.entries(hash)) setHashParam(k, v);
  stopSettings();
  view.box.dataset.mode = sec ? 'section' : 'list';
  for (const a of view.box.querySelectorAll('.pf-nav a')) {
    if (a.dataset.key === view.key) a.setAttribute('aria-current', 'page');
    else a.removeAttribute('aria-current');
  }
  const main = view.box.querySelector('.pf-main');
  if (!sec) {
    main.replaceChildren(phoneList(side));
    return;
  }
  const host = h('div', { class: 'pf-content' });
  const title = h('h1', { id: 'pf-section-title', tabindex: '-1' }, sec.label());
  const head = h('header', { class: 'pf-head' });
  if (narrow.matches) {
    const back = h('button', { class: 'pf-up', type: 'button' });
    back.append(icon('back', 16), side === 'profile' ? t('Profile') : t('Settings'));
    back.onclick = () => (view.pushed > 1 ? history.back() : go(side, '', { push: false }));
    head.append(back);
  } else head.append(h('p', { class: 'pf-crumb' }, sec.group()));
  head.append(title, h('p', { class: 'pf-desc' }, sec.desc()));
  main.replaceChildren(head, host, saveBar(host, () => go(side, sec.key, { push: false })));
  watch(host);
  sec.draw(host, result);
  title.focus({ preventScroll: true });
  main.scrollTop = 0;
  view.box.querySelector('.pf-scroll').scrollTop = 0;
}

/* ---------- the frame ---------- */

function topBar(side) {
  const bar = h('header', { class: 'pf-top' });
  const inner = h('div', { class: 'pf-top-inner' });
  const back = h('button', { class: 'pf-back', type: 'button' });
  back.append(icon('back', 18), h('span', {}, t('Dashboard')));
  back.onclick = close;
  const sw = h('nav', { class: 'pf-switch', 'aria-label': t('Profile or settings') });
  for (const [s, label] of [['profile', t('Profile')], ['settings', t('Settings')]]) {
    const a = h('a', { href: '#' + s + '=' }, label);
    if (s === side) a.setAttribute('aria-current', 'page');
    a.onclick = (e) => {
      e.preventDefault();
      if (s !== view.side) go(s, '', { push: false });
    };
    sw.append(a);
  }
  const who = h('div', { class: 'pf-who' });
  const text = h('div', { class: 'who-text' });
  text.append(h('b', {}, account.user?.name || ''), h('span', {}, roleLabel(account.user?.role)));
  who.append(text, face());
  inner.append(back, sw, who);
  bar.append(inner);
  return bar;
}

export const roleLabel = (role) => ({ admin: t('Admin'), viewer: t('Viewer') })[role] || role || '';

// face is the user's picture, or the first letter of their name.
export function face(cls = 'avatar') {
  const el = h('span', { class: cls, 'aria-hidden': 'true' });
  if (account.user?.avatar) el.append(h('img', { src: account.user.avatar, alt: '' }));
  else el.textContent = (account.user?.name || '?').slice(0, 1).toUpperCase();
  return el;
}

function body(side) {
  const wrap = h('div', { class: 'pf-scroll' });
  const inner = h('div', { class: 'pf-body' });
  const nav = h('nav', { class: 'pf-nav', 'aria-label': side === 'profile' ? t('Profile') : t('Settings') });
  for (const [group, items] of grouped(side)) {
    const g = h('div', { class: 'pf-group' });
    const ul = h('ul');
    for (const s of items) {
      const a = h('a', { href: `#${side}=${s.key}`, 'data-key': s.key });
      a.append(icon(s.icon), h('span', {}, s.label()), h('i', { class: 'dot', hidden: '' }));
      a.onclick = (e) => {
        e.preventDefault();
        go(side, s.key, { push: false });
      };
      ul.append(h('li'));
      ul.lastChild.append(a);
    }
    g.append(h('h3', {}, group), ul);
    nav.append(g);
  }
  inner.append(nav, h('div', { class: 'pf-main' }));
  wrap.append(inner);
  loadStatus(side, inner);
  return wrap;
}

function grouped(side) {
  const out = new Map();
  for (const s of visible(side)) {
    const g = s.group();
    if (!out.has(g)) out.set(g, []);
    out.get(g).push(s);
  }
  return out;
}

// The phone list: the sections in groups, each with its state.
function phoneList(side) {
  const wrap = h('div', { class: 'pf-list' });
  wrap.append(h('h1', { id: 'pf-section-title', tabindex: '-1' }, side === 'profile' ? t('Profile') : t('Settings')));
  for (const [group, items] of grouped(side)) {
    const sec = h('section');
    const ul = h('ul');
    for (const s of items) {
      const a = h('a', { href: `#${side}=${s.key}`, 'data-key': s.key });
      const tile = h('span', { class: 'ic' });
      tile.style.background = s.color; // not a style attribute: the CSP forbids those
      tile.append(icon(s.icon, 17));
      const chev = icon('chev', 14);
      chev.classList.add('chev');
      a.append(tile, h('span', { class: 'name' }, s.label()), h('span', { class: 'status' }), chev);
      a.onclick = (e) => {
        e.preventDefault();
        go(side, s.key, { push: true });
      };
      ul.append(h('li'));
      ul.lastChild.append(a);
    }
    sec.append(h('h3', {}, group), ul);
    wrap.append(sec);
  }
  if (view.status) paintStatus(wrap, view.status);
  return wrap;
}

/* ---------- each section's state, at a glance ---------- */

// loadStatus fetches what the sidebar and the phone list say about each
// section: "2 passkeys", "Connected", a dot where something needs attention.
async function loadStatus(side, root) {
  const out = {};
  view.status = out;
  const get = (path) => api(path).then((r) => (r.ok ? r.json() : null), () => null);
  const jobs = [];
  if (side === 'settings') {
    jobs.push(get('security').then((s) => {
      if (!s) return;
      const n = s.keys?.length || 0;
      if (s.required && !n && !s.totp) out['sign-in'] = ['warn', t('Set up a second factor')];
      else out['sign-in'] = ['', n ? tn(n, '{n} passkey or key', '{n} passkeys or keys') : s.totp ? t('Authenticator app') : t('Password only')];
    }));
    jobs.push(get('sessions').then((l) => { if (l) out.devices = ['', tn(l.length, '{n} device', '{n} devices')]; }));
    if (admin()) {
      jobs.push(get('admin/users').then((l) => { if (l) out.users = ['', String(l.length)]; }));
      jobs.push(get('admin/settings').then((d) => {
        if (!d) return;
        const v = (k) => d.settings[k] || {};
        const secretSet = (k) => ['set', 'env'].includes(v(k).state);
        out.yazio = v('yazio_email').value && secretSet('yazio_password') ? ['ok', t('Set up')] : ['off', t('Not set up')];
        const g = d.google || {};
        out.google = g.connected ? (g.missing?.length ? ['warn', t('Partly connected')] : ['ok', t('Connected')]) : ['off', t('Not connected')];
        if (v('sync_interval').value) out.sync = ['', t('Every {every}', { every: v('sync_interval').value })];
        out.review = /^(1|t|true)$/i.test(v('review').value) ? ['ok', t('On')] : ['off', t('Off')];
        const m = d.mail || {};
        out.mail = !v('smtp_host').value ? ['off', t('Off')] : m.last_error ? ['warn', t('Last mail failed')] : ['ok', t('On')];
        out.backups = /^(1|t|true)$/i.test(v('backups').value) ? ['ok', t('Daily')] : ['off', t('Off')];
      }));
    }
  } else {
    if (seesPhases()) {
      const running = state.phases?.filter((p) => p.kind !== 'event' && (!p.end || p.end >= (state.today || ''))).length;
      if (running) out.phases = ['', tn(running, '{n} running', '{n} running')];
    }
  }
  await Promise.all(jobs);
  if (view?.status !== out) return;
  paintStatus(root, out);
  if (view?.box) paintStatus(view.box, out);
}

function paintStatus(root, status) {
  for (const a of root.querySelectorAll('a[data-key]')) {
    const st = status[a.dataset.key];
    const dot = a.querySelector('.dot');
    if (dot) {
      dot.className = 'dot ' + (st?.[0] || '');
      dot.hidden = !st || !['ok', 'warn', 'bad'].includes(st[0]);
    }
    const text = a.querySelector('.status');
    if (text) {
      text.replaceChildren();
      if (st?.[0] === 'warn') text.append(h('i', { class: 'dot warn', 'aria-hidden': 'true' }));
      if (st) text.append(st[1]);
    }
  }
}

/* ---------- unsaved changes ---------- */

// The current values of a section's data-save forms, to tell a change.
function snapshot(host) {
  return JSON.stringify([...host.querySelectorAll('form[data-save]')].map((f) =>
    [...f.elements].filter((el) => !['submit', 'button', 'fieldset'].includes(el.type)).map(valueOf)));
}

// watch notices edits in the section's forms; a section that draws itself
// again (after saving) starts afresh.
function watch(host) {
  let base = snapshot(host);
  const check = () => {
    if (!view) return;
    view.dirty = snapshot(host) !== base;
    host.closest('.pf-main')?.querySelector('.savebar')?.toggleAttribute('hidden', !view.dirty);
    for (const f of host.querySelectorAll('form[data-save]')) {
      for (const el of f.elements) el.classList?.toggle('changed', f._initial?.get(el) !== undefined && f._initial.get(el) !== valueOf(el));
    }
  };
  const remember = () => {
    base = snapshot(host);
    for (const f of host.querySelectorAll('form[data-save]')) f._initial = new Map([...f.elements].map((el) => [el, valueOf(el)]));
    check();
  };
  host.addEventListener('input', check);
  host.addEventListener('change', check);
  // A form that saved without being drawn again (Profile → Account).
  host.addEventListener('saved', remember);
  let queued = false;
  new MutationObserver(() => {
    if (queued) return;
    queued = true;
    queueMicrotask(() => { queued = false; remember(); });
  }).observe(host, { childList: true });
  remember();
}
const valueOf = (el) => (el.type === 'checkbox' ? el.checked : el.value);

// saveBar saves every changed form of the section at once (a section may
// offer host._saveAll for that), or throws the changes away.
function saveBar(host, redraw) {
  const bar = h('div', { class: 'savebar', role: 'region', 'aria-label': t('Unsaved changes'), hidden: '' });
  const discard = h('button', { class: 'btn quiet', type: 'button' }, t('Discard'));
  const save = h('button', { class: 'btn primary', type: 'button' }, t('Save'));
  discard.onclick = () => {
    view.dirty = false;
    redraw();
  };
  save.onclick = async () => {
    save.disabled = true;
    try {
      if (host._saveAll) await host._saveAll();
      else for (const f of host.querySelectorAll('form[data-save]')) f.requestSubmit();
    } finally {
      save.disabled = false;
    }
  };
  bar.append(h('span', {}, t('Unsaved changes')), discard, save);
  return bar;
}

/* ---------- addresses ---------- */

// openFromHash opens the view an address asks for, also the old ones:
// "#profile", "#security", "#settings=google", "#admin=roles". It answers
// whether it did.
export function openFromHash() {
  const result = hashParam('google') || '';
  if (result) setHashParam('google', null);
  if (hashParam('security') != null) {
    setHashParam('security', null);
    openPrefs('settings', 'sign-in');
    return true;
  }
  const adminTab = hashParam('admin');
  if (adminTab != null) {
    setHashParam('admin', null);
    openPrefs('settings', adminTab || 'users');
    return true;
  }
  for (const side of ['profile', 'settings']) {
    const key = hashParam(side);
    if (key == null) continue;
    openPrefs(side, key, { result });
    return true;
  }
  return false;
}

// A section can ask to show another one (e.g. Backups → Google).
document.addEventListener('prefs-go', (e) => view && go(view.side, e.detail, { push: false }));

export const prefsOpen = () => !!view;
