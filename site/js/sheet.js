// The day sheet: a side panel on desktop, a bottom sheet on phones.

import { MEALS, MEAL_ICONS, MEAL_TABS, actIcon, actLabel, actName, activityDetail, dfLong, fmt, fmtAmount, logged, mealThumb, parse } from './format.js';
import { h, emoji, nameCell, qualityBadges, setHashParam, tabButton, tabKeys, thumbImg } from './dom.js';
import { state } from './state.js';
import { openDialog } from './dialog.js';
import { openLightbox, closeLightbox } from './lightbox.js';
import { dayActivities, workoutDetail, workoutIcon, workoutName } from './health.js';
import { openWorkout, closeWorkout } from './workout.js';
import { sleepPanel, hasRecovery } from './recovery.js';
import { closeRecipe, openRecipe, recipeOf } from './recipe.js';
import { closeHistory, foodName } from './history.js';
import { dayShareButton } from './cards.js';
import { closeShare } from './share.js';
import { t } from './i18n.js';

let win = null;

// Tab names accepted in links besides the tab keys (breakfast, lunch,
// dinner, snack, activities, sleep).
const TAB_ALIASES = { snacks: 'snack', activity: 'activities', workouts: 'activities', recovery: 'sleep' };

export function openDay(date, tabKey, focusNav) {
  const day = state.byDate.get(date);
  if (!day) return;
  const days = state.data.days;
  const idx = days.findIndex((d) => d.date === date);
  setHashParam('day', date);

  const tabs = [];
  if (day.foods?.length || day.photos?.length || logged(day)) {
    // Photos the API did not attribute to a meal go on the first tab.
    const loose = (day.photos || []).filter((p) => !MEALS.some(([k]) => k === p.m));
    for (const [key, label] of MEALS) {
      const items = (day.foods || []).filter((f) => f.m === key);
      const photos = (day.photos || []).filter((p) => p.m === key);
      if (key === MEALS[0][0]) photos.unshift(...loose);
      tabs.push({ key, label, tab: MEAL_TABS[key], items, photos, type: 'foods', kcal: day.meals?.[key] });
    }
  }
  // Every photo of the day, in meal order, so the viewer can page through all of them.
  const dayPhotos = tabs.flatMap((tb) => tb.photos.map((p) => ({
    p, src: p.src, icon: MEAL_ICONS[tb.key], caption: `${tb.label} · ${dfLong.format(parse(day.date))}`,
  })));
  const acts = dayActivities(day);
  if (acts.length) tabs.push({ key: 'activities', label: t('Activities'), tab: t('Activities|tab'), items: acts, photos: [], type: 'activities' });
  if (hasRecovery(day)) tabs.push({ key: 'sleep', label: t('Sleep'), items: [], photos: [], type: 'sleep', icon: '😴' });

  const sheet = h('div', { class: 'sheet' });
  const head = h('div', { class: 'sheet-head' });
  head.append(h('h2', { id: 'sheet-title' }, dfLong.format(parse(day.date))));
  const prev = h('button', { class: 'sheet-nav', 'aria-label': t('Previous day'), type: 'button' }, '‹');
  const next = h('button', { class: 'sheet-nav', 'aria-label': t('Next day'), type: 'button' }, '›');
  const close = h('button', { class: 'sheet-close', 'aria-label': t('Close'), type: 'button' }, '×');
  prev.disabled = idx <= 0;
  next.disabled = idx >= days.length - 1;
  prev.onclick = () => openDay(days[idx - 1].date, current, 'prev');
  next.onclick = () => openDay(days[idx + 1].date, current, 'next');
  head.append(prev, next, dayShareButton(day, () => current), close);

  const sum = h('div', { class: 'sheet-sum' });
  const goal = day.energy_goal || day.energy_base;
  [[t('Eaten'), day.energy], [t('Goal'), goal], [t('Burned'), day.activity_energy]].forEach(([k, v]) => {
    const b = h('b', {}, v ? fmt(v) : '–');
    if (v) b.append(h('small', {}, 'kcal'));
    const d = h('div');
    d.append(h('span', {}, k), b);
    sum.append(d);
  });

  const nav = h('div', { class: 'tabs', role: 'tablist', 'aria-label': t('Meal') });
  const panel = h('div', { class: 'sheet-body', role: 'tabpanel' });
  let current = null;

  const setTab = (key) => {
    current = key;
    // The open tab is part of the link, e.g. "#day=2026-10-11&tab=lunch".
    setHashParam('tab', key ?? null);
    nav.querySelectorAll('button').forEach((b) => {
      const on = b.dataset.tab === key;
      b.setAttribute('aria-selected', String(on));
      b.tabIndex = on ? 0 : -1;
    });
    const tab = tabs.find((tb) => tb.key === key);
    panel.replaceChildren();
    panel.scrollTop = 0;
    if (tab?.photos?.length) {
      const strip = h('div', { class: 'photos' });
      tab.photos.forEach((p, k) => {
        const a = h('a', { href: p.src, target: '_blank', rel: 'noopener', 'aria-label': t('Show {meal} photo {n} large', { meal: tab.label, n: k + 1 }) });
        a.append(h('img', { src: p.src, alt: t('{meal} photo {n}', { meal: tab.label, n: k + 1 }), loading: 'lazy' }));
        a.addEventListener('click', (e) => {
          // Ctrl/Cmd/middle click still opens the photo in a new tab.
          if (e.ctrlKey || e.metaKey || e.shiftKey || e.button !== 0) return;
          e.preventDefault();
          openLightbox(dayPhotos, dayPhotos.findIndex((x) => x.p === p), a);
        });
        strip.append(a);
      });
      panel.append(strip);
    }
    if (tab?.type === 'sleep') {
      panel.append(sleepPanel(day));
      return;
    }
    if (!tab || !tab.items.length) {
      panel.append(h('p', { class: 'empty-tab' }, tab?.kcal ? t('{kcal} kcal logged; food names are not published', { kcal: fmt(tab.kcal) }) : t('Nothing logged')));
      return;
    }
    if (tab.type === 'foods') {
      for (const f of tab.items) {
        const rec = recipeOf(f);
        if (!rec) {
          const row = h('div', { class: 'day-item' });
          const name = nameCell(f.n, f.ai);
          // The name opens the food's history on top of the day.
          name.querySelector('.txt').replaceWith(foodName(f.n));
          const badges = qualityBadges(f.x, state.data.products?.[f.x]);
          if (badges) name.append(badges);
          row.append(name, h('span', { class: 'amt' }, fmtAmount(f)), h('span', { class: 'val' }, `${fmt(f.e)} kcal`));
          panel.append(row);
          continue;
        }
        // Recipes open a window with their photo and ingredients.
        const row = h('button', { class: 'day-item workout-row recipe-row', type: 'button', 'aria-label': t('{name}, {amount}, {kcal} kcal: show ingredients', { name: f.n, amount: fmtAmount(f), kcal: fmt(f.e) }) });
        const name = nameCell(f.n, f.ai);
        name.prepend(rec.img ? thumbImg(rec.img, 'row-thumb') : emoji('🥣'));
        row.append(name, h('span', { class: 'amt' }, fmtAmount(f)), h('span', { class: 'val' }, `${fmt(f.e)} kcal`), h('span', { class: 'chev', 'aria-hidden': 'true' }, '›'));
        row.addEventListener('click', () => openRecipe(day, f, row));
        panel.append(row);
      }
      if (tab.items.length > 1) {
        const total = tab.items.reduce((a, f) => a + f.e, 0);
        const row = h('div', { class: 'day-item total' });
        row.append(nameCell(t('Total')), h('span', { class: 'amt' }), h('span', { class: 'val' }, `${fmt(total)} kcal`));
        panel.append(row);
      }
    } else {
      for (const { a, w } of tab.items) {
        const key = w && w.type && w.type !== 'OTHER' ? workoutName(w.type) : actName(a.name);
        const name = actLabel(key);
        const detail = a ? activityDetail(a) : workoutDetail(w);
        const kcal = a ? a.energy : w.energy;
        if (!w) {
          const row = h('div', { class: 'day-item' });
          row.append(nameCell(name, false, actIcon(key)), h('span', { class: 'amt' }, detail), h('span', { class: 'val' }, `${fmt(kcal)} kcal`));
          panel.append(row);
          continue;
        }
        // Rows with Fitbit data open the workout window.
        const row = h('button', { class: 'day-item workout-row', type: 'button', 'aria-label': t('{name}, {detail}: show heart rate and zones', { name, detail }) });
        const cell = nameCell(name, false, a ? actIcon(key) : workoutIcon(w.type));
        if (!a) cell.append(h('span', { class: 'src-badge', title: t('Recorded by Fitbit, not logged in Yazio') }, 'Fitbit'));
        row.append(cell, h('span', { class: 'amt' }, detail), h('span', { class: 'val' }, kcal ? `${fmt(kcal)} kcal` : '–'), h('span', { class: 'chev', 'aria-hidden': 'true' }, '›'));
        row.addEventListener('click', () => openWorkout(day, w.id, row));
        panel.append(row);
      }
    }
  };

  for (const tb of tabs) {
    const thumb = tb.type === 'foods' ? mealThumb(day, tb.key, state.data.recipes) || (tb.photos[0]?.src ?? null) : null;
    const btn = tabButton(tb.key, tb.tab || tb.label, tb.icon || (tb.key === 'activities' ? '🏅' : MEAL_ICONS[tb.key]), tb.items.length || tb.photos.length, thumb);
    btn.addEventListener('click', () => setTab(tb.key));
    nav.append(btn);
  }
  nav.addEventListener('keydown', (e) => tabKeys(e, nav));

  sheet.append(head, sum);
  if (tabs.length) sheet.append(nav);
  sheet.append(panel);

  closeWindowsOnTop();
  if (win) {
    // Switching days: same window, no re-entrance.
    win.el.replaceChildren(sheet);
    sheet.style.animation = 'none';
  } else {
    win = openDialog({
      cls: 'sheet-overlay', id: 'modal', labelledBy: 'sheet-title', content: [sheet], returnFocus: document.activeElement,
      onClose: () => {
        win = null;
        closeWindowsOnTop();
        setHashParam('day', null);
        setHashParam('tab', null);
      },
    });
  }
  const wanted = TAB_ALIASES[tabKey] || tabKey;
  const first = tabs.find((tb) => tb.key === wanted) || tabs.find((tb) => tb.items.length || tb.photos.length) || tabs[0];
  setTab(first?.key);
  // Keep focus on the arrow that was used, so paging through days stays on it.
  const used = focusNav === 'prev' ? prev : focusNav === 'next' ? next : close;
  (used.disabled ? close : used).focus();

  close.onclick = closeDay;
  // Windows opened on top are dialogs of their own, so their arrow keys
  // never reach this one and don't page the days.
  win.el.onkeydown = (e) => {
    if (e.target.closest('[role="tablist"]')) return;
    if (e.key === 'ArrowLeft' && !prev.disabled) prev.click();
    else if (e.key === 'ArrowRight' && !next.disabled) next.click();
  };
}

export function closeDay() {
  win?.close();
}

function closeWindowsOnTop() {
  closeHistory(true);
  closeLightbox(true);
  closeWorkout(true);
  closeRecipe(true);
  closeShare(true);
}
