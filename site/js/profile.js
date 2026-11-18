// The profile (Profile → Account): the picture and what users say about
// themselves; and under Settings → Devices & API tokens their API tokens
// and the devices they are signed in on. All of it is their own
// (profile.go). Making a token needs a recent proof of who one is, which
// api() asks for when needed (stepup.js).

import { LOCALE, deviceName } from './format.js';
import { h } from './dom.js';
import { account, api, reason, refreshUser } from './account.js';
import { confirmNote } from './stepup.js';
import { imageForUpload } from './upload.js';
import { t, tn } from './i18n.js';

const dfDate = new Intl.DateTimeFormat(LOCALE, { day: 'numeric', month: 'short', year: 'numeric' });
const dfTime = new Intl.DateTimeFormat(LOCALE, { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' });

// The server scales it to 512 px; this leaves it something to work with.
const PICTURE_SIDE = 1024;

const failed = (body) => body.replaceChildren(h('p', { class: 'empty-tab' }, t('This could not be loaded.')));

// renderAccount draws the picture and "About you" into body.
export async function renderAccount(body) {
  const res = await api('profile').catch(() => null);
  if (!res?.ok) return failed(body);
  const p = await res.json();
  body.replaceChildren(pictureSection(p), aboutSection(p));
}

// renderDevices draws the API tokens and the signed-in devices into body;
// newToken is one just made, shown this once.
export async function renderDevices(body, newToken) {
  const [tokens, sessions] = await Promise.all(['tokens', 'sessions'].map((p) => api(p).catch(() => null)));
  if (!tokens?.ok || !sessions?.ok) return failed(body);
  const again = () => renderDevices(body);
  body.replaceChildren(sessionsSection(await sessions.json(), again), tokensSection(body, await tokens.json(), newToken));
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
      say(msg, res.status === 413 ? t('That image is too large.') : why === 'bad_image' ? t('That image can’t be used.')
        : why === 'busy' ? t('The server is busy with other images. Try again in a moment.') : t('That didn’t work. Try again.'), true);
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
  const form = h('form', { class: 'set-form pf-form', 'data-save': '' });
  const field = (key, label, attrs = {}, hint) => {
    const row = h('label', { class: 'set-field' });
    const input = h('input', { class: 'field', type: 'text', value: p[key], ...attrs });
    row.append(h('span', { class: 'set-label' }, label), input);
    if (hint) row.append(hint);
    form.append(row);
    return input;
  };
  const first = field('first_name', t('First name'), { autocomplete: 'given-name', maxlength: '64' });
  const surname = field('surname', t('Surname'), { autocomplete: 'family-name', maxlength: '64' });
  // Whether the address is confirmed: only then do alerts go there.
  const state = h('span', { class: 'muted set-hint pf-email-state' });
  const email = field('email', t('E-mail'), { type: 'email', autocomplete: 'email', maxlength: '254' }, state);
  const showState = (q) => {
    const resend = h('button', { class: 'pill-btn small', type: 'button' }, q.email_pending ? t('Send the link again') : t('Send a confirmation link'));
    resend.onclick = async (e) => {
      e.preventDefault();
      resend.disabled = true;
      const res = await api('profile/email/send', 'POST').catch(() => null);
      if (res?.ok) return showState({ ...(await res.json()), sent: true });
      resend.disabled = false;
      if (res && (await reason(res.clone())) === 'reauth') return form.before(confirmNote());
    };
    let text = '';
    if (q.email_pending) text = t('Waiting for {email} to be confirmed: open the link sent there.', { email: q.email_pending });
    else if (!q.email) text = '';
    else if (q.email_verified) text = t('Confirmed: security alerts go to this address.');
    else if (q.mail) text = t('Not confirmed yet: no alerts go there.');
    else text = t('This server sends no e-mail, so the address isn’t confirmed.');
    if (q.sent) text = t('Sent. Open the link in the mail while signed in here.');
    state.replaceChildren(text);
    state.classList.toggle('ok', !!q.email_verified && !q.email_pending && !q.sent);
    if (q.mail && !q.sent && (q.email_pending || (q.email && !q.email_verified))) state.append(' ', resend);
    state.hidden = !text;
  };
  showState(p);
  const instagram = field('instagram', t('Instagram handle'), { autocapitalize: 'none', spellcheck: 'false', maxlength: '31', placeholder: '@' },
    h('span', { class: 'muted set-hint' }, t('Shown on share cards.')));
  const msg = message();
  const save = h('button', { class: 'pill-btn pf-save', type: 'submit' }, t('Save'));
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
        email.value = q.email;
        showState(q);
        form.dispatchEvent(new Event('saved', { bubbles: true }));
        return say(msg, q.email_pending ? t('Saved. A confirmation link went to the new address; until then mail goes to the old one.') : t('Saved.'));
      }
      const why = await reason(res);
      if (why === 'reauth') return form.before(confirmNote());
      say(msg, PROFILE_ERRORS[why]?.() || t('That didn’t work. Try again.'), true);
    } catch {
      say(msg, t('Server not reachable.'), true);
    } finally {
      save.disabled = false;
    }
  };
  sec.append(form);
  return sec;
}

/* ---------- API tokens ---------- */

function tokensSection(body, tokens, newToken) {
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
      if (res?.ok) return renderDevices(body);
      revoke.disabled = false;
    };
    row.append(name, h('span', { class: 'amt' }, note.join(' · ')), revoke);
    sec.append(row);
  }
  sec.append(tokenForm(body));
  return sec;
}

function tokenForm(body) {
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
      if (res.ok) return await renderDevices(body, (await res.json()).token);
      const why = await reason(res);
      if (why === 'reauth') return form.before(confirmNote());
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
