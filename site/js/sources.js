// The Macro sources card: which foods the protein, carbs, fat and fibre of
// the range come from, and the best sources per calorie. Every row opens
// that food's history window.

import { fmt } from './format.js';
import { $, h, hbar, tabButton, tabKeys } from './dom.js';
import { store } from './state.js';
import { historyLink, openFood } from './history.js';
import { MACRO_TABS, macroSources, rankSources } from './query.js';
import { t } from './i18n.js';

const ICONS = { pr: '💪', c: '🍞', f: '🥑', fi: '🌾', su: '🍬' };
let tab = MACRO_TABS.some((x) => x.key === store.get('macroTab')) ? store.get('macroTab') : 'pr';

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
  const { foods, totals } = macroSources(s.rows);
  card.hidden = !totals.pr && !totals.c && !totals.f;
  if (card.hidden) return;

  const days = s.rows.filter((r) => r.foods?.length).length;
  const tabs = $('#sources-tabs');
  const draw = () => {
    tabs.querySelectorAll('button').forEach((b) => {
      const on = b.dataset.tab === tab;
      b.setAttribute('aria-selected', String(on));
      b.tabIndex = on ? 0 : -1;
    });
    const def = MACRO_TABS.find((x) => x.key === tab);
    const { top, best } = rankSources(foods, def);
    const total = totals[tab];
    const max = top[0]?.[1][tab] || 1;
    const perDay = total / Math.max(1, days);

    // Fibre and sugar are missing for AI entries: say how much is known.
    const known = def.partial && totals.e ? totals[def.partial] / totals.e : 1;
    $('#sources-sub').textContent = AVG[tab].replace('{g}', fmt(perDay, def.partial ? 1 : 0))
      + (def.partial ? ' · ' + t('known for {pct} % of calories (AI entries have none)', { pct: fmt(known * 100) }) : '');

    $('#sources-top').replaceChildren(...(top.length ? top.map(([name, c]) => historyLink(
      hbar(name, `${fmt(c[tab], c[tab] < 100 ? 1 : 0)} g`, `${fmt((c[tab] / total) * 100)} % · ${fmt(c.n)}×`, c[tab] / max, def.cls),
      name, openFood,
    )) : [h('p', { class: 'empty-tab' }, t('Nothing logged'))]));

    const [title, unit, show] = BEST[tab];
    $('#sources-best-title').textContent = title;
    $('#sources-best-note').textContent = unit;
    const vals = best.map(([, c]) => def.best(c));
    const bmax = Math.max(...vals, 0) || 1;
    $('#sources-best').replaceChildren(...(best.length ? best.map(([name, c], i) => historyLink(
      // Lower is better for saturated fat: the bar shows the unsaturated share.
      hbar(name, show(vals[i]), `${fmt(c.n)}× · ${fmt(c[tab] / c.n, 1)} g ${t('per entry')}`, def.asc ? 1 - vals[i] : vals[i] / bmax, def.cls),
      name, openFood,
    )) : [h('p', { class: 'empty-tab' }, t('Too few foods eaten at least 3 times'))]));
  };

  tabs.replaceChildren(...MACRO_TABS.map(({ key, label }) => {
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
