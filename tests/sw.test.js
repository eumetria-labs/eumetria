import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFile, readdir } from 'node:fs/promises';

// The service worker precaches the page for offline use. A module missing
// from SHELL breaks the page offline; a deleted one breaks the install.
const site = new URL('../site/', import.meta.url);

async function shell() {
  const src = await readFile(new URL('sw.js', site), 'utf8');
  const list = src.match(/const SHELL = \[([\s\S]*?)\];/)?.[1];
  assert.ok(list, 'SHELL array not found in sw.js');
  return [...list.matchAll(/'([^']+)'/g)].map((m) => m[1]);
}

test('sw.js precaches every page module', async () => {
  const listed = await shell();
  const modules = (await readdir(new URL('js/', site))).filter((f) => f.endsWith('.js')).map((f) => 'js/' + f);
  for (const f of [...modules, 'index.html', 'style.css', 'manifest.webmanifest']) {
    assert.ok(listed.includes(f), `${f} is missing from SHELL in sw.js`);
  }
});

test('every file in sw.js SHELL exists', async () => {
  for (const f of await shell()) {
    if (f === './' || f === 'stats.json') continue; // the page itself, and the generated data
    await assert.doesNotReject(readFile(new URL(f, site)), `${f} is listed in SHELL but does not exist`);
  }
});
