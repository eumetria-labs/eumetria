// The dashboard's cards in the order the user chose, without the ones they
// hid (account menu → Customize dashboard). The choice is stored with the
// account (api/profile/layout), so it follows them to every device.
//
// Some cards fit half the width: two of them next to each other share a
// row on wide screens; one alone, or next to a wide card, takes the row.

import { $, h } from './dom.js';
import { openDialog } from './dialog.js';
import { account, api } from './account.js';
import { t } from './i18n.js';

const grid = $('#cards');
// The page's own order, before any change.
const DEFAULT = [...grid.children].map((c) => c.id);
const HALF = new Set(['weight-card', 'movement-card', 'foods-card', 'top-activities-card']);

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
}

const titleOf = (c) => (c.querySelector('h2')?.textContent || c.id).trim();

let win = null;
let saving = Promise.resolve();

export function openLayout(returnFocus) {
  win?.close(true);
  const close = h('button', { class: 'sheet-close', type: 'button', 'aria-label': t('Close') }, '×');
  const head = h('div', { class: 'sheet-head wo-head' });
  const titles = h('div', { class: 'wo-titles' });
  titles.append(h('h2', { id: 'lo-title' }, t('Customize dashboard')),
    h('p', { class: 'muted' }, t('The order of the cards and which ones you see, on all your devices.')));
  head.append(titles, close);
  const list = h('ol', { class: 'lo-list' });
  const status = h('p', { class: 'muted lo-status', role: 'status' });
  const reset = h('button', { class: 'pill-btn', type: 'button' }, t('Reset'));
  const foot = h('div', { class: 'lo-foot' });
  foot.append(reset, status);
  const body = h('div', { class: 'wo-body' });
  body.append(list, foot);
  const panel = h('div', { class: 'wo-panel lo-panel' });
  panel.append(head, body);
  win = openDialog({ cls: 'wo-overlay', labelledBy: 'lo-title', content: [panel], returnFocus });
  close.onclick = () => win.close();

  const save = (next, focusId, dir) => {
    if (!account.user) return;
    account.user.layout = next;
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
