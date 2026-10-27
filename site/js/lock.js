// Password lock: stats.json may be encrypted by eumetria (STATS_PASSWORD,
// internal/seal). The page then asks for the password once, derives the key
// and, with "Remember me", keeps the key (never the password) on this device.
// Only the browser's WebCrypto is used, so this module also runs under Node
// for the tests.

import { t } from './i18n.js';

const KEY_ITEM = 'stats-key';
let memoKey = null; // { tag, key }; reused by the hourly refresh

export const isSealed = (data) => data != null && typeof data === 'object' && data.sealed === 1;

const b64 = (s) => Uint8Array.from(atob(s), (c) => c.charCodeAt(0));
const toB64 = (buf) => btoa(String.fromCharCode(...new Uint8Array(buf)));
// A stored key only fits envelopes with the same KDF settings.
const tagOf = (env) => `${env.kdf}:${env.iterations}:${env.salt}`;

function checkEnvelope(env) {
  if (env.sealed !== 1 || env.kdf !== 'PBKDF2-SHA256') throw new Error('unsupported stats.json encryption');
}

export async function deriveKey(env, password) {
  checkEnvelope(env);
  const base = await crypto.subtle.importKey('raw', new TextEncoder().encode(password), 'PBKDF2', false, ['deriveKey']);
  return crypto.subtle.deriveKey(
    { name: 'PBKDF2', hash: 'SHA-256', salt: b64(env.salt), iterations: env.iterations },
    base, { name: 'AES-GCM', length: 256 }, true, ['decrypt'],
  );
}

// open decrypts env with key and returns the parsed stats; it rejects for a
// wrong key.
export async function open(env, key) {
  checkEnvelope(env);
  const plain = await crypto.subtle.decrypt({ name: 'AES-GCM', iv: b64(env.iv) }, key, b64(env.data));
  // Newer envelopes hold gzip, since ciphertext can't be compressed in transit.
  if (env.zip === 'gzip') return new Response(new Blob([plain]).stream().pipeThrough(new DecompressionStream('gzip'))).json();
  if (env.zip) throw new Error(`unsupported compression ${env.zip}`);
  return JSON.parse(new TextDecoder().decode(plain));
}

// unseal returns the decrypted stats, asking for the password only when no
// known key fits.
export async function unseal(env) {
  const tag = tagOf(env);
  for (const cand of [memoKey, await loadKey()]) {
    if (!cand || cand.tag !== tag) continue;
    try {
      const data = await open(env, cand.key);
      memoKey = cand;
      return data;
    } catch { /* the password changed */ }
  }
  forgetKey();
  return ask(env, tag);
}

let asking = null; // the refresh timer must not open a second prompt

function ask(env, tag) {
  asking ??= new Promise((resolve) => {
    const form = document.getElementById('lock');
    const input = document.getElementById('lock-password');
    const remember = document.getElementById('lock-remember');
    const button = form.querySelector('button');
    const error = document.getElementById('lock-error');
    document.documentElement.dataset.locked = 'true';
    document.getElementById('updated').textContent = t('Locked');
    form.hidden = false;
    input.focus();
    form.onsubmit = async (e) => {
      e.preventDefault();
      if (!input.value) return;
      button.disabled = true;
      button.textContent = t('Unlocking…');
      error.hidden = true;
      try {
        const key = await deriveKey(env, input.value);
        const data = await open(env, key);
        memoKey = { tag, key };
        if (remember.checked) saveKey(memoKey);
        form.hidden = true;
        form.onsubmit = null;
        input.value = '';
        delete document.documentElement.dataset.locked;
        asking = null;
        resolve(data);
      } catch {
        error.hidden = false;
        input.select();
      } finally {
        button.disabled = false;
        button.textContent = t('Unlock');
      }
    };
  });
  return asking;
}

async function saveKey({ tag, key }) {
  try {
    const raw = await crypto.subtle.exportKey('raw', key);
    localStorage.setItem(KEY_ITEM, JSON.stringify({ tag, key: toB64(raw) }));
  } catch { /* storage unavailable: ask again next visit */ }
}

async function loadKey() {
  try {
    const { tag, key } = JSON.parse(localStorage.getItem(KEY_ITEM));
    return { tag, key: await crypto.subtle.importKey('raw', b64(key), 'AES-GCM', true, ['decrypt']) };
  } catch {
    return null;
  }
}

function forgetKey() {
  memoKey = null;
  try { localStorage.removeItem(KEY_ITEM); } catch { /* storage unavailable */ }
}
