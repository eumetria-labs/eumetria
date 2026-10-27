import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { deriveKey, isSealed, open } from '../site/js/lock.js';

// Written by internal/seal, whose Go test opens the same file.
const env = JSON.parse(await readFile(new URL('./fixtures/sealed.json', import.meta.url), 'utf8'));

test('isSealed', () => {
  assert.equal(isSealed(env), true);
  assert.equal(isSealed({ days: [] }), false);
  assert.equal(isSealed(null), false);
});

test('opens what Go sealed', async () => {
  const key = await deriveKey(env, 'gym bros only');
  assert.deepEqual(await open(env, key), { title: 'Fixture' });
});

test('opens a compressed envelope', async () => {
  const gz = JSON.parse(await readFile(new URL('./fixtures/sealed-gzip.json', import.meta.url), 'utf8'));
  const key = await deriveKey(gz, 'gym bros only');
  assert.deepEqual(await open(gz, key), { title: 'Fixture' });
});

test('wrong password is rejected', async () => {
  const key = await deriveKey(env, 'gym bro');
  await assert.rejects(open(env, key));
});
