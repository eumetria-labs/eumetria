// Users & roles (account menu, admins only): the accounts with their role,
// second factor and state; the roles as sets of scopes; everyone's
// sessions; the audit log. The server (manage.go) checks every request and
// refuses what would lock the admin out; changes need a sign-in within the
// last 10 minutes, as in the security settings, and the window offers to
// sign in again and come back here ("#admin").

import { LOCALE, deviceName } from './format.js';
import { h, setHashParam, tabKeys } from './dom.js';
import { openDialog } from './dialog.js';
import { account, api, reason, signOut } from './account.js';
import { t, tn } from './i18n.js';

const dfTime = new Intl.DateTimeFormat(LOCALE, { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' });

// What each scope gives, as the roles tab lists them.
const SCOPES = {
  'view.nutrition': () => [t('Nutrition'), t('Calories, macros, meals, goals, weight, water')],
  'view.foods': () => [t('Foods'), t('Food names and amounts, recipes, Open Food Facts')],
  'view.photos': () => [t('Photos'), t('Meal and recipe photos')],
  'view.activity': () => [t('Activity'), t('Steps and Yazio activities')],
  'view.health': () => [t('Fitbit'), t('Workouts, heart rate, sleep, recovery, the tracker')],
  'view.routes': () => [t('Routes'), t('GPS tracks of workouts (needs Fitbit)')],
  'view.notes': () => [t('Read notes'), t('Workout notes')],
  'view.review': () => [t('Weekly review'), t('The review written by Claude')],
  'edit.notes': () => [t('Write notes'), t('Add, change and delete workout notes')],
  sync: () => [t('Sync'), t('Sync now and refresh everything; needs a second factor')],
  admin: () => [t('Admin'), t('Everything, including users, roles and the audit log; needs a second factor')],
};

// The server's reasons for refusing, in the page's words.
const ERRORS = {
  self: () => t('You can’t do that to your own account. Change your password under Security.'),
  last_admin: () => t('That would leave no admin.'),
  name_taken: () => t('That name is taken.'),
  bad_name: () => t('Names: up to 64 letters, digits and . _ - @, starting with a letter or digit.'),
  bad_role_name: () => t('Role names: up to 32 letters, digits, spaces and . _ -, starting with a letter or digit.'),
  bad_scopes: () => t('Routes need Fitbit too.'),
  builtin: () => t('The built-in roles can’t be changed.'),
  in_use: () => t('Someone still has this role. Give them another one first.'),
  too_short: () => t('The password needs at least 12 characters.'),
  too_long: () => t('The password is too long.'),
  common: () => t('That password is one of the most used ones. Pick another.'),
  is_name: () => t('The password can’t be the user name.'),
};

// What the audit log's actions mean.
const ACTIONS = {
  login: () => t('signed in'), login_failed: () => t('sign-in failed'), logout: () => t('signed out'),
  logout_all: () => t('signed out everywhere'), session_ended: () => t('ended a session'),
  user_created: () => t('added a user'), role_set: () => t('changed a role'), user_disabled: () => t('disabled a user'),
  user_enabled: () => t('enabled a user'), password_reset: () => t('set a new password'), password_changed: () => t('changed their password'),
  second_factor_required: () => t('required a second factor'), role_saved: () => t('saved a role'), role_deleted: () => t('deleted a role'),
  sync: () => t('started a sync'), password_change_failed: () => t('typed a wrong password'),
  second_factor_failed: () => t('second step failed'), second_factors_reset: () => t('lost every second factor'),
  key_added: () => t('added a passkey or key'), key_removed: () => t('removed a passkey or key'),
  totp_added: () => t('set up an authenticator app'), totp_removed: () => t('removed the authenticator app'),
  recovery_codes_made: () => t('made recovery codes'), recovery_code_used: () => t('used a recovery code'),
  token_created: () => t('made an API token'),
};

// The role whose form stays open after it is drawn again (lower case).
let openRole = '';

const TABS = [['users', () => t('Users')], ['roles', () => t('Roles')], ['sessions', () => t('Sessions')], ['audit', () => t('Audit log')]];

let win = null;

export function openManage(returnFocus, tab = 'users') {
  win?.close(true);
  const close = h('button', { class: 'sheet-close', type: 'button', 'aria-label': t('Close') }, '×');
  const head = h('div', { class: 'sheet-head wo-head' });
  const titles = h('div', { class: 'wo-titles' });
  titles.append(h('h2', { id: 'adm-title' }, t('Users & roles')), h('p', { class: 'muted' }, t('Who may see what')));
  head.append(titles, close);
  const tabs = h('div', { class: 'tabs adm-tabs', role: 'tablist', 'aria-label': t('Users & roles') });
  const body = h('div', { class: 'wo-body adm-body', role: 'tabpanel' });
  const buttons = TABS.map(([key, label]) => {
    const b = h('button', { role: 'tab', type: 'button', 'data-tab': key }, label());
    b.onclick = () => show(key);
    return b;
  });
  tabs.append(...buttons);
  tabs.addEventListener('keydown', (e) => tabKeys(e, tabs));
  const panel = h('div', { class: 'wo-panel adm-panel' });
  panel.append(head, tabs, body);
  win = openDialog({ cls: 'wo-overlay', labelledBy: 'adm-title', content: [panel], returnFocus });
  close.onclick = () => win.close();

  function show(key) {
    for (const b of buttons) {
      const on = b.dataset.tab === key;
      b.setAttribute('aria-selected', String(on));
      b.tabIndex = on ? 0 : -1;
    }
    load(body, key);
  }
  show(TABS.some(([k]) => k === tab) ? tab : 'users');
}

// load fetches what a tab shows and draws it.
async function load(body, tab) {
  body.dataset.tab = tab;
  const path = { users: 'admin/users', roles: 'admin/roles', sessions: 'admin/sessions', audit: 'audit?limit=200' }[tab];
  const [res, roles] = await Promise.all([api(path).catch(() => null), tab === 'users' ? api('admin/roles').catch(() => null) : null]);
  if (body.dataset.tab !== tab) return; // another tab was picked meanwhile
  if (!res?.ok) {
    const why = res ? await reason(res) : '';
    body.replaceChildren(h('p', { class: 'empty-tab' }, why === 'second_factor_required'
      ? t('Set up a second factor under Security first: admins need one.')
      : t('This could not be loaded.')));
    return;
  }
  const data = await res.json();
  const roleData = roles?.ok ? await roles.json() : null;
  const draw = { users: () => usersTab(body, data, roleData), roles: () => rolesTab(body, data), sessions: () => sessionsTab(body, data), audit: () => auditTab(data) }[tab];
  body.replaceChildren(...draw());
}

// change sends a change and draws the tab again; a refusal is shown above
// the tab, and an old sign-in offers to sign in again.
async function change(body, button, run, after) {
  body.querySelector('.adm-error')?.remove();
  if (button) button.disabled = true;
  try {
    const res = await run();
    if (!res.ok) {
      const why = await reason(res);
      if (why === 'reauth') return body.prepend(reauthNote(body.dataset.tab));
      body.prepend(h('p', { class: 'lock-error adm-error', role: 'alert' }, ERRORS[why]?.() || t('That didn’t work. Try again.')));
      return;
    }
    after?.();
    await load(body, body.dataset.tab);
  } catch {
    body.prepend(h('p', { class: 'lock-error adm-error', role: 'alert' }, t('Server not reachable.')));
  } finally {
    if (button && document.contains(button)) button.disabled = false;
  }
}

function reauthNote(tab) {
  const box = h('div', { class: 'notice notice-row adm-error' });
  const again = h('button', { class: 'pill-btn', type: 'button' }, t('Sign in again'));
  again.onclick = () => {
    setHashParam('admin', tab);
    signOut(false);
  };
  box.append(h('span', {}, t('To change anything here, sign in again: your last sign-in was more than 10 minutes ago.')), again);
  return box;
}

function section(title, note) {
  const sec = h('section', { class: 'wo-sec' });
  const hd = h('div', { class: 'wo-sec-head' });
  hd.append(h('h3', {}, title));
  if (note) hd.append(h('span', { class: 'muted' }, note));
  sec.append(hd);
  return sec;
}

function roleSelect(roles, value, label) {
  const sel = h('select', { class: 'field', 'aria-label': label });
  for (const r of roles) sel.append(h('option', { value: r.name }, roleName(r.name)));
  sel.value = roles.find((r) => r.name.toLowerCase() === String(value).toLowerCase())?.name ?? value;
  return sel;
}

// The built-in roles' names in the page's language.
const roleName = (name) => ({ admin: t('Admin'), viewer: t('Viewer') })[name] || name;

/* ---------- users ---------- */

function usersTab(body, users, roleData) {
  const roles = roleData?.roles || [];
  const me = account.user?.name;
  const sec = section(t('Users'), tn(users.length, '{n} account', '{n} accounts'));
  for (const u of users) {
    const row = h('div', { class: 'adm-user' + (u.disabled ? ' off' : '') });
    const info = h('div', { class: 'adm-info' });
    const name = h('b', {}, u.name);
    if (u.name === me) name.append(h('span', { class: 'adm-tag' }, t('you')));
    if (u.disabled) name.append(h('span', { class: 'adm-tag warn' }, t('disabled')));
    const facts = [
      u.second_factor ? t('second factor on') : u.require_second_factor ? t('must set up a second factor') : t('no second factor'),
      u.last_seen ? t('last seen {date}', { date: dfTime.format(new Date(u.last_seen)) }) : t('never signed in'),
      tn(u.sessions, '{n} session', '{n} sessions'),
    ];
    info.append(name, h('span', { class: 'muted' }, facts.join(' · ')));
    const role = roleSelect(roles, u.role, t('Role of {name}', { name: u.name }));
    role.disabled = u.name === me;
    role.onchange = () => change(body, role, () => api('admin/users/' + u.id, 'PATCH', { role: role.value }));

    const actions = h('div', { class: 'sec-row adm-actions' });
    const pw = h('button', { class: 'pill-btn', type: 'button' }, t('New password'));
    pw.hidden = u.name === me;
    pw.onclick = () => row.querySelector('.adm-pw') ? row.querySelector('.adm-pw').remove() : row.append(passwordForm(body, u));
    const mfa = h('button', { class: 'pill-btn', type: 'button' }, u.require_second_factor ? t('Don’t require a second factor') : t('Require a second factor'));
    mfa.hidden = u.second_factor && !u.require_second_factor;
    mfa.onclick = () => change(body, mfa, () => api('admin/users/' + u.id, 'PATCH', { require_second_factor: !u.require_second_factor }));
    const off = h('button', { class: 'pill-btn', type: 'button' }, u.disabled ? t('Enable') : t('Disable'));
    off.hidden = u.name === me;
    off.onclick = () => {
      if (!u.disabled && !confirm(t('Disable {name}? Their sessions end and they can’t sign in until you enable the account again.', { name: u.name }))) return;
      change(body, off, () => api('admin/users/' + u.id, 'PATCH', { disabled: !u.disabled }));
    };
    actions.append(pw, mfa, off);
    row.append(info, role, actions);
    sec.append(row);
  }
  return [sec, addUserSection(body, roles)];
}

// passwordForm sets a new password for someone who forgot theirs.
function passwordForm(body, u) {
  const form = h('form', { class: 'sec-add adm-pw' });
  const input = h('input', { class: 'field', type: 'password', autocomplete: 'new-password', placeholder: t('New password for {name}', { name: u.name }), 'aria-label': t('New password for {name}', { name: u.name }), required: '' });
  const ok = h('button', { class: 'pill-btn', type: 'submit' }, t('Set'));
  form.append(input, ok, h('p', { class: 'muted adm-hint' }, t('Their sessions end. Tell them the password in person or through a password manager.')));
  form.onsubmit = (e) => {
    e.preventDefault();
    change(body, ok, () => api('admin/users/' + u.id + '/password', 'POST', { password: input.value }));
  };
  queueMicrotask(() => input.focus());
  return form;
}

function addUserSection(body, roles) {
  const sec = section(t('Add someone'), t('They can change the password and add a passkey themselves.'));
  const form = h('form', { class: 'sec-add adm-add' });
  const name = h('input', { class: 'field', type: 'text', autocomplete: 'off', autocapitalize: 'none', spellcheck: 'false', maxlength: '64', placeholder: t('User name'), 'aria-label': t('User name'), required: '' });
  const pw = h('input', { class: 'field', type: 'password', autocomplete: 'new-password', placeholder: t('Password (12+ characters)'), 'aria-label': t('Password'), required: '' });
  const role = roleSelect(roles, 'viewer', t('Role'));
  const add = h('button', { class: 'pill-btn', type: 'submit' }, t('Add'));
  form.append(name, pw, role, add);
  form.onsubmit = (e) => {
    e.preventDefault();
    change(body, add, () => api('admin/users', 'POST', { name: name.value.trim(), password: pw.value, role: role.value }));
  };
  sec.append(form);
  return sec;
}

/* ---------- roles ---------- */

function rolesTab(body, { roles, scopes }) {
  const parts = roles.map((r) => roleSection(body, r, scopes));
  parts.push(roleSection(body, null, scopes, roles.map((r) => r.name.toLowerCase())));
  return parts;
}

// roleSection shows a role's scopes as checkboxes; null is the form for a
// new role, which may not take the name of one (taken, in lower case):
// saving would replace that role's scopes.
function roleSection(body, r, scopes, taken = []) {
  const fresh = !r;
  // What a role gives, in one line.
  const what = (sc) => (sc.includes('admin') ? t('Everything.') : sc.map((x) => SCOPES[x]?.()[0] ?? x).join(', ') || t('Nothing yet.'));
  // The built-in roles can't change.
  if (r?.builtin) {
    const sec = section(roleName(r.name), t('built in'));
    sec.append(h('p', { class: 'muted adm-builtin' }, what(r.scopes)));
    return sec;
  }
  // One's own roles are folded to that line; the one just saved stays open.
  let sec;
  if (fresh) {
    sec = section(t('New role'), t('For anyone who should see less, e.g. a coach.'));
  } else {
    sec = h('details', { class: 'wo-sec adm-fold' });
    sec.open = openRole === r.name.toLowerCase();
    const sum = h('summary');
    sum.append(h('h3', {}, r.name), h('span', { class: 'muted' }, what(r.scopes)));
    sec.append(sum);
  }
  const form = h('form', { class: 'adm-role' });
  let name = null;
  if (fresh) {
    name = h('input', { class: 'field', type: 'text', maxlength: '32', placeholder: t('Name, e.g. Coach'), 'aria-label': t('Name of the role'), required: '' });
    form.append(name);
  }
  const grid = h('div', { class: 'adm-scopes' });
  const boxes = scopes.map((sc) => {
    const [label, note] = SCOPES[sc]?.() || [sc, ''];
    const box = h('input', { type: 'checkbox', value: sc });
    box.checked = !!r && (r.scopes.includes(sc) || r.scopes.includes('admin'));
    const item = h('label', { class: 'adm-scope' });
    const txt = h('span');
    txt.append(h('b', {}, label), h('span', { class: 'muted' }, note));
    item.append(box, txt);
    grid.append(item);
    return box;
  });
  form.append(grid);
  const row = h('div', { class: 'sec-row' });
  const save = h('button', { class: 'pill-btn', type: 'submit' }, fresh ? t('Create role') : t('Save'));
  row.append(save);
  if (!fresh) {
    const del = h('button', { class: 'pill-btn', type: 'button' }, t('Delete'));
    del.onclick = () => {
      if (confirm(t('Delete the role “{name}”?', { name: r.name }))) change(body, del, () => api('admin/roles/' + encodeURIComponent(r.name), 'DELETE'));
    };
    row.append(del);
  }
  form.append(row);
  form.onsubmit = (e) => {
    e.preventDefault();
    const role = fresh ? name.value.trim() : r.name;
    openRole = role.toLowerCase();
    if (fresh && taken.includes(role.toLowerCase())) {
      body.querySelector('.adm-error')?.remove();
      return body.prepend(h('p', { class: 'lock-error adm-error', role: 'alert' }, t('That name is taken.')));
    }
    const picked = boxes.filter((b) => b.checked).map((b) => b.value);
    change(body, save, () => api('admin/roles/' + encodeURIComponent(role), 'PUT', { scopes: picked }));
  };
  sec.append(form);
  return sec;
}

/* ---------- sessions and the audit log ---------- */

function sessionsTab(body, list) {
  const sec = section(t('Sessions'), tn(list.length, '{n} signed-in device', '{n} signed-in devices'));
  for (const s of list) {
    const row = h('div', { class: 'day-item sec-item' });
    const name = h('span', { class: 'name' });
    name.append(h('span', { class: 'txt' }, `${s.user} · ${deviceName(s.user_agent)}`));
    const note = [t('last seen {date}', { date: dfTime.format(new Date(s.last_seen)) }), s.ip].filter(Boolean);
    const end = h('button', { class: 'pill-btn', type: 'button' }, s.current ? t('This session') : t('End'));
    end.disabled = s.current;
    end.onclick = () => change(body, end, () => api('admin/sessions/' + s.id, 'DELETE'));
    row.append(name, h('span', { class: 'amt' }, note.join(' · ')), end);
    sec.append(row);
  }
  return [sec];
}

function auditTab(list) {
  const sec = section(t('Audit log'), t('The last {n} entries', { n: list.length }));
  if (!list.length) sec.append(h('p', { class: 'empty-tab' }, t('Nothing yet.')));
  for (const e of list) {
    const row = h('div', { class: 'adm-log' });
    const what = h('span');
    what.append(h('b', {}, e.actor || '–'), ' ' + (ACTIONS[e.action]?.() || e.action));
    if (e.detail) what.append(h('span', { class: 'muted' }, ' · ' + e.detail));
    row.append(h('time', { class: 'muted', datetime: e.at }, dfTime.format(new Date(e.at))), what, h('span', { class: 'muted adm-ip' }, e.ip || ''));
    sec.append(row);
  }
  return [sec];
}
