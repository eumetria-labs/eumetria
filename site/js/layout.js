// The dashboard's cards in the order the user chose, without the ones they
// hid (Profile → Customize Dashboard). The choice is stored with the
// account (api/profile/layout), so it follows them to every device.
//
// Some cards fit half the width: two of them next to each other share a
// row on wide screens; one alone, or next to a wide card, takes the row.

import { $, h } from './dom.js';
import { account, api } from './account.js';
import { t, tn } from './i18n.js';
import { DEFAULT_RETEST } from './health.js';

const grid = $('#cards');
// The page's own order, before any change.
const DEFAULT = [...grid.children].map((c) => c.id);
const HALF = new Set(['weight-card', 'movement-card', 'foods-card', 'top-activities-card']);

// Which part of the page each card belongs to: the group labels between
// the cards and the phone's tabs (shell.js).
export const GROUP = {
  today: 'today', 'review-card': 'today', tiles: 'today',
  'energy-card': 'nutrition', 'foods-card': 'nutrition', 'sources-card': 'nutrition', 'quality-card': 'nutrition',
  'weight-card': 'body', 'movement-card': 'body', 'recovery-card': 'body',
  'training-card': 'training', 'top-activities-card': 'training', 'insights-card': 'more',
  'data-card': 'more', 'more-card': 'more',
};
// The labels between the cards on wide screens follow the sidebar's parts.
const LABEL = {
  'energy-card': 'Nutrition', 'weight-card': 'Body', 'movement-card': 'Body', 'recovery-card': 'Sleep', 'training-card': 'Training', 'insights-card': 'Insights',
  'foods-card': 'Foods', 'top-activities-card': 'Foods', 'sources-card': 'Foods', 'quality-card': 'Foods', 'data-card': 'Data',
};
const LABEL_TEXT = { Nutrition: () => t('Nutrition'), Body: () => t('Body'), Sleep: () => t('Sleep'), Training: () => t('Training'), Foods: () => t('Foods'), Insights: () => t('Insights'), Data: () => t('Data') };

const layout = () => account.user?.layout || { order: [], hidden: [] };

// The cards in the chosen order; cards the choice doesn't know (new ones,
// or never moved) keep their place after the card they follow on the page.
function ordered(order) {
  const out = order.filter((id, i) => DEFAULT.includes(id) && order.indexOf(id) === i);
  DEFAULT.forEach((id, i) => {
    if (out.includes(id)) return;
    const prev = DEFAULT.slice(0, i).findLast((p) => out.includes(p));
    out.splice(prev ? out.indexOf(prev) + 1 : 0, 0, id);
  });
  return out;
}

// applyLayout puts the cards in order, hides the hidden ones and pairs the
// half-width ones. Again after each render: cards without data come and go.
export function applyLayout() {
  const { order, hidden } = layout();
  const ids = ordered(order);
  const cards = ids.map((id) => document.getElementById(id));
  for (const l of grid.querySelectorAll(':scope > .group-label')) l.remove();
  if (cards.some((c, i) => grid.children[i] !== c)) grid.append(...cards);
  for (const c of cards) c.toggleAttribute('data-off', hidden.includes(c.id));
  const shown = cards.filter((c) => !c.hidden && !c.hasAttribute('data-off'));
  for (let i = 0; i < shown.length; i++) {
    const c = shown[i], next = shown[i + 1];
    if (HALF.has(c.id) && next && HALF.has(next.id)) {
      c.classList.remove('span-2');
      next.classList.remove('span-2');
      i++;
    } else c.classList.add('span-2');
  }
  // A label where the part of the page changes, in the cards' order.
  let last = '';
  for (const c of shown) {
    const g = LABEL[c.id] || 'Data';
    if (g !== last) c.before(groupLabel(g));
    last = g;
  }
}

function groupLabel(g) {
  const el = h('div', { class: 'group-label' });
  el.append(h('h2', {}, LABEL_TEXT[g]()));
  return el;
}

const titleOf = (c) => (c.querySelector('h2')?.textContent || c.id).trim();

let saving = Promise.resolve();

// renderLayout draws the list of cards into host (Profile → Customize
// Dashboard): each can be hidden or moved up and down.
export function renderLayout(host) {
  const list = h('ol', { class: 'lo-list' });
  const status = h('p', { class: 'muted lo-status', role: 'status' });
  const reset = h('button', { class: 'pill-btn', type: 'button' }, t('Reset'));
  const foot = h('div', { class: 'lo-foot' });
  foot.append(reset, status);
  const sec = h('section', { class: 'wo-sec lo-panel' });
  const head = h('div', { class: 'wo-sec-head' });
  head.append(h('h3', {}, t('Cards')), h('span', { class: 'muted' }, t('Unticked cards are hidden. Changes apply at once.')));
  sec.append(head, list, foot);

  // Retest hints: after how many months a lift or benchmark workout is
  // offered again (Today, Personal bests).
  const hints = h('section', { class: 'wo-sec lo-panel' });
  const hintsHead = h('div', { class: 'wo-sec-head' });
  hintsHead.append(h('h3', {}, t('Retest hints')), h('span', { class: 'muted' }, t('A hint on the Today card and in Personal bests when a lift or benchmark workout wasn’t done for a while.')));
  const months = h('select', { class: 'field', id: 'lo-retest' });
  months.append(h('option', { value: '0' }, t('Off')), ...[3, 6, 9, 12, 18, 24].map((m) => h('option', { value: String(m) }, tn(m, 'after {n} month', 'after {n} months'))));
  months.value = String(layout().retest_months ?? DEFAULT_RETEST);
  months.onchange = () => save({ ...layout(), retest_months: Number(months.value) }, 'lo-retest');
  const row = h('label', { class: 'lo-hint', for: 'lo-retest' });
  row.append(h('span', {}, t('Offer a retest')), months);
  hints.append(hintsHead, row);
  host.replaceChildren(sec, hints);

  const save = (next, focusId, dir) => {
    if (!account.user) return;
    // The retest setting stays when the cards change, and the other way round.
    const before = layout().retest_months;
    next = { retest_months: before, ...next };
    if (next.retest_months == null) delete next.retest_months;
    account.user.layout = next;
    if (next.retest_months !== before) document.dispatchEvent(new Event('phases-change'));
    applyLayout();
    draw(focusId, dir);
    saving = saving.then(async () => {
      const res = await api('profile/layout', 'PUT', next).catch(() => null);
      status.textContent = res?.ok ? '' : t('Not saved: the server could not be reached.');
    });
  };
  reset.onclick = () => save({ order: [], hidden: [] });

  // Cards without data in this account (no Fitbit, no scope) aren't listed;
  // they keep their place among the others.
  function draw(focusId, dir) {
    const { order, hidden } = layout();
    const all = ordered(order);
    const listed = all.filter((id) => !document.getElementById(id).hidden);
    list.replaceChildren(...listed.map((id, i) => {
      const card = document.getElementById(id);
      const name = titleOf(card);
      const row = h('li', { class: 'lo-row' + (hidden.includes(id) ? ' off' : '') });
      const box = h('input', { type: 'checkbox', id: 'lo-' + id });
      box.checked = !hidden.includes(id);
      box.onchange = () => save({ order: all, hidden: box.checked ? hidden.filter((x) => x !== id) : [...hidden, id] }, 'lo-' + id);
      const label = h('label', { for: 'lo-' + id }, name);
      const move = (d) => {
        // Swap with the next listed card up or down, past unlisted ones.
        const other = listed[i + d];
        const next = [...all];
        const a = next.indexOf(id), b = next.indexOf(other);
        [next[a], next[b]] = [next[b], next[a]];
        save({ order: next, hidden }, id, d < 0 ? 'up' : 'down');
      };
      const up = h('button', { class: 'icon-btn lo-move', type: 'button', 'aria-label': t('Move {card} up', { card: name }), 'data-dir': 'up' }, '↑');
      const down = h('button', { class: 'icon-btn lo-move', type: 'button', 'aria-label': t('Move {card} down', { card: name }), 'data-dir': 'down' }, '↓');
      up.disabled = i === 0;
      down.disabled = i === listed.length - 1;
      up.onclick = () => move(-1);
      down.onclick = () => move(1);
      row.dataset.id = id;
      row.append(box, label, up, down);
      return row;
    }));
    // Focus stays on what was pressed, or on its other arrow at an end.
    if (!focusId) return;
    if (dir) {
      const row = list.querySelector(`[data-id="${focusId}"]`);
      const b = row?.querySelector(`[data-dir="${dir}"]`);
      (b && !b.disabled ? b : row?.querySelector(`[data-dir="${dir === 'up' ? 'down' : 'up'}"]`))?.focus();
    } else document.getElementById(focusId)?.focus();
  }
  draw();
}
