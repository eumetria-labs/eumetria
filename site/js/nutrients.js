// Alcohol, fibre, minerals and vitamins (stats: Day.nut, nutrients.go):
// the Calories card's Nutrients tab, the alcohol line under the macros, and
// the helpers the day window, the macro sources and the export share.
//
// Each day's amount comes with its coverage, the share of the day's kcal
// whose foods list the nutrient: Yazio doesn't know every nutrient of every
// food, and an AI entry none. An average is only as good as that share, so
// the tab says it, and greys out what too little is known about.

import { fmt, logged } from './format.js';
import { $, h } from './dom.js';
import { state } from './state.js';
import { account } from './account.js';
import { t, tn } from './i18n.js';

// The nutrients as the server sends them, with their unit; group: how the
// tab lists them.
export const NUTRIENTS = [
  { key: 'alcohol', label: () => t('Alcohol'), unit: 'g', group: 'alcohol' },
  { key: 'fiber', label: () => t('Fibre'), unit: 'g', group: 'other' },
  { key: 'sodium', label: () => t('Sodium'), unit: 'mg', group: 'minerals' },
  { key: 'potassium', label: () => t('Potassium'), unit: 'mg', group: 'minerals' },
  { key: 'magnesium', label: () => t('Magnesium'), unit: 'mg', group: 'minerals' },
  { key: 'calcium', label: () => t('Calcium'), unit: 'mg', group: 'minerals' },
  { key: 'iron', label: () => t('Iron'), unit: 'mg', group: 'minerals' },
  { key: 'zinc', label: () => t('Zinc'), unit: 'mg', group: 'minerals' },
  { key: 'iodine', label: () => t('Iodine'), unit: 'µg', group: 'minerals' },
  { key: 'vitamin_a', label: () => t('Vitamin A'), unit: 'µg', group: 'vitamins' },
  { key: 'folate', label: () => t('Folate'), unit: 'µg', group: 'vitamins' },
  { key: 'vitamin_b12', label: () => t('Vitamin B12'), unit: 'µg', group: 'vitamins' },
  { key: 'vitamin_c', label: () => t('Vitamin C'), unit: 'mg', group: 'vitamins' },
  { key: 'vitamin_d', label: () => t('Vitamin D'), unit: 'µg', group: 'vitamins' },
  { key: 'vitamin_e', label: () => t('Vitamin E'), unit: 'mg', group: 'vitamins' },
];
export const nutrient = (key) => NUTRIENTS.find((n) => n.key === key);

// Below this share of known kcal an average says little.
export const LOW_COVERAGE = 0.5;
// A day with at least this much alcohol counts as a drinking day (Patterns).
export const ALCOHOL_DAY = 10;
const KCAL_PER_G_ALCOHOL = 7;

// rangeNutrient averages one nutrient over logged days: per day, and the
// coverage weighted by each day's kcal. Days whose foods are all unknown
// for it count with coverage 0.
export function rangeNutrient(rows, key) {
  const days = rows.filter(logged);
  if (!days.length) return null;
  let sum = 0, kcal = 0, known = 0;
  for (const d of days) {
    const [amount, cov] = d.nut?.[key] || [0, 0];
    sum += amount;
    kcal += d.energy || 0;
    known += cov * (d.energy || 0);
  }
  return { avg: sum / days.length, coverage: kcal ? known / kcal : 0, days: days.length };
}

const hasNutrients = () => state.data.days.some((d) => d.nut);

// The reference values to compare with: the viewer's own, from their sex
// and date of birth in the profile (/api/me); without an account (plain
// files, the demo) those the data comes with.
export const nutrientRefs = () => (account.user ? account.user.nutrient_refs || null : state.data.nutrient_refs || null);

// The Calories card's tabs get "Nutrients" when there are any.
export const nutrientsTab = () => (hasNutrients() ? [['nutrients', t('Nutrients'), '🥦']] : []);

// renderNutrients draws the Nutrients tab for the range's rows.
export function renderNutrients(s) {
  const host = $('#nutrients-part');
  if (!host) return;
  const rows = s.complete;
  const refs = nutrientRefs();
  const parts = [];
  parts.push(alcoholSummary(rows, true));
  const groups = [['minerals', t('Minerals')], ['vitamins', t('Vitamins')], ['other', '']];
  for (const [group, title] of groups) {
    const list = h('div', { class: 'hbars nut-list' });
    for (const n of NUTRIENTS.filter((x) => x.group === group)) {
      const r = rangeNutrient(rows, n.key);
      // Not listed by any food eaten: nothing to say.
      if (!r || r.coverage === 0) continue;
      list.append(nutrientRow(n, r, refs?.[n.key]));
    }
    if (!list.children.length) continue;
    if (title) parts.push(h('h3', { class: 'mini-title' }, title));
    parts.push(list);
  }
  const note = refs
    ? t('Average a day on logged days, against the daily reference values of the DGE/ÖGE/SGE (a reference, not a goal). Greyed out: known for less than half of the kcal eaten.')
    : t('Average a day on logged days. Add your sex and date of birth under Profile → Account to compare with reference values. Greyed out: known for less than half of the kcal eaten.');
  parts.push(h('p', { class: 'note muted' }, note));
  host.replaceChildren(...parts.filter(Boolean));
}

function nutrientRow(n, r, ref) {
  const low = r.coverage < LOW_COVERAGE;
  // Sodium's reference is what the body needs; most people eat far more,
  // and more isn't better: over it, the bar says so.
  const over = n.key === 'sodium' && ref && r.avg > ref.amount;
  const row = h('div', { class: 'hbar nut-row' + (low ? ' low' : '') + (over ? ' over' : '') });
  const name = h('span', { class: 'name' }, n.label());
  const val = h('span', { class: 'val' }, `${fmt(r.avg, r.avg < 10 ? 1 : 0)} ${n.unit}`);
  const extra = [];
  if (ref) extra.push(t('{pct}% of {ref} {unit}', { pct: fmt((r.avg / ref.amount) * 100), ref: fmt(ref.amount), unit: n.unit }));
  extra.push(t('known for {pct}%', { pct: fmt(r.coverage * 100) }));
  val.append(h('small', {}, extra.join(' · ')));
  row.append(name, val);
  const track = h('span', { class: 'track' });
  // The bar runs to 150 % of the reference, with the reference marked.
  const bar = h('i');
  const max = ref ? ref.amount * 1.5 : null;
  bar.style.width = max ? `${Math.min(100, (r.avg / max) * 100)}%` : '0';
  track.append(bar);
  if (ref) {
    const mark = h('b', { class: 'ref', title: refKind(ref.kind) });
    mark.style.left = `${(1 / 1.5) * 100}%`;
    track.append(mark);
  }
  if (max) row.append(track);
  row.title = (ref ? refKind(ref.kind) + ': ' + fmt(ref.amount) + ' ' + n.unit + '. ' : '')
    + (over ? t('More than the body needs; less salt is better.') + ' ' : '')
    + t('{pct}% of the kcal eaten come from foods that list it.', { pct: fmt(r.coverage * 100) });
  return row;
}

const refKind = (kind) => ({ recommended: t('Recommended intake'), estimated: t('Estimated value'), guide: t('Guide value, at least') })[kind] || '';

// alcoholSummary is a line about the alcohol of the rows' days, or null
// when there was none. full: with the drinking days, for the tab.
export function alcoholSummary(rows, full) {
  const r = rangeNutrient(rows, 'alcohol');
  if (!r || r.avg <= 0) return null;
  const days = rows.filter(logged);
  const free = days.filter((d) => !(d.nut?.alcohol?.[0] > 0)).length;
  const box = h('div', { class: 'alcohol-line' });
  box.append(h('b', {}, t('Alcohol')), ' ',
    t('Ø {g} g a day ({kcal} kcal)', { g: fmt(r.avg, 1), kcal: fmt(r.avg * KCAL_PER_G_ALCOHOL) }), ' · ',
    t('{free} of {days} days without', { free, days: days.length }));
  if (full) {
    const heavy = days.filter((d) => (d.nut?.alcohol?.[0] || 0) >= ALCOHOL_DAY).length;
    if (heavy) box.append(' · ', tn(heavy, '{n} day with {g} g or more', '{n} days with {g} g or more', { g: fmt(ALCOHOL_DAY) }));
  }
  return box;
}

// renderAlcoholLine puts the alcohol line under the macros.
export function renderAlcoholLine(s) {
  const host = $('#alcohol-macros');
  if (!host) return;
  const line = alcoholSummary(s.complete, false);
  host.hidden = !line;
  host.replaceChildren(...(line ? [line] : []));
}

// entryNutrients is one food entry's tracked nutrients, or null when its
// food's aren't known (Day.foods[i].nk/ns).
export function entryNutrients(f) {
  const per = f.nk && state.data.food_nutrients?.[f.nk - 1];
  if (!per) return null;
  const out = {};
  for (const [k, v] of Object.entries(per)) out[k] = v * (f.ns || 0);
  return out;
}

// dayNutrientList is the day window's folded list of the day's minerals and
// vitamins, against the references; null when none are known.
export function dayNutrientList(day) {
  const list = NUTRIENTS.filter((n) => n.group !== 'alcohol' && day.nut?.[n.key]);
  if (!list.length) return null;
  const refs = nutrientRefs() || {};
  const box = h('details', { class: 'sheet-nut' });
  box.append(h('summary', {}, t('Vitamins & minerals')));
  const rows = h('div', { class: 'sheet-nut-list' });
  for (const n of list) {
    const [amount, cov] = day.nut[n.key];
    const ref = refs[n.key];
    const row = h('div', { class: 'day-item' + (cov < LOW_COVERAGE ? ' low' : '') });
    row.append(h('span', { class: 'name' }, n.label()),
      h('span', { class: 'amt' }, [ref ? t('{pct}% of {ref} {unit}', { pct: fmt((amount / ref.amount) * 100), ref: fmt(ref.amount), unit: n.unit }) : '', t('known for {pct}%', { pct: fmt(cov * 100) })].filter(Boolean).join(' · ')),
      h('span', { class: 'val' }, `${fmt(amount, amount < 10 ? 1 : 0)} ${n.unit}`));
    rows.append(row);
  }
  box.append(rows);
  return box;
}
