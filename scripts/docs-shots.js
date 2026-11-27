// Takes the screenshots of the documentation (docs/src/assets/screens/)
// from a running Eumetria, so they can be made again in one go whenever the
// page changes:
//
//   go build -o /tmp/eumetria ./cmd/eumetria && node scripts/docs-shots.js /tmp/eumetria
//
// Two servers on free ports, each with an empty data folder of its own:
//   1. a fresh one, without data, for the screens of a new installation (no
//      account yet, the enrolment link, the Yazio settings);
//   2. one with -demo's sample data, for the dashboard, its cards and
//      windows, on a wide screen and on a phone (412 px, a Pixel).
// Each makes an admin with an authenticator app, as a real one has. The
// sample data is made for now (its "last synced" is the real time, so a
// fixed time would read "updated in 20 hours").
// Only Eumetria's own screens: Google Cloud's or Anthropic's consoles have
// to be taken by hand.

import { spawn, spawnSync } from 'node:child_process';
import { createHmac } from 'node:crypto';
import { mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises';
import { createServer } from 'node:net';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { findChrome, launch } from './browser.js';

const bin = resolve(process.argv[2] || '');
const chrome = findChrome();
if (!process.argv[2] || !chrome) {
  console.error(!chrome ? 'No Chrome found; set CHROME_BIN.' : 'usage: node scripts/docs-shots.js <eumetria binary>');
  process.exit(2);
}
const OUT = resolve(new URL('../docs/src/assets/screens', import.meta.url).pathname);
const SITE = resolve(new URL('../site', import.meta.url).pathname);
const ADMIN = ['admin', 'correct-horse-battery-admin'];
const WIDE = { width: 1280, height: 800 };
const PHONE = { width: 412, height: 915, mobile: true };

const wait = (ms) => new Promise((ok) => setTimeout(ok, ms));
const freePort = () => new Promise((ok) => { const s = createServer().listen(0, '127.0.0.1', () => { const p = s.address().port; s.close(() => ok(p)); }); });

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

// A server on a free port with its own data folder; demo adds -demo's data.
async function server({ demo }) {
  const data = await mkdtemp(join(tmpdir(), 'eumetria-docs-'));
  const port = await freePort();
  // localhost, not 127.0.0.1: the session cookie is __Host- and Secure,
  // which browsers accept over plain http only there.
  const base = `http://localhost:${port}`;
  const env = { ...process.env, PUBLIC_URL: base, SETTINGS_KEY: 'a'.repeat(64), TZ: 'Europe/Berlin' };
  const args = ['-serve', `127.0.0.1:${port}`, '-site', SITE, '-data', data, ...(demo ? ['-demo'] : [])];
  const proc = spawn(bin, args, { stdio: ['ignore', 'ignore', 'pipe'], env });
  let log = '';
  proc.stderr.on('data', (b) => { log += b; });
  for (let i = 0; ; i++) {
    if (await fetch(base + '/healthz').then(() => true, () => false)) break;
    if (i > 100) throw new Error('the server did not start:\n' + log);
    await wait(100);
  }
  // The first admin, as the docs say: -create-user prints an enrolment link.
  const createAdmin = () => {
    const made = spawnSync(bin, ['-data', data, ...(demo ? ['-demo'] : []), '-create-user', ADMIN[0], '-role', 'admin'], { input: ADMIN[1] + '\n' + ADMIN[1] + '\n', encoding: 'utf8', env });
    if (made.status !== 0) throw new Error('-create-user: ' + made.stderr);
    return (made.stdout + made.stderr).match(/(\S*#enrol=[\w-]+)/)?.[1];
  };
  return { base, createAdmin, async stop() { proc.kill(); await rm(data, { recursive: true, force: true }); } };
}

// Signs in and sets up the authenticator app with the enrolment link; the
// session cookie.
async function enrolAdmin(base, link) {
  let cookie = '';
  const post = async (path, body) => {
    const res = await fetch(base + '/api/' + path, { method: 'POST', headers: { 'content-type': 'application/json', cookie, origin: base }, body: JSON.stringify(body) });
    const set = res.headers.get('set-cookie');
    if (set) cookie = set.split(';')[0];
    return res;
  };
  await post('login', { name: ADMIN[0], password: ADMIN[1], remember: true });
  const enrol = link.split('#enrol=')[1];
  const begun = await (await post('security/totp/begin', { enrol })).json();
  const ok = await post('security/totp/confirm', { code: totp(begun.secret) });
  if (!ok.ok) throw new Error('setting up the authenticator app failed: ' + ok.status);
  // A code works once: the next sign-in takes the next one.
  await wait(30500 - (Date.now() % 30000));
  cookie = '';
  const first = await (await post('login', { name: ADMIN[0], password: ADMIN[1], remember: true })).json();
  await post('login/code', { ticket: first.ticket, kind: 'totp', code: totp(begun.secret) });
  return cookie;
}

await mkdir(OUT, { recursive: true });
const browser = await launch(chrome);
const ev = (js) => browser.eval(js);
let count = 0;
const save = async (name, data) => {
  await writeFile(join(OUT, name + '.png'), Buffer.from(data, 'base64'));
  count++;
  console.log('ok   ' + name);
};
// The whole screen.
const shot = async (name) => save(name, (await browser.send('Page.captureScreenshot', { format: 'png' })).data);
// One element, with a margin, wherever it is on the page: scrolled to the
// top of the window (made taller when the element doesn't fit) and taken
// from what's on screen (the clip is in page coordinates). Capturing beyond
// the window lays the page out again and shifts it.
async function shotOf(name, selector, pad = 12) {
  // The header and the footer stay in place instead of over the element.
  // (Through the DOM: the page's CSP refuses a <style> element.)
  await ev(`document.querySelectorAll('.top, .foot').forEach((el) => el.style.setProperty('position', 'static', 'important'))`);
  const place = () => ev(`(() => { const el = document.querySelector(${JSON.stringify(selector)}); if (!el) return 'null';
    scrollTo({ top: el.getBoundingClientRect().top + scrollY - ${pad}, behavior: 'instant' });
    const r = el.getBoundingClientRect(); return JSON.stringify({ x: r.left + scrollX, y: r.top + scrollY, w: r.width, h: r.height }); })()`).then(JSON.parse);
  let r = await place();
  if (!r) return console.log('FAIL ' + name + ': no ' + selector);
  // Charts draw once in view, and what's above may change height meanwhile.
  await wait(700);
  r = await place();
  const tall = r.h + pad * 2 > WIDE.height;
  if (tall) {
    await browser.send('Emulation.setDeviceMetricsOverride', { width: WIDE.width, height: Math.ceil(r.h + pad * 2 + 40), deviceScaleFactor: 1, mobile: false });
    await wait(500);
    r = await place();
    await wait(300);
  }
  const { data } = await browser.send('Page.captureScreenshot', { format: 'png', clip: { x: Math.max(0, r.x - pad), y: Math.max(0, r.y - pad), width: r.w + pad * 2, height: r.h + pad * 2, scale: 1 } });
  if (tall) await browser.send('Emulation.setDeviceMetricsOverride', { width: WIDE.width, height: WIDE.height, deviceScaleFactor: 1, mobile: false });
  await save(name, data);
}
const useCookie = async (base, cookie) => {
  const [name, value] = cookie.split(/=(.*)/s);
  await browser.send('Network.setCookie', { name, value, url: base + '/', secure: true, httpOnly: true, sameSite: 'Lax' });
};
const open = async (url, size = WIDE, settle = 1200) => {
  await browser.view(url, size);
  await wait(settle);
};
const click = (selector) => ev(`document.querySelector(${JSON.stringify(selector)})?.click()`);
// The notice about the new authenticator app, which a real admin dismisses once.
const dismissNotice = () => ev(`[...document.querySelectorAll('button')].find((b) => b.textContent.trim() === 'Got it')?.click()`);

/* ---------- 1. a new installation ---------- */
{
  const s = await server({ demo: false });
  await browser.send('Network.enable');
  await open(s.base + '/');
  await shot('setup-no-account');
  const link = s.createAdmin();
  // The enrolment link, signed in through the page: it opens the security
  // settings to set up the second factor.
  await open(link);
  await ev(`(() => { const set = (sel, v) => { const el = document.querySelector(sel); el.value = v; el.dispatchEvent(new Event('input', { bubbles: true })); }; set('input[autocomplete="username"], #login-name', ${JSON.stringify(ADMIN[0])}); set('input[type=password]', ${JSON.stringify(ADMIN[1])}); })()`);
  await shot('setup-sign-in');
  await ev(`document.querySelector('input[type=password]').form.requestSubmit()`);
  await wait(2000);
  await shot('setup-enrol-link');
  await useCookie(s.base, await enrolAdmin(s.base, link));
  await open(s.base + '/');
  await dismissNotice();
  await wait(400);
  await shot('setup-first-sign-in');
  await open(s.base + '/#settings=yazio');
  await shot('settings-yazio');
  await open(s.base + '/#settings=yazio', PHONE);
  await shot('settings-yazio-phone');
  await s.stop();
}

/* ---------- 2. the demo: what it looks like with data ---------- */
{
  const s = await server({ demo: true });
  await useCookie(s.base, await enrolAdmin(s.base, s.createAdmin()));
  const day = await ev(`new Date().toISOString().slice(0, 10)`);
  const B = s.base;

  // The dashboard, wide and on a phone.
  await open(B + '/#range=30', WIDE, 1800);
  await dismissNotice();
  await wait(400);
  await shot('dashboard');
  await open(B + '/#range=30', PHONE, 1800);
  await shot('phone-today');
  for (const v of ['nutrition', 'body', 'training', 'insights']) {
    await open(B + '/#range=30&view=' + v, PHONE, 1500);
    await shot('phone-' + v);
  }
  await open(B + '/#range=30', PHONE, 1500);
  await click('#quick-btn');
  await wait(400);
  await shot('phone-log-menu');

  // Cards.
  await open(B + '/#range=30', WIDE, 1800);
  for (const [name, sel] of [['card-today', '#today'], ['card-energy', '#energy-card'], ['card-weight', '#weight-card'], ['card-movement', '#movement-card'],
    ['card-recovery', '#recovery-card'], ['card-training', '#training-card'], ['card-foods', '#foods-card'], ['card-sources', '#sources-card'], ['card-insights', '#insights-card']]) {
    await shotOf(name, sel);
  }
  await click('#training-tabs [data-tab="bests"]');
  await wait(500);
  await shotOf('card-personal-bests', '#training-card');
  await click('#training-tabs [data-tab="calendar"]');
  await wait(500);
  await shotOf('card-training-calendar', '#training-card');
  await click('#training-tabs [data-tab="overview"]');
  await ev(`document.querySelector('#insights-tabs [data-tab="explore"]')?.click()`);
  await wait(800);
  await shotOf('card-insights-explore', '#insights-card');

  // Windows.
  await open(B + `/#range=30&day=${day}`, WIDE, 1500);
  await shot('window-day');
  const w = await ev(`import('/js/state.js').then(({ state }) => { const d = [...state.byDate.values()].reverse().find((x) => x.workouts?.some((w) => w.hr?.length > 1)); const w = d.workouts.find((w) => w.hr?.length > 1); return JSON.stringify({ date: d.date, id: w.id }); })`);
  const wo = JSON.parse(w);
  await open(B + `/#range=30&day=${wo.date}&workout=${wo.id}`, WIDE, 1800);
  await shot('window-workout');
  await ev(`[...document.querySelectorAll('.wo-training .pill-btn')][0]?.click()`);
  await wait(800);
  await ev(`document.querySelectorAll('.session-form .ses-part').forEach((d) => { d.open = true; })`);
  await wait(300);
  await shot('window-training-form');
  await open(B + '/#range=30', WIDE, 1500);
  await click('#training-unlogged');
  await wait(600);
  await shot('window-not-logged');
  const lift = await ev(`import('/js/state.js').then(({ state }) => state.lifts.at(-1)?.lift.toLowerCase() || '')`);
  await open(B + '/#range=30', WIDE, 1500);
  await ev(`import('/js/lifts.js').then((m) => m.openLift(${JSON.stringify(lift)}))`);
  await wait(1000);
  await shot('window-lift');
  await open(B + '/#range=30', WIDE, 1500);
  await ev(`import('/js/checkin.js').then((m) => m.openCheckin({}))`);
  await wait(600);
  await shot('window-check-in');
  await open(B + '/#range=30', WIDE, 1500);
  await ev(`import('/js/yearreview.js').then((m) => m.openYear('2026'))`);
  await wait(1200);
  await shot('window-year');
  await open(B + `/#range=30&day=${wo.date}&workout=${wo.id}`, WIDE, 1800);
  await click('.wo-head .share-btn');
  await wait(2000);
  await shot('window-share');

  // Profile and Settings.
  for (const [name, hash] of [['profile-account', 'profile=account'], ['profile-training', 'profile=training'], ['profile-layout', 'profile=layout'], ['profile-phases', 'profile=phases'],
    ['settings-sign-in', 'settings=sign-in'], ['settings-devices', 'settings=devices'], ['settings-users', 'settings=users'], ['settings-roles', 'settings=roles'],
    ['settings-google', 'settings=google'], ['settings-ai', 'settings=ai'], ['settings-backups', 'settings=backups'], ['settings-general', 'settings=general'], ['settings-update', 'settings=update']]) {
    await open(B + '/#' + hash, WIDE, 1500);
    await shot(name);
  }
  await s.stop();
}

await browser.close();
console.log(`${count} screenshots in ${OUT}`);
