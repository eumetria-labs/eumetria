// Accounts: the server hands the data only to signed-in users (api.go). At
// start the page asks who is signed in; without a session it shows the
// sign-in form and goes on once someone signed in. The account menu in the
// header opens the profile and the security settings, switches the style
// and the language, and signs out.
//
// Signing in: name and password, then a second factor if the account has
// one (a passkey or security key, a code from an authenticator app, or a
// recovery code). A passkey that verifies its user signs in on its own,
// from the button or from the browser's suggestions in the name field.
//
// The page also runs from plain files (local development with -out, the
// smoke test): there is no API then, and nobody needs to sign in.
//
// What this device keeps for offline use belongs to the user who loaded
// it: signing out, a 401 and another user signing in all clear it.

import { $, h, hashParam, popupMenu, setHashParam } from './dom.js';
import { autofillSupported, credentialJSON, requestOptions, supported } from './webauthn.js';
import { openSecurity, useEnrolLink } from './security.js';
import { openManage } from './manage.js';
import { openProfile } from './profile.js';
import { openSettings } from './settings.js';
import { confirmIdentity } from './stepup.js';
import { store } from './state.js';
import { DATE_LOCALE, LANG, setLang, t } from './i18n.js';

// Who is signed in: { name, role, avatar, scopes, sync, second_factor,
// needs_second_factor }, or null without accounts.
export const account = { user: null };

const changeListeners = [];
// onUserChange runs fn whenever what the signed-in user may do changes
// (e.g. an admin set up a second factor and may sync now).
export function onUserChange(fn) {
  changeListeners.push(fn);
}

// api calls the server's API. Anything that changes state is sent as JSON,
// which the server requires so other sites can't trigger it; a DELETE too,
// though it has no body.
//
// When the server wants the user to confirm who they are first (403
// "reauth": changing how the account signs in, the admin tools), it asks
// them (stepup.js) and sends the request once more. Closed without
// confirming, the caller gets the 403 and says so.
export async function api(path, method = 'GET', body) {
  const send = async () => {
    const opts = { method, cache: 'no-store', credentials: 'same-origin' };
    if (method !== 'GET') {
      opts.headers = { 'Content-Type': 'application/json' };
      if (method !== 'DELETE') opts.body = JSON.stringify(body ?? {});
    }
    const res = await fetch('api/' + path, opts);
    // The session ended: what this device kept of the data goes (F11).
    if (res.status === 401 && (await reason(res.clone())) === 'signin') await forgetData();
    return res;
  };
  const res = await send();
  if (res.status !== 403 || (await reason(res.clone())) !== 'reauth') return res;
  return (await confirmIdentity()) ? send() : res;
}

export async function reason(res) {
  try { return (await res.json()).error; } catch { return ''; }
}

let waiting = null; // one sign-in at a time, however many requests found out

// signedIn resolves once someone is signed in (or the site has no
// accounts). It shows the sign-in form when needed.
export function signedIn() {
  waiting ??= (async () => {
    let res;
    try {
      res = await api('me');
    } catch {
      // Offline: the service worker has the data only if this device may
      // keep it ("Keep me signed in"). Otherwise wait for the network.
      if (await offlineOk()) return;
      return waitOnline();
    }
    if (res.ok) return welcome(await res.json());
    if (res.status === 503 && (await reason(res)) === 'setup') return setup();
    if (res.status !== 401) return; // no API: plain files
    // "#invite=<token>": the welcome mail's link; choose a password first.
    const invite = hashParam('invite');
    if (invite) {
      const user = await askInvite(invite);
      if (user?.name) return welcome(user);
      return welcome(await askSignIn(user?.why));
    }
    return welcome(await askSignIn());
  })().finally(() => { waiting = null; });
  return waiting;
}

// Offline without data kept for it: say so and try again once online.
async function waitOnline() {
  document.documentElement.dataset.locked = 'true';
  $('#updated').textContent = t('Offline. Sign in once you’re back online.');
  await new Promise((ok) => addEventListener('online', ok, { once: true }));
  delete document.documentElement.dataset.locked;
  waiting = null;
  return signedIn();
}

// This device may keep the data for offline use only when the session is
// "Keep me signed in" (pentest F11): a flag the service worker reads (sw.js).
const FLAGS = 'eumetria-flags';
async function offlineOk() {
  try { return !!(await (await caches.open(FLAGS)).match('offline-ok')); } catch { return false; }
}
async function keepOffline(on) {
  try {
    const flags = await caches.open(FLAGS);
    if (on) await flags.put('offline-ok', new Response('1'));
    else await flags.delete('offline-ok');
  } catch { /* no cache storage */ }
}

// refreshUser asks the server again who is signed in, after the second
// factors changed.
export async function refreshUser() {
  const res = await api('me').catch(() => null);
  if (res?.ok) await welcome(await res.json());
}

async function welcome(user) {
  if (store.get('user') !== user.name) {
    await forgetData();
    store.set('user', user.name);
  }
  account.user = user;
  // Not "Keep me signed in": nothing of the data stays on this device, also
  // nothing kept before (by an older page, or another session).
  if (user.remember) await keepOffline(true);
  else await forgetData();
  $('#account-initial').textContent = user.name.slice(0, 1).toUpperCase();
  showPicture(user.avatar);
  $('#account-name').textContent = user.name;
  $('#account').hidden = false;
  $('#mfa-notice').hidden = !user.needs_second_factor;
  // Not an admin: a role that writes, or an admin asked this user for a
  // second factor.
  if (!user.scopes?.includes('admin') && !user.scopes?.includes('sync')) {
    $('#mfa-text').textContent = user.scopes?.some((s) => s.startsWith('edit.'))
      ? t('Please set up a second factor, such as a passkey: until then you can’t write notes or lifts.')
      : t('Please set up a second factor, such as a passkey: until then the data stays hidden.');
  }
  showNotices(user.notices || []);
  // "#email=<token>": the link that confirms an e-mail address (phase 6).
  const emailToken = hashParam('email');
  if (emailToken != null) {
    setHashParam('email', null);
    api('profile/email/confirm', 'POST', { token: emailToken }).then((res) => {
      if (res.ok) openProfile($('#account-btn'));
      else $('#updated').textContent = t('That confirmation link didn’t work: it was used already or is older than a day.');
    }, () => {});
  }
  // "#enrol=<token>": the enrolment link an admin made; it opens the
  // security settings, where the first second factor is set up with it.
  const enrol = hashParam('enrol');
  if (enrol != null) {
    setHashParam('enrol', null);
    useEnrolLink(enrol);
    if (user.needs_second_factor) openSecurity($('#account-btn'));
  }
  $('#account-admin').hidden = $('#account-settings').hidden = !user.scopes?.includes('admin');
  for (const fn of changeListeners) fn(user);
  // "#profile" opens the profile, also after signing in again.
  if (hashParam('profile') != null) {
    setHashParam('profile', null);
    openProfile($('#account-btn'));
  }
  // "#security" opens the security settings, also after signing in again.
  if (hashParam('security') != null) {
    setHashParam('security', null);
    openSecurity($('#account-btn'));
  }
  // "#settings=google" opens that tab, also after signing in again and when
  // Connect Google comes back ("&google=connected").
  const settings = hashParam('settings');
  if (settings != null && user.scopes?.includes('admin')) {
    const result = hashParam('google') || '';
    setHashParam('settings', null);
    setHashParam('google', null);
    openSettings($('#account-btn'), settings, result);
  }
  // "#admin=roles" opens Users & Roles on that tab, also after signing in again.
  const admin = hashParam('admin');
  if (admin != null && user.scopes?.includes('admin')) {
    setHashParam('admin', null);
    openManage($('#account-btn'), admin);
  }
}

// What a security notice says (pentest F18/F19): a change to how the account
// signs in, made somewhere else than in this session.
const NOTICES = {
  key_added: (n) => t('A passkey or key “{name}” was added', { name: n.detail }),
  key_removed: () => t('A passkey or key was removed'),
  totp_added: () => t('An authenticator app was set up'),
  totp_removed: () => t('The authenticator app was removed'),
  recovery_codes_made: () => t('New recovery codes were made'),
  token_created: (n) => t('An API token “{name}” was made', { name: n.detail }),
  password_changed: () => t('Your password was changed'),
  password_reset: (n) => t('An admin ({name}) set a new password', { name: n.detail }),
  factors_reset: () => t('Your second factors were removed on the server'),
  factors_dropped: () => t('Your account got new rights: second factors set up without an enrolment link were removed'),
};
const noticeTime = new Intl.DateTimeFormat(DATE_LOCALE, { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' });

// showNotices lists the changes the user hasn't dismissed yet, with a way
// to the security settings in case one wasn't them.
function showNotices(list) {
  const box = $('#sec-notices');
  box.hidden = !list.length;
  if (!list.length) return box.replaceChildren();
  const items = h('ul', { class: 'sec-notice-list' });
  for (const n of list) {
    const when = [noticeTime.format(new Date(n.at)), n.ip].filter(Boolean).join(' · ');
    items.append(h('li', {}, (NOTICES[n.kind]?.(n) ?? n.kind) + ' (' + when + ')'));
  }
  const open = h('button', { class: 'pill-btn', type: 'button' }, t('Security settings'));
  open.onclick = () => openSecurity(open);
  const ok = h('button', { class: 'pill-btn', type: 'button' }, t('Got it'));
  ok.onclick = async () => {
    ok.disabled = true;
    const res = await api('notices/seen', 'POST', { up_to: Math.max(...list.map((n) => n.id)) }).catch(() => null);
    if (res?.ok) showNotices([]);
    ok.disabled = false;
  };
  const row = h('div', { class: 'sec-row' });
  row.append(open, ok);
  box.replaceChildren(
    h('b', {}, t('Changes to how you sign in:')), items,
    h('p', { class: 'muted' }, t('Not you? Remove what you don’t know in the security settings and change your password.')), row);
}

// The round button shows the picture, or the initial without one.
function showPicture(src) {
  const photo = $('#account-photo');
  if (src) photo.src = src;
  photo.hidden = !src;
  $('#account-initial').hidden = !!src;
}

// setup says how to make the first account; the page waits forever.
function setup() {
  document.documentElement.dataset.locked = 'true';
  $('#updated').textContent = '';
  $('#setup').hidden = false;
  return new Promise(() => {});
}

/* ---------- signing in ---------- */

let autofill = null; // the passkey request the name field's suggestions answer

// askSignIn shows the sign-in form; note, if given, says why first.
function askSignIn(note) {
  return new Promise((resolve) => {
    const form = $('#login');
    const name = $('#login-name');
    const password = $('#login-password');
    const error = $('#login-error');
    const button = form.querySelector('.lock-btn');
    const passkeyBtn = $('#login-passkey');
    const remember = () => $('#login-remember').checked;
    document.documentElement.dataset.locked = 'true';
    $('#updated').textContent = t('Signed out');
    form.hidden = false;
    if (note) {
      error.textContent = note;
      error.hidden = false;
    }
    (name.value ? password : name).focus();

    const done = (user) => {
      autofill?.abort();
      form.hidden = true;
      $('#login-2').hidden = true;
      form.onsubmit = null;
      password.value = '';
      delete document.documentElement.dataset.locked;
      resolve(user);
    };
    const showError = (text) => {
      error.textContent = text;
      error.hidden = false;
    };

    // Saved passkeys show up as suggestions in the name field.
    const offerPasskeys = async () => {
      if (!(await autofillSupported())) return;
      passkeySignIn('conditional', remember).then(done, () => {});
    };
    passkeyBtn.hidden = !supported();
    passkeyBtn.onclick = async () => {
      error.hidden = true;
      try {
        done(await passkeySignIn(undefined, remember));
      } catch (err) {
        if (err.name !== 'AbortError') {
          console.warn('passkey sign-in:', err.name, err.message);
          showError(t('Signing in with a passkey didn’t work. Try again, or use your password.'));
        }
        offerPasskeys();
      }
    };
    offerPasskeys();

    form.onsubmit = async (e) => {
      e.preventDefault();
      button.disabled = true;
      error.hidden = true;
      try {
        const res = await api('login', 'POST', { name: name.value.trim(), password: password.value, remember: remember() });
        if (res.ok) {
          const ans = await res.json();
          if (ans.next !== 'second_factor') return done(ans);
          form.hidden = true;
          // The name field's passkey request would block the key's: a
          // browser runs one at a time, and refuses the next one outright.
          autofill?.abort();
          autofill = null;
          try {
            done(await secondStep(ans));
          } catch {
            // Back to the first step, with its suggestions again.
            form.hidden = false;
            password.select();
            offerPasskeys();
          }
          return;
        }
        showError(await failure(res));
        password.select();
      } catch {
        showError(t('Server not reachable.'));
      } finally {
        button.disabled = false;
      }
    };
  });
}

// askInvite shows the welcome form of an invitation: the user name, a new
// password twice. It resolves with the signed-in user, or with { why } when
// the link doesn't work (any more); then the usual sign-in follows, saying
// why. With a role that needs a second factor, the answer carries an
// enrolment token and the security settings open for it (welcome).
async function askInvite(token) {
  setHashParam('invite', null);
  const check = await api('invite', 'POST', { token }).catch(() => null);
  if (!check?.ok) {
    return { why: check?.status === 429 ? t('Too many attempts. Try again later.') : INVITE_GONE() };
  }
  const { name, needs_second_factor: factor } = await check.json();
  return new Promise((resolve) => {
    document.documentElement.dataset.locked = 'true';
    const pw = h('input', { type: 'password', autocomplete: 'new-password', placeholder: t('New password'), 'aria-label': t('New password'), required: '' });
    const again = h('input', { type: 'password', autocomplete: 'new-password', placeholder: t('New password again'), 'aria-label': t('New password again'), required: '' });
    // For password managers: whose password this is.
    const user = h('input', { type: 'text', autocomplete: 'username', value: name, hidden: '', 'aria-hidden': 'true', tabindex: '-1' });
    const remember = h('input', { type: 'checkbox', checked: '' });
    const keep = h('label', { class: 'lock-remember' });
    keep.append(remember, ' ' + t('Keep me signed in on this device'));
    const error = h('p', { class: 'lock-error', role: 'alert', hidden: '' });
    const go = h('button', { class: 'lock-btn', type: 'submit' }, t('Choose password'));
    const form = h('form', { class: 'card lock' });
    form.append(h('img', { src: 'icons/mark.svg', alt: '', width: '44', height: '44' }), h('h2', {}, t('Welcome, {name}', { name })),
      h('p', { class: 'muted' }, factor ? t('Choose your password: at least 12 characters. Then you set up a second factor.') : t('Choose your password: at least 12 characters.')),
      user, pw, again, keep, error, go);
    $('#login').before(form);
    pw.focus();
    const fail = (text) => {
      error.textContent = text;
      error.hidden = false;
    };
    form.onsubmit = async (e) => {
      e.preventDefault();
      error.hidden = true;
      if (pw.value !== again.value) return fail(t('The two new passwords differ.'));
      go.disabled = true;
      try {
        const res = await api('invite/accept', 'POST', { token, password: pw.value, remember: remember.checked });
        if (res.ok) {
          const { enrol } = await res.json();
          if (enrol) setHashParam('enrol', enrol); // welcome opens the security settings with it
          form.remove();
          delete document.documentElement.dataset.locked;
          const me = await api('me');
          return resolve(me.ok ? await me.json() : null);
        }
        const why = await reason(res);
        if (why === 'expired') {
          form.remove();
          delete document.documentElement.dataset.locked;
          return resolve({ why: INVITE_GONE() });
        }
        fail(PASSWORD_TEXTS[why]?.() || (res.status === 429 ? t('Too many attempts. Try again later.') : t('That didn’t work. Try again.')));
      } catch {
        fail(t('Server not reachable.'));
      } finally {
        go.disabled = false;
      }
    };
  });
}

const INVITE_GONE = () => t('This invitation doesn’t work: it was used already or is older than three days. Ask your admin for a new one.');

// Why a new password is refused, as the server says it.
const PASSWORD_TEXTS = {
  too_short: () => t('The new password needs at least 12 characters.'),
  too_long: () => t('The new password is too long.'),
  common: () => t('That password is one of the most used ones. Pick another.'),
  is_name: () => t('The password can’t be your user name.'),
  busy: () => t('The server is busy. Try again in a moment.'),
};

// passkeySignIn signs in with a passkey alone; mediation "conditional"
// waits for the user to pick one from the name field's suggestions.
async function passkeySignIn(mediation, remember) {
  const res = await api('login/passkey/begin', 'POST');
  if (!res.ok) throw new Error(res.status);
  const { id, options } = await res.json();
  autofill?.abort();
  autofill = new AbortController();
  const cred = await navigator.credentials.get({ publicKey: requestOptions(options), mediation, signal: autofill.signal });
  const fin = await api('login/passkey/finish', 'POST', { id, remember: remember(), credential: credentialJSON(cred) });
  if (!fin.ok) throw new Error(fin.status);
  return fin.json();
}

// secondStep asks for the second factor; it resolves with the user, or
// rejects when the user goes back to the first step.
function secondStep({ ticket, methods }) {
  return new Promise((resolve, reject) => {
    const form = $('#login-2');
    const code = $('#login-code');
    const label = $('#login-code-label');
    const error = $('#login-2-error');
    const keyBtn = $('#login-key');
    const confirm = $('#login-code-btn');
    const recoveryBtn = $('#login-recovery');
    let kind = methods.includes('totp') ? 'totp' : null;
    const show = () => {
      const typing = !!kind;
      code.hidden = confirm.hidden = label.hidden = !typing;
      if (kind === 'totp') {
        label.textContent = t('Enter the 6-digit code from your authenticator app.');
        code.placeholder = '123456';
        code.inputMode = 'numeric';
      } else if (kind === 'recovery') {
        label.textContent = t('Enter one of your recovery codes. Each works once.');
        code.placeholder = 'abcd-efgh-jkmn';
        code.inputMode = 'text';
      }
      recoveryBtn.hidden = kind === 'recovery' || !methods.includes('recovery');
      code.value = '';
    };
    const fail = (text) => {
      error.textContent = text;
      error.hidden = false;
    };
    const finish = (user) => {
      form.onsubmit = null;
      resolve(user);
    };
    error.hidden = true;
    keyBtn.hidden = !methods.includes('key') || !supported();
    show();
    form.hidden = false;
    (kind ? code : keyBtn).focus();

    keyBtn.onclick = async () => {
      error.hidden = true;
      try {
        const res = await api('login/key/begin', 'POST', { ticket });
        if (!res.ok) throw Object.assign(new Error(), { res });
        const options = await res.json();
        // Only one request at a time (see askSignIn).
        autofill?.abort();
        autofill = null;
        const cred = await navigator.credentials.get({ publicKey: requestOptions(options) });
        const fin = await api('login/key/finish', 'POST', { ticket, credential: credentialJSON(cred) });
        if (fin.ok) return finish(await fin.json());
        fail(await codeFailure(fin));
      } catch (err) {
        if (err.res) fail(await codeFailure(err.res));
        else if (err.name !== 'AbortError' && err.name !== 'NotAllowedError') {
          console.warn('key sign-in:', err.name, err.message);
          fail(t('The key didn’t answer. Try again.'));
        }
      }
    };
    recoveryBtn.onclick = () => {
      kind = 'recovery';
      show();
      code.focus();
    };
    $('#login-back').onclick = () => {
      form.hidden = true;
      form.onsubmit = null;
      reject(new Error('back'));
    };
    form.onsubmit = async (e) => {
      e.preventDefault();
      if (!kind || !code.value.trim()) return;
      confirm.disabled = true;
      error.hidden = true;
      try {
        const res = await api('login/code', 'POST', { ticket, kind, code: code.value.trim() });
        if (res.ok) return finish(await res.json());
        fail(await codeFailure(res));
        code.select();
      } catch {
        fail(t('Server not reachable.'));
      } finally {
        confirm.disabled = false;
      }
    };
  });
}

async function failure(res) {
  if (res.status === 401) return t('Wrong name or password.');
  if (res.status === 429) return lockedText(res);
  if (res.status === 503 && (await reason(res.clone())) === 'busy') return t('The server is busy. Try again in a moment.');
  return t('Sign-in failed (HTTP {status}).', { status: res.status });
}

async function codeFailure(res) {
  if (res.status === 429) return lockedText(res);
  if (res.status === 401 && (await reason(res.clone())) === 'expired') return t('That took too long. Go back and sign in again.');
  if (res.status === 401) return t('That didn’t work. Try again.');
  return t('Sign-in failed (HTTP {status}).', { status: res.status });
}

async function lockedText(res) {
  let wait = 0;
  try { wait = (await res.json()).retry_after; } catch { /* no body */ }
  return t('Too many attempts. Try again in {min} min.', { min: Math.max(1, Math.ceil(wait / 60)) });
}

/* ---------- the account menu ---------- */

// The profile, security settings, the style and the language, or signing
// out here or everywhere. Signing out also makes this device forget the
// data it kept for offline use. toggleTheme switches the style.
export function initAccount(toggleTheme) {
  $('#lang-label').textContent = LANG === 'de' ? 'English' : 'Deutsch';
  $('#lang-label').lang = LANG === 'de' ? 'en' : 'de';
  // Offline, or for a picture just removed: the initial instead.
  $('#account-photo').onerror = () => showPicture(null);
  popupMenu($('#account-btn'), $('#account-menu'), async (item) => {
    if (item.dataset.action === 'profile') return openProfile($('#account-btn'));
    if (item.dataset.action === 'settings') return openSettings($('#account-btn'));
    if (item.dataset.action === 'theme') return toggleTheme();
    if (item.dataset.action === 'lang') return setLang(LANG === 'de' ? 'en' : 'de');
    if (item.dataset.action === 'security') return openSecurity($('#account-btn'));
    if (item.dataset.action === 'admin') return openManage($('#account-btn'));
    await signOut(item.dataset.out === 'all');
  });
  $('#mfa-setup').onclick = () => openSecurity($('#mfa-setup'));
}

// signOut ends the session (or all of them) and reloads the page, which
// then asks to sign in. Whatever is in the URL's hash stays.
export async function signOut(everywhere) {
  const res = await api(everywhere ? 'logout-all' : 'logout', 'POST').catch(() => null);
  if (!res?.ok && res?.status !== 401) {
    $('#updated').textContent = t('Signing out failed. Try again.');
    return;
  }
  await forgetData();
  location.reload();
}

// forgetData deletes the data this device kept for offline use: the
// stats, the notes and the files the stats point to (sw.js). The page code
// stays.
export async function forgetData() {
  await keepOffline(false);
  try {
    for (const name of await caches.keys()) {
      if (name.startsWith('stats-data')) await caches.delete(name);
      else {
        const cache = await caches.open(name);
        for (const path of ['api/stats', 'api/notes']) await cache.delete(path, { ignoreSearch: true });
      }
    }
  } catch { /* no cache storage */ }
}
