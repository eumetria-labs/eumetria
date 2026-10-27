// Refresh button for the site owner. Only the self-hosted server (eumetria
// -serve with ADMIN_TOKEN) has the sync API, so the button stays hidden until
// an admin token is stored on this device: open the page once with "#admin"
// and enter the token. Visitors never see it.
//
// The button syncs the recent days; its menu also offers a full refresh of
// the whole history (POST api/sync?all=1).

import { $, hashParam, setHashParam } from './dom.js';
import { store } from './state.js';
import { t, tn } from './i18n.js';

const TOKEN_ITEM = 'admin-token';
const POLL_MS = 2000;
const MAX_POLLS = 600; // 20 minutes; a full refresh with photos takes a few

let reload = async () => {};
let restore = () => {};

// initAdmin wires the button. onSynced reloads stats.json; onIdle puts the
// header's "Updated …" line back after a message.
export function initAdmin({ onSynced, onIdle }) {
  reload = onSynced;
  restore = onIdle;
  $('#sync-group').hidden = !token();
  $('#sync').addEventListener('click', () => runSync(false));
  initMenu();
  // "#admin" reads as an empty value. Typing it into the address bar of an
  // open page doesn't reload, hence the listener.
  if (hashParam('admin') != null) askToken();
  addEventListener('hashchange', () => { if (hashParam('admin') != null) askToken(); });
  if (token()) resume();
}

function token() {
  return store.get(TOKEN_ITEM);
}

function forget() {
  try { localStorage.removeItem(TOKEN_ITEM); } catch { /* storage unavailable */ }
  $('#sync-group').hidden = true;
}

/* ---------- menu ---------- */

function initMenu() {
  const more = $('#sync-more');
  const menu = $('#sync-menu');
  const items = () => [...menu.querySelectorAll('[role="menuitem"]')];
  const open = (focus) => {
    menu.hidden = false;
    // Opens to the left of the buttons, unless they sit at the left edge
    // (narrow phones), where it would leave the screen.
    menu.classList.remove('from-left');
    menu.classList.toggle('from-left', menu.getBoundingClientRect().left < 8);
    more.setAttribute('aria-expanded', 'true');
    if (focus) items()[0].focus();
  };
  const close = (refocus) => {
    if (menu.hidden) return;
    menu.hidden = true;
    more.setAttribute('aria-expanded', 'false');
    if (refocus) more.focus();
  };
  more.addEventListener('click', () => (menu.hidden ? open(true) : close()));
  menu.addEventListener('click', (e) => {
    const b = e.target.closest('[role="menuitem"]');
    if (!b) return;
    close(true);
    runSync(b.dataset.all === '1');
  });
  menu.addEventListener('keydown', (e) => {
    const list = items();
    const i = list.indexOf(document.activeElement);
    const next = { ArrowDown: i + 1, ArrowUp: i - 1, Home: 0, End: list.length - 1 }[e.key];
    if (next != null) {
      e.preventDefault();
      list[(next + list.length) % list.length].focus();
    } else if (e.key === 'Escape') {
      e.preventDefault();
      close(true);
    } else if (e.key === 'Tab') close();
  });
  document.addEventListener('click', (e) => { if (!$('#sync-group').contains(e.target)) close(); });
}

// The menu names the number of days a normal sync re-fetches.
function showRefreshDays(n) {
  if (n > 0) $('#sync-recent-note').textContent = tn(n, 'Re-fetches the last day. Takes seconds.', 'Re-fetches the last {n} days. Takes seconds.');
}

function api(path, method = 'GET', tok = token()) {
  return fetch('api/' + path, { method, cache: 'no-store', headers: { Authorization: 'Bearer ' + tok } });
}

function say(text) {
  $('#updated').textContent = text;
}

function setBusy(busy) {
  const btn = $('#sync');
  btn.classList.toggle('spinning', busy);
  btn.disabled = busy;
  btn.setAttribute('aria-busy', String(busy));
  $('#sync-more').disabled = busy;
}

const busyText = (all) => (all ? t('Refreshing the whole history from YAZIO… (a few minutes)') : t('Syncing with YAZIO…'));

// A sync can already be running (scheduled, or started in another tab).
async function resume() {
  try {
    const res = await api('status');
    if (res.status === 401) return forget();
    if (!res.ok) return;
    const st = await res.json();
    showRefreshDays(st.refresh_days);
    if (st.running) await follow(st.all);
  } catch { /* offline */ }
}

async function runSync(all) {
  setBusy(true);
  say(busyText(all));
  try {
    const res = await api(all ? 'sync?all=1' : 'sync', 'POST');
    if (res.status === 401) {
      forget();
      return done(t('Admin token no longer valid. Open #admin to enter it again.'));
    }
    if (res.status === 429) return done(t('Too many attempts. Try again in a minute.'));
    if (res.status !== 202 && res.status !== 409) return done(t('Sync could not start (HTTP {status}).', { status: res.status }));
    // 409: a sync was already running; follow that one instead.
    const st = await res.json().catch(() => ({}));
    await follow(res.status === 202 ? all : !!st.all);
  } catch {
    done(t('Sync failed: server not reachable.'));
  }
}

// follow polls until the running sync ends, then reloads the data.
async function follow(all) {
  setBusy(true);
  say(busyText(all));
  for (let i = 0; i < MAX_POLLS; i++) {
    await new Promise((r) => setTimeout(r, POLL_MS));
    let st;
    try {
      const res = await api('status');
      if (!res.ok) continue;
      st = await res.json();
    } catch { continue; }
    if (st.running) continue;
    if (st.last_error) return done(t('Sync failed: {error}', { error: st.last_error }));
    await reload();
    return done(all ? t('Full refresh done.') : t('Synced just now.'));
  }
  done(t('Sync is taking unusually long. Check the server logs.'));
}

function done(message) {
  setBusy(false);
  say(message);
  setTimeout(restore, 6000);
}

// askToken shows the token form; it is checked against the server before it
// is stored.
function askToken() {
  const form = $('#admin');
  const input = $('#admin-token');
  const error = $('#admin-error');
  const button = form.querySelector('.lock-btn');
  const close = () => {
    form.hidden = true;
    form.onsubmit = null;
    input.value = '';
    setHashParam('admin', null);
  };
  form.hidden = false;
  input.focus();
  $('#admin-cancel').onclick = close;
  form.onsubmit = async (e) => {
    e.preventDefault();
    const tok = input.value.trim();
    if (!tok) return;
    button.disabled = true;
    error.hidden = true;
    try {
      const res = await api('status', 'GET', tok);
      if (res.ok) {
        store.set(TOKEN_ITEM, tok);
        $('#sync-group').hidden = false;
        res.json().then((st) => showRefreshDays(st.refresh_days)).catch(() => {});
        close();
        return;
      }
      error.textContent = res.status === 401 ? t('Wrong token. Try again.')
        : res.status === 429 ? t('Too many attempts. Wait a minute.')
          : t('This site has no sync API (it only exists on the self-hosted server).');
    } catch {
      error.textContent = t('Server not reachable.');
    } finally {
      button.disabled = false;
    }
    error.hidden = false;
    input.select();
  };
}
