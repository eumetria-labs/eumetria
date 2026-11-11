// Confirming who one is (step-up): changing how an account signs in and the
// admin tools need a recent proof, the sign-in or this. When the server
// answers 403 "reauth", api() (account.js) opens this window and tries the
// request again once it worked. The user confirms with what a stolen
// session cookie doesn't come with: one of their passkeys or keys, a code
// from their authenticator app, or, without a second factor, the password.
// Recovery codes don't count here; they are for signing in.
//
// This module calls the server with fetch() itself, not api(): api() opens
// it, and a request from here must never open it again.

import { h } from './dom.js';
import { openDialog } from './dialog.js';
import { credentialJSON, requestOptions, supported } from './webauthn.js';
import { t } from './i18n.js';

function post(path, body) {
  return fetch('api/' + path, {
    method: 'POST', cache: 'no-store', credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body ?? {}),
  });
}

async function errorOf(res) {
  try { return (await res.json()).error; } catch { return ''; }
}

let open = null; // one window at a time, however many requests asked

// confirmIdentity resolves true once the user confirmed who they are, false
// when they closed the window.
export function confirmIdentity() {
  open ??= ask().finally(() => { open = null; });
  return open;
}

async function ask() {
  const res = await fetch('api/security', { cache: 'no-store', credentials: 'same-origin' }).catch(() => null);
  if (!res?.ok) return false;
  const s = await res.json();
  const hasKeys = s.keys.length > 0 && supported();
  const factor = s.totp || s.keys.length > 0;

  return new Promise((resolve) => {
    let ok = false;
    const close = h('button', { class: 'sheet-close', type: 'button', 'aria-label': t('Close') }, '×');
    const head = h('div', { class: 'sheet-head wo-head' });
    const titles = h('div', { class: 'wo-titles' });
    titles.append(h('h2', { id: 'stepup-title' }, t('Confirm it’s you')));
    head.append(titles, close);
    const body = h('div', { class: 'wo-body sec-body' });
    const panel = h('div', { class: 'wo-panel sec-panel stepup-panel' });
    panel.append(head, body);
    const win = openDialog({ cls: 'wo-overlay', labelledBy: 'stepup-title', content: [panel], onClose: () => resolve(ok) });
    close.onclick = () => win.close();

    const msg = h('p', { class: 'lock-error', role: 'alert', hidden: '' });
    const fail = async (r) => {
      const why = r ? await errorOf(r) : '';
      msg.textContent = r?.status === 429 ? t('Too many attempts. Try again later.')
        : why === 'invalid' ? t('That didn’t work. Try again.')
        : r ? t('That didn’t work. Try again.') : t('Server not reachable.');
      msg.hidden = false;
    };
    const done = () => {
      ok = true;
      win.close();
    };

    body.append(h('p', { class: 'muted' }, factor
      ? t('This change needs you to confirm it’s you, with a passkey, a key or your authenticator app.')
      : t('This change needs you to confirm it’s you with your password.')));

    if (hasKeys) {
      const key = h('button', { class: 'pill-btn', type: 'button' }, t('Use passkey or key'));
      key.onclick = async () => {
        msg.hidden = true;
        key.disabled = true;
        try {
          const begin = await post('security/reauth/key/begin');
          if (!begin.ok) return fail(begin);
          const cred = await navigator.credentials.get({ publicKey: requestOptions(await begin.json()) });
          const fin = await post('security/reauth/key/finish', { credential: credentialJSON(cred) });
          if (fin.ok) return done();
          fail(fin);
        } catch (err) {
          if (err.name !== 'NotAllowedError' && err.name !== 'AbortError') fail(null);
        } finally {
          key.disabled = false;
        }
      };
      body.append(key);
    }

    // A code from the app, or the password without any second factor.
    if (s.totp || !factor) {
      const form = h('form', { class: 'sec-add' });
      const input = s.totp
        ? h('input', { class: 'field', type: 'text', inputmode: 'numeric', autocomplete: 'one-time-code', maxlength: '6', placeholder: '123456', 'aria-label': t('Code from the app'), required: '' })
        : h('input', { class: 'field', type: 'password', autocomplete: 'current-password', placeholder: t('Password'), 'aria-label': t('Password'), required: '' });
      const confirm = h('button', { class: 'pill-btn', type: 'submit' }, t('Confirm'));
      form.append(input, confirm);
      form.onsubmit = async (e) => {
        e.preventDefault();
        msg.hidden = true;
        confirm.disabled = true;
        try {
          const r = await post('security/reauth', s.totp ? { code: input.value.trim() } : { password: input.value });
          if (r.ok) return done();
          await fail(r);
          input.select();
        } catch {
          fail(null);
        } finally {
          confirm.disabled = false;
        }
      };
      if (s.totp) body.append(h('p', {}, t('Enter the 6-digit code from your authenticator app.')));
      body.append(form);
      input.focus();
    }
    body.append(msg);
  });
}

// confirmNote is what a window shows when its change was refused because
// the user closed the confirmation: a button to confirm, then they try again.
export function confirmNote() {
  const box = h('div', { class: 'notice notice-row adm-error sec-error' });
  const again = h('button', { class: 'pill-btn', type: 'button' }, t('Confirm it’s you'));
  again.onclick = async () => {
    if (await confirmIdentity()) box.remove();
  };
  box.append(h('span', {}, t('This change needs you to confirm it’s you first. Then try again.')), again);
  return box;
}
