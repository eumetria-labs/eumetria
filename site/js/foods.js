// The Foods and Top activities cards. Every row opens that food's or
// activity's history window; the Recipes tab opens the recipe window.

import { MEALS, MEAL_ICONS, MEAL_TABS, actIcon, actLabel, activityDetail, addDays, dfShort, fmt, fmtAvgQty, parse } from './format.js';
import { $, h, hbar, tabButton, tabKeys } from './dom.js';
import { state, store } from './state.js';
import { historyLink, openActivity, openFood } from './history.js';
import { openRecipe } from './recipe.js';
import { FOOD_SORTS, dayActivities, sessionsPerWeek, sortFoods } from './query.js';
import { bucketTitle, drawBars, timeChart } from './charts.js';
import { t, tn } from './i18n.js';

// The Recipes tab lists the recipes built in Yazio instead of a meal's foods.
const FOOD_TABS = [['all', t('All')], ...MEALS.map(([k]) => [k, MEAL_TABS[k]]), ['recipes', t('Recipes|tab')]];
const TAB_ICONS = { ...MEAL_ICONS, all: '🍽️', recipes: '🍲' };
const TOP_FOODS = 8;
let foodTab = FOOD_TABS.some(([k]) => k === store.get('foodTab')) ? store.get('foodTab') : 'all';
let foodSort = FOOD_SORTS.some(([k]) => k === store.get('foodSort')) ? store.get('foodSort') : 'count';
let foodQuery = '', foodsAll = false;

export function renderFoods(s) {
  const card = $('#foods-card');
  card.hidden = !state.data.days.some((d) => d.foods?.length);
  if (card.hidden) return;

  // Count per meal; "all" counts every entry of a food regardless of meal,
  // "recipes" the entries of recipes stats.json has the ingredients of.
  const hasRecipes = !!Object.keys(state.data.recipes || {}).length;
  const tabs = FOOD_TABS.filter(([k]) => k !== 'recipes' || hasRecipes);
  if (!tabs.some(([k]) => k === foodTab)) foodTab = 'all';
  const counts = new Map(tabs.map(([k]) => [k, new Map()]));
  for (const r of s.rows) for (const f of r.foods || []) {
    for (const key of ['all', f.m, f.r && state.data.recipes?.[f.r] ? 'recipes' : null]) {
      const m = counts.get(key);
      if (!m) continue;
      const c = m.get(f.n) || { n: 0, e: 0, amount: 0, units: new Set(), ai: false };
      c.n++; c.e += f.e; c.amount += f.a || 0; c.units.add(f.u || ''); c.ai ||= !!f.ai;
      c.last = { day: r, f }; // rows run oldest first
      m.set(f.n, c);
    }
  }

  const tabList = $('#foods-tabs');
  const more = $('#foods-more');
  const draw = () => {
    const recipes = foodTab === 'recipes';
    tabList.querySelectorAll('button').forEach((b) => {
      const on = b.dataset.tab === foodTab;
      b.setAttribute('aria-selected', String(on));
      b.tabIndex = on ? 0 : -1;
    });
    const all = counts.get(foodTab);
    const found = sortFoods([...all], foodSort, foodQuery);
    // A search shows every match; otherwise the top foods, all on request.
    const list = foodQuery || foodsAll ? found : found.slice(0, TOP_FOODS);
    $('#foods-sub').textContent = !all.size ? (recipes ? t('No recipes eaten in this range') : t('Nothing logged in this range'))
      : foodQuery ? t('{found} of {all} foods match', { found: fmt(found.length), all: fmt(all.size) })
        : recipes ? tn(all.size, '{n} recipe eaten in this range', '{n} recipes eaten in this range') : tn(all.size, '{n} different food in this range', '{n} different foods in this range');
    more.hidden = !!foodQuery || found.length <= TOP_FOODS;
    more.textContent = foodsAll ? t('Show top {n}', { n: TOP_FOODS }) : t('Show all {n}', { n: fmt(found.length) });
    more.setAttribute('aria-expanded', String(foodsAll));
    $('#foods').classList.toggle('scroll', list.length > TOP_FOODS);
    const metric = FOOD_SORTS.find(([k]) => k === foodSort)[2] || ((c) => c.n);
    const max = Math.max(...list.map(([, c]) => metric(c)), 1);
    $('#foods').replaceChildren(...(list.length ? list.map(([name, c]) => {
      // Average amount only when every entry used the same unit (e.g. always
      // grams); recipes leave it out to make room for the date last eaten.
      const unit = !recipes && c.units.size === 1 ? [...c.units][0] : '';
      const amount = unit && c.amount ? fmtAvgQty(c.amount / c.n, unit) + ' · ' : '';
      const avg = t('avg {what}', { what: `${amount}${fmt(c.e / c.n)} kcal` });
      const [value, note] = foodSort === 'kcal' ? [`${fmt(c.e)} kcal`, `${fmt(c.n)}× · ${avg}`]
        : foodSort === 'avg' ? [`${fmt(c.e / c.n)} kcal`, t('avg {what}', { what: `${amount}${fmt(c.n)}×` })]
          : [`${fmt(c.n)}×`, avg];
      if (recipes) {
        // A recipe opens its window with the ingredients of the last time it was eaten.
        const last = t('last {date}', { date: dfShort.format(parse(c.last.day.date)) });
        const row = hbar(name, value, `${note} · ${last}`, metric(c) / max, '', c.ai, '🍲');
        historyLink(row, name, () => openRecipe(c.last.day, c.last.f, row));
        row.title = t('{name}: ingredients and photo', { name });
        return row;
      }
      // Each food opens its window with the whole history.
      return historyLink(hbar(name, value, note, metric(c) / max, '', c.ai), name, openFood);
    }) : [h('p', { class: 'empty-tab' }, foodQuery ? t('No food matches') : recipes ? t('No recipes') : t('Nothing logged'))]));
  };
  tabList.replaceChildren(...tabs.map(([key, label]) => {
    const n = [...counts.get(key).values()].reduce((a, c) => a + c.n, 0);
    const b = tabButton(key, label, TAB_ICONS[key], key !== 'all' ? n : 0);
    b.setAttribute('aria-controls', 'foods');
    return b;
  }));
  tabList.onclick = (e) => {
    const b = e.target.closest('button');
    if (!b) return;
    foodTab = b.dataset.tab;
    store.set('foodTab', foodTab);
    draw();
  };
  tabList.onkeydown = (e) => tabKeys(e, tabList);
  const sort = $('#foods-sort');
  if (!sort.options.length) sort.replaceChildren(...FOOD_SORTS.map(([k, label]) => h('option', { value: k }, label)));
  sort.value = foodSort;
  sort.onchange = () => {
    foodSort = sort.value;
    store.set('foodSort', foodSort);
    draw();
  };
  $('#foods-q').oninput = (e) => {
    foodQuery = e.target.value.trim();
    draw();
  };
  more.onclick = () => {
    foodsAll = !foodsAll;
    draw();
  };
  draw();
}

export function renderTopActivities(s) {
  const card = $('#top-activities-card');
  card.hidden = !state.data.days.some((d) => d.activities?.length);
  if (card.hidden) return;

  const stats = new Map();
  for (const r of s.rows) {
    for (const a of dayActivities(r)) {
      const c = stats.get(a.key) || { n: 0, e: 0, dur: 0, durN: 0 };
      c.n++;
      c.e += a.energy;
      if (a.dur) {
        c.dur += a.dur;
        c.durN++;
      }
      stats.set(a.key, c);
    }
  }

  const top = [...stats]
    .sort((a, b) => b[1].n - a[1].n || b[1].e - a[1].e)
    .slice(0, 8);

  const sessions = [...stats.values()].reduce((a, c) => a + c.n, 0);
  $('#top-activities-sub').textContent = top.length
    ? tn(sessions, '{n} session in this range', '{n} sessions in this range')
    : t('No activities in this range');

  const max = top[0]?.[1].n || 1;
  renderActivityWeeks(s);

  $('#top-activities').replaceChildren(
    ...(top.length
      ? top.map(([name, c]) => {
          const dur = c.durN
            ? activityDetail({ dur: c.dur / c.durN }) + ' · '
            : '';

          // Each activity opens its window with the whole history.
          return historyLink(
            hbar(
              actLabel(name),
              `${fmt(c.n)}×`,
              t('avg {what}', { what: `${dur}${fmt(c.e / c.n)} kcal` }),
              c.n / max,
              's3',
              false,
              actIcon(name),
            ),
            name,
            openActivity,
            actLabel(name),
          );
        })
      : [h('p', { class: 'empty-tab' }, t('No activities'))]),
  );
}

// Activities per week below the list: over the range, but at least the last
// 12 weeks, so a short range still shows a rhythm.
function renderActivityWeeks(s) {
  const part = $('#act-weeks-part');
  const days = state.data.days;
  const weeks = sessionsPerWeek(state.byDate, new Date(Math.min(s.start, addDays(s.end, -83))), s.end, parse(days[0].date));
  part.hidden = weeks.length < 2;
  if (part.hidden) return;
  const vals = weeks.map((w) => w.n);
  const avg = vals.reduce((a, b) => a + b, 0) / vals.length;
  part.querySelector('.mini-title').textContent = t('Sessions per week · {avg} on average', { avg: fmt(avg, 1) });
  timeChart($('#chart-act-weeks'), {
    aria: t('Activities per week'),
    kind: 'band',
    empty: !vals.some(Boolean),
    n: weeks.length,
    dates: weeks.map((w) => w.start),
    y: [0, Math.max(...vals, 1) * 1.1],
    goal: avg ? { value: avg, label: t('Avg {avg}', { avg: fmt(avg, 1) }) } : null,
    draw(g, { x, y, bw }) { drawBars(g, vals, 's3', x, y, bw); },
    tip(i) {
      const w = weeks[i];
      return {
        title: bucketTitle(w, true),
        rows: w.n
          ? [{ cls: 'rect s3', value: `${fmt(w.n)}`, label: w.n === 1 ? t('session') : t('sessions') },
            ...[...w.keys].sort((a, b) => b[1] - a[1]).map(([k, n]) => ({ value: `${fmt(n)}×`, label: actLabel(k) }))]
          : [{ value: t('No activities'), label: '' }],
      };
    },
  });
}
