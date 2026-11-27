// Every file of the page parses: a syntax error in one module (a name
// declared twice) stops the whole page, also files no other test imports.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { readdirSync } from 'node:fs';

test('every page module parses', () => {
  const dir = new URL('../site/js/', import.meta.url).pathname;
  const bad = [];
  for (const f of readdirSync(dir).filter((f) => f.endsWith('.js'))) {
    try {
      execFileSync(process.execPath, ['--check', dir + f], { stdio: 'pipe' });
    } catch (err) {
      bad.push(`${f}: ${String(err.stderr).split('\n').find((l) => /Error/.test(l))}`);
    }
  }
  assert.deepEqual(bad, []);
});
