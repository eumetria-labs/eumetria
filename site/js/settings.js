// The settings (Settings → Data sources and Site, admins only). Each section
// saves on its own; the server checks every value (settings.go) and a change applies at
// once. A secret never comes back from the server: the page only learns
// whether one is set, and a field left empty keeps it. Changes need a recent
// proof of who one is, which api() asks for when needed (stepup.js).

import { LOCALE } from './format.js';
import { h } from './dom.js';
import { api, reason } from './account.js';
import { confirmNote } from './stepup.js';
import { t } from './i18n.js';
import { badge, revealable } from './kit.js';
import { renderMarkdown } from './markdown.js';
import { setUpdate, updateLabel } from './updates.js';

const dfTime = new Intl.DateTimeFormat(LOCALE, { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' });

const POLL_MS = 2000;

let polling = 0; // the status poll that may go on; leaving the section ends it

// renderSettings draws one part of the settings (general, sync, yazio,
// google, ai, backups, mail, update) into body: a section of the Profile /
// Settings view. result is how Connect Google ended, when it brought the
// browser back here.
export function renderSettings(body, tab, result = '') {
  polling++;
  return load(body, tab, result);
}

// stopSettings ends a status poll when its section is left.
export function stopSettings() {
  polling++;
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
  if (tab === 'update') {
    const up = await api('admin/update').catch(() => null);
    data.update = up?.ok ? await up.json() : null;
    if (body.dataset.tab !== tab) return;
  }
  // tab comes from the URL (#settings=…): only the object's own tabs count,
  // not what every object has, such as "constructor" (pentest F8).
  const tabs = { general: generalTab, sync: syncTab, yazio: yazioTab, google: googleTab, ai: aiTab, backups: backupsTab, mail: mailTab, update: updateTab };
  body.replaceChildren(...(Object.hasOwn(tabs, tab) ? tabs[tab] : generalTab)(body, data, result));
  // The save bar saves every form of the section in one request.
  body._saveAll = () => {
    const forms = [...body.querySelectorAll('form[data-save]')];
    const values = Object.assign({}, ...forms.map((f) => f._values?.() || {}));
    if (!Object.keys(values).length) return;
    const names = Object.assign({}, ...forms.map((f) => f._names || {}));
    return send(body, forms[0].querySelector('[type="submit"]'), () => api('admin/settings', 'PUT', { values }),
      (why) => why.error === 'bad_value' && t('Check this: {name}', { name: names[why.key] || why.key }));
  };
}

function section(title, note, cls = '') {
  const sec = h('section', { class: ('wo-sec ' + cls).trim() });
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

// send sends a change and draws the tab again; a refusal is shown above it.
async function send(body, button, run, words) {
  body.querySelector('.adm-error')?.remove();
  button.disabled = true;
  try {
    const res = await run();
    if (res.ok) {
      // A new mail server doesn't get the old one's password (pentest
      // re-test N4): the server cleared it, so ask for it again.
      const done = res.status === 200 ? await res.clone().json().catch(() => ({})) : {};
      await load(body, body.dataset.tab);
      if (done.cleared?.includes('smtp_password')) {
        body.prepend(h('p', { class: 'notice adm-error', role: 'status' }, t('The mail server changed, so its stored password was removed. Enter the password again.')));
      }
      return;
    }
    const why = await (async () => { try { return await res.clone().json(); } catch { return {}; } })();
    if (why.error === 'reauth') return body.prepend(confirmNote());
    if (why.error === 'no_key') return showError(body, t('Secrets can’t be stored: SETTINGS_KEY isn’t set on the server.'));
    showError(body, words?.(why) || (why.error === 'no_passphrase' && t('Set a passphrase first: nothing leaves the server unencrypted.'))
      || (why.error === 'needs_public_url' && t('Set the public URL first (General): links in mails go only there.')) || t('That didn’t work. Try again.'));
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
  const el = h('form', { class: 'set-form', 'data-save': '' });
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
    label.append(head, item.secret ? revealable(input) : input);
    if (f.hint) label.append(h('span', { class: 'muted set-hint' }, f.hint));
    if (f.list) label.append(f.list);
    el.append(label);
    return { f, item, input, label };
  });
  // A field with showIf shows only while that checkbox is ticked.
  for (const { f, label } of inputs) {
    const box = f.showIf && inputs.find((x) => x.f.key === f.showIf)?.input;
    if (!box) continue;
    const sync = () => { label.hidden = !box.checked; };
    box.addEventListener('change', sync);
    sync();
  }
  const row = h('div', { class: 'sec-row' });
  const save = h('button', { class: 'pill-btn pf-save', type: 'submit' }, t('Save'));
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
  // The values that changed: what Save sends.
  el._values = () => {
    const values = {};
    for (const { f, item, input } of inputs) {
      const value = f.type === 'checkbox' ? String(input.checked) : input.value.trim();
      const was = f.type === 'checkbox' ? String(/^(1|t|true)$/i.test(item.value)) : item.value ?? '';
      if (item.secret ? value : value !== was) values[f.key] = item.secret ? input.value : value;
    }
    return values;
  };
  el._names = names;
  el.onsubmit = (e) => {
    e.preventDefault();
    const values = el._values();
    if (!Object.keys(values).length) return;
    send(body, save, () => api('admin/settings', 'PUT', { values }), (why) => why.error === 'bad_value' && t('Check this: {name}', { name: names[why.key] || why.key }));
  };
  return el;
}

function noKeyNote(data) {
  return data.can_encrypt ? [] : [h('p', { class: 'notice' }, t('SETTINGS_KEY isn’t set on the server, so secrets stay in the env file and can’t be changed here, and the authenticator apps’ secrets are kept unencrypted in the database and its backups.'))];
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
    {
      key: 'public_url', label: t('Public address'), type: 'url', placeholder: location.origin,
      hint: t('The address people open, e.g. https://eumetria.example.org. Passkeys, Google sign-in and enrolment links use it; passkeys then work only there. Empty: taken from each request.'),
    },
    { key: 'language', label: t('Default language'), hint: t('Until someone picks their own in the menu.'), options: [['', 'English'], ['de', 'Deutsch']] },
    { key: 'timezone', label: t('Time zone'), hint: t('What “today” is and when a week ends. In use: {zone}', { zone: zoneNote }), list: zones, placeholder: data.zone.name },
  ]));
  if (custom) {
    const back = h('button', { class: 'pill-btn set-back', type: 'button' }, t('Use TZ again'));
    back.onclick = () => send(body, back, () => api('admin/settings', 'PUT', { reset: ['timezone'] }));
    sec.append(back);
  }
  if (data.version) sec.append(versionLine(data.version));
  return [sec];
}

// The version the server runs; a release links to its notes.
function versionLine(version) {
  const p = h('p', { class: 'muted set-version' }, t('Eumetria {version}', { version }));
  if (/^\d+\.\d+\.\d+$/.test(version)) {
    p.append(' · ', h('a', { href: `https://github.com/eumetria-labs/eumetria/releases/tag/v${version}`, target: '_blank', rel: 'noopener noreferrer' }, t('What’s new')));
  }
  return p;
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
    { key: 'yazio_client_id', label: t('OAuth client ID'), hint: t('The Yazio app’s, the same for everyone: see the docs, Connect → Yazio.') },
    { key: 'yazio_client_secret', label: t('OAuth client secret') },
  ]));
  const msg = h('p', { class: 'sec-msg', role: 'status', hidden: '' });
  const test = h('button', { class: 'pill-btn', type: 'button' }, t('Test connection'));
  test.onclick = async () => {
    test.disabled = true;
    msg.hidden = true;
    const res = await api('admin/settings/test/yazio', 'POST').catch(() => null);
    test.disabled = false;
    if (res?.status === 403 && (await reason(res.clone())) === 'reauth') {
      body.querySelector('.adm-error')?.remove();
      return body.prepend(confirmNote());
    }
    const ans = res?.ok ? await res.json() : null;
    msg.textContent = !ans ? t('Server not reachable.') : ans.ok ? t('Signed in to Yazio.') : t('That didn’t work: {why}', { why: ans.error });
    msg.classList.toggle('lock-error', !ans?.ok);
    msg.hidden = false;
  };
  const check = section(t('Test connection'), t('With the saved values.'));
  check.append(msg, test);
  return [...noKeyNote(data), sec, check];
}

/* ---------- email ---------- */

// The mail server for security alerts and links (email.go): optional. Only
// encrypted connections; the test mail goes to the admin's own confirmed
// address.
const MAIL_ERRORS = {
  no_address: () => t('Confirm your own e-mail address in your profile first: the test mail goes there.'),
  mail_off: () => t('Fill in the server and the sender first, and save.'),
  too_many: () => t('That was a lot of mail for one hour. Try again later.'),
  demo: () => t('The demo never sends mail.'),
};

// What went wrong sending, by kind: the server says no more (pentest
// re-test I4), its log has the details.
const SEND_ERRORS = {
  dns: () => t('the server’s name wasn’t found'),
  refused: () => t('the server refused the connection'),
  timeout: () => t('the server didn’t answer in time'),
  tls: () => t('no secure connection (certificate or STARTTLS)'),
  auth: () => t('user name or password refused'),
  rejected: () => t('the server refused the mail'),
  not_set_up: () => t('e-mail isn’t set up'),
  other: () => t('see the server log'),
};
const sendError = (kind) => SEND_ERRORS[kind]?.() || kind;

function mailTab(body, data) {
  const sec = section(t('Mail server'), t('Optional. Security alerts and links then reach people by e-mail too, at their confirmed address.'));
  sec.append(form(body, data, [
    { key: 'smtp_host', label: t('Server'), hint: t('e.g. smtp.example.org') },
    { key: 'smtp_port', label: t('Port'), type: 'number', hint: t('587 with STARTTLS, 465 with TLS.') },
    { key: 'smtp_tls', label: t('Encryption'), options: [['starttls', 'STARTTLS'], ['implicit', 'TLS']], hint: t('Always encrypted: a server without it gets nothing.') },
    { key: 'smtp_user', label: t('User name') },
    { key: 'smtp_password', label: t('Password'), hint: t('Changing the server, port, encryption or user name needs the password again.') },
    { key: 'smtp_from', label: t('Sender address'), type: 'email', hint: t('The address the mails come from; SPF and DKIM for it are your mail provider’s part.') },
  ]));
  const status = h('p', { class: 'muted' });
  const m = data.mail || {};
  const when = (d) => dfTime.format(new Date(d));
  status.textContent = [
    m.last_sent ? t('Last sent: {when}', { when: when(m.last_sent) }) : t('Nothing sent yet'),
    m.last_error ? t('Last error ({when}): {why}', { when: when(m.error_at), why: sendError(m.last_error) }) : '',
    m.waiting ? t('{n} waiting', { n: m.waiting }) : '',
  ].filter(Boolean).join(' · ');
  const msg = h('p', { class: 'sec-msg', role: 'status', hidden: '' });
  const test = h('button', { class: 'pill-btn', type: 'button' }, t('Send a test mail'));
  test.onclick = async () => {
    test.disabled = true;
    msg.hidden = true;
    const res = await api('admin/settings/test/smtp', 'POST').catch(() => null);
    test.disabled = false;
    if (res?.status === 403 && (await reason(res.clone())) === 'reauth') {
      body.querySelector('.adm-error')?.remove();
      return body.prepend(confirmNote());
    }
    const ans = res?.ok ? await res.json() : null;
    const why = !res?.ok && res ? await reason(res) : '';
    msg.textContent = !res ? t('Server not reachable.') : ans?.ok ? t('Sent to {to}.', { to: ans.to }) : ans ? t('That didn’t work: {why}', { why: sendError(ans.error) }) : MAIL_ERRORS[why]?.() || t('That didn’t work. Try again.');
    msg.classList.toggle('lock-error', !ans?.ok);
    msg.hidden = false;
  };
  const check = section(t('Test'), status.textContent);
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
  const said = Object.hasOwn(GOOGLE_RESULTS, result) ? GOOGLE_RESULTS[result]() : undefined;
  if (said) parts.push(h('p', { class: said[1] ? 'lock-error' : 'sec-msg', role: 'status' }, said[0]));
  const client = section(t('OAuth client'), t('From the Google Cloud console.'));
  client.append(form(body, data, [
    { key: 'google_client_id', label: t('Client ID') },
    { key: 'google_client_secret', label: t('Client secret') },
  ]));
  const uri = h('div', { class: 'pf-code' });
  const copy = h('button', { class: 'pill-btn small', type: 'button' }, t('Copy'));
  copy.onclick = () => navigator.clipboard?.writeText(g.callback).then(() => { copy.textContent = t('Copied'); });
  uri.append(h('code', { class: 'sec-secret pf-secret' }, g.callback), copy);
  client.append(h('p', { class: 'muted set-hint' }, t('Authorized redirect URI of the client, character for character:')), uri);
  const conn = section(t('Connection'), g.issued_at ? t('Since {date}', { date: dfTime.format(new Date(g.issued_at)) }) : '');
  conn.querySelector('.wo-sec-head').append(g.connected ? (g.missing?.length ? badge('warn', t('Partly connected')) : badge('ok', t('Connected'))) : badge('off', t('Not connected')));
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
    if (why === 'reauth') return body.prepend(confirmNote());
    showError(body, why === 'no_client' ? t('Save the client ID and secret first.') : t('That didn’t work. Try again.'));
  };
  row.append(connect);
  conn.append(row);
  // Disconnecting sits apart, in red: it stops the Fitbit data.
  const danger = [];
  if (g.connected) {
    const zone = section(t('Disconnect Google'), t('Syncing Fitbit data stops until you connect again. What was synced stays.'), 'danger');
    const off = h('button', { class: 'pill-btn danger-btn', type: 'button' }, t('Disconnect'));
    off.onclick = () => {
      if (confirm(t('Disconnect Google? Syncing Fitbit data stops until you connect again.'))) send(body, off, () => api('google', 'DELETE'));
    };
    zone.append(off);
    danger.push(zone);
  }
  return [...parts, conn, client, ...danger];
}

/* ---------- weekly review ---------- */

const MODELS = () => [['claude-opus-5-5', 'Claude Opus 5.5'], ['claude-sonnet-5-5', 'Claude Sonnet 5.5'], ['claude-haiku-5-5', 'Claude Haiku 5.5']];
const EFFORTS = () => ['low', 'medium', 'high', 'xhigh', 'max'].map((e) => [e, e]);
const BACKENDS = () => [['api', t('Anthropic API')], ['claude', t('Claude Code (your subscription)')]];

// The fields of a part with its own model and effort: a checkbox, and the
// two selects shown while it's ticked.
const ownModel = (part) => [
  { key: part + '_own', label: t('Own model and effort'), type: 'checkbox' },
  { key: part + '_model', label: t('Model'), options: MODELS(), showIf: part + '_own' },
  { key: part + '_effort', label: t('Effort'), options: EFFORTS(), showIf: part + '_own' },
];

function aiTab(body, data) {
  const key = section(t('Anthropic API'), t('Used by the weekly review and the coach. Billed to the key’s account; prepaid credit with auto-reload off keeps the cost in hand.'));
  key.append(form(body, data, [
    { key: 'anthropic_key', label: t('Anthropic API key') },
    { key: 'ai_credit_usd', label: t('Credit bought (USD)'), hint: t('Warns admins when it runs low. Empty: no warning.'), type: 'number' },
  ]));
  const model = section(t('Model and effort'), t('For everything Claude writes, on the API and on Claude Code. The coach and the weekly review can each have their own.'));
  model.append(form(body, data, [
    { key: 'ai_model', label: t('Model'), options: MODELS(), hint: t('On the API, about per coach question: Opus $0.20, Sonnet $0.10, Haiku under a cent. On Claude Code only your limit counts: Opus uses it fastest.') },
    { key: 'ai_effort', label: t('Effort'), options: EFFORTS() },
  ]));
  const coach = section(t('Coach'), t('A chat on the dashboard: Claude answers questions about the data the asker’s role sees. Roles need “Ask the coach”.'));
  coach.append(form(body, data, [
    { key: 'coach', label: t('Coach'), options: [['off', t('Off')], ...BACKENDS()] },
    ...ownModel('coach'),
  ]));
  const cc = data.claude || {};
  const claude = section(t('Claude Code'), t('Your Claude subscription instead of the API, through the runner container (docs: “Claude: review and coach”). Only for your own account: everyone else is answered on the API.'));
  const until = cc.limit_until ? new Date(cc.limit_until).toLocaleTimeString(LOCALE, { hour: '2-digit', minute: '2-digit' }) : '';
  claude.querySelector('.wo-sec-head')?.append(!cc.set_up ? badge('off', t('Not set up'))
    : until ? badge('warn', t('Limit reached until {time}', { time: until }))
      : cc.reachable ? badge('ok', t('Ready')) : badge('warn', t('Not reachable')));
  if (cc.set_up && (cc.version || cc.error)) claude.append(h('p', { class: 'muted set-hint' }, cc.error ? cc.error : cc.version));
  claude.append(form(body, data, [
    { key: 'claude_fallback_api', label: t('Answer on the API while the limit is reached'), type: 'checkbox' },
  ]));
  const board = section(t('Whiteboard photos'), t('In a workout note, Claude writes out the workout on a photo (the gym’s whiteboard) as text, so search finds it.'));
  board.append(form(body, data, [
    { key: 'whiteboard_backend', label: t('Read by'), options: BACKENDS() },
    ...ownModel('whiteboard'),
  ]));
  const sec = section(t('Weekly review'), t('Written by Claude after each week.'));
  sec.append(form(body, data, [
    { key: 'review', label: t('Write a review every week'), type: 'checkbox' },
    { key: 'review_backend', label: t('Written by'), options: BACKENDS() },
    ...ownModel('review'),
  ]));
  // Writing every stored review again, part of the review's card.
  const again = h('div', { class: 'set-again' });
  again.append(h('p', { class: 'muted set-hint' }, t('Write all reviews again: in English and German, one after the other.')));
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
      if (why === 'reauth') return body.prepend(confirmNote());
      return showError(body, t('That didn’t work. Try again.'));
    }
    poll(body, line, () => load(body, 'ai'));
  };
  if (data.status.running) poll(body, line, () => load(body, 'ai'));
  again.append(line, run);
  sec.append(again);
  return [...noKeyNote(data), key, claude, model, coach, board, sec];
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
    form.append(revealable(input));
    return input;
  };
  const one = field(t('New passphrase (12+ characters)'));
  const two = field(t('The same again'));
  const row = h('div', { class: 'sec-row' });
  const make = h('button', { class: 'pill-btn', type: 'button' }, t('Make one'));
  make.disabled = !data.can_encrypt;
  make.onclick = () => {
    one.value = two.value = newPassphrase();
    // Shown, to copy it.
    if (one.type === 'password') one.parentElement.querySelector('.eye')?.click();
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
    go.onclick = () => body.dispatchEvent(new CustomEvent('prefs-go', { bubbles: true, detail: 'google' }));
    box.append(h('span', {}, t('Google hasn’t allowed Drive yet: use Connect again there.')), go);
    drive.append(box);
  }
  const msg = h('p', { class: 'sec-msg', role: 'status', hidden: '' });
  const test = h('button', { class: 'pill-btn set-back', type: 'button' }, t('Test connection'));
  test.onclick = async () => {
    test.disabled = true;
    msg.hidden = true;
    const res = await api('admin/backups/test-drive', 'POST').catch(() => null);
    test.disabled = false;
    if (res?.status === 403 && (await reason(res.clone())) === 'reauth') {
      body.querySelector('.adm-error')?.remove();
      return body.prepend(confirmNote());
    }
    const ans = res?.ok ? await res.json() : null;
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
    if (res?.status === 403 && (await reason(res.clone())) === 'reauth') {
      now.disabled = false;
      return body.prepend(confirmNote());
    }
    poll(body, line, () => load(body, 'backups'));
  };
  if (data.status.running) poll(body, line, () => load(body, 'backups'));
  state.append(line, now);
  return [...noKeyNote(data), when, passphraseSection(body, data), drive, state];
}

/* ---------- update ---------- */

const RELEASES = 'https://github.com/eumetria-labs/eumetria/releases';

// How long, roughly: "3 d 4 h", "5 h 12 min", "8 min".
function span(secs) {
  const d = Math.floor(secs / 86400), hh = Math.floor((secs % 86400) / 3600), m = Math.floor((secs % 3600) / 60);
  if (d) return t('{d} d {h} h', { d, h: hh });
  if (hh) return t('{h} h {m} min', { h: hh, m });
  return t('{m} min', { m });
}

function factRow(label, value, mono = false) {
  const row = h('div', { class: 'day-item sec-item set-fact' });
  const name = h('span', { class: 'name' });
  name.append(h('span', { class: 'txt' }, label));
  row.append(name, h('span', { class: 'amt' + (mono ? ' mono' : '') }, value));
  return row;
}

// A paragraph with a link that opens in a new tab.
function link(href, text) {
  const p = h('p');
  p.append(h('a', { href, target: '_blank', rel: 'noopener noreferrer' }, text));
  return p;
}

// The release notes (Markdown) of one release, folded.
function releaseNotes(r) {
  const box = h('details', { class: 'upd-notes' });
  const sum = h('summary');
  sum.append(h('b', {}, r.version));
  if (r.published) sum.append(' · ' + dfTime.format(new Date(r.published)));
  box.append(sum);
  if (r.notes) box.append(renderMarkdown(r.notes));
  if (r.url) box.append(link(r.url, t('On GitHub')));
  return box;
}

// updateTab is Settings → Update (PLAN 15a): the version, what the last
// check found, the channel and automatic installs, and the system.
function updateTab(body, data) {
  const u = data.update;
  if (!u) return [h('p', { class: 'empty-tab' }, t('This could not be loaded.'))];
  const BUILDS = { release: t('Release'), edge: t('Edge build'), dev: t('Development build') };

  const ver = section(t('Version'), BUILDS[u.build] || '');
  const line = h('p', { class: 'upd-version' });
  line.append(h('b', {}, 'Eumetria ' + u.version));
  if (u.build === 'release') line.append(' · ', h('a', { href: `${RELEASES}/tag/v${u.version}`, target: '_blank', rel: 'noopener noreferrer' }, t('What’s new')));
  ver.append(line, factRow(t('Database schema'), String(u.system.schema)));

  const last = u.last;
  const st = section(t('Updates'), last ? t('Checked {when}', { when: dfTime.format(new Date(last.checked_at)) }) : t('Not checked yet'));
  if (u.demo) st.append(h('p', { class: 'muted' }, t('The demo doesn’t look for updates.')));
  if (last?.error) st.append(h('p', { class: 'lock-error' }, t('Couldn’t check: {why}', { why: last.error })));
  if (last?.available) {
    st.append(h('p', { class: 'upd-avail' }, t('Update available: {version}', { version: updateLabel(last.target) })));
    for (const r of last.releases || []) st.append(releaseNotes(r));
    if (last.commits?.length) {
      const list = h('ul', { class: 'upd-commits' });
      for (const c of last.commits) {
        const li = h('li');
        li.append(h('a', { class: 'mono', href: c.url, target: '_blank', rel: 'noopener noreferrer' }, c.sha), ' ' + c.subject);
        list.append(li);
      }
      st.append(list);
      if (last.ahead > last.commits.length) st.append(h('p', { class: 'muted' }, t('… and {n} more', { n: last.ahead - last.commits.length })));
    }
    // Installing from here: the updater container (PLAN 15c); without it,
    // the command.
    if (!u.updater?.present) {
      const how = h('div', { class: 'upd-box' });
      how.append(h('p', {}, t('To install it, run on the server:')), h('pre', { class: 'mono upd-cmd' }, 'cd /srv/eumetria\ndocker compose pull && docker compose up -d'));
      st.append(how);
    }
  } else if (last?.rebuild) {
    st.append(h('p', { class: 'upd-avail' }, t('A security rebuild of this version is available.')));
  } else if (last && !last.error) {
    st.append(h('p', { class: 'upd-ok' }, u.build === 'dev' ? t('A development build: it is updated by hand.') : t('Up to date.')));
  }
  // With the updater: install from here (PLAN 15c), and see it through.
  const up = u.updater?.status;
  const canApply = !!up && !u.demo && u.build !== 'dev';
  const busy = u.apply?.running || u.apply?.step === 'updating' || !!up?.job;
  if (busy) st.append(progress(body, u));
  else if (canApply) {
    const switching = (u.channel === 'edge') !== (up.tag === 'latest');
    const what = switching ? t('Switch to {channel} now', { channel: u.channel === 'edge' ? t('Edge') : t('Stable') })
      : last?.available ? t('Install {version}', { version: updateLabel(last.target) })
        : last?.rebuild ? t('Install the security rebuild') : '';
    if (what) st.append(applyButton(body, u, 'update', what));
    if (u.apply?.error) st.append(h('p', { class: 'lock-error' }, u.apply.error));
  }
  if (last?.major) {
    const major = last.major.version.split('.')[0];
    const box = h('div', { class: 'notice upd-major' });
    box.append(h('p', {}, t('Eumetria {version} is out. A new major version needs steps by hand before it is installed: read them first.', { version: last.major.version })),
      h('p', {}, t('Version {major}.x gets no more security fixes.', { major: u.version.split('.')[0] })));
    if (last.major.url) box.append(link(last.major.url, t('What to do for {major}.0', { major })));
    if (canApply && !busy) {
      box.append(applyButton(body, u, 'major', t('Switch to {major}', { major }),
        t('Did you do the steps for {major}.0? Eumetria makes a backup, then switches to version {major}. Going back to {old} isn’t possible afterwards.', { major, old: u.version.split('.')[0] })));
    }
    st.append(box);
  }
  const msg = h('p', { class: 'sec-msg', role: 'status', hidden: '' });
  const check = h('button', { class: 'pill-btn set-back', type: 'button' }, t('Check now'));
  check.disabled = u.demo || u.checking;
  check.onclick = async () => {
    check.disabled = true;
    msg.hidden = true;
    check.textContent = t('Checking…');
    const res = await api('admin/update/check', 'POST').catch(() => null);
    if (res?.ok) {
      setUpdate(await res.json());
      return load(body, 'update');
    }
    check.disabled = false;
    check.textContent = t('Check now');
    msg.textContent = res?.status === 429 ? t('Just checked. Try again in a moment.') : res?.status === 409 ? t('A check is running already.') : t('Server not reachable.');
    msg.classList.add('lock-error');
    msg.hidden = false;
  };
  st.append(msg, check);

  const how = section(t('Updating'), '');
  how.append(form(body, data, [
    { key: 'update_channel', label: t('Channel'), options: [['stable', t('Stable: new releases')], ['edge', t('Edge: every change that passed its tests')]],
      hint: t('Stable gets new minor versions and fixes; a new major version is only announced. Edge follows the code as it is written.') },
    { key: 'update_auto', label: t('Install updates by themselves'), type: 'checkbox', hint: t('With a backup first. Needs the updater container.') },
    { key: 'update_time', label: t('Time of day'), hint: t('In the site’s time zone.'), type: 'time', showIf: 'update_auto' },
    { key: 'update_mail', label: t('E-mail me when an update is out'), type: 'checkbox', hint: u.mail_on ? t('Once per version, to your confirmed address.') : t('Set up e-mail first.') },
  ]));

  const s = u.system;
  const sys = section(t('System'), s.container ? t('In a container') : '');
  const host = u.updater?.status?.host;
  if (host) sys.append(factRow(t('Host'), host, true));
  const running = Math.max(0, Math.round((Date.now() - new Date(s.started)) / 1000));
  sys.append(
    factRow(host ? t('Container') : t('Hostname'), s.hostname || '–', true),
    factRow(t('Runs as'), s.user, true),
    factRow(t('Running since'), `${dfTime.format(new Date(s.started))} (${span(running)})`),
    ...(s.host_uptime ? [factRow(t('Host up for'), span(s.host_uptime))] : []),
    factRow(t('Working directory'), s.cwd || '–', true),
    factRow(t('Data folder'), s.data_dir || '–', true),
    factRow(t('Database'), s.db_bytes ? size(s.db_bytes) : '–'),
    factRow(t('Go'), s.go, true),
    factRow(t('Platform'), `${s.platform} · ${t('{n} CPUs', { n: s.cpus })}`),
    factRow(t('Time zone'), s.zone),
  );
  return [ver, st, how, ...updaterSection(u.updater), sys];
}

const JOB_STATES = {
  done: () => t('Installed'), unchanged: () => t('Nothing new'), rolled_back: () => t('Went back'), failed: () => t('Failed'), running: () => t('Running'),
};

// The updater container: whether it answers, its channel tag and the last
// updates it made.
function updaterSection(up) {
  if (!up?.present) return [];
  const sec = section(t('Updater'), up.status ? t('Tag {tag}', { tag: up.status.tag }) : '');
  if (up.error) {
    sec.append(h('p', { class: 'lock-error' }, t('The updater doesn’t answer: {why}', { why: up.error })));
    return [sec];
  }
  const jobs = up.status.history || [];
  if (!jobs.length) sec.append(h('p', { class: 'muted' }, t('No updates yet.')));
  for (const j of jobs.slice(0, 5)) {
    const row = h('div', { class: 'day-item sec-item set-run' });
    const name = h('span', { class: 'name' });
    name.append(h('span', { class: 'txt' }, dfTime.format(new Date(j.started))));
    const after = j.after?.eumetria || j.before?.eumetria || '';
    row.append(name, h('span', { class: 'amt' }, [JOB_STATES[j.state]?.() || j.state, after].filter(Boolean).join(' · ')));
    if (j.error) row.append(h('span', { class: 'lock-error set-why' }, j.error));
    sec.append(row);
  }
  return [sec];
}

// applyButton installs (kind "update") or moves to a new major version
// ("major"): Eumetria backs up, then the updater takes over. ask is a
// question to confirm first.
function applyButton(body, u, kind, label, ask = '') {
  const b = h('button', { class: 'pill-btn upd-apply', type: 'button' }, label);
  b.onclick = async () => {
    if (ask && !confirm(ask)) return;
    b.disabled = true;
    body.querySelector('.adm-error')?.remove();
    const res = await api('admin/update', 'POST', { kind }).catch(() => null);
    if (res?.ok) return load(body, 'update');
    b.disabled = false;
    const why = res ? await reason(res.clone()) : '';
    if (why === 'reauth') return body.prepend(confirmNote());
    showError(body, { busy: t('An update is being started already.'), no_updater: t('The updater doesn’t answer.'), no_target: t('Nothing to install: check for updates first.') }[why]
      || (res ? t('That didn’t work. Try again.') : t('Server not reachable.')));
  };
  return b;
}

const STEPS = () => [['backup', t('Backup')], ['pull', t('Download')], ['restart', t('Restart')], ['health', t('Health check')]];
const STEP_OF = { backup: 'backup', asking: 'pull', updating: 'pull', pull: 'pull', restart: 'restart', health: 'health', rollback: 'health', handover: 'health' };

// progress follows an update until Eumetria runs again: the steps, then
// "Waiting for Eumetria…" while it restarts, then the page loads anew (with
// the new version's files). It stops when the update ends without a
// restart: nothing new, or refused before anything restarted.
function progress(body, u) {
  const box = h('div', { class: 'upd-progress', role: 'status' });
  const list = h('ol', { class: 'upd-steps' });
  const note = h('p', { class: 'muted' });
  box.append(list, note);
  const startedAt = u.system.started, version = u.version;
  const before = u.updater?.status?.history?.[0]?.id || '';
  const draw = (info, waiting) => {
    const step = STEP_OF[info?.updater?.status?.job?.step || info?.apply?.step] || 'pull';
    const at = STEPS().findIndex(([k]) => k === step);
    list.replaceChildren(...STEPS().map(([, label], i) => h('li', { class: i < at ? 'done' : i === at ? 'now' : '' }, label)));
    note.textContent = waiting ? t('Waiting for Eumetria to come back…')
      : info?.updater?.status?.job?.step === 'rollback' ? t('The new version didn’t start: going back…') : t('Don’t close this page; it reloads when Eumetria runs again.');
  };
  draw(u, false);
  const mine = ++polling;
  const tick = async () => {
    if (mine !== polling || !document.contains(box)) return;
    const res = await fetch('api/admin/update', { cache: 'no-store', credentials: 'same-origin' }).catch(() => null);
    const info = res?.ok ? await res.json().catch(() => null) : null;
    if (!info) {
      draw(null, true);
      return setTimeout(tick, 2000);
    }
    // Eumetria restarted (a new version, a rebuild, or after going back).
    if (info.version !== version || info.system?.started !== startedAt) return location.reload();
    const last = info.updater?.status?.history?.[0];
    if (info.apply?.error || (last && last.id !== before && !info.updater.status.job)) return load(body, 'update');
    draw(info, false);
    setTimeout(tick, 2000);
  };
  setTimeout(tick, 2000);
  return box;
}
