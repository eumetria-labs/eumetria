// Accounts: the server hands the data only to signed-in users (api.go). At
// start the page asks who is signed in; without a session it shows the
// sign-in form and goes on once someone signed in. The account menu in the
// header opens the security settings and signs out.
//
// Signing in: name and password, then a second factor if the account has
// one (a passkey or security key, a code from an authenticator app, or a
// recovery code). A passkey that verifies its user signs in on its own,
// from the button or from the browser's suggestions in the name field.
//
// The page also runs from plain files (local development, the smoke test):
// there is no API then, and nobody needs to sign in.

import { $, hashParam, popupMenu, setHashParam } from './dom.js';
import { autofillSupported, credentialJSON, requestOptions, supported } from './webauthn.js';
import { openSecurity } from './security.js';
import { t } from './i18n.js';

// Who is signed in: { name, role, sync, second_factor, needs_second_factor },
// or null without accounts.
export const account = { user: null };

const changeListeners = [];
// onUserChange runs fn whenever what the signed-in user may do changes
// (e.g. an admin set up a second factor and may sync now).
export function onUserChange(fn) {
  changeListeners.push(fn);
}

// api calls the server's API. Anything that changes state is sent as JSON,
// which the server requires so other sites can't trigger it.
export function api(path, method = 'GET', body) {
  const opts = { method, cache: 'no-store', credentials: 'same-origin' };
  if (method !== 'GET' && method !== 'DELETE') {
    opts.headers = { 'Content-Type': 'application/json' };
    opts.body = JSON.stringify(body ?? {});
  }
  return fetch('api/' + path, opts);
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
      return; // offline: the service worker may still have the data
    }
    if (res.ok) return welcome(await res.json());
    if (res.status === 503 && (await reason(res)) === 'setup') return setup();
    if (res.status !== 401) return; // no API: plain files
    welcome(await askSignIn());
  })().finally(() => { waiting = null; });
  return waiting;
}

// refreshUser asks the server again who is signed in, after the second
// factors changed.
export async function refreshUser() {
  const res = await api('me').catch(() => null);
  if (res?.ok) welcome(await res.json());
}

function welcome(user) {
  account.user = user;
  $('#account-initial').textContent = user.name.slice(0, 1).toUpperCase();
  $('#account-name').textContent = user.name;
  $('#account').hidden = false;
  $('#mfa-notice').hidden = !user.needs_second_factor;
  for (const fn of changeListeners) fn(user);
  // "#security" opens the security settings, also after signing in again.
  if (hashParam('security') != null) {
    setHashParam('security', null);
    openSecurity($('#account-btn'));
  }
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

function askSignIn() {
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
        if (err.name !== 'AbortError') showError(t('Signing in with a passkey didn’t work. Try again, or use your password.'));
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
          try {
            done(await secondStep(ans));
          } catch {
            // Back to the first step.
            form.hidden = false;
            password.select();
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
        const cred = await navigator.credentials.get({ publicKey: requestOptions(options) });
        const fin = await api('login/key/finish', 'POST', { ticket, credential: credentialJSON(cred) });
        if (fin.ok) return finish(await fin.json());
        fail(await codeFailure(fin));
      } catch (err) {
        if (err.res) fail(await codeFailure(err.res));
        else if (err.name !== 'AbortError' && err.name !== 'NotAllowedError') fail(t('The key didn’t answer. Try again.'));
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

// Security settings, or signing out here or everywhere. Signing out also
// makes this device forget the data it kept for offline use.
export function initAccount() {
  popupMenu($('#account-btn'), $('#account-menu'), async (item) => {
    if (item.dataset.action === 'security') return openSecurity($('#account-btn'));
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

async function forgetData() {
  try {
    for (const name of await caches.keys()) {
      if (name.startsWith('stats-data')) await caches.delete(name);
      else await (await caches.open(name)).delete('stats.json', { ignoreSearch: true });
    }
  } catch { /* no cache storage */ }
}
