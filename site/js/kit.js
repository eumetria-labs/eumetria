// Small building blocks shared by the Profile / Settings view and the
// windows: line icons, a password field that can show what was typed, and a
// status badge.

import { h, svg } from './dom.js';
import { t } from './i18n.js';

// Line icons on a 24 × 24 grid, drawn with a 2 px round stroke like the
// other icons of the page.
const PATHS = {
  user: '<circle cx="12" cy="8" r="4"/><path d="M4 20c1.5-4 4.5-6 8-6s6.5 2 8 6"/>',
  layout: '<rect x="3" y="3" width="7" height="9" rx="2"/><rect x="14" y="3" width="7" height="5" rx="2"/><rect x="14" y="12" width="7" height="9" rx="2"/><rect x="3" y="16" width="7" height="5" rx="2"/>',
  flag: '<path d="M5 21V4M5 4h11l-2 4 2 4H5"/>',
  moon: '<path d="M20 14.5A8 8 0 1 1 9.5 4a6.5 6.5 0 0 0 10.5 10.5z"/>',
  lock: '<rect x="5" y="11" width="14" height="10" rx="2"/><path d="M8 11V8a4 4 0 0 1 8 0v3"/>',
  key: '<circle cx="8" cy="15" r="4"/><path d="M11 12l9-9M16 7l3 3"/>',
  users: '<circle cx="9" cy="8" r="3.5"/><path d="M2.5 20c1-3.5 3.5-5 6.5-5s5.5 1.5 6.5 5M16 4.5a3.5 3.5 0 0 1 0 7M18 15c2 .7 3.2 2.3 3.5 5"/>',
  shield: '<path d="M12 3l8 3v6c0 4.5-3.5 8-8 9-4.5-1-8-4.5-8-9V6z"/><path d="M9 12l2 2 4-4"/>',
  monitor: '<rect x="3" y="4" width="18" height="12" rx="2"/><path d="M8 20h8M12 16v4"/>',
  note: '<rect x="5" y="3" width="14" height="18" rx="2"/><path d="M9 8h6M9 12h6M9 16h4"/>',
  list: '<path d="M9 6h11M9 12h11M9 18h11M4.5 6h.01M4.5 12h.01M4.5 18h.01"/>',
  food: '<path d="M4 11h16a8 8 0 0 1-16 0zM9 4c0 2 2 2 2 4M14 4c0 2 2 2 2 4"/>',
  pulse: '<path d="M3 12h4l2-5 4 10 2-5h6"/>',
  sync: '<path d="M20 12a8 8 0 1 1-2.34-5.66M20 4v5h-5"/>',
  sliders: '<path d="M4 6h9M17 6h3M4 12h3M11 12h9M4 18h11M19 18h1"/><circle cx="15" cy="6" r="2"/><circle cx="9" cy="12" r="2"/><circle cx="17" cy="18" r="2"/>',
  spark: '<path d="M12 3l1.8 4.7L18.5 9.5l-4.7 1.8L12 16l-1.8-4.7L5.5 9.5l4.7-1.8z"/><path d="M18 16l.8 2 2 .8-2 .8-.8 2-.8-2-2-.8 2-.8z"/>',
  mail: '<rect x="3" y="5" width="18" height="14" rx="2"/><path d="M3 7l9 6 9-6"/>',
  archive: '<rect x="3" y="4" width="18" height="5" rx="1.5"/><path d="M5 9v9a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V9M10 13h4"/>',
  back: '<path d="M15 6l-6 6 6 6"/>',
  chev: '<path d="M9 6l6 6-6 6"/>',
  eye: '<path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12z"/><circle cx="12" cy="12" r="3"/>',
  eyeOff: '<path d="M3 3l18 18M10.6 5.1A10.4 10.4 0 0 1 12 5c6.5 0 10 7 10 7a17.6 17.6 0 0 1-3.2 4.1M6.6 6.6C3.7 8.5 2 12 2 12s3.5 7 10 7a9.7 9.7 0 0 0 5.4-1.6"/><path d="M9.9 9.9a3 3 0 0 0 4.2 4.2"/>',
  phone: '<rect x="7" y="2" width="10" height="20" rx="2.5"/><path d="M11 18h2"/>',
};

// icon(name, size) is the icon as an <svg> element, hidden from screen readers.
export function icon(name, size = 18) {
  const el = svg('svg', { viewBox: '0 0 24 24', width: size, height: size, fill: 'none', stroke: 'currentColor', 'stroke-width': 2,
    'stroke-linecap': 'round', 'stroke-linejoin': 'round', 'aria-hidden': 'true' });
  el.innerHTML = PATHS[name] || '';
  return el;
}

// revealable wraps a password field with an eye button at its right end that
// shows or hides what was typed. Focus and the selection stay; the field is
// masked again when its form is submitted. A saved secret never comes back
// from the server, so there is only ever something typed to show.
export function revealable(input) {
  const wrap = h('span', { class: 'secret' });
  const eye = h('button', { class: 'eye', type: 'button', 'aria-pressed': 'false' });
  const set = (shown) => {
    input.type = shown ? 'text' : 'password';
    eye.setAttribute('aria-pressed', String(shown));
    eye.setAttribute('aria-label', shown ? t('Hide password') : t('Show password'));
    eye.title = eye.getAttribute('aria-label');
    eye.replaceChildren(icon(shown ? 'eyeOff' : 'eye'));
  };
  eye.onclick = () => {
    const { selectionStart: a, selectionEnd: b } = input;
    set(input.type === 'password');
    input.focus();
    try { input.setSelectionRange(a, b); } catch { /* not a text field */ }
  };
  // Whoever disables the field disables its eye too.
  new MutationObserver(() => { eye.disabled = input.disabled; }).observe(input, { attributes: true, attributeFilter: ['disabled'] });
  eye.disabled = input.disabled;
  wrap.append(input, eye);
  wrap._mask = () => set(false);
  set(false);
  return wrap;
}

// revealInPlace puts the eye on a password field that is already in the page.
export function revealInPlace(input) {
  const anchor = document.createComment('');
  input.replaceWith(anchor);
  anchor.replaceWith(revealable(input));
}

// Submitting a form masks its shown passwords again: nothing stays readable
// on screen after it was used.
document.addEventListener('submit', (e) => {
  for (const w of e.target.querySelectorAll?.('.secret') || []) w._mask?.();
}, true);

// badge is a small status pill: kind is ok, warn, bad or off.
export function badge(kind, text) {
  const el = h('span', { class: 'badge ' + kind });
  el.append(h('i', { class: 'dot', 'aria-hidden': 'true' }), text);
  return el;
}
