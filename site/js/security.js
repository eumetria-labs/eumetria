// The security settings (account menu → Security): the password, passkeys
// and security keys, the authenticator app and recovery codes. Changing a
// second factor needs a sign-in within the last 10 minutes; the server says
// when it's older, and the window offers to sign in again and come back
// here. The password needs no such sign-in: the current one proves it.

import { LOCALE } from './format.js';
import { $, download, h, setHashParam } from './dom.js';
import { openDialog } from './dialog.js';
import { account, api, reason, refreshUser, signOut } from './account.js';
import { credentialJSON, creationOptions, supported } from './webauthn.js';
import { t, tn } from './i18n.js';

const dfDate = new Intl.DateTimeFormat(LOCALE, { day: 'numeric', month: 'short', year: 'numeric' });

let win = null;

export async function openSecurity(returnFocus) {
  win?.close(true);
  const close = h('button', { class: 'sheet-close', type: 'button', 'aria-label': t('Close') }, '×');
  const head = h('div', { class: 'sheet-head wo-head' });
  const titles = h('div', { class: 'wo-titles' });
  titles.append(h('h2', { id: 'sec-title' }, t('Security')), h('p', { class: 'muted' }, account.user?.name || ''));
  head.append(titles, close);
  const body = h('div', { class: 'wo-body sec-body' });
  const panel = h('div', { class: 'wo-panel sec-panel' });
  panel.append(head, body);
  win = openDialog({ cls: 'wo-overlay', labelledBy: 'sec-title', content: [panel], returnFocus });
  close.onclick = () => win.close();
  await render(body);
}

async function render(body) {
  const res = await api('security').catch(() => null);
  if (!res?.ok) {
    body.replaceChildren(h('p', { class: 'empty-tab' }, t('The security settings could not be loaded.')));
    return;
  }
  const s = await res.json();
  const parts = [];
  if (s.required && !s.keys.length && !s.totp) {
    parts.push(h('p', { class: 'notice' }, t('As an admin you need a second factor before you can sync or use the admin tools. A passkey is the easiest.')));
  }
  parts.push(passwordSection());
  if (!s.fresh) parts.push(reauthNote());
  parts.push(keysSection(s, body), totpSection(s, body), recoverySection(s, body));
  body.replaceChildren(...parts);
}

// After the sign-in got old: sign in again and come back to this window.
function reauthNote() {
  const box = h('div', { class: 'notice notice-row' });
  const again = h('button', { class: 'pill-btn', type: 'button' }, t('Sign in again'));
  again.onclick = () => {
    setHashParam('security', '');
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

// change runs a request that changes a factor and shows the window again;
// when the sign-in is too old it shows that instead. failText is what to
// say when the server refuses.
async function change(body, button, run, failText = t('That didn’t work. Try again.')) {
  body.querySelector('.sec-error')?.remove();
  button.disabled = true;
  try {
    const res = await run();
    if (!res.ok) {
      if ((await reason(res)) === 'reauth') return render(body);
      throw new Error(failText);
    }
    await refreshUser();
    await render(body);
  } catch (err) {
    // Closing the browser's passkey prompt is no error.
    if (err.name === 'NotAllowedError' || err.name === 'AbortError') return;
    console.error('security settings', err);
    body.prepend(h('p', { class: 'lock-error sec-error', role: 'alert' }, err.message === failText ? failText : t('That didn’t work. Try again.')));
  } finally {
    button.disabled = false;
  }
}

/* ---------- password ---------- */

// The server's reasons for refusing a new password, in the page's words.
const PASSWORD_ERRORS = {
  wrong_password: () => t('That isn’t your current password.'),
  too_short: () => t('The new password needs at least 12 characters.'),
  too_long: () => t('The new password is too long.'),
  common: () => t('That password is one of the most used ones. Pick another.'),
  is_name: () => t('The password can’t be your user name.'),
  same: () => t('The new password is the same as the current one.'),
};

function passwordSection() {
  const sec = section(t('Password'), t('At least 12 characters. Long beats complicated.'));
  const form = h('form', { class: 'sec-password' });
  // For password managers: whose password this is.
  const user = h('input', { type: 'text', autocomplete: 'username', value: account.user?.name || '', hidden: '', 'aria-hidden': 'true', tabindex: '-1' });
  const field = (placeholder, auto) => h('input', { class: 'field', type: 'password', autocomplete: auto, placeholder, 'aria-label': placeholder, required: '' });
  const current = field(t('Current password'), 'current-password');
  const next = field(t('New password'), 'new-password');
  const again = field(t('New password again'), 'new-password');
  const msg = h('p', { class: 'sec-msg', role: 'status', hidden: '' });
  const save = h('button', { class: 'pill-btn', type: 'submit' }, t('Change password'));
  const say = (text, bad) => {
    msg.textContent = text;
    msg.classList.toggle('lock-error', bad);
    msg.hidden = false;
  };
  form.append(user, current, next, again, msg, save);
  form.onsubmit = async (e) => {
    e.preventDefault();
    msg.hidden = true;
    if (next.value !== again.value) return say(t('The two new passwords differ.'), true);
    if ([...next.value].length < 12) return say(PASSWORD_ERRORS.too_short(), true);
    save.disabled = true;
    try {
      const res = await api('security/password', 'POST', { current: current.value, new: next.value });
      if (res.ok) {
        form.reset();
        user.value = account.user?.name || '';
        return say(t('Password changed. Your other devices are signed out.'), false);
      }
      if (res.status === 429) return say(t('Too many wrong passwords. Try again later.'), true);
      const why = await reason(res);
      say(PASSWORD_ERRORS[why]?.() || t('That didn’t work. Try again.'), true);
      if (why === 'wrong_password') current.select();
    } catch {
      say(t('Server not reachable.'), true);
    } finally {
      save.disabled = false;
    }
  };
  sec.append(form);
  return sec;
}

/* ---------- passkeys and security keys ---------- */

function keysSection(s, body) {
  const sec = section(t('Passkeys and security keys'), t('Sign in with a fingerprint, your face, a PIN or a key like a YubiKey.'));
  for (const k of s.keys) {
    const row = h('div', { class: 'day-item sec-item' });
    const name = h('span', { class: 'name' });
    name.append(h('span', { class: 'txt' }, k.name));
    const note = [t('added {date}', { date: dfDate.format(new Date(k.created_at)) })];
    if (k.last_used) note.push(t('used {date}', { date: dfDate.format(new Date(k.last_used)) }));
    if (k.passwordless) note.push(t('signs in on its own'));
    const remove = h('button', { class: 'pill-btn', type: 'button' }, t('Remove'));
    remove.disabled = !s.fresh;
    remove.onclick = () => {
      if (confirm(t('Remove “{name}”?', { name: k.name }))) change(body, remove, () => api('security/key/' + k.id, 'DELETE'));
    };
    row.append(name, h('span', { class: 'amt' }, note.join(' · ')), remove);
    sec.append(row);
  }
  if (!supported()) {
    sec.append(h('p', { class: 'muted' }, t('This browser can’t use passkeys.')));
    return sec;
  }
  const add = h('form', { class: 'sec-add' });
  const input = h('input', { class: 'field', type: 'text', maxlength: '60', placeholder: t('Name, e.g. iPhone or YubiKey'), 'aria-label': t('Name of the key') });
  const button = h('button', { class: 'pill-btn', type: 'submit' }, t('Add passkey or key'));
  input.disabled = button.disabled = !s.fresh;
  add.append(input, button);
  add.onsubmit = (e) => {
    e.preventDefault();
    change(body, button, async () => {
      const res = await api('security/key/begin', 'POST', { name: input.value.trim() });
      if (!res.ok) return res;
      const cred = await navigator.credentials.create({ publicKey: creationOptions(await res.json()) });
      return api('security/key/finish', 'POST', { credential: credentialJSON(cred) });
    });
  };
  sec.append(add);
  return sec;
}

/* ---------- authenticator app ---------- */

function totpSection(s, body) {
  const sec = section(t('Authenticator app'), s.totp ? t('On') : t('Off'));
  if (s.totp) {
    const remove = h('button', { class: 'pill-btn', type: 'button' }, t('Remove'));
    remove.disabled = !s.fresh;
    remove.onclick = () => {
      if (confirm(t('Remove the authenticator app?'))) change(body, remove, () => api('security/totp', 'DELETE'));
    };
    sec.append(h('p', { class: 'muted' }, t('Codes from your app work as the second step after the password.')), remove);
    return sec;
  }
  const start = h('button', { class: 'pill-btn', type: 'button' }, t('Set up'));
  start.disabled = !s.fresh;
  start.onclick = async () => {
    start.disabled = true;
    const res = await api('security/totp/begin', 'POST').catch(() => null);
    start.disabled = false;
    if (res?.ok) return showSetup(sec, body, await res.json());
    if (res && (await reason(res)) === 'reauth') render(body);
  };
  sec.append(h('p', { class: 'muted' }, t('For example Google Authenticator, Aegis, 2FAS or a YubiKey with Yubico Authenticator.')), start);
  return sec;
}

// showSetup shows the QR code and the secret, and asks for the first code.
function showSetup(sec, body, { secret, qr }) {
  const box = h('div', { class: 'sec-totp' });
  const img = h('img', { src: qr, alt: t('QR code for your authenticator app'), width: '200', height: '200', class: 'sec-qr' });
  const steps = h('div', { class: 'sec-steps' });
  steps.append(
    h('p', {}, t('Scan the code with your app, or type in this key:')),
    h('code', { class: 'sec-secret' }, secret.replace(/(.{4})/g, '$1 ').trim()),
  );
  const form = h('form', { class: 'sec-add' });
  const code = h('input', { class: 'field', type: 'text', inputmode: 'numeric', autocomplete: 'one-time-code', maxlength: '6', placeholder: '123456', 'aria-label': t('Code from the app') });
  const ok = h('button', { class: 'pill-btn', type: 'submit' }, t('Confirm'));
  form.append(code, ok);
  form.onsubmit = (e) => {
    e.preventDefault();
    change(body, ok, () => api('security/totp/confirm', 'POST', { code: code.value.trim() }),
      t('Wrong code. Check that the time on your phone is right, then try the next one.')).then(() => {
      if (document.contains(code)) code.select();
    });
  };
  steps.append(h('p', {}, t('Then enter the code it shows:')), form);
  box.append(img, steps);
  sec.querySelector('.pill-btn')?.remove();
  sec.append(box);
  code.focus();
}

/* ---------- recovery codes ---------- */

function recoverySection(s, body) {
  const left = s.recovery_left;
  const sec = section(t('Recovery codes'), left ? tn(left, '{n} unused', '{n} unused') : t('None yet'));
  sec.append(h('p', { class: 'muted' }, t('If you lose your phone or key, a recovery code gets you in instead. Each works once. Keep them in your password manager or on paper.')));
  const make = h('button', { class: 'pill-btn', type: 'button' }, left ? t('Make new codes') : t('Make codes'));
  make.disabled = !s.fresh;
  make.onclick = async () => {
    if (left && !confirm(t('New codes replace the old ones. Go on?'))) return;
    make.disabled = true;
    const res = await api('security/recovery', 'POST').catch(() => null);
    if (!res?.ok) {
      if (res && (await reason(res.clone())) === 'reauth') return render(body);
      make.disabled = false;
      return;
    }
    const { codes } = await res.json();
    await render(body);
    showCodes(body, codes);
  };
  sec.append(make);
  return sec;
}

// showCodes shows new recovery codes this once, to copy or save.
function showCodes(body, codes) {
  const box = h('div', { class: 'sec-codes' });
  box.append(h('p', { class: 'notice' }, t('Your new recovery codes. They are shown only now.')));
  const list = h('ol', { class: 'sec-code-list' });
  for (const c of codes) list.append(h('li', {}, c));
  const text = `Eumetria recovery codes for ${account.user?.name}\n\n${codes.join('\n')}\n`;
  const copy = h('button', { class: 'pill-btn', type: 'button' }, t('Copy'));
  copy.onclick = () => navigator.clipboard?.writeText(text).then(() => { copy.textContent = t('Copied'); });
  const save = h('button', { class: 'pill-btn', type: 'button' }, t('Save as file'));
  save.onclick = () => download('eumetria-recovery-codes.txt', 'text/plain', text);
  const row = h('div', { class: 'sec-row' });
  row.append(copy, save);
  box.append(list, row);
  body.querySelector('.wo-sec:last-child')?.append(box);
  box.scrollIntoView({ block: 'nearest' });
}
