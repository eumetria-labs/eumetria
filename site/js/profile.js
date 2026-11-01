// The profile (account menu → Profile): the picture, what users say about
// themselves, a way to the security settings, their API tokens and the
// devices they are signed in on. All of it is their own (profile.go). Making
// a token needs a sign-in within the last 10 minutes; the window offers to
// sign in again and come back here ("#profile").

import { LOCALE, deviceName } from './format.js';
import { h, setHashParam } from './dom.js';
import { openDialog } from './dialog.js';
import { account, api, reason, refreshUser, signOut } from './account.js';
import { openSecurity } from './security.js';
import { imageForUpload } from './upload.js';
import { t, tn } from './i18n.js';

const dfDate = new Intl.DateTimeFormat(LOCALE, { day: 'numeric', month: 'short', year: 'numeric' });
const dfTime = new Intl.DateTimeFormat(LOCALE, { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' });

// The server scales it to 512 px; this leaves it something to work with.
const PICTURE_SIDE = 1024;

let win = null;

export function openProfile(returnFocus) {
  win?.close(true);
  const close = h('button', { class: 'sheet-close', type: 'button', 'aria-label': t('Close') }, '×');
  const head = h('div', { class: 'sheet-head wo-head' });
  const titles = h('div', { class: 'wo-titles' });
  titles.append(h('h2', { id: 'pf-title' }, t('Profile')), h('p', { class: 'muted' }, account.user?.name || ''));
  head.append(titles, close);
  const body = h('div', { class: 'wo-body sec-body' });
  const panel = h('div', { class: 'wo-panel sec-panel' });
  panel.append(head, body);
  win = openDialog({ cls: 'wo-overlay', labelledBy: 'pf-title', content: [panel], returnFocus });
  close.onclick = () => win.close();
  render(body, returnFocus);
}

async function render(body, returnFocus, newToken) {
  const [profile, tokens, sessions] = await Promise.all(['profile', 'tokens', 'sessions'].map((p) => api(p).catch(() => null)));
  if (!profile?.ok || !tokens?.ok || !sessions?.ok) {
    body.replaceChildren(h('p', { class: 'empty-tab' }, t('This could not be loaded.')));
    return;
  }
  const p = await profile.json();
  const again = () => render(body, returnFocus);
  body.replaceChildren(
    pictureSection(p),
    aboutSection(p),
    securitySection(returnFocus),
    tokensSection(body, await tokens.json(), newToken, returnFocus),
    sessionsSection(await sessions.json(), again),
  );
}

function section(title, note) {
  const sec = h('section', { class: 'wo-sec' });
  const hd = h('div', { class: 'wo-sec-head' });
  hd.append(h('h3', {}, title));
  if (note) hd.append(h('span', { class: 'muted' }, note));
  sec.append(hd);
  return sec;
}

const message = () => h('p', { class: 'sec-msg', role: 'status', hidden: '' });

function say(msg, text, bad) {
  msg.textContent = text;
  msg.classList.toggle('lock-error', !!bad);
  msg.hidden = false;
}

/* ---------- picture ---------- */

function pictureSection(p) {
  const sec = section(t('Picture'), t('Shown in the menu and next to your notes.'));
  const face = h('div', { class: 'pf-face' });
  const show = (src) => {
    face.replaceChildren(src ? h('img', { src, alt: t('Your picture') }) : h('span', { 'aria-hidden': 'true' }, p.name.slice(0, 1).toUpperCase()));
    remove.hidden = !src;
  };
  const msg = message();
  const picker = h('input', { type: 'file', accept: 'image/*', hidden: '' });
  const pick = h('button', { class: 'pill-btn', type: 'button' }, t('Choose a picture'));
  const remove = h('button', { class: 'pill-btn', type: 'button' }, t('Remove'));
  pick.onclick = () => picker.click();
  const send = async (button, run) => {
    button.disabled = true;
    msg.hidden = true;
    try {
      const res = await run();
      if (res.ok) {
        show((await res.json()).avatar);
        await refreshUser();
        return;
      }
      const why = await reason(res);
      say(msg, res.status === 413 ? t('That image is too large.') : why === 'bad_image' ? t('That image can’t be used.') : t('That didn’t work. Try again.'), true);
    } catch (err) {
      say(msg, err instanceof TypeError ? t('Server not reachable.') : t('That image can’t be used.'), true);
    } finally {
      button.disabled = false;
    }
  };
  picker.onchange = () => {
    const file = picker.files[0];
    picker.value = '';
    if (file) send(pick, async () => api('profile/avatar', 'POST', { image: await imageForUpload(file, PICTURE_SIDE) }));
  };
  remove.onclick = () => send(remove, () => api('profile/avatar', 'DELETE'));
  show(p.avatar);
  const row = h('div', { class: 'pf-picture' });
  const actions = h('div', { class: 'sec-row' });
  actions.append(pick, remove, picker);
  row.append(face, actions);
  sec.append(row, msg);
  return sec;
}

/* ---------- about you ---------- */

const PROFILE_ERRORS = {
  bad_person_name: () => t('Names can have up to 64 characters.'),
  bad_email: () => t('That isn’t an e-mail address.'),
  bad_instagram: () => t('Instagram handles have up to 30 letters, digits, dots and underscores.'),
};

function aboutSection(p) {
  const sec = section(t('About you'), t('All optional.'));
  const form = h('form', { class: 'pf-form' });
  const field = (key, label, attrs = {}) => {
    const input = h('input', { class: 'field', type: 'text', value: p[key], 'aria-label': label, placeholder: label, ...attrs });
    form.append(input);
    return input;
  };
  const first = field('first_name', t('First name'), { autocomplete: 'given-name', maxlength: '64' });
  const surname = field('surname', t('Surname'), { autocomplete: 'family-name', maxlength: '64' });
  const email = field('email', t('E-mail'), { type: 'email', autocomplete: 'email', maxlength: '254' });
  const instagram = field('instagram', t('Instagram handle'), { autocapitalize: 'none', spellcheck: 'false', maxlength: '31' });
  const msg = message();
  const save = h('button', { class: 'pill-btn', type: 'submit' }, t('Save'));
  form.append(msg, save);
  form.noValidate = true; // the server's words, in the page's language
  form.onsubmit = async (e) => {
    e.preventDefault();
    save.disabled = true;
    msg.hidden = true;
    try {
      const res = await api('profile', 'PUT', { first_name: first.value, surname: surname.value, email: email.value, instagram: instagram.value });
      if (res.ok) {
        const q = await res.json();
        instagram.value = q.instagram;
        return say(msg, t('Saved.'));
      }
      say(msg, PROFILE_ERRORS[await reason(res)]?.() || t('That didn’t work. Try again.'), true);
    } catch {
      say(msg, t('Server not reachable.'), true);
    } finally {
      save.disabled = false;
    }
  };
  sec.append(form);
  return sec;
}

/* ---------- security ---------- */

function securitySection(returnFocus) {
  const sec = section(t('Password and second factors'));
  const open = h('button', { class: 'pill-btn', type: 'button' }, t('Open security settings'));
  open.onclick = () => {
    win.close(true);
    openSecurity(returnFocus);
  };
  sec.append(open);
  return sec;
}

/* ---------- API tokens ---------- */

function tokensSection(body, tokens, newToken, returnFocus) {
  const sec = section(t('API tokens'), t('For scripts. A token reads what you may read.'));
  if (newToken) sec.append(tokenShown(newToken));
  const now = Date.now();
  for (const k of tokens) {
    const row = h('div', { class: 'day-item sec-item' });
    const name = h('span', { class: 'name' });
    name.append(h('span', { class: 'txt' }, k.name));
    const old = k.expires && new Date(k.expires) < now;
    const note = [k.scopes.join(', '), old ? t('expired') : k.expires ? t('until {date}', { date: dfDate.format(new Date(k.expires)) }) : t('never expires')];
    if (k.last_used) note.push(t('used {date}', { date: dfDate.format(new Date(k.last_used)) }));
    const revoke = h('button', { class: 'pill-btn', type: 'button' }, t('Revoke'));
    revoke.onclick = async () => {
      if (!confirm(t('Revoke “{name}”? Scripts using it stop working.', { name: k.name }))) return;
      revoke.disabled = true;
      const res = await api('tokens/' + k.id, 'DELETE').catch(() => null);
      if (res?.ok) return render(body, returnFocus);
      revoke.disabled = false;
    };
    row.append(name, h('span', { class: 'amt' }, note.join(' · ')), revoke);
    sec.append(row);
  }
  sec.append(tokenForm(body, returnFocus));
  return sec;
}

function tokenForm(body, returnFocus) {
  const form = h('form', { class: 'sec-add pf-token' });
  const name = h('input', { class: 'field', type: 'text', maxlength: '64', placeholder: t('Name, e.g. Grafana'), 'aria-label': t('Name of the token'), required: '' });
  const expiry = h('select', { class: 'field', 'aria-label': t('Expires') });
  for (const [days, label] of [[30, t('30 days')], [90, t('90 days')], [365, t('1 year')], [0, t('Never')]]) expiry.append(h('option', { value: days }, label));
  expiry.value = '365';
  const check = (value, label) => {
    const box = h('input', { type: 'checkbox', value });
    const item = h('label', { class: 'share-check' });
    item.append(box, ' ' + label);
    form.append(item);
    return box;
  };
  const add = h('button', { class: 'pill-btn', type: 'submit' }, t('Make token'));
  const msg = message();
  form.append(name, expiry);
  const boxes = [check('read', t('Read the data'))];
  boxes[0].checked = true;
  if (account.user?.scopes?.includes('sync')) boxes.push(check('sync', t('Start a sync')));
  form.append(add, msg);
  form.onsubmit = async (e) => {
    e.preventDefault();
    add.disabled = true;
    msg.hidden = true;
    try {
      const res = await api('tokens', 'POST', { name: name.value.trim(), scopes: boxes.filter((b) => b.checked).map((b) => b.value), days: Number(expiry.value) });
      if (res.ok) return await render(body, returnFocus, (await res.json()).token);
      const why = await reason(res);
      if (why === 'reauth') return form.before(reauthNote());
      say(msg, { bad_scopes: t('Pick at least one thing the token may do.'), bad_name: t('Give the token a name.'), second_factor_required: t('Set up a second factor first: syncing needs one.') }[why] || t('That didn’t work. Try again.'), true);
    } catch {
      say(msg, t('Server not reachable.'), true);
    } finally {
      add.disabled = false;
    }
  };
  return form;
}

// A new token, this once.
function tokenShown(token) {
  const box = h('div', { class: 'sec-codes' });
  box.append(h('p', { class: 'notice' }, t('Your new token. It is shown only now.')), h('code', { class: 'sec-secret pf-secret' }, token));
  const copy = h('button', { class: 'pill-btn', type: 'button' }, t('Copy'));
  copy.onclick = () => navigator.clipboard?.writeText(token).then(() => { copy.textContent = t('Copied'); });
  box.append(copy);
  return box;
}

function reauthNote() {
  const box = h('div', { class: 'notice notice-row' });
  const again = h('button', { class: 'pill-btn', type: 'button' }, t('Sign in again'));
  again.onclick = () => {
    setHashParam('profile', '');
    signOut(false);
  };
  box.append(h('span', {}, t('To make a token, sign in again: your last sign-in was more than 10 minutes ago.')), again);
  return box;
}

/* ---------- sessions ---------- */

function sessionsSection(list, again) {
  const sec = section(t('Sessions'), tn(list.length, '{n} signed-in device', '{n} signed-in devices'));
  for (const s of list) {
    const row = h('div', { class: 'day-item sec-item' });
    const name = h('span', { class: 'name' });
    name.append(h('span', { class: 'txt' }, deviceName(s.user_agent)));
    const end = h('button', { class: 'pill-btn', type: 'button' }, s.current ? t('This session') : t('End'));
    end.disabled = s.current;
    end.onclick = async () => {
      end.disabled = true;
      const res = await api('sessions/' + s.id, 'DELETE').catch(() => null);
      if (res?.ok) return again();
      end.disabled = false;
    };
    const note = [t('last seen {date}', { date: dfTime.format(new Date(s.last_seen)) }), s.ip].filter(Boolean);
    row.append(name, h('span', { class: 'amt' }, note.join(' · ')), end);
    sec.append(row);
  }
  return sec;
}
