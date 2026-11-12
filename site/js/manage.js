// Users & Roles (account menu, admins only): the accounts with their role,
// second factor and state; the roles as sets of scopes; everyone's
// sessions; notes whose workout is gone; the audit log. The server (manage.go) checks every request and
// refuses what would lock the admin out; changes need a recent proof of who
// one is, which api() asks for when needed (stepup.js).

import { LOCALE, deviceName } from './format.js';
import { h, tabKeys } from './dom.js';
import { openDialog } from './dialog.js';
import { account, api, reason } from './account.js';
import { confirmNote } from './stepup.js';
import { noteFold, withText } from './notes.js';
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
  'edit.lifts': () => [t('Write personal bests'), t('Add, change and delete lifts and benchmark workouts (needs Fitbit)')],
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
  bad_scopes: () => t('Routes, notes and lifts need Fitbit too, and writing notes needs reading them.'),
  builtin: () => t('The built-in roles can’t be changed.'),
  in_use: () => t('Someone still has this role. Give them another one first.'),
  too_short: () => t('The password needs at least 12 characters.'),
  too_long: () => t('The password is too long.'),
  common: () => t('That password is one of the most used ones. Pick another.'),
  is_name: () => t('The password can’t be the user name.'),
  bad_email: () => t('That isn’t an e-mail address.'),
  mail_off: () => t('Inviting needs e-mail (Settings → Email). Give them a password instead.'),
  not_sent: () => t('The invitation couldn’t be sent. Try again later.'),
  no_public_url: () => t('Mails with links need the public URL (Settings → General).'),
  not_invited: () => t('They chose their password already.'),
  not_disabled: () => t('Disable the account first, then delete it.'),
};

// What the audit log's actions mean.
const ACTIONS = {
  login: () => t('signed in'), login_failed: () => t('sign-in failed'), logout: () => t('signed out'),
  logout_all: () => t('signed out everywhere'), session_ended: () => t('ended a session'),
  user_created: () => t('added a user'), user_deleted: () => t('deleted a user'), user_invited: () => t('invited a user'), invite_accepted: () => t('accepted the invitation'), role_set: () => t('changed a role'), user_disabled: () => t('disabled a user'),
  user_enabled: () => t('enabled a user'), password_reset: () => t('set a new password'), password_changed: () => t('changed their password'),
  second_factor_required: () => t('required a second factor'), role_saved: () => t('saved a role'), role_deleted: () => t('deleted a role'),
  sync: () => t('started a sync'), password_change_failed: () => t('typed a wrong password'),
  second_factor_failed: () => t('second step failed'), second_factors_reset: () => t('lost every second factor'),
  key_added: () => t('added a passkey or key'), key_removed: () => t('removed a passkey or key'),
  totp_added: () => t('set up an authenticator app'), totp_removed: () => t('removed the authenticator app'),
  recovery_codes_made: () => t('made recovery codes'), recovery_code_used: () => t('used a recovery code'),
  token_created: () => t('made an API token'),
  enrol_link_made: () => t('made an enrolment link'), factors_dropped: () => t('lost second factors set up without a link (new rights)'), first_factor_enrolled: () => t('set up their first second factor'),
  reauth: () => t('confirmed it’s them'), reauth_failed: () => t('failed to confirm it’s them'),
  login_pressure: () => t('was guessed at often (sign-ins slowed down)'),
  email_change_requested: () => t('changed their e-mail address'), email_verified: () => t('confirmed their e-mail address'),
  email_removed: () => t('removed their e-mail address'), mail_test: () => t('sent a test mail'),
  key_clone_warning: () => t('was refused: a key may be copied'),
  note_added: () => t('added a note'), note_changed: () => t('changed a note'), note_deleted: () => t('deleted a note'),
  note_image_added: () => t('added an image to a note'),
  lift_added: () => t('added a lift result'), lift_changed: () => t('changed a lift result'), lift_deleted: () => t('deleted a lift result'),
  wod_added: () => t('added a benchmark workout'), wod_changed: () => t('changed a benchmark workout'), wod_deleted: () => t('deleted a benchmark workout'),
  wod_result_added: () => t('added a workout result'), wod_result_changed: () => t('changed a workout result'), wod_result_deleted: () => t('deleted a workout result'),
  profile_changed: () => t('changed their profile'), picture_changed: () => t('changed their picture'),
  picture_removed: () => t('removed their picture'), token_deleted: () => t('revoked an API token'),
  backup_started: () => t('started a backup'), settings_changed: () => t('changed settings'), reviews_rewritten: () => t('had the reviews written again'),
  google_connected: () => t('connected Google'), google_disconnected: () => t('disconnected Google'), google_failed: () => t('couldn’t connect Google'),
};

// The role whose form stays open after it is drawn again (lower case).
let openRole = '';

const TABS = [['users', () => t('Users')], ['roles', () => t('Roles')], ['sessions', () => t('Sessions')], ['notes', () => t('Notes')], ['audit', () => t('Audit log')]];

let win = null;

export function openManage(returnFocus, tab = 'users') {
  win?.close(true);
  const close = h('button', { class: 'sheet-close', type: 'button', 'aria-label': t('Close') }, '×');
  const head = h('div', { class: 'sheet-head wo-head' });
  const titles = h('div', { class: 'wo-titles' });
  titles.append(h('h2', { id: 'adm-title' }, t('Users & Roles')), h('p', { class: 'muted' }, t('Who may see what')));
  head.append(titles, close);
  const tabs = h('div', { class: 'tabs adm-tabs', role: 'tablist', 'aria-label': t('Users & Roles') });
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
  const path = { users: 'admin/users', roles: 'admin/roles', sessions: 'admin/sessions', notes: 'admin/notes', audit: 'audit?limit=200&which=' + auditWhich }[tab];
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
  const draw = { users: () => usersTab(body, data, roleData), roles: () => rolesTab(body, data), sessions: () => sessionsTab(body, data), notes: () => notesTab(body, data), audit: () => auditTab(body, data) }[tab];
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
      if (why === 'reauth') return body.prepend(confirmNote());
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
      u.invited ? t('invited, no password chosen yet') : u.second_factor ? t('second factor on') : u.require_second_factor ? t('must set up a second factor') : t('no second factor'),
      u.last_seen ? t('last seen {date}', { date: dfTime.format(new Date(u.last_seen)) }) : t('never signed in'),
      tn(u.sessions, '{n} session', '{n} sessions'),
    ];
    if (u.security_changed) facts.push(t('sign-in changed {date}', { date: dfTime.format(new Date(u.security_changed)) }));
    info.append(name, h('span', { class: 'muted' }, facts.join(' · ')));
    const role = roleSelect(roles, u.role, t('Role of {name}', { name: u.name }));
    role.disabled = u.name === me;
    role.onchange = () => promote(body, role, () => api('admin/users/' + u.id, 'PATCH', { role: role.value }), u.name);

    const actions = h('div', { class: 'sec-row adm-actions' });
    const pw = h('button', { class: 'pill-btn', type: 'button' }, t('New password'));
    pw.hidden = u.name === me;
    pw.onclick = () => row.querySelector('.adm-pw') ? row.querySelector('.adm-pw').remove() : row.append(passwordForm(body, u));
    const mfa = h('button', { class: 'pill-btn', type: 'button' }, u.require_second_factor ? t('Don’t require a second factor') : t('Require a second factor'));
    mfa.hidden = u.second_factor && !u.require_second_factor;
    mfa.onclick = () => promote(body, mfa, () => api('admin/users/' + u.id, 'PATCH', { require_second_factor: !u.require_second_factor }), u.name);
    const off = h('button', { class: 'pill-btn', type: 'button' }, u.disabled ? t('Enable') : t('Disable'));
    off.hidden = u.name === me;
    off.onclick = () => {
      if (!u.disabled && !confirm(t('Disable {name}? Their sessions end and they can’t sign in until you enable the account again.', { name: u.name }))) return;
      change(body, off, () => api('admin/users/' + u.id, 'PATCH', { disabled: !u.disabled }));
    };
    // The first second factor needs a link from here (pentest F19).
    const link = h('button', { class: 'pill-btn', type: 'button' }, t('Enrolment link'));
    link.hidden = u.second_factor;
    link.onclick = () => enrolLink(body, row, link, u);
    // Invited and hasn't come yet: send a new link.
    const reinvite = h('button', { class: 'pill-btn', type: 'button' }, t('Send invitation again'));
    reinvite.hidden = !u.invited;
    reinvite.onclick = () => {
      let sent = false;
      change(body, reinvite, async () => {
        const res = await api('admin/users/' + u.id + '/invite', 'POST');
        sent = res.ok;
        return res;
      }).then(() => { if (sent) invitedNote(body, true, t('A new invitation is on its way to {name}.', { name: u.name })); });
    };
    link.hidden ||= u.invited; // the invitation brings its own
    // Gone for good: only once disabled, never oneself.
    const del = h('button', { class: 'pill-btn danger', type: 'button' }, t('Delete'));
    del.hidden = !u.disabled || u.name === me;
    del.onclick = () => {
      if (!confirm(t('Delete {name} for good? Their sign-in, keys, tokens and profile go. Notes, lifts and workouts they wrote stay, without an author. This can’t be undone.', { name: u.name }))) return;
      change(body, del, () => api('admin/users/' + u.id, 'DELETE'));
    };
    actions.append(pw, mfa, link, reinvite, off, del);
    row.append(info, role, actions);
    sec.append(row);
  }
  return [sec, addUserSection(body, roles)];
}

// enrolLink makes a new enrolment link for u and shows it to copy: with it
// they set up their first second factor, once, within a day.
async function enrolLink(body, row, button, u) {
  row.querySelector('.adm-enrol')?.remove();
  body.querySelector('.adm-error')?.remove();
  button.disabled = true;
  try {
    const res = await api('admin/users/' + u.id + '/enrol-link', 'POST');
    if (!res.ok) {
      const why = await reason(res);
      if (why === 'reauth') return body.prepend(confirmNote());
      return body.prepend(h('p', { class: 'lock-error adm-error', role: 'alert' }, t('That didn’t work. Try again.')));
    }
    const { token, expires } = await res.json();
    const url = location.origin + location.pathname + '#enrol=' + token;
    const box = h('div', { class: 'sec-add adm-enrol' });
    const field = h('input', { class: 'field', type: 'text', readonly: '', value: url, 'aria-label': t('Enrolment link for {name}', { name: u.name }) });
    const copy = h('button', { class: 'pill-btn', type: 'button' }, t('Copy'));
    copy.onclick = () => navigator.clipboard?.writeText(url).then(() => { copy.textContent = t('Copied'); });
    box.append(field, copy);
    // With a confirmed address, the link can go by mail: a new one, which
    // replaces this one.
    if (u.email_verified) {
      const send = h('button', { class: 'pill-btn', type: 'button' }, t('Send by e-mail'));
      send.onclick = async () => {
        send.disabled = true;
        const r = await api('admin/users/' + u.id + '/enrol-link', 'POST', { mail: true }).catch(() => null);
        const a = r?.ok ? await r.json() : null;
        if (a?.mailed) {
          field.value = location.origin + location.pathname + '#enrol=' + a.token;
          send.textContent = t('Sent to their confirmed address');
          return;
        }
        send.disabled = false;
        send.textContent = a?.mail_error === 'mail_off' ? t('E-mail isn’t set up (Settings → Email)') : t('That didn’t work. Try again.');
      };
      box.append(send);
    }
    box.append(h('p', { class: 'muted adm-hint' }, t('With this link {name} sets up their first second factor: once, until {date}. Hand it over separately from the password.', { name: u.name, date: dfTime.format(new Date(expires)) })));
    row.append(box);
    field.select();
  } catch {
    body.prepend(h('p', { class: 'lock-error adm-error', role: 'alert' }, t('Server not reachable.')));
  } finally {
    button.disabled = false;
  }
}

// promote runs a change that may give someone rights that need a second
// factor. The server then removes the factors they set up without an
// enrolment link and answers with new links (pentest re-test N5): for
// name, or by name for a role's holders. They are shown above the list.
function promote(body, button, run, name) {
  let links = null;
  change(body, button, async () => {
    const res = await run();
    if (res.status === 200) {
      const a = await res.clone().json().catch(() => null);
      links = typeof a?.enrol === 'string' ? { [name]: a.enrol } : a?.enrol || null;
    }
    return res;
  }).then(() => { if (links) enrolNote(body, links); });
}

function enrolNote(body, links) {
  const box = h('div', { class: 'notice adm-error', role: 'status' });
  box.append(h('p', {}, t('These accounts need a second factor now. Factors they set up without an enrolment link were removed and their sessions ended. Hand each the link to set up a new one (it works once, within a day):')));
  for (const [name, token] of Object.entries(links)) {
    const url = location.origin + location.pathname + '#enrol=' + token;
    const field = h('input', { class: 'field', type: 'text', readonly: '', value: url, 'aria-label': t('Enrolment link for {name}', { name }) });
    const copy = h('button', { class: 'pill-btn', type: 'button' }, t('Copy'));
    copy.onclick = () => navigator.clipboard?.writeText(url).then(() => { copy.textContent = t('Copied'); });
    const line = h('div', { class: 'sec-add' });
    line.append(h('b', {}, name), field, copy);
    box.append(line);
  }
  body.prepend(box);
}

// invitedNote says above the list how an invitation went. "On its way":
// the server sends in the background; Settings → Email shows the last
// error, the server log every mail.
function invitedNote(body, ok, text) {
  body.querySelector('.adm-error')?.remove();
  body.prepend(h('p', { class: (ok ? 'notice' : 'lock-error') + ' adm-error', role: ok ? 'status' : 'alert' }, text));
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
  const sec = section(t('Add someone'), t('With their e-mail address and no password, they get a welcome mail, choose their password and set up a second factor if their role needs one. With a password: admins and roles that sync or write then need an enrolment link for their second factor.'));
  const form = h('form', { class: 'sec-add adm-add' });
  const name = h('input', { class: 'field', type: 'text', autocomplete: 'off', autocapitalize: 'none', spellcheck: 'false', maxlength: '64', placeholder: t('User name'), 'aria-label': t('User name'), required: '' });
  const email = h('input', { class: 'field', type: 'email', autocomplete: 'off', maxlength: '254', placeholder: t('E-mail, to invite them'), 'aria-label': t('E-mail, to invite them') });
  const pw = h('input', { class: 'field', type: 'password', autocomplete: 'new-password', placeholder: t('Or a password (12+ characters)'), 'aria-label': t('Password') });
  const role = roleSelect(roles, 'viewer', t('Role'));
  const add = h('button', { class: 'pill-btn', type: 'submit' }, t('Add'));
  // An address and no password: the button says what happens.
  const label = () => { add.textContent = email.value.trim() && !pw.value ? t('Invite') : t('Add'); };
  email.oninput = pw.oninput = label;
  form.append(name, email, pw, role, add);
  form.onsubmit = (e) => {
    e.preventDefault();
    const to = email.value.trim();
    let said = null;
    change(body, add, async () => {
      const res = await api('admin/users', 'POST', { name: name.value.trim(), password: pw.value, role: role.value, email: to });
      if (res.ok) {
        const a = await res.clone().json();
        if (a.invited) said = a.mailed ? [true, t('The invitation is on its way to {email}.', { email: to })] : [false, ERRORS[a.mail_error]?.() || t('The invitation couldn’t be sent. Try again later.')];
      }
      return res;
    }).then(() => { if (said) invitedNote(body, ...said); });
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
    promote(body, save, () => api('admin/roles/' + encodeURIComponent(role), 'PUT', { scopes: picked }));
  };
  sec.append(form);
  return sec;
}

/* ---------- sessions, notes and the audit log ---------- */

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

// Notes whose workout is gone from the synced data: only admins still see them.
function notesTab(body, list) {
  const sec = section(t('Notes without a workout'), t('Their workout is no longer in the synced data.'));
  if (!list.length) sec.append(h('p', { class: 'empty-tab' }, t('Every note has its workout.')));
  for (const n of list) {
    const row = h('div', { class: 'adm-note' });
    const del = h('button', { class: 'pill-btn', type: 'button' }, t('Delete'));
    del.onclick = () => {
      if (confirm(t('Delete this note?'))) change(body, del, () => api('notes/' + encodeURIComponent(n.workout_id), 'DELETE'));
    };
    const info = h('span', { class: 'muted' }, t('by {name}, {date}', { name: n.updated_by || t('deleted user'), date: dfTime.format(new Date(n.updated_at)) }));
    row.append(info, del, noteFold(withText(n)));
    sec.append(row);
  }
  return [sec];
}

// Which entries the audit log shows: all, the changes and sign-ins without
// the failed tries, or only those (pentest F20: guesses mustn't bury the rest).
let auditWhich = '';

function auditTab(body, list) {
  const sec = section(t('Audit log'), t('The last {n} entries', { n: list.length }));
  const pick = h('select', { class: 'field adm-audit-which', 'aria-label': t('Show') });
  for (const [value, label] of [['', t('Everything')], ['security', t('Without failed tries')], ['failures', t('Only failed tries')]]) {
    pick.append(h('option', { value }, label));
  }
  pick.value = auditWhich;
  pick.onchange = () => {
    auditWhich = pick.value;
    load(body, 'audit');
  };
  sec.append(pick);
  if (!list.length) sec.append(h('p', { class: 'empty-tab' }, t('Nothing yet.')));
  for (const e of list) {
    const row = h('div', { class: 'adm-log' });
    const what = h('span');
    what.append(h('b', {}, e.actor || '–'), ' ' + (ACTIONS[e.action]?.() || e.action));
    // A repeated failure is one entry: how often, the time of the latest.
    if (e.count > 1) what.append(h('span', { class: 'adm-tag warn' }, '×' + e.count));
    if (e.detail) what.append(h('span', { class: 'muted' }, ' · ' + e.detail));
    row.append(h('time', { class: 'muted', datetime: e.at }, dfTime.format(new Date(e.at))), what, h('span', { class: 'muted adm-ip' }, e.ip || ''));
    sec.append(row);
  }
  return [sec];
}
