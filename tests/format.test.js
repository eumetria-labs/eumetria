import { test } from 'node:test';
import assert from 'node:assert/strict';
import { actIcon, actName, compact, fitBlocks, fmt, fmtAmount, fmtAvgQty, fmtDistance, fmtDuration, hasDetail, mealThumb, recipeShare, recipeTotals, signed, syncEvery, todayISO } from '../site/js/format.js';

test('fmtDuration', () => {
  assert.equal(fmtDuration(19), '19 min');
  assert.equal(fmtDuration(60), '1 h');
  assert.equal(fmtDuration(95), '1 h 35 min');
});

test('fmtDistance: Yazio reports metres', () => {
  assert.equal(fmtDistance(62), '62 m');
  assert.equal(fmtDistance(5609), '5,6 km');
});

test('fmtAmount', () => {
  assert.equal(fmtAmount({ a: 20, u: 'ml' }), '20 ml');
  assert.equal(fmtAmount({ a: 1.5, u: 'portion' }), '1,5 portions');
  assert.equal(fmtAmount({ a: 500, u: 'ml', s: 'bottle', sq: 1 }), '1 × bottle · 500 ml');
  assert.equal(fmtAmount({ e: 645 }), '');
});

test('activity names are English, with icons', () => {
  assert.equal(actName('Krafttraining'), 'Strength training');
  assert.equal(actName('linefishing'), 'Fishing');
  assert.equal(actName('my_custom-thing'), 'My custom thing');
  assert.equal(actIcon('CrossFit'), '🤸');
  assert.equal(actIcon('Unknown'), '🏅');
});

test('hasDetail', () => {
  assert.equal(hasDetail(null), false);
  assert.equal(hasDetail({ steps: 5000 }), false);
  assert.equal(hasDetail({ energy: 1800 }), true);
  assert.equal(hasDetail({ activities: [{}] }), true);
});

test('todayISO uses the sync time zone', () => {
  const lateEvening = new Date('2026-10-13T22:30:00Z'); // 00:30 on Oct 14 in Berlin
  assert.equal(todayISO('Europe/Berlin', lateEvening), '2026-10-14');
  assert.equal(todayISO('UTC', lateEvening), '2026-10-13');
  assert.match(todayISO('Not/A_Zone', lateEvening), /^\d{4}-\d{2}-\d{2}$/);
});

test('syncEvery', () => {
  assert.equal(syncEvery(30), 'every 30 minutes');
  assert.equal(syncEvery(60), 'every hour');
  assert.equal(syncEvery(180), 'every 3 hours');
  assert.equal(syncEvery(90), 'every 90 minutes');
  assert.equal(syncEvery(undefined), '');
});

test('recipe ingredients scale to the portions eaten', () => {
  const rec = { portions: 4, items: [{ e: 1400, pr: 50, c: 280, f: 6 }, { e: 1000, pr: 100, c: 0, f: 80 }] };
  assert.equal(recipeShare({ a: 1 }, rec), 0.25);
  assert.equal(recipeShare({}, { portions: 0 }), 1);
  assert.deepEqual(recipeTotals(rec.items, 0.5), { energy: 1200, protein: 75, carb: 140, fat: 43 });
});

test('meal thumbnails prefer the meal photo, then a recipe photo', () => {
  const recipes = { r1: { img: 'photos/r1.webp' }, r2: {} };
  const day = {
    photos: [{ m: 'lunch', src: 'photos/lunch.jpg' }],
    foods: [{ m: 'lunch', r: 'r1' }, { m: 'breakfast', r: 'r2' }, { m: 'breakfast', r: 'r1' }, { m: 'dinner', n: 'Soup' }],
  };
  assert.equal(mealThumb(day, 'lunch', recipes), 'photos/lunch.jpg');
  assert.equal(mealThumb(day, 'breakfast', recipes), 'photos/r1.webp');
  assert.equal(mealThumb(day, 'dinner', recipes), null);
  assert.equal(mealThumb(day, 'breakfast'), null);
});

test('numbers use a dot for thousands and a comma for decimals', () => {
  assert.equal(fmt(1000), '1.000');
  assert.equal(fmt(2071.4), '2.071');
  assert.equal(fmt(78.64, 1), '78,6');
  assert.equal(signed(-1234), '−1.234');
  assert.equal(compact.format(10000), '10.000');
});

test('fmtAvgQty rounds grams and millilitres, not portions', () => {
  assert.equal(fmtAvgQty(126.3, 'g'), '126 g');
  assert.equal(fmtAvgQty(1499.6, 'ml'), '1.500 ml');
  assert.equal(fmtAvgQty(0.75, 'portion'), '0,8 portions');
});

test('fitBlocks shrinks blocks in proportion, then drops optional ones from the end', () => {
  const hs = (fit) => fit.map((f) => f.h);
  // Fits: unchanged.
  assert.deepEqual(hs(fitBlocks([{ h: 100 }, { h: 50 }], 200, 10)), [100, 50]);
  // 50 px too tall (360 with the gaps): the photo can give 100, the list 20,
  // so they give 50 × 100/120 and 50 × 20/120.
  assert.deepEqual(hs(fitBlocks([{ h: 200, min: 100 }, { h: 80, min: 60 }, { h: 60 }], 310, 10)), [158, 71, 60]);
  // Even at their minimum too tall: the optional photo goes, the others keep their minimum.
  const dropped = fitBlocks([{ h: 200, min: 100, optional: true, id: 'photo' }, { h: 300, min: 250 }], 280, 10);
  assert.deepEqual(dropped.map((f) => [f.block.id, f.h]), [[undefined, 250]]);
});

test('fitBlocks lets blocks that can grow take the room to spare', () => {
  const fit = fitBlocks([{ h: 100 }, { h: 200, max: 400 }, { h: 100, max: 150 }], 600, 0);
  // 200 px to spare; the chart can take 200 and the list 50, so they share it 4:1.
  assert.deepEqual(fit.map((f) => f.h), [100, 360, 140]);
});
