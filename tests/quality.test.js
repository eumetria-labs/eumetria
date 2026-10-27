import { test } from 'node:test';
import assert from 'node:assert/strict';
import { foodQuality, novaStep, nutriStep, offURL } from '../site/js/quality.js';

const products = {
  111: { n: 'Skyr', ns: 'a', nova: 3 },
  222: { n: 'Crunchy', ns: 'c', nova: 4, add: ['E322'] },
  333: { n: 'Quark', add: ['E322', 'E330'] }, // in Open Food Facts, not rated
};
const recipes = { r: { portions: 1, items: [{ n: 'Skyr', x: '111', e: 300 }, { n: 'Crunchy', x: '222', e: 100 }] } };
const rows = [
  { foods: [{ n: 'Crunchy', e: 200, x: '222' }, { n: 'Apple', e: 100 }, { n: 'Quark', e: 100, x: '333' }] },
  { foods: [{ n: 'Bowl', e: 600, r: 'r' }] }, // 450 kcal skyr, 150 kcal crunchy
  {},
];

test('calories split by NOVA group and Nutri-Score, recipes by ingredient', () => {
  const q = foodQuality(rows, products, recipes);
  assert.equal(q.total, 1000);
  assert.equal(q.coverage, 0.9);
  assert.deepEqual(q.nova, { 1: 0, 2: 0, 3: 0.45, 4: 0.35, unrated: 0.1, none: 0.1 });
  assert.deepEqual(q.nutri, { a: 0.45, b: 0, c: 0.35, d: 0, e: 0, unrated: 0.1, none: 0.1 });
  assert.deepEqual(q.ultra, [{ name: 'Crunchy', kcal: 350, n: 2, x: '222' }]);
  assert.deepEqual(q.additives, [{ code: 'E322', foods: ['Crunchy', 'Quark'] }, { code: 'E330', foods: ['Quark'] }]);
});

test('no foods, no shares', () => {
  const q = foodQuality([{}], products, recipes);
  assert.equal(q.total, 0);
  assert.equal(q.coverage, 0);
  assert.equal(q.nova[4], 0);
});

test('grades map onto the good-to-bad color steps', () => {
  assert.deepEqual([1, 2, 3, 4, 0].map(novaStep), [1, 2, 4, 5, 0]);
  assert.deepEqual(['a', 'c', 'e', 'x'].map(nutriStep), [1, 3, 5, 0]);
  assert.equal(offURL('4067796000306'), 'https://world.openfoodfacts.org/product/4067796000306');
  assert.equal(offURL('demo-almonds'), null);
});
