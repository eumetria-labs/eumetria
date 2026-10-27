// The recipe window: opened from a recipe in the day sheet. The recipe
// photo and its ingredients, scaled to the portions eaten that day.

import { MEALS, MEAL_ICONS, dfLong, fmt, fmtAmount, fmtQty, parse, recipeShare, recipeTotals } from './format.js';
import { h, emoji, qualityBadges, setHashParam } from './dom.js';
import { state } from './state.js';
import { openDialog } from './dialog.js';
import { openLightbox } from './lightbox.js';
import { foodName, openFood } from './history.js';
import { recipeShareButton } from './cards.js';
import { t, tn } from './i18n.js';

let win = null;

// The recipe behind a food entry, if stats.json has one.
export const recipeOf = (f) => (f?.r ? state.data?.recipes?.[f.r] || null : null);

export function openRecipe(day, f, returnFocus) {
  const rec = recipeOf(f);
  if (!rec) return;
  closeRecipe(true);
  setHashParam('recipe', f.r);
  const share = recipeShare(f, rec);
  const meal = MEALS.find(([k]) => k === f.m)?.[1];

  const close = h('button', { class: 'sheet-close', type: 'button', 'aria-label': t('Close recipe') }, '×');
  const title = h('h2', { id: 'rc-title' });
  title.append(emoji(MEAL_ICONS[f.m] || '🍽️'), ' ', rec.name);
  const eaten = rec.portions !== 1 ? t('{eaten} of {total}', { eaten: fmtQty(f.a || 1, 'portion'), total: fmtQty(rec.portions, 'portion') }) : fmtQty(f.a || 1, 'portion');
  const sub = [meal, dfLong.format(parse(day.date)), eaten].filter(Boolean).join(' · ');
  const head = h('div', { class: 'sheet-head wo-head' });
  const titles = h('div', { class: 'wo-titles' });
  titles.append(title, h('p', { class: 'muted' }, sub));
  // Every day this recipe was eaten, in the food history window on top.
  const history = h('button', { class: 'pill-btn rc-history', type: 'button', title: t('{name}: every time you logged it', { name: f.n }) }, t('History'));
  history.onclick = () => openFood(f.n, history);
  head.append(titles, history, recipeShareButton(day, f), close);

  const body = h('div', { class: 'wo-body' });
  // The recipe was edited after this day and the version eaten is unknown:
  // the day's totals are Yazio's own, the ingredients are today's.
  if (f.rs) body.append(h('p', { class: 'rc-note', role: 'note' }, t('This recipe was changed after this day and the version you ate is not stored. The calories are what Yazio counted that day; the ingredients are the current version.')));
  if (rec.img) {
    const photo = h('button', { class: 'rc-photo', type: 'button', 'aria-label': t('Show the photo of {name} large', { name: rec.name }) });
    photo.append(h('img', { src: rec.img, alt: rec.name }));
    photo.onclick = () => openLightbox([{ src: rec.img, caption: rec.name, icon: MEAL_ICONS[f.m] }], 0, photo);
    body.append(photo);
  }

  const items = rec.items || [];
  const tot = recipeTotals(items, share);
  const stats = h('div', { class: 'wo-stats' });
  for (const [label, value, unit] of [
    [t('Calories'), fmt(f.e), 'kcal'],
    // Macros come from the ingredients, which are only right for the
    // version eaten.
    ...(items.length && !f.rs ? [[t('Protein'), fmt(tot.protein), 'g'], [t('Carbs'), fmt(tot.carb), 'g'], [t('Fat'), fmt(tot.fat), 'g']] : []),
  ]) {
    const d = h('div');
    const b = h('b', {}, value);
    b.append(h('small', {}, unit));
    d.append(h('span', {}, label), b);
    stats.append(d);
  }
  body.append(stats);

  if (items.length) {
    const sec = h('section', { class: 'wo-sec' });
    const hd = h('div', { class: 'wo-sec-head' });
    hd.append(h('h3', {}, t('Ingredients')), h('span', { class: 'muted' }, share !== 1 ? t('Scaled to {eaten}', { eaten }) : tn(items.length, '{n} ingredient', '{n} ingredients')));
    sec.append(hd);
    for (const it of items) {
      const row = h('div', { class: 'day-item rc-item' });
      const name = h('span', { class: 'name rc-name', title: it.p ? `${it.n} (${it.p})` : it.n });
      const line = h('span', { class: 'rc-line' });
      line.append(foodName(it.n));
      const badges = qualityBadges(it.x, state.data.products?.[it.x]);
      if (badges) line.append(badges);
      name.append(line);
      if (it.p) name.append(h('small', { class: 'rc-producer' }, it.p));
      const amount = fmtAmount({ a: it.a * share, u: it.u, s: it.s, sq: it.sq * share });
      row.append(name, h('span', { class: 'amt' }, amount), h('span', { class: 'val' }, `${fmt(it.e * share)} kcal`));
      sec.append(row);
    }
    if (!f.rs) {
      const total = h('div', { class: 'day-item total' });
      total.append(h('span', { class: 'name' }, t('Total')), h('span', { class: 'amt' }), h('span', { class: 'val' }, `${fmt(tot.energy)} kcal`));
      sec.append(total);
    }
    body.append(sec);
  }

  const panel = h('div', { class: 'wo-panel' });
  panel.append(head, body);
  win = openDialog({ cls: 'wo-overlay', labelledBy: 'rc-title', content: [panel], returnFocus, onClose: () => setHashParam('recipe', null) });
  close.onclick = () => closeRecipe();
  close.focus();
}

export function closeRecipe(instant) {
  win?.close(instant);
  win = null;
}

// A link like "#day=2026-10-20&recipe=<id>" opens the day's first entry of
// that recipe.
export function openRecipeFromHash(day, id, returnFocus) {
  const f = id && (day?.foods || []).find((x) => x.r === id);
  if (f) openRecipe(day, f, returnFocus);
}
