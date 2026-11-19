// Checks the Profile / Settings view and the account menu against a running
// server, which the smoke test (plain files, no accounts) can't reach.
//
//   go build -o /tmp/eumetria ./cmd/eumetria && node scripts/prefs-check.js /tmp/eumetria
//
// It starts the binary with -demo on a free port and an empty data folder,
// makes an admin (with an authenticator app) and a viewer, signs in as each
// and checks:
//   - every section they may see opens, at 1280, 390 and 320 px, without a
//     JavaScript error and without anything wider than the screen;
//   - the viewer doesn't get the admin's sections;
//   - the menu, the theme switch, the eye on password fields, the save bar
//     (Discard and Save), Back, and the old addresses (#security, #admin=…).
// It prints one line per check and fails if one fails.

import { spawn, spawnSync } from 'node:child_process';
import { createHmac } from 'node:crypto';
import { mkdtemp, rm } from 'node:fs/promises';
import { createServer } from 'node:net';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { findChrome, launch } from './browser.js';

const bin = resolve(process.argv[2] || '');
const chrome = findChrome();
if (!process.argv[2] || !chrome) {
  console.error(!chrome ? 'No Chrome found; set CHROME_BIN.' : 'usage: node scripts/prefs-check.js <eumetria binary>');
  process.exit(2);
}
for (const ev of ['uncaughtException', 'unhandledRejection']) {
  process.on(ev, (err) => {
    console.error(`FAIL prefs check aborted (${ev}): ${err?.stack || err}`);
    cleanup().finally(() => process.exit(1));
  });
}

const data = await mkdtemp(join(tmpdir(), 'eumetria-prefs-'));
const port = await new Promise((ok) => { const s = createServer().listen(0, '127.0.0.1', () => { const p = s.address().port; s.close(() => ok(p)); }); });
// localhost, not 127.0.0.1: the session cookie is __Host- and Secure, which
// browsers accept over plain http only there.
const base = `http://localhost:${port}`;
const site = resolve(new URL('../site', import.meta.url).pathname);
const server = spawn(bin, ['-demo', '-serve', `127.0.0.1:${port}`, '-site', site, '-data', data], { stdio: ['ignore', 'ignore', 'pipe'] });
let log = '';
server.stderr.on('data', (b) => { log += b; });
let browser = null;
async function cleanup() {
  await browser?.close().catch(() => {});
  server.kill();
  await rm(data, { recursive: true, force: true });
}
for (let i = 0; ; i++) {
  if (await fetch(base + '/healthz').then((r) => r.ok, () => false)) break;
  if (i > 100) throw new Error('the server did not start:\n' + log);
  await new Promise((ok) => setTimeout(ok, 100));
}

// Accounts: an admin with an enrolment link for the authenticator app, and a viewer.
const ADMIN = ['boss', 'correct-horse-battery-admin'], VIEWER = ['vera', 'correct-horse-battery-viewer'];
const make = (name, role, password) => {
  const r = spawnSync(bin, ['-demo', '-data', data, '-create-user', name, '-role', role], { input: password + '\n', encoding: 'utf8' });
  if (r.status !== 0) throw new Error(`-create-user ${name}: ${r.stderr}`);
  return (r.stdout + r.stderr).match(/#enrol=([\w-]+)/)?.[1];
};
const enrol = make(...ADMIN.slice(0, 1), 'admin', ADMIN[1]);
make(VIEWER[0], 'viewer', VIEWER[1]);

function totp(secret, at = Date.now()) {
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';
  let bits = '';
  for (const c of secret.toUpperCase()) bits += alphabet.indexOf(c).toString(2).padStart(5, '0');
  const key = Buffer.from(bits.match(/.{8}/g).map((b) => parseInt(b, 2)));
  const counter = Buffer.alloc(8);
  counter.writeBigUInt64BE(BigInt(Math.floor(at / 30000)));
  const mac = createHmac('sha1', key).update(counter).digest();
  const o = mac[19] & 15;
  return String(((mac.readUInt32BE(o) & 0x7fffffff) % 1e6)).padStart(6, '0');
}

// signIn answers a session cookie for name.
async function signIn(name, password, secret) {
  let cookie = '';
  const post = async (path, body) => {
    const res = await fetch(base + '/api/' + path, { method: 'POST', headers: { 'content-type': 'application/json', cookie, origin: base }, body: JSON.stringify(body) });
    const set = res.headers.get('set-cookie');
    if (set) cookie = set.split(';')[0];
    return res;
  };
  const first = await (await post('login', { name, password, remember: true })).json();
  if (first.ticket) await post('login/code', { ticket: first.ticket, kind: 'totp', code: totp(secret) });
  if (!cookie) throw new Error(`signing in ${name} failed: ${JSON.stringify(first)}`);
  return { cookie, post };
}

// The admin sets up the authenticator app with the enrolment link, then
// signs in again with a code (in the next 30 s window: a code works once).
const setup = await signIn(...ADMIN);
const begun = await (await setup.post('security/totp/begin', { enrol })).json();
const confirmed = await setup.post('security/totp/confirm', { code: totp(begun.secret) });
if (!confirmed.ok) throw new Error('setting up the authenticator app failed: ' + confirmed.status);
await new Promise((ok) => setTimeout(ok, 30500 - (Date.now() % 30000)));

browser = await launch(chrome);
await browser.send('Network.enable');
const wait = (ms) => new Promise((ok) => setTimeout(ok, ms));
const ev = (js) => browser.eval(js);
let fails = 0;
const check = (what, ok, extra = '') => {
  console.log((ok ? 'ok   ' : 'FAIL ') + what + (!ok && extra ? '\n     ' + extra : ''));
  fails += !ok;
};
const useSession = async ({ cookie }) => {
  const [name, value] = cookie.split(/=(.*)/s);
  await browser.send('Network.setCookie', { name, value, url: base + '/', secure: true, httpOnly: true, sameSite: 'Lax' });
};
// open loads an address of the page and waits for the view's title.
const open = async (hash, size) => {
  await browser.view(base + '/#' + hash, size);
  for (let i = 0; i < 40 && !(await ev(`!!document.getElementById('pf-section-title')`)); i++) await wait(100);
  await wait(500);
};
const overflow = () => ev(`(() => {
  const W = innerWidth;
  const out = [...document.querySelectorAll('.pf *')].filter((el) => {
    const r = el.getBoundingClientRect();
    if (!(r.width > 0 && r.right > W + 1)) return false;
    for (let p = el.parentElement; p && !p.classList.contains('pf'); p = p.parentElement) if (getComputedStyle(p).overflowX !== 'visible') return false;
    return true;
  });
  return out.filter((el) => !out.some((o) => o !== el && el.contains(o))).slice(0, 4).map((el) => el.tagName.toLowerCase() + '.' + el.className).join(', ');
})()`);
const sizes = [['1280', { width: 1280, height: 900 }], ['390', { width: 390, height: 844, mobile: true }], ['320', { width: 320, height: 700, mobile: true }]];

async function sections(who, expect) {
  for (const [side, keys] of Object.entries(expect)) {
    for (const key of keys) {
      for (const [label, size] of sizes) {
        await open(`${side}=${key}`, size);
        const res = JSON.parse(await ev(`JSON.stringify({ hash: location.hash, title: document.getElementById('pf-section-title')?.textContent || '',
          error: document.documentElement.dataset.error || '', empty: !document.querySelector('.pf-content')?.children.length })`));
        const wide = await overflow();
        const ok = res.hash.includes(`${side}=${key}`) && res.title && !res.error && !res.empty && !wide;
        check(`${who}: ${side} → ${key} at ${label} px`, ok, JSON.stringify({ ...res, wide }));
      }
    }
  }
}

/* ---------- admin ---------- */
const adminSession = await signIn(...ADMIN, begun.secret);
await useSession(adminSession);
await sections('admin', {
  profile: ['account', 'layout', 'phases'],
  settings: ['sign-in', 'devices', 'users', 'roles', 'sessions', 'notes', 'audit', 'yazio', 'google', 'sync', 'general', 'review', 'mail', 'backups'],
});

await browser.view(base + '/#range=30', { width: 1280, height: 900 });
await wait(600);
await ev(`document.getElementById('account-btn').click()`);
await wait(200);
check('menu: Profile, Settings, Theme, Language, Sign out', await ev(`[...document.querySelectorAll('#account-menu .acct-item span, #account-menu .acct-line > span:first-child > span')].map((e) => e.textContent).join() === 'Profile,Settings,Theme,Language,Sign out'`));
await ev(`document.querySelector('#theme-switch [data-theme=dark]').click()`);
check('theme Dark applies, the menu stays open', await ev(`document.documentElement.dataset.theme === 'dark' && !document.getElementById('account-menu').hidden`));
await ev(`document.querySelector('#theme-switch [data-theme=system]').click()`);
check('theme Auto follows the device again', await ev(`!document.documentElement.dataset.theme && !localStorage.getItem('theme')`));
await ev(`document.querySelector('#account-menu [data-action=signout]').click()`);
await wait(500);
check('Sign out asks: this device or everywhere', await ev(`document.querySelectorAll('.so-choice').length === 2 && document.activeElement?.classList.contains('default')`));
await ev(`document.querySelector('.so-cancel').click()`);
await wait(300);
await ev(`document.querySelector('#account-menu [data-action=settings]')?.click() || (document.getElementById('account-btn').click(), document.querySelector('#account-menu [data-action=settings]').click())`);
await wait(900);
check('Settings opens on Sign-in', await ev(`location.hash.includes('settings=sign-in')`), await ev('location.hash'));

const pw = `document.querySelector('.pf-content .secret input')`;
await ev(`(() => { const i = ${pw}; i.focus(); i.value = 'hunter2hunter2'; i.setSelectionRange(2, 5); i.nextElementSibling.click(); })()`);
check('eye shows what was typed, keeps focus and selection', await ev(`(() => { const i = ${pw}; return i.type === 'text' && i.selectionStart === 2 && i.selectionEnd === 5 && document.activeElement === i; })()`));
await ev(`${pw}.form.dispatchEvent(new Event('submit', { cancelable: true, bubbles: true }))`);
await wait(300);
check('submitting masks it again', await ev(`[...document.querySelectorAll('.pf-content .secret input')].every((i) => i.type === 'password')`));

await open('settings=general', { width: 1280, height: 900 });
const title = `document.querySelector('.pf-content input.field')`;
const type = (v) => ev(`(() => { const i = ${title}; i.value = ${JSON.stringify(v)}; i.dispatchEvent(new Event('input', { bubbles: true })); })()`);
const press = (word) => ev(`[...document.querySelectorAll('.savebar button')].find((b) => b.textContent === ${JSON.stringify(word)}).click()`);
await type('Eumetria Test');
check('a change shows the save bar', await ev(`!document.querySelector('.savebar').hidden`));
await press('Discard');
await wait(800);
check('Discard puts it back', await ev(`${title}.value === 'Eumetria' && document.querySelector('.savebar').hidden`));
await type('Eumetria Test');
await press('Save');
await wait(1200);
const saved = await (await fetch(base + '/api/admin/settings', { headers: { cookie: adminSession.cookie } })).json();
check('Save stores it', saved.settings.title.value === 'Eumetria Test' && (await ev(`document.querySelector('.savebar').hidden`)), saved.settings.title.value);

await ev(`document.querySelector('.pf-switch a:first-child').click()`);
await wait(700);
check('the switch goes to Profile → Account', await ev(`location.hash.includes('profile=account')`), await ev('location.hash'));
await ev('history.back()');
await wait(700);
check('Back closes the view', await ev(`!document.querySelector('dialog.pf-overlay') && !/profile=|settings=/.test(location.hash)`), await ev('location.hash'));
await open('admin=roles', { width: 1280, height: 900 });
check('#admin=roles leads to Settings → Roles', await ev(`location.hash.includes('settings=roles')`), await ev('location.hash'));
await open('security', { width: 1280, height: 900 });
check('#security leads to Settings → Sign-in', await ev(`location.hash.includes('settings=sign-in')`), await ev('location.hash'));
await open('settings=', { width: 390, height: 844, mobile: true });
check('phones get the list of sections', await ev(`document.querySelector('.pf').dataset.mode === 'list'`));
await ev(`document.querySelector('.pf-list a[data-key=google]').click()`);
await wait(700);
await ev('history.back()');
await wait(700);
check('Back from a section goes to the list', await ev(`document.querySelector('.pf')?.dataset.mode === 'list'`), await ev('location.hash'));

/* ---------- the dashboard's frame, signed in ---------- */
await fetch(base + '/api/profile', { method: 'PUT', headers: { 'content-type': 'application/json', cookie: adminSession.cookie, origin: base }, body: JSON.stringify({ first_name: 'Bea' }) });
await browser.view(base + '/#range=30', { width: 1280, height: 900 });
await wait(800);
check('greeting with the first name', /^Good (morning|afternoon|evening), Bea$/.test(await ev(`document.getElementById('greeting').textContent`)), await ev(`document.getElementById('greeting').textContent`));
// On a birthday (date of birth today, 30 years ago) the greeting says so,
// and a woman's reference values come with /api/me.
{
  const d = new Date();
  const born = `${d.getFullYear() - 30}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
  await fetch(base + '/api/profile', { method: 'PUT', headers: { 'content-type': 'application/json', cookie: adminSession.cookie, origin: base }, body: JSON.stringify({ first_name: 'Bea', sex: 'female', birth_date: born }) });
  await browser.view(base + '/#range=30', { width: 1280, height: 900 });
  await wait(800);
  check('birthday greeting', await ev(`document.getElementById('greeting').textContent === 'Happy birthday, Bea! 🎂' && document.getElementById('greeting').classList.contains('birthday')`),
    await ev(`document.getElementById('greeting').textContent`));
  const me = await (await fetch(base + '/api/me', { headers: { cookie: adminSession.cookie } })).json();
  check('reference values for the profile', me.nutrient_refs?.iron?.amount === 16 && me.sex === 'female', JSON.stringify(me.nutrient_refs?.iron));
  await fetch(base + '/api/profile', { method: 'PUT', headers: { 'content-type': 'application/json', cookie: adminSession.cookie, origin: base }, body: JSON.stringify({ first_name: 'Bea' }) });
  await browser.view(base + '/#range=30', { width: 1280, height: 900 });
  await wait(600);
}
check('sidebar: brand, sections, you at the foot', await ev(`getComputedStyle(document.getElementById('side')).display !== 'none' && document.getElementById('side-name').textContent === 'Bea' && !!document.querySelector('.side-me #account-btn') && document.querySelectorAll('#sections a:not([hidden])').length >= 5`));
await ev(`document.getElementById('side-name').click()`);
await wait(200);
check('sidebar: the name opens the menu too', await ev(`!document.getElementById('account-menu').hidden`));
check('sidebar: the open menu makes it scroll neither way', await ev(`(() => { const s = document.getElementById('side'); return s.scrollWidth <= s.clientWidth && s.scrollHeight <= s.clientHeight; })()`));
await ev(`document.body.click()`);
await wait(150);
check('footer: one line at the bottom of the window', await ev(`(() => { const f = document.querySelector('.foot').getBoundingClientRect(); return f.height < 40 && Math.abs(f.bottom - innerHeight) < 2; })()`),
  await ev(`JSON.stringify(document.querySelector('.foot').getBoundingClientRect())`));
check('"+ Log" offers lift, WOD result and phase', await ev(`!document.getElementById('quick').hidden && ['lift', 'wod', 'phase'].every((k) => !document.querySelector('#quick-menu [data-log=' + k + ']').hidden)`));
await ev(`document.getElementById('quick-btn').click()`);
await wait(200);
await ev(`document.querySelector('#quick-menu [data-log=wod]').click()`);
await wait(500);
check('"+ Log → WOD result" asks which workout', await ev(`document.querySelectorAll('.pick-item').length > 0`));
await ev(`document.querySelector('.pick-item').click()`);
await wait(500);
check('… and opens its result form', await ev(`!!document.getElementById('wod-result-title')`));
await browser.view(base + '/#range=30', { width: 1000, height: 800 });
await wait(600);
check('medium screens: the sidebar keeps its icons', await ev(`document.getElementById('side').getBoundingClientRect().width === 72`), await ev(`document.getElementById('side').getBoundingClientRect().width`));
await browser.view(base + '/#range=30', { width: 390, height: 844, mobile: true });
await wait(800);
check('phone: tab bar, the account at the top right', await ev(`getComputedStyle(document.getElementById('tabbar')).display === 'grid' && !!document.querySelector('#top-me #account-btn') && getComputedStyle(document.getElementById('side')).display === 'none'`));
await ev(`document.querySelector('#tabbar a[data-view=training]').click()`);
await wait(500);
check('phone: Training shows only training', await ev(`location.hash.includes('view=training') && getComputedStyle(document.getElementById('training-card')).display !== 'none' && getComputedStyle(document.getElementById('energy-card')).display === 'none' && getComputedStyle(document.getElementById('today')).display === 'none'`));
check('phone: the round "+" sits above the tab bar', await ev(`(() => { const q = document.getElementById('quick-btn').getBoundingClientRect(), bar = document.getElementById('tabbar').getBoundingClientRect(); return q.bottom <= bar.top && q.right <= innerWidth && q.width > 40; })()`));
await ev(`document.querySelector('#tabbar a[data-view=more]').click()`);
await wait(400);
check('phone: More has search, year in review, profile and settings', await ev(`document.querySelectorAll('#more-card .more-item').length === 4 && getComputedStyle(document.getElementById('more-card')).display !== 'none'`));
check('phone: nothing wider than the screen', await ev(`document.documentElement.scrollWidth <= innerWidth`));

/* ---------- the daily check-in ---------- */
{
  const d = new Date();
  const today = `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
  await browser.view(base + '/#range=30', { width: 1280, height: 900 });
  await wait(800);
  check('check-in: the Today card asks', await ev(`document.querySelector('#today-checkin .checkin-line.ask') !== null`));
  const res = await fetch(base + '/api/checkins/' + today, { method: 'PUT', headers: { 'content-type': 'application/json', cookie: adminSession.cookie, origin: base, 'sec-fetch-site': 'same-origin' }, body: JSON.stringify({ energy: 2, tags: ['sick'] }) });
  check('check-in: saved', res.status === 200, String(res.status));
  await browser.view(base + '/#range=30', { width: 1280, height: 900 });
  await wait(800);
  check('check-in: the line on the Today card', await ev(`(() => { const b = document.querySelector('#today-checkin .checkin-line'); return b.title === 'Energy low · 🤒 Sick' && b.querySelector('.ci-ans.lv-bad svg') !== null; })()`), await ev(`document.querySelector('#today-checkin .checkin-line')?.title`));
  check('check-in: Readiness says rest on a sick day', await ev(`[...document.querySelectorAll('#today-scores .sc-word')].some((w) => w.textContent.includes('Sick: rest first'))`), await ev(`document.getElementById('today-scores').textContent`));
}

/* ---------- viewer ---------- */
await browser.send('Network.deleteCookies', { name: adminSession.cookie.split('=')[0], url: base + '/' });
await useSession(await signIn(...VIEWER));
await sections('viewer', { profile: ['account', 'layout', 'phases'], settings: ['sign-in', 'devices'] });
await open('settings=', { width: 1280, height: 900 });
check('viewer: no admin sections', await ev(`[...document.querySelectorAll('.pf-nav a')].map((a) => a.dataset.key).join() === 'sign-in,devices'`),
  await ev(`[...document.querySelectorAll('.pf-nav a')].map((a) => a.dataset.key).join()`));
await browser.view(base + '/#range=30', { width: 1280, height: 900 });
await wait(600);
check('viewer: no "+ Log" without the right to write', await ev(`document.getElementById('quick').hidden`));
check('viewer: sees the check-in, can\'t change it', await ev(`document.querySelector('#today-checkin button')?.disabled === true`));
await open('settings=users', { width: 1280, height: 900 });
check('viewer: #settings=users shows Sign-in instead', await ev(`location.hash.includes('settings=sign-in')`), await ev('location.hash'));

await cleanup();
console.log(fails ? `${fails} failed` : 'all passed');
process.exit(fails ? 1 : 0);
