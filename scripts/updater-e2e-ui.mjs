// The browser half of scripts/updater-e2e.sh (PLAN 15c): against Eumetria
// running in the e2e compose project, with the updater next to it.
//
//   node scripts/updater-e2e-ui.mjs <base URL> <admin> <password> <enrol token>
//
// It sets up the admin's authenticator app with the enrolment link, signs
// in, opens Settings → Update and clicks the install button the page
// offers (the channel switch: the updater runs edge, the setting says
// stable). Then it waits for the page to reload by itself once Eumetria
// runs again, and checks that the update is in the updater's history.
// It prints one line per check and exits 1 if one fails.

import { createHmac } from 'node:crypto';
import { findChrome, launch } from './browser.js';

const [base, name, password, enrol] = process.argv.slice(2);
const chrome = findChrome();
if (!enrol || !chrome) {
  console.error(!chrome ? 'No Chrome found; set CHROME_BIN.' : 'usage: node scripts/updater-e2e-ui.mjs <base> <admin> <password> <enrol token>');
  process.exit(2);
}

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

async function signIn(secret) {
  let cookie = '';
  const post = async (path, body) => {
    const res = await fetch(base + '/api/' + path, { method: 'POST', headers: { 'content-type': 'application/json', cookie, origin: base }, body: JSON.stringify(body) });
    const set = res.headers.get('set-cookie');
    if (set) cookie = set.split(';')[0];
    return res;
  };
  const first = await (await post('login', { name, password, remember: true })).json();
  if (first.ticket) await post('login/code', { ticket: first.ticket, kind: 'totp', code: totp(secret) });
  if (!cookie) throw new Error('signing in failed: ' + JSON.stringify(first));
  return { cookie, post };
}

let fails = 0;
const check = (what, ok, extra = '') => {
  console.log((ok ? 'ok   ' : 'FAIL ') + what + (!ok && extra ? '\n     ' + extra : ''));
  fails += !ok;
};
const wait = (ms) => new Promise((ok) => setTimeout(ok, ms));

// The authenticator app, then a sign-in with a code of the next window
// (a code works once).
const setup = await signIn();
const begun = await (await setup.post('security/totp/begin', { enrol })).json();
const confirmed = await setup.post('security/totp/confirm', { code: totp(begun.secret) });
if (!confirmed.ok) throw new Error('setting up the authenticator app failed: ' + confirmed.status);
await wait(30500 - (Date.now() % 30000));
const session = await signIn(begun.secret);

const browser = await launch(chrome);
try {
  await browser.send('Network.enable');
  const [cname, value] = session.cookie.split(/=(.*)/s);
  await browser.send('Network.setCookie', { name: cname, value, url: base + '/', secure: true, httpOnly: true, sameSite: 'Lax' });
  const ev = (js) => browser.eval(js);
  await browser.view(base + '/#settings=update', { width: 1280, height: 900 });
  let label = '';
  for (let i = 0; i < 60 && !label; i++) {
    await wait(500);
    label = await ev(`document.querySelector('.upd-apply')?.textContent || ''`);
  }
  check('Settings → Update offers to install', !!label, await ev(`document.querySelector('.pf-content')?.textContent.slice(0, 400) || ''`));
  const host = await ev(`[...document.querySelectorAll('.set-fact')].find((r) => r.textContent.startsWith('Host'))?.textContent || ''`);
  check('the real host name comes from the updater', !!host, host);
  const tagBefore = await ev(`[...document.querySelectorAll('.wo-sec-head')].find((x) => x.textContent.startsWith('Updater'))?.textContent || ''`);

  // A marker that only this load of the page has: gone after the reload.
  await ev(`window.__e2e = 1; document.querySelector('.upd-apply').click()`);
  let steps = '';
  for (let i = 0; i < 40 && !steps; i++) {
    await wait(250);
    steps = await ev(`document.querySelector('.upd-steps')?.textContent || ''`).catch(() => '');
  }
  check('the steps show (backup first)', steps.includes('Backup'), steps);

  let reloaded = false;
  for (let i = 0; i < 150 && !reloaded; i++) {
    await wait(2000);
    reloaded = await ev(`!window.__e2e && document.documentElement.dataset.rendered === 'true'`).catch(() => false);
  }
  check('the page reloads by itself when Eumetria runs again', reloaded);
  await wait(2500);
  const after = JSON.parse(await ev(`(() => {
    const sec = [...document.querySelectorAll('.wo-sec')].find((x) => x.querySelector('.wo-sec-head')?.textContent.startsWith('Updater'));
    return JSON.stringify({ head: sec?.querySelector('.wo-sec-head')?.textContent || '', newest: sec?.querySelector('.set-run')?.textContent || '' });
  })()`).catch(() => '{}'));
  check('the newest update in the history: installed, 1.3.0', /Installed · 1\.3\.0/.test(after.newest || ''), JSON.stringify(after));
  check('the updater is on the stable tag now', /Tag 1$/.test(after.head || '') && tagBefore.includes('latest'), `${tagBefore} → ${after.head}`);
} finally {
  await browser.close();
}
process.exit(fails ? 1 : 0);
