// Refresh button for admins: syncs the recent days now; its menu also
// offers a full refresh of the whole history (POST api/sync?all=1). The
// server lets only admins (and API tokens with the sync scope) start a sync.

import { $, popupMenu } from './dom.js';
import { account, api, signedIn } from './account.js';
import { t, tn } from './i18n.js';

const POLL_MS = 2000;
const MAX_POLLS = 600; // 20 minutes; a full refresh with photos takes a few

let reload = async () => {};
let restore = () => {};
let wired = false;

// initAdmin shows the button when the signed-in user may sync (an admin
// with a second factor), and runs again whenever that changes. onSynced
// reloads stats.json; onIdle puts the header's "Updated …" line back after
// a message.
export function initAdmin({ onSynced, onIdle }) {
  reload = onSynced;
  restore = onIdle;
  $('#sync-group').hidden = !account.user?.sync;
  if (!account.user?.sync || wired) return;
  wired = true;
  $('#sync').addEventListener('click', () => runSync(false));
  popupMenu($('#sync-more'), $('#sync-menu'), (b) => runSync(b.dataset.all === '1'));
  resume();
}

// The menu names the number of days a normal sync re-fetches.
function showRefreshDays(n) {
  if (n > 0) $('#sync-recent-note').textContent = tn(n, 'Re-fetches the last day. Takes seconds.', 'Re-fetches the last {n} days. Takes seconds.');
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
      done(t('Signed out. Sign in again to sync.'));
      return signedIn();
    }
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
