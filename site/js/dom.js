// DOM helpers shared by every part of the page.

import { fmt } from './format.js';
import { LANG, t } from './i18n.js';
import { state, reduceMotion } from './state.js';
import { NOVA, novaStep, nutriStep, offURL } from './quality.js';

const NS = 'http://www.w3.org/2000/svg';

export const $ = (sel) => document.querySelector(sel);

export function h(tag, attrs = {}, text) {
  const n = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) n.setAttribute(k, v);
  if (text != null) n.textContent = text;
  return n;
}

export function svg(tag, attrs = {}, text) {
  const n = document.createElementNS(NS, tag);
  for (const [k, v] of Object.entries(attrs)) n.setAttribute(k, v);
  if (text != null) n.textContent = text;
  return n;
}

// Decorative emoji: hidden from screen readers, which already get the text next to it.
export function emoji(char, cls = '') {
  return h('span', { class: ('emoji ' + cls).trim(), 'aria-hidden': 'true' }, char);
}

// A food or activity name that truncates with an ellipsis while the AI badge stays visible.
export function nameCell(name, ai, icon) {
  const cell = h('span', { class: 'name', title: name });
  if (icon) cell.append(emoji(icon));
  cell.append(h('span', { class: 'txt' }, name));
  if (ai) cell.append(h('span', { class: 'ai-badge', title: t('Logged with Yazio AI recognition') }, 'AI'));
  return cell;
}

// A tab with its emoji above the label, so a row of tabs always fits its
// width. With a thumb (a photo URL) the photo takes the emoji's place.
export function tabButton(key, label, icon, count, thumb) {
  const b = h('button', { role: 'tab', 'data-tab': key, type: 'button' });
  const line = h('span', { class: 'tab-label' });
  line.append(label);
  if (count) line.append(h('span', { class: 'tab-count' }, ` ${fmt(count)}`));
  b.append(thumb ? thumbImg(thumb, 'tab-thumb') : emoji(icon, 'tab-icon'), line);
  return b;
}

// Nutri-Score and NOVA badges for a food Open Food Facts knows; a link to
// its product page when the barcode is real. Null without a grade.
export function qualityBadges(ean, p) {
  if (!p || (!nutriStep(p.ns) && !p.nova)) return null;
  const url = offURL(ean);
  // German nouns keep their capital letter.
  const nova = p.nova ? NOVA[p.nova - 1][1] : '';
  const label = [nutriStep(p.ns) ? `Nutri-Score ${p.ns.toUpperCase()}` : '', p.nova ? `NOVA ${p.nova}: ${LANG === 'de' ? nova : nova.toLowerCase()}` : '']
    .filter(Boolean).join(', ') + ' (Open Food Facts)';
  const box = h(url ? 'a' : 'span', { class: 'q-badges', title: label, 'aria-label': label });
  if (url) {
    box.href = url;
    box.target = '_blank';
    box.rel = 'noopener noreferrer';
  }
  if (nutriStep(p.ns)) box.append(h('span', { class: `q-badge q${nutriStep(p.ns)}`, 'aria-hidden': 'true' }, p.ns.toUpperCase()));
  if (p.nova) box.append(h('span', { class: `q-badge nova q${novaStep(p.nova)}`, 'aria-hidden': 'true' }, String(p.nova)));
  return box;
}

// A small decorative photo; the text next to it already names the meal.
export function thumbImg(src, cls) {
  return h('img', { class: 'thumb ' + cls, src, alt: '', loading: 'lazy', decoding: 'async' });
}

// Arrow keys move between tabs, as in the WAI-ARIA tabs pattern.
export function tabKeys(e, list) {
  const btns = [...list.querySelectorAll('[role="tab"]')];
  const i = btns.indexOf(document.activeElement);
  const moves = { ArrowLeft: i - 1, ArrowRight: i + 1, Home: 0, End: btns.length - 1 };
  if (i < 0 || !(e.key in moves)) return;
  e.preventDefault();
  e.stopPropagation();
  const next = btns[(moves[e.key] + btns.length) % btns.length];
  next.focus();
  next.click();
}

export function hbar(name, value, note, frac, cls, ai, icon) {
  const row = h('div', { class: 'hbar' });
  row.append(nameCell(name, ai, icon));
  const v = h('span', { class: 'val' }, value);
  if (note) v.append(h('small', {}, note));
  row.append(v);
  const track = h('div', { class: 'track' });
  const fill = h('i', cls ? { class: cls } : {});
  growWidth(fill, Math.max(0, frac * 100));
  track.append(fill);
  row.append(track);
  return row;
}

export function meter(frac, over, cls = '') {
  const m = h('div', { class: 'meter' + (cls ? ' ' + cls : ''), role: 'presentation' });
  const fill = h('i', over ? { class: 'over' } : {});
  m.append(fill);
  growWidth(fill, Math.min(100, Math.max(0, frac * 100)));
  return m;
}

// Animates a bar's width from 0 on entrance; plain set otherwise.
export function growWidth(el, pct) {
  if (!state.animateNext || reduceMotion.matches) { el.style.width = pct + '%'; return; }
  el.style.width = '0';
  requestAnimationFrame(() => requestAnimationFrame(() => { el.style.width = pct + '%'; }));
}

// Counts a number up from the value last shown under the same key.
const shown = new Map();
export function countUp(el, key, to, format) {
  if (to == null || Number.isNaN(to)) { el.textContent = '–'; shown.delete(key); return; }
  const from = shown.get(key) ?? 0;
  shown.set(key, to);
  if (!state.animateNext || reduceMotion.matches || from === to) { el.textContent = format(to); return; }
  const t0 = performance.now(), dur = 650;
  const step = (t) => {
    const k = Math.min(1, (t - t0) / dur);
    el.textContent = format(from + (to - from) * (1 - (1 - k) ** 3));
    if (k < 1) requestAnimationFrame(step);
  };
  el.textContent = format(from);
  requestAnimationFrame(step);
}

// The width of a text in the page font, for chart margins that must fit
// their labels ("15.000", "Höchstleistung").
let measure = null;
export function textWidth(text, px) {
  measure ??= document.createElement('canvas').getContext('2d');
  measure.font = `${px}px ${getComputedStyle(document.body).fontFamily}`;
  return Math.ceil(measure.measureText(text).width);
}

/* ---------- tooltip ---------- */

const tt = $('#tt');

export function showTip(cx, cy, title, rows, hint) {
  // Inside an open window the tooltip has to live in its <dialog>: anything
  // outside the top layer is drawn below it. The last open one is on top.
  const host = [...document.querySelectorAll('dialog[open]')].at(-1) || document.body;
  if (tt.parentNode !== host) host.append(tt);
  tt.replaceChildren(h('div', { class: 't' }, title));
  for (const r of rows) {
    const row = h('div', { class: 'row' });
    if (r.cls) row.append(h('i', { class: 'k ' + r.cls }));
    row.append(h('b', {}, r.value));
    if (r.label) row.append(h('span', {}, r.label));
    tt.append(row);
  }
  if (hint && matchMedia('(hover: hover)').matches) tt.append(h('div', { class: 'hint' }, hint));
  tt.hidden = false;
  const w = tt.offsetWidth, ht = tt.offsetHeight;
  let left = cx + 16;
  if (left + w > innerWidth - 8) left = cx - w - 16;
  tt.style.left = Math.max(8, left) + 'px';
  tt.style.top = Math.max(8, Math.min(innerHeight - ht - 8, cy - ht / 2)) + 'px';
}

export function hideTip() { tt.hidden = true; }

// A closing window hands the tooltip back to the page before it is removed.
export function parkTip(dialog) {
  if (!dialog.contains(tt)) return;
  hideTip();
  document.body.append(tt);
}

// The URL hash holds shareable view state, e.g. "#range=30&day=2026-10-13".
export function hashParam(key) {
  return new URLSearchParams(location.hash.slice(1)).get(key);
}

export function setHashParam(key, value) {
  const p = new URLSearchParams(location.hash.slice(1));
  if (value == null) p.delete(key);
  else p.set(key, value);
  const s = p.toString();
  history.replaceState(null, '', s ? '#' + s : location.pathname + location.search);
}

// Saves bytes (text, a Uint8Array or a Blob) as a file named filename.
export function download(filename, type, bytes) {
  const blob = bytes instanceof Blob ? bytes : new Blob([bytes], { type });
  const a = h('a', { href: URL.createObjectURL(blob), download: filename });
  document.body.append(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(a.href), 1000);
}

// A button that opens a menu next to it: arrow keys move between the
// items, Escape, Tab and a click elsewhere close it. onPick gets the item
// chosen. The button and the menu share a positioned parent.
export function popupMenu(button, menu, onPick) {
  const items = () => [...menu.querySelectorAll('[role="menuitem"]')];
  const open = (focus) => {
    menu.hidden = false;
    // Opens to the left of the button, unless it sits at the left edge
    // (narrow phones), where it would leave the screen.
    menu.classList.remove('from-left');
    menu.classList.toggle('from-left', menu.getBoundingClientRect().left < 8);
    button.setAttribute('aria-expanded', 'true');
    if (focus) items()[0].focus();
  };
  const close = (refocus) => {
    if (menu.hidden) return;
    menu.hidden = true;
    button.setAttribute('aria-expanded', 'false');
    if (refocus) button.focus();
  };
  button.addEventListener('click', () => (menu.hidden ? open(true) : close()));
  menu.addEventListener('click', (e) => {
    const b = e.target.closest('[role="menuitem"]');
    if (!b) return;
    close(true);
    onPick(b);
  });
  menu.addEventListener('keydown', (e) => {
    const list = items();
    const i = list.indexOf(document.activeElement);
    const next = { ArrowDown: i + 1, ArrowUp: i - 1, Home: 0, End: list.length - 1 }[e.key];
    if (next != null) {
      e.preventDefault();
      list[(next + list.length) % list.length].focus();
    } else if (e.key === 'Escape') {
      e.preventDefault();
      close(true);
    } else if (e.key === 'Tab') close();
  });
  document.addEventListener('click', (e) => { if (!button.parentElement.contains(e.target)) close(); });
}
