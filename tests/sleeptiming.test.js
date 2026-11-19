import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { asleepMask, chronoType, chronotype, clockOf, freeNightTest, midMin, onsetMin, regularity, wakeMin } from '../site/js/sleeptiming.js';

// The same cases internal/stats/sleeptiming_test.go checks.
const { cases } = JSON.parse(readFileSync(new URL('./fixtures/sleeptiming.json', import.meta.url), 'utf8'));

for (const c of cases) {
  test(`sleep timing: ${c.name}`, () => {
    const r = regularity(c.days);
    for (const k of ['nights', 'onsetSD', 'wakeSD', 'pairs', 'sri']) if (k in c.want) assert.equal(r[k], c.want[k], k);
    const isFree = freeNightTest({ freeDays: c.free_days, holidays: Object.fromEntries((c.holidays || []).map((d) => [d, 'a holiday'])), off: new Set(c.off || []) });
    const ch = chronotype(c.days, (d) => c.travel.includes(d), isFree);
    for (const [k, v] of Object.entries(c.want.chrono)) assert.equal(ch[k], v, 'chrono ' + k);
  });
}

test('times past midnight and the clock', () => {
  assert.equal(onsetMin({ start: '23:30', end: '07:00' }), 690);
  assert.equal(onsetMin({ start: '00:30', end: '07:00' }), 750);
  assert.equal(wakeMin({ start: '23:30', end: '07:00' }), 780);
  assert.equal(midMin({ start: '23:00', end: '07:00' }), 900);
  assert.equal(clockOf(900), '03:00');
  assert.equal(clockOf(988), '04:28');
  assert.equal(chronoType(988), 'mid'); // 04:28
  assert.equal(chronoType(917), 'lark'); // 03:17
  assert.equal(chronoType(869), 'lark2'); // 02:29
  assert.equal(chronoType(870), 'lark');
  assert.equal(chronoType(1020), 'owl'); // 05:00
  assert.equal(chronoType(1100), 'owl2'); // 06:20
});

test('a night into the afternoon stops at noon', () => {
  const m = asleepMask({ start: '03:00', end: '14:00' });
  assert.equal(m.length, 1440);
  assert.equal(m[1439], 1);
  assert.equal(m.reduce((a, b) => a + b, 0), 1440 - 900);
});

test('too few nights say nothing', () => {
  const r = regularity([{ date: '2026-11-02', sleep: { start: '23:00', end: '07:00' } }, { date: '2026-11-03', sleep: { start: '23:00', end: '07:00' } }]);
  assert.equal(r.onsetSD, null);
  assert.equal(r.sri, null);
});
