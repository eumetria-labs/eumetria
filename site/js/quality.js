// Food quality from Open Food Facts: how the calories of a range split by
// NOVA processing group and by Nutri-Score, which ultra-processed foods
// contribute most, and which additives come up. Only labels Open Food Facts
// gives are counted; nothing is estimated. No DOM access, so Node can test
// this file.

import { t } from './i18n.js';

export const NOVA = [
  [1, t('Unprocessed or minimally processed')],
  [2, t('Processed culinary ingredients')],
  [3, t('Processed')],
  [4, t('Ultra-processed')],
];
export const NUTRI = ['a', 'b', 'c', 'd', 'e'];

// Color steps of the page's diverging scale (good → bad): q1 … q5.
export const novaStep = (n) => ({ 1: 1, 2: 2, 3: 4, 4: 5 })[n] || 0;
export const nutriStep = (g) => NUTRI.indexOf(g) + 1;

// A barcode is linked to its Open Food Facts page only when it is a real one
// (the demo uses made-up keys).
export const offURL = (ean) => (/^\d{8,14}$/.test(ean || '') ? `https://world.openfoodfacts.org/product/${ean}` : null);

// The parts of a food entry that carry their own barcode: the entry itself,
// or, for a recipe, its ingredients with the entry's calories split by their
// share of the recipe.
function parts(f, recipes) {
  const rec = f.r ? recipes[f.r] : null;
  const items = rec?.items || [];
  const total = items.reduce((a, it) => a + (it.e || 0), 0);
  if (!items.length || total <= 0) return [{ x: f.x, n: f.n, e: f.e || 0 }];
  return items.map((it) => ({ x: it.x, n: it.n, e: (f.e || 0) * ((it.e || 0) / total) }));
}

// foodQuality sums the range's food entries. Shares are of all logged
// calories; "unrated" has a barcode but no grade, "none" has no barcode
// (loose foods like fruit) or isn't known to Open Food Facts.
export function foodQuality(rows, products = {}, recipes = {}) {
  const nova = { 1: 0, 2: 0, 3: 0, 4: 0, unrated: 0, none: 0 };
  const nutri = { a: 0, b: 0, c: 0, d: 0, e: 0, unrated: 0, none: 0 };
  const ultra = new Map(); // name → { kcal, n }
  const additives = new Map(); // E number → Set of food names
  let total = 0;
  for (const r of rows) for (const f of r.foods || []) {
    for (const part of parts(f, recipes)) {
      const p = part.x ? products[part.x] : null;
      total += part.e;
      if (!p) {
        nova.none += part.e;
        nutri.none += part.e;
        continue;
      }
      nova[p.nova || 'unrated'] += part.e;
      nutri[NUTRI.includes(p.ns) ? p.ns : 'unrated'] += part.e;
      if (p.nova === 4) {
        const u = ultra.get(part.n) || { kcal: 0, n: 0, x: part.x };
        u.kcal += part.e;
        u.n++;
        ultra.set(part.n, u);
      }
      for (const a of p.add || []) {
        if (!additives.has(a)) additives.set(a, new Set());
        additives.get(a).add(part.n);
      }
    }
  }
  const share = (o) => Object.fromEntries(Object.entries(o).map(([k, v]) => [k, total ? v / total : 0]));
  return {
    total,
    coverage: total ? 1 - nova.none / total : 0,
    nova: share(nova),
    nutri: share(nutri),
    ultra: [...ultra].map(([name, u]) => ({ name, ...u })).sort((a, b) => b.kcal - a.kcal),
    additives: [...additives].map(([code, foods]) => ({ code, foods: [...foods].sort() }))
      .sort((a, b) => b.foods.length - a.foods.length || a.code.localeCompare(b.code, undefined, { numeric: true })),
  };
}
