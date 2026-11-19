// The Macro sources card: which foods the protein, carbs, fat and fibre of
// the range come from, and the best sources per calorie. Every row opens
// that food's history window.

import { MEALS, fmt } from './format.js';
import { $, h, hbar, meter, tabButton, tabKeys } from './dom.js';
import { state, store } from './state.js';
import { historyLink, openFood } from './history.js';
import { MACRO_TABS, macroSources, mealProteinTarget, nutrientTab, proteinPerMeal, rankSources } from './query.js';
import { NUTRIENTS, entryNutrients, nutrient } from './nutrients.js';
import { t } from './i18n.js';

const ICONS = { pr: '💪', c: '🍞', f: '🥑', fi: '🌾', su: '🍬', 'n:alcohol': '🍺', micro: '🥦' };
let tab = store.get('macroTab') || 'pr';
// The mineral or vitamin the "Vitamins & minerals" tab shows.
let micro = store.get('macroMicro') || 'magnesium';

// The subtitle per tab ({g}: grams per logged day).
const AVG = {
  pr: t('{g} g protein on average per logged day'),
  c: t('{g} g carbs on average per logged day'),
  f: t('{g} g fat on average per logged day'),
  fi: t('{g} g fibre on average per logged day'),
  su: t('{g} g sugar on average per logged day'),
};

// What the "best" list measures, per tab, and how a value reads.
const BEST = {
  pr: [t('Most protein per calorie'), t('grams per 100 kcal'), (v) => `${fmt(v, 1)} g`],
  c: [t('Most fibre per carb'), t('grams of fibre per 10 g carbs'), (v) => `${fmt(v, 1)} g`],
  f: [t('Least saturated fat'), t('share of the fat that is saturated'), (v) => `${fmt(v * 100)} %`],
  fi: [t('Most fibre per calorie'), t('grams per 100 kcal'), (v) => `${fmt(v, 1)} g`],
  su: [t('Most sugar per calorie'), t('grams per 100 kcal · the sweetest foods'), (v) => `${fmt(v, 1)} g`],
};

export function renderSources(s) {
  const card = $('#sources-card');
  const { foods, totals } = macroSources(s.rows, entryNutrients);
  card.hidden = !totals.pr && !totals.c && !totals.f;
  if (card.hidden) return;
  // Alcohol and the minerals and vitamins any food eaten lists.
  const micros = NUTRIENTS.filter((n) => n.group === 'minerals' || n.group === 'vitamins').filter((n) => totals['nE:' + n.key] > 0);
  if (!micros.some((n) => n.key === micro)) micro = micros[0]?.key;
  const tabList = [
    ...MACRO_TABS.map(({ key, label }) => [key, label]),
    ...(totals['n:alcohol'] > 0 ? [['n:alcohol', t('Alcohol')]] : []),
    ...(micros.length ? [['micro', t('Vitamins & minerals')]] : []),
  ];
  if (!tabList.some(([k]) => k === tab)) tab = 'pr';

  const days = s.rows.filter((r) => r.foods?.length).length;
  const tabs = $('#sources-tabs');
  const draw = () => {
    tabs.querySelectorAll('button').forEach((b) => {
      const on = b.dataset.tab === tab;
      b.setAttribute('aria-selected', String(on));
      b.tabIndex = on ? 0 : -1;
    });
    const def = tab === 'micro' ? nutrientTab(micro) : tab === 'n:alcohol' ? nutrientTab('alcohol') : MACRO_TABS.find((x) => x.key === tab);
    const key = def.key;
    const n = def.nutrient && nutrient(def.nutrient);
    const unit = n ? n.unit : 'g';
    const { top, best } = rankSources(foods, def);
    const total = totals[key] || 0;
    const max = top[0]?.[1][key] || 1;
    const perDay = total / Math.max(1, days);
    const amount = (v) => `${fmt(v, v < 100 ? 1 : 0)} ${unit}`;

    // Fibre, sugar, alcohol, minerals and vitamins are missing for some
    // entries (AI ones, foods that don't list them): say how much is known.
    const known = def.partial && totals.e ? (totals[def.partial] || 0) / totals.e : 1;
    const avgText = n ? t('{name}: {amount} on average per logged day', { amount: amount(perDay), name: n.label() }) : AVG[key].replace('{g}', fmt(perDay, def.partial ? 1 : 0));
    $('#sources-sub').textContent = avgText
      + (def.partial ? ' · ' + t('known for {pct} % of calories', { pct: fmt(known * 100) }) : '');

    const pick = $('#sources-micro');
    pick.hidden = tab !== 'micro';
    if (!pick.hidden) {
      pick.replaceChildren(...micros.map((m) => h('option', { value: m.key }, m.label())));
      pick.value = micro;
      pick.onchange = () => { micro = pick.value; store.set('macroMicro', micro); draw(); };
    }

    $('#sources-top').replaceChildren(...(top.length ? top.map(([name, c]) => historyLink(
      hbar(name, amount(c[key]), `${fmt((c[key] / total) * 100)} % · ${fmt(c.n)}×`, c[key] / max, def.cls || 's3'),
      name, openFood,
    )) : [h('p', { class: 'empty-tab' }, t('Nothing logged'))]));

    renderProteinMeals(s, tab === 'pr');

    // Alcohol has no "best" sources.
    const bestBox = $('#sources-best').parentElement;
    bestBox.hidden = tab === 'n:alcohol';
    if (bestBox.hidden) return;
    const [title, note, show] = n ? [t('Most per calorie'), t('{unit} per 100 kcal', { unit }), (v) => amount(v)] : BEST[key];
    $('#sources-best-title').textContent = title;
    $('#sources-best-note').textContent = note;
    const vals = best.map(([, c]) => def.best(c));
    const bmax = Math.max(...vals, 0) || 1;
    $('#sources-best').replaceChildren(...(best.length ? best.map(([name, c], i) => historyLink(
      // Lower is better for saturated fat: the bar shows the unsaturated share.
      hbar(name, show(vals[i]), `${fmt(c.n)}× · ${amount(c[key] / c.n)} ${t('per entry')}`, def.asc ? 1 - vals[i] : vals[i] / bmax, def.cls || 's3'),
      name, openFood,
    )) : [h('p', { class: 'empty-tab' }, t('Too few foods eaten at least 3 times'))]));
  };

  tabs.replaceChildren(...tabList.map(([key, label]) => {
    const b = tabButton(key, label, ICONS[key]);
    b.setAttribute('aria-controls', 'sources-lists');
    return b;
  }));
  tabs.onclick = (e) => {
    const b = e.target.closest('button');
    if (!b) return;
    tab = b.dataset.tab;
    store.set('macroTab', tab);
    draw();
  };
  tabs.onkeydown = (e) => tabKeys(e, tabs);
  draw();
}

// On the protein tab: how much protein each meal brings, and how often it
// reaches what a meal should have for the user's weight.
function renderProteinMeals(s, show) {
  const box = $('#protein-meals');
  const rows = s.rows.filter((r) => r.foods?.length);
  box.hidden = !show || !rows.length;
  if (box.hidden) return;
  const end = s.rows.at(-1)?.date;
  const weight = state.data.days.findLast((d) => d.weight && (!end || d.date <= end))?.weight;
  const target = mealProteinTarget(weight);
  $('#protein-meals-note').textContent = weight
    ? t('average on the days it was logged · goal {g} g a meal (0.4 g per kg)', { g: fmt(target) })
    : t('average on the days it was logged · goal {g} g a meal', { g: fmt(target) });
  const per = proteinPerMeal(rows, target);
  $('#protein-meals-list').replaceChildren(...MEALS.map(([key, label]) => {
    const m = per[key];
    const el = h('div', { class: 'stat' });
    const k = h('div', { class: 'k' });
    k.append(h('span', {}, label), h('span', {}, m.days ? `${fmt((m.hits / m.days) * 100)}%` : ''));
    const v = h('div', { class: 'v' }, m.days ? fmt(m.avg) : '–');
    v.append(h('small', {}, 'g'));
    el.append(k, v, meter(m.days ? m.hits / m.days : 0),
      h('p', { class: 'note' }, m.days ? t('{hits} of {days} days with {g} g or more', { hits: fmt(m.hits), days: fmt(m.days), g: fmt(target) }) : t('Not logged')));
    return el;
  }));
}
