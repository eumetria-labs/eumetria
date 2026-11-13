// Chart legends as switches: each entry (li[data-s]) turns its series on
// and off, the same in every chart. What a user switched is remembered on
// the device, per chart; series in `off` start switched off. One series of
// a chart always stays on.

import { h } from './dom.js';
import { store } from './state.js';

// legendToggles(ul, name, redraw, off) → on(key): whether a series shows.
// redraw runs after a switch; the chart then asks on() again.
export function legendToggles(ul, name, redraw, off = []) {
  const saved = new Map();
  for (const part of (store.get('legend-' + name) || '').split(',')) {
    const [k, v] = part.split(':');
    if (k) saved.set(k, v === '1');
  }
  const on = (k) => (saved.has(k) ? saved.get(k) : !off.includes(k));
  for (const li of ul.querySelectorAll(':scope > li[data-s]')) {
    const k = li.dataset.s;
    let b = li.querySelector(':scope > .leg-toggle');
    if (!b) {
      b = h('button', { type: 'button', class: 'leg-toggle' });
      b.append(...li.childNodes);
      li.append(b);
    }
    b.setAttribute('aria-pressed', String(on(k)));
    b.onclick = () => {
      const shown = [...ul.querySelectorAll(':scope > li[data-s]:not([hidden])')].filter((x) => on(x.dataset.s));
      if (on(k) && shown.length <= 1) return;
      saved.set(k, !on(k));
      store.set('legend-' + name, [...saved].map(([key, v]) => `${key}:${v ? 1 : 0}`).join(','));
      redraw();
    };
  }
  return on;
}
