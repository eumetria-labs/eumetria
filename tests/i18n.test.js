import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFile, readdir } from 'node:fs/promises';
import { DE } from '../site/js/de.js';
import { SERVINGS } from '../site/js/de.js';
import { t, tn } from '../site/js/i18n.js';
import { ACTIVITY_KEYS } from '../site/js/format.js';
import { WORKOUT_KEYS } from '../site/js/health.js';

// Every text the page shows must have its German entry, and the dictionary
// must not keep texts the code no longer uses.
const site = new URL('../site/', import.meta.url);
const STR = String.raw`(['"])((?:\\.|(?!\1).)*)\1`;
const unescape = (s) => s.replace(/\\(.)/g, '$1');

// Texts in index.html that are the same in every language.
const SAME = new Set(['CSV', 'JSON', 'English', 'Deutsch', 'Eumetria', 'AI', 'REM', 'HRV', 'Fitbit', 'YAZIO', 'Nutri-Score', 'NOVA', 'kcal', 'g', 'kg', 'min', 'bpm', 'ms', 'Open Food Facts', 'Open Database License']);

async function codeTexts() {
  const out = new Map(); // text → file
  for (const f of (await readdir(new URL('js/', site))).filter((f) => f.endsWith('.js') && f !== 'de.js')) {
    const src = await readFile(new URL('js/' + f, site), 'utf8');
    for (const m of src.matchAll(new RegExp(String.raw`\bt\(\s*` + STR, 'g'))) out.set(unescape(m[2]), f);
    for (const m of src.matchAll(new RegExp(String.raw`\btn\([^,]+,\s*` + STR + String.raw`\s*,\s*` + STR.replace('\\1', '\\3').replace('(?!\\1)', '(?!\\3)'), 'g'))) {
      out.set(unescape(m[2]), f);
      out.set(unescape(m[4]), f);
    }
  }
  return out;
}

async function pageTexts() {
  const html = (await readFile(new URL('index.html', site), 'utf8')).replace(/<head>[\s\S]*<\/head>|<script[\s\S]*?<\/script>|<!--[\s\S]*?-->/g, '');
  const out = new Set();
  // The browser reads entities as characters; so does translatePage.
  const decode = (s) => s.replace(/&amp;/g, '&').replace(/&nbsp;/g, '\u00a0').replace(/&lt;/g, '<').replace(/&gt;/g, '>').replace(/&quot;/g, '"');
  for (const m of html.matchAll(/>([^<>]+)</g)) {
    const s = decode(m[1]).trim().replace(/\s+/g, ' ');
    if (/[A-Za-z]/.test(s) && !SAME.has(s)) out.add(s);
  }
  for (const m of html.matchAll(/\b(?:aria-label|title|placeholder)="([^"]+)"/g)) if (!SAME.has(m[1])) out.add(decode(m[1]));
  return out;
}

test('every text of the page has a German translation', async () => {
  const missing = [];
  for (const [s, f] of await codeTexts()) if (!(s in DE)) missing.push(`${f}: ${s}`);
  for (const s of await pageTexts()) if (!(s in DE)) missing.push(`index.html: ${s}`);
  for (const s of [...ACTIVITY_KEYS, ...WORKOUT_KEYS]) if (!(s in DE)) missing.push(`activity: ${s}`);
  assert.deepEqual(missing, []);
});

test('the dictionary has no unused texts', async () => {
  const used = new Set([...(await codeTexts()).keys(), ...(await pageTexts()), ...ACTIVITY_KEYS, ...WORKOUT_KEYS, ...Object.keys(SERVINGS)]);
  assert.deepEqual(Object.keys(DE).filter((k) => !used.has(k)), []);
});

// A translation may leave a value out ("vs previous day" needs no 1), but
// never use one the code doesn't fill in.
test('translations only use the placeholders of their English text', () => {
  for (const [en, de] of Object.entries(DE)) {
    const names = (s) => [...s.matchAll(/\{(\w+)\}/g)].map((m) => m[1]);
    for (const n of names(de)) assert.ok(names(en).includes(n), `${en} → ${de}: {${n}}`);
  }
});

test('t and tn', () => {
  assert.equal(t('{n} days', { n: 3 }), '3 days');
  assert.equal(tn(1, '{n} day', '{n} days'), '1 day');
  assert.equal(tn(1500, '{n} day', '{n} days'), '1.500 days');
  assert.equal(t('Not in the dictionary', null, 'de'), 'Not in the dictionary');
  assert.equal(tn(2, '{n} day', '{n} days', null, 'de'), '2 Tage');
});
