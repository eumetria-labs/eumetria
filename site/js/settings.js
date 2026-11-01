// The settings (account menu → Settings, admins only). Each tab saves on its
// own; the server checks every value (settings.go) and a change applies at
// once. A secret never comes back from the server: the page only learns
// whether one is set, and a field left empty keeps it. Changes need a sign-in
// within the last 10 minutes, as in Users & roles; the window offers to sign
// in again and come back here ("#settings=google").

import { LOCALE } from './format.js';
import { h, setHashParam, tabKeys } from './dom.js';
import { openDialog } from './dialog.js';
import { api, reason, signOut } from './account.js';
import { t } from './i18n.js';

const dfTime = new Intl.DateTimeFormat(LOCALE, { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' });

const TABS = [['general', () => t('General')], ['sync', () => t('Sync')], ['yazio', () => 'Yazio'], ['google', () => 'Google'], ['review', () => t('Weekly review')], ['backups', () => t('Backups')]];
const POLL_MS = 2000;

let win = null;
let polling = 0; // the status poll that may go on; closing the window ends it

// result is how Connect Google ended, when it brought the browser back here.
export function openSettings(returnFocus, tab = 'general', result = '') {
  win?.close(true);
  const close = h('button', { class: 'sheet-close', type: 'button', 'aria-label': t('Close') }, '×');
  const head = h('div', { class: 'sheet-head wo-head' });
  const titles = h('div', { class: 'wo-titles' });
  titles.append(h('h2', { id: 'set-title' }, t('Settings')), h('p', { class: 'muted' }, t('Changes apply at once.')));
  head.append(titles, close);
  const tabs = h('div', { class: 'tabs adm-tabs', role: 'tablist', 'aria-label': t('Settings') });
  const body = h('div', { class: 'wo-body sec-body', role: 'tabpanel' });
  const buttons = TABS.map(([key, label]) => {
    const b = h('button', { role: 'tab', type: 'button', 'data-tab': key }, label());
    b.onclick = () => show(key);
    return b;
  });
  tabs.append(...buttons);
  tabs.addEventListener('keydown', (e) => tabKeys(e, tabs));
  const panel = h('div', { class: 'wo-panel adm-panel' });
  panel.append(head, tabs, body);
  win = openDialog({ cls: 'wo-overlay', labelledBy: 'set-title', content: [panel], returnFocus, onClose: () => { polling++; } });
  close.onclick = () => win.close();

  function show(key) {
    for (const b of buttons) {
      const on = b.dataset.tab === key;
      b.setAttribute('aria-selected', String(on));
      b.tabIndex = on ? 0 : -1;
    }
    polling++;
    load(body, key, result);
    result = '';
  }
  show(TABS.some(([k]) => k === tab) ? tab : 'general');
}

async function load(body, tab, result) {
  body.dataset.tab = tab;
  const res = await api('admin/settings').catch(() => null);
  if (body.dataset.tab !== tab) return; // another tab was picked meanwhile
  if (!res?.ok) {
    const why = res ? await reason(res) : '';
    body.replaceChildren(h('p', { class: 'empty-tab' }, why === 'second_factor_required'
      ? t('Set up a second factor under Security first: admins need one.')
      : t('This could not be loaded.')));
    return;
  }
  const data = await res.json();
  if (tab === 'backups') {
    const runs = await api('admin/backups').catch(() => null);
    data.backups = runs?.ok ? await runs.json() : { runs: [] };
    if (body.dataset.tab !== tab) return;
  }
  body.replaceChildren(...{ general: generalTab, sync: syncTab, yazio: yazioTab, google: googleTab, review: reviewTab, backups: backupsTab }[tab](body, data, result));
}

function section(title, note) {
  const sec = h('section', { class: 'wo-sec' });
  const hd = h('div', { class: 'wo-sec-head' });
  hd.append(h('h3', {}, title));
  if (note) hd.append(h('span', { class: 'muted' }, note));
  sec.append(hd);
  return sec;
}

function showError(body, text) {
  body.querySelector('.adm-error')?.remove();
  body.prepend(h('p', { class: 'lock-error adm-error', role: 'alert' }, text));
}

function reauthNote(tab) {
  const box = h('div', { class: 'notice notice-row adm-error' });
  const again = h('button', { class: 'pill-btn', type: 'button' }, t('Sign in again'));
  again.onclick = () => {
    setHashParam('settings', tab);
    signOut(false);
  };
  box.append(h('span', {}, t('To change anything here, sign in again: your last sign-in was more than 10 minutes ago.')), again);
  return box;
}

// send sends a change and draws the tab again; a refusal is shown above it.
async function send(body, button, run, words) {
  body.querySelector('.adm-error')?.remove();
  button.disabled = true;
  try {
    const res = await run();
    if (res.ok) return await load(body, body.dataset.tab);
    const why = await (async () => { try { return await res.clone().json(); } catch { return {}; } })();
    if (why.error === 'reauth') return body.prepend(reauthNote(body.dataset.tab));
    if (why.error === 'no_key') return showError(body, t('Secrets can’t be stored: SETTINGS_KEY isn’t set on the server.'));
    showError(body, words?.(why) || (why.error === 'no_passphrase' && t('Set a passphrase first: nothing leaves the server unencrypted.')) || t('That didn’t work. Try again.'));
  } catch {
    showError(body, t('Server not reachable.'));
  } finally {
    if (document.contains(button)) button.disabled = false;
  }
}

/* ---------- a form of settings ---------- */

const SOURCE = { env: () => t('from the env file'), default: () => t('default') };

// A form saving the fields it holds, each { key, label, type, hint, options }.
// What didn't change isn't sent, and a secret field left empty keeps the secret.
function form(body, data, fields) {
  const el = h('form', { class: 'set-form' });
  const names = {};
  const inputs = fields.map((f) => {
    const item = data.settings[f.key];
    names[f.key] = f.label;
    const label = h('label', { class: 'set-field' });
    const head = h('span', { class: 'set-label' }, f.label);
    let input;
    if (item.secret) {
      const state = { set: t('set'), env: t('from the env file'), none: t('not set'), unreadable: t('can’t be read: was SETTINGS_KEY changed?') }[item.state];
      head.append(h('small', { class: item.state === 'unreadable' ? 'set-bad' : '' }, state));
      input = h('input', { class: 'field', type: 'password', autocomplete: 'new-password', placeholder: item.state === 'none' ? '' : t('Leave empty to keep it') });
      input.disabled = !data.can_encrypt;
    } else if (f.options) {
      input = h('select', { class: 'field' });
      for (const [value, text] of f.options) input.append(h('option', { value }, text));
      input.value = item.value ?? '';
    } else if (f.type === 'checkbox') {
      input = h('input', { type: 'checkbox' });
      input.checked = /^(1|t|true)$/i.test(item.value);
      label.classList.add('set-check');
    } else {
      input = h('input', { class: 'field', type: f.type || 'text', value: item.value ?? '' });
      if (f.list) input.setAttribute('list', f.list.id);
      if (f.placeholder) input.placeholder = f.placeholder;
      else if (item.source !== 'db' && SOURCE[item.source]) head.append(h('small', {}, SOURCE[item.source]()));
    }
    label.append(head, input);
    if (f.hint) label.append(h('span', { class: 'muted set-hint' }, f.hint));
    if (f.list) label.append(f.list);
    el.append(label);
    return { f, item, input };
  });
  const row = h('div', { class: 'sec-row' });
  const save = h('button', { class: 'pill-btn', type: 'submit' }, t('Save'));
  row.append(save);
  const reset = inputs.filter(({ item }) => item.secret && item.state === 'set');
  for (const { f } of reset) {
    const remove = h('button', { class: 'pill-btn', type: 'button' }, t('Remove {name}', { name: f.label }));
    remove.onclick = () => {
      if (confirm(t('Remove “{name}”?', { name: f.label }))) send(body, remove, () => api('admin/settings', 'PUT', { reset: [f.key] }));
    };
    row.append(remove);
  }
  el.append(row);
  el.onsubmit = (e) => {
    e.preventDefault();
    const values = {};
    for (const { f, item, input } of inputs) {
      const value = f.type === 'checkbox' ? String(input.checked) : input.value.trim();
      const was = f.type === 'checkbox' ? String(/^(1|t|true)$/i.test(item.value)) : item.value ?? '';
      if (item.secret ? value : value !== was) values[f.key] = item.secret ? input.value : value;
    }
    if (!Object.keys(values).length) return;
    send(body, save, () => api('admin/settings', 'PUT', { values }), (why) => why.error === 'bad_value' && t('Check this: {name}', { name: names[why.key] || why.key }));
  };
  return el;
}

function noKeyNote(data) {
  return data.can_encrypt ? [] : [h('p', { class: 'notice' }, t('SETTINGS_KEY isn’t set on the server, so secrets stay in the env file and can’t be changed here.'))];
}

/* ---------- general ---------- */

function generalTab(body, data) {
  const sec = section(t('General'));
  const zones = h('datalist', { id: 'set-zones' });
  for (const z of Intl.supportedValuesOf?.('timeZone') || []) zones.append(h('option', { value: z }));
  const custom = data.settings.timezone.source === 'db';
  const zoneNote = custom ? t('{zone}, set here', { zone: data.zone.name }) : t('{zone}, from TZ', { zone: data.zone.name });
  sec.append(form(body, data, [
    { key: 'title', label: t('Title') },
    { key: 'language', label: t('Default language'), hint: t('Until someone picks their own in the menu.'), options: [['', 'English'], ['de', 'Deutsch']] },
    { key: 'timezone', label: t('Time zone'), hint: t('What “today” is and when a week ends. In use: {zone}', { zone: zoneNote }), list: zones, placeholder: data.zone.name },
  ]));
  if (custom) {
    const back = h('button', { class: 'pill-btn set-back', type: 'button' }, t('Use TZ again'));
    back.onclick = () => send(body, back, () => api('admin/settings', 'PUT', { reset: ['timezone'] }));
    sec.append(back);
  }
  return [sec];
}

/* ---------- sync ---------- */

function statusLine(s) {
  const when = (d) => (d ? dfTime.format(new Date(d)) : t('never'));
  const parts = [t('Last success: {when}', { when: when(s.last_success) })];
  if (s.running) parts.unshift(s.job === 'backup' ? t('Backing up…') : s.job ? t('Writing reviews…') : t('Syncing…'));
  return parts.join(' · ');
}

function syncTab(body, data) {
  const sec = section(t('Sync'));
  sec.append(form(body, data, [
    { key: 'sync_interval', label: t('Time between syncs'), hint: t('At least 5 minutes, e.g. 30m or 2h.') },
    { key: 'refresh_days', label: t('Days to fetch again'), hint: t('Each sync fetches this many recent days again.'), type: 'number' },
    { key: 'since', label: t('First day'), hint: t('Empty: a year back.'), type: 'date' },
    { key: 'openfoodfacts', label: t('Look up foods on Open Food Facts'), hint: t('Only barcodes are sent.'), type: 'checkbox' },
  ]));
  const state = section(t('Status'));
  const line = h('p', { class: 'muted' }, statusLine(data.status));
  const error = h('p', { class: 'lock-error' }, data.status.last_error || '');
  error.hidden = !data.status.last_error;
  const row = h('div', { class: 'sec-row' });
  const now = h('button', { class: 'pill-btn', type: 'button' }, t('Sync now'));
  const all = h('button', { class: 'pill-btn', type: 'button' }, t('Full refresh'));
  const start = async (button, path) => {
    button.disabled = true;
    await api(path, 'POST').catch(() => null);
    poll(body, line, () => { button.disabled = false; load(body, 'sync'); });
  };
  now.onclick = () => start(now, 'sync');
  all.onclick = () => start(all, 'sync?all=1');
  now.disabled = all.disabled = data.status.running;
  row.append(now, all);
  state.append(line, error, row);
  if (data.status.running) poll(body, line, () => load(body, 'sync'));
  return [sec, state];
}

// poll shows the status until the running job ends.
function poll(body, line, done) {
  const mine = ++polling;
  const step = async () => {
    await new Promise((ok) => setTimeout(ok, POLL_MS));
    if (mine !== polling) return;
    const res = await api('status').catch(() => null);
    if (mine !== polling) return;
    const s = res?.ok ? await res.json() : null;
    if (s) line.textContent = s.job_total ? t('Writing reviews: {done} of {total}', { done: s.job_done, total: s.job_total }) : statusLine(s);
    if (s && !s.running) return done();
    step();
  };
  step();
}

/* ---------- yazio ---------- */

function yazioTab(body, data) {
  const sec = section('Yazio');
  sec.append(form(body, data, [
    { key: 'yazio_email', label: t('E-mail'), type: 'email' },
    { key: 'yazio_password', label: t('Password') },
    { key: 'yazio_client_id', label: t('OAuth client ID'), hint: t('The Yazio app’s, the same for everyone: see the README.') },
    { key: 'yazio_client_secret', label: t('OAuth client secret') },
  ]));
  const msg = h('p', { class: 'sec-msg', role: 'status', hidden: '' });
  const test = h('button', { class: 'pill-btn', type: 'button' }, t('Test connection'));
  test.onclick = async () => {
    test.disabled = true;
    msg.hidden = true;
    const res = await api('admin/settings/test/yazio', 'POST').catch(() => null);
    const ans = res?.ok ? await res.json() : null;
    test.disabled = false;
    msg.textContent = !ans ? t('Server not reachable.') : ans.ok ? t('Signed in to Yazio.') : t('That didn’t work: {why}', { why: ans.error });
    msg.classList.toggle('lock-error', !ans?.ok);
    msg.hidden = false;
  };
  const check = section(t('Test connection'), t('With the saved values.'));
  check.append(msg, test);
  return [...noKeyNote(data), sec, check];
}

/* ---------- google ---------- */

const GOOGLE_RESULTS = {
  connected: () => [t('Google is connected. The next sync brings the data.'), false],
  denied: () => [t('Google wasn’t given access.'), true],
  expired: () => [t('That took too long or came from elsewhere. Try again.'), true],
  failed: () => [t('Google refused. Check the client ID and secret, and the redirect URI.'), true],
};

function googleTab(body, data, result) {
  const g = data.google;
  const parts = noKeyNote(data);
  const said = GOOGLE_RESULTS[result]?.();
  if (said) parts.push(h('p', { class: said[1] ? 'lock-error' : 'sec-msg', role: 'status' }, said[0]));
  const client = section(t('OAuth client'), t('From the Google Cloud console.'));
  client.append(form(body, data, [
    { key: 'google_client_id', label: t('Client ID') },
    { key: 'google_client_secret', label: t('Client secret') },
  ]));
  client.append(h('p', { class: 'muted set-hint' }, t('Authorized redirect URI of the client, character for character:')), h('code', { class: 'sec-secret pf-secret' }, g.callback));
  const conn = section(t('Connection'), g.connected ? t('Connected') : t('Not connected'));
  if (g.issued_at) conn.append(h('p', { class: 'muted' }, t('Since {date}', { date: dfTime.format(new Date(g.issued_at)) })));
  if (g.missing?.length) conn.append(h('p', { class: 'lock-error' }, t('Not granted, so some data is missing: {scopes}', { scopes: g.missing.join(', ') })));
  const row = h('div', { class: 'sec-row' });
  const connect = h('button', { class: 'pill-btn', type: 'button' }, g.connected ? t('Connect again') : t('Connect Google'));
  connect.onclick = async () => {
    connect.disabled = true;
    body.querySelector('.adm-error')?.remove();
    const res = await api('google/connect', 'POST').catch(() => null);
    if (res?.ok) {
      location.href = (await res.json()).url;
      return;
    }
    connect.disabled = false;
    const why = res ? await reason(res) : '';
    if (why === 'reauth') return body.prepend(reauthNote('google'));
    showError(body, why === 'no_client' ? t('Save the client ID and secret first.') : t('That didn’t work. Try again.'));
  };
  row.append(connect);
  if (g.connected) {
    const off = h('button', { class: 'pill-btn', type: 'button' }, t('Disconnect'));
    off.onclick = () => {
      if (confirm(t('Disconnect Google? Syncing Fitbit data stops until you connect again.'))) send(body, off, () => api('google', 'DELETE'));
    };
    row.append(off);
  }
  conn.append(row);
  return [...parts, client, conn];
}

/* ---------- weekly review ---------- */

function reviewTab(body, data) {
  const sec = section(t('Weekly review'), t('Written by Claude after each week.'));
  sec.append(form(body, data, [
    { key: 'review', label: t('Write a review every week'), type: 'checkbox' },
    { key: 'anthropic_key', label: t('Anthropic API key') },
    { key: 'ai_model', label: t('Model') },
    { key: 'ai_effort', label: t('Effort'), options: ['low', 'medium', 'high', 'xhigh', 'max'].map((e) => [e, e]) },
    { key: 'ai_credit_usd', label: t('Credit bought (USD)'), hint: t('Warns admins when it runs low. Empty: no warning.'), type: 'number' },
  ]));
  const again = section(t('Write all reviews again'), t('In English and German, one after the other.'));
  const line = h('p', { class: 'muted' }, data.status.job_error || '');
  line.classList.toggle('lock-error', !!data.status.job_error);
  const run = h('button', { class: 'pill-btn', type: 'button' }, t('Write all reviews again'));
  run.disabled = data.status.running;
  run.onclick = async () => {
    if (!confirm(t('This asks Claude once per stored review, about $0.10 each. Go on?'))) return;
    run.disabled = true;
    body.querySelector('.adm-error')?.remove();
    const res = await api('admin/reviews/rewrite', 'POST').catch(() => null);
    if (res?.status === 409) {
      run.disabled = false;
      return showError(body, t('A sync is running. Try again when it’s done.'));
    }
    if (!res?.ok) {
      run.disabled = false;
      const why = res ? await reason(res) : '';
      if (why === 'reauth') return body.prepend(reauthNote('review'));
      return showError(body, t('That didn’t work. Try again.'));
    }
    poll(body, line, () => load(body, 'review'));
  };
  if (data.status.running) poll(body, line, () => load(body, 'review'));
  again.append(line, run);
  return [...noKeyNote(data), sec, again];
}

/* ---------- backups ---------- */

const size = (n) => `${(n / 1e6).toLocaleString(LOCALE, { maximumFractionDigits: 1 })} MB`;

// A long passphrase, easy to copy and to read aloud: no 0/O or 1/l.
function newPassphrase() {
  const letters = 'abcdefghjkmnpqrstuvwxyz23456789';
  const bytes = crypto.getRandomValues(new Uint8Array(30));
  const out = [...bytes].map((b) => letters[b % letters.length]).join('');
  return out.match(/.{6}/g).join('-');
}

function passphraseSection(body, data) {
  const item = data.settings.backup_passphrase;
  const on = item.state === 'set';
  const at = data.settings.backup_passphrase_at.value;
  const sec = section(t('Passphrase'), on ? t('Set on {date}', { date: at }) : t('Not set: local archives aren’t encrypted'));
  sec.append(h('p', { class: 'notice' }, t('Keep a copy in your password manager. If the server is lost, its copy is too, and without the passphrase the backups can’t be opened.')));
  const form = h('form', { class: 'set-form' });
  const field = (label) => {
    const input = h('input', { class: 'field', type: 'password', autocomplete: 'new-password', 'aria-label': label, placeholder: label });
    input.disabled = !data.can_encrypt;
    form.append(input);
    return input;
  };
  const one = field(t('New passphrase (12+ characters)'));
  const two = field(t('The same again'));
  const row = h('div', { class: 'sec-row' });
  const make = h('button', { class: 'pill-btn', type: 'button' }, t('Make one'));
  make.disabled = !data.can_encrypt;
  make.onclick = () => {
    one.value = two.value = newPassphrase();
    one.type = 'text'; // to copy it
    one.select();
  };
  const save = h('button', { class: 'pill-btn', type: 'submit' }, t('Save'));
  save.disabled = !data.can_encrypt;
  row.append(save, make);
  if (on && data.settings.backup_drive.value !== 'true') {
    const remove = h('button', { class: 'pill-btn', type: 'button' }, t('Remove'));
    remove.onclick = () => {
      if (confirm(t('Remove the passphrase? New local archives won’t be encrypted.'))) send(body, remove, () => api('admin/settings', 'PUT', { reset: ['backup_passphrase'] }));
    };
    row.append(remove);
  }
  form.append(row);
  form.onsubmit = (e) => {
    e.preventDefault();
    if (one.value !== two.value) return showError(body, t('The two passphrases differ.'));
    if (!one.value) return;
    send(body, save, () => api('admin/settings', 'PUT', { values: { backup_passphrase: one.value } }), (why) => why.error === 'bad_value' && t('The passphrase needs at least 12 characters.'));
  };
  sec.append(form, h('p', { class: 'muted set-hint' }, t('A new one applies to new backups only: each archive says which one it needs.')));
  return sec;
}

function runRow(r) {
  const row = h('div', { class: 'day-item sec-item set-run' });
  const name = h('span', { class: 'name' });
  name.append(h('span', { class: 'txt' }, dfTime.format(new Date(r.started_at))));
  const note = r.error ? [] : [r.size ? size(r.size) : '', r.encrypted ? t('encrypted') : t('not encrypted')];
  if (r.drive) note.push(r.drive === 'ok' ? t('on Drive') : t('not on Drive'));
  row.append(name, h('span', { class: 'amt' }, note.filter(Boolean).join(' · ')));
  const problem = r.error || (r.drive && r.drive !== 'ok' ? r.drive : '');
  if (problem) row.append(h('span', { class: 'lock-error set-why' }, problem));
  return row;
}

function backupsTab(body, data) {
  const runs = data.backups.runs;
  const when = section(t('Schedule'), t('On this server, in {dir}', { dir: data.backups.dir || '' }));
  when.append(form(body, data, [
    { key: 'backups', label: t('Back up every day'), type: 'checkbox' },
    { key: 'backup_time', label: t('Time of day'), hint: t('In the site’s time zone.'), type: 'time' },
    { key: 'backup_keep_days', label: t('Keep local backups (days)'), type: 'number' },
  ]));

  const drive = section(t('Google Drive'), t('Encrypted, and only files the app made itself.'));
  drive.append(form(body, data, [
    { key: 'backup_drive', label: t('Also upload to Google Drive'), type: 'checkbox' },
    { key: 'backup_folder', label: t('Folder') },
    { key: 'backup_drive_keep_days', label: t('Keep on Drive (days)'), type: 'number' },
  ]));
  const on = data.settings.backup_drive.value === 'true';
  if (on && !data.google.drive) {
    const box = h('div', { class: 'notice notice-row' });
    const go = h('button', { class: 'pill-btn', type: 'button' }, t('To Google'));
    go.onclick = () => body.closest('.adm-panel').querySelector('[data-tab="google"]').click();
    box.append(h('span', {}, t('Google hasn’t allowed Drive yet: use Connect again there.')), go);
    drive.append(box);
  }
  const msg = h('p', { class: 'sec-msg', role: 'status', hidden: '' });
  const test = h('button', { class: 'pill-btn set-back', type: 'button' }, t('Test connection'));
  test.onclick = async () => {
    test.disabled = true;
    msg.hidden = true;
    const res = await api('admin/backups/test-drive', 'POST').catch(() => null);
    const ans = res?.ok ? await res.json() : null;
    test.disabled = false;
    msg.textContent = !ans ? t('Server not reachable.') : ans.ok ? t('Uploaded and deleted a small file.') : t('That didn’t work: {why}', { why: ans.error });
    msg.classList.toggle('lock-error', !ans?.ok);
    msg.hidden = false;
  };
  drive.append(msg, test);

  const state = section(t('Last backups'), runs.length ? '' : t('None yet'));
  for (const r of runs) state.append(runRow(r));
  const line = h('p', { class: 'muted' }, data.status.running ? statusLine(data.status) : '');
  const now = h('button', { class: 'pill-btn set-back', type: 'button' }, t('Back up now'));
  now.disabled = data.status.running;
  now.onclick = async () => {
    now.disabled = true;
    body.querySelector('.adm-error')?.remove();
    const res = await api('admin/backups', 'POST').catch(() => null);
    if (res?.status === 409) {
      now.disabled = false;
      return showError(body, t('A sync is running. Try again when it’s done.'));
    }
    poll(body, line, () => load(body, 'backups'));
  };
  if (data.status.running) poll(body, line, () => load(body, 'backups'));
  state.append(line, now);
  return [...noKeyNote(data), when, passphraseSection(body, data), drive, state];
}
