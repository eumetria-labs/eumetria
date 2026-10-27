// Every window of the page (day sheet, workout, recipe, history, photo
// viewer) is a native <dialog> opened with showModal(): the browser puts it
// in the top layer above the windows opened before it, makes the rest of
// the page inert, keeps focus inside and turns Escape into a cancel event.
//
// The <dialog> itself covers the viewport and is the dimmed backdrop (the
// .sheet-overlay / .wo-overlay / .lightbox classes); its child is the panel,
// so a click on the dialog itself is a click outside the panel.

import { h, parkTip } from './dom.js';
import { reduceMotion } from './state.js';

// Long enough for the .closing fade-out animations in style.css.
const CLOSE_MS = 170;

// openDialog({ cls, labelledBy, label, id, content, returnFocus, onClose }) → { el, close(instant) }
// onClose runs once, when closing starts (e.g. to clear a hash parameter).
export function openDialog({ cls, labelledBy, label, id, content = [], returnFocus, onClose }) {
  const el = h('dialog', { class: 'dlg ' + cls });
  if (id) el.id = id;
  if (labelledBy) el.setAttribute('aria-labelledby', labelledBy);
  if (label) el.setAttribute('aria-label', label);
  el.append(...content);
  document.body.append(el);
  el.showModal();
  syncScrollLock();

  let open = true;
  const done = () => {
    parkTip(el);
    if (el.open) el.close();
    el.remove();
    syncScrollLock();
    returnFocus?.focus?.();
  };
  const close = (instant) => {
    if (!open) return;
    open = false;
    onClose?.();
    if (instant || reduceMotion.matches) return done();
    el.classList.add('closing');
    setTimeout(done, CLOSE_MS);
  };
  // Escape closes only this window, animated. Left to the browser, one Escape
  // closes every window opened without a click in between (e.g. a day and a
  // workout from one link) at once, so it is handled before it gets there.
  el.addEventListener('keydown', (e) => {
    if (e.key !== 'Escape') return;
    e.preventDefault();
    e.stopPropagation();
    close();
  });
  el.addEventListener('cancel', (e) => { e.preventDefault(); close(); });
  // The browser can still close it without us (it ignores a repeated cancel
  // preventDefault without user activation); clean up then.
  el.addEventListener('close', () => { if (open) { open = false; onClose?.(); done(); } });
  el.addEventListener('click', (e) => { if (e.target === el) close(); });
  return { el, close };
}

// The page behind an open window must not scroll along on phones.
function syncScrollLock() {
  document.body.classList.toggle('modal-open', !!document.querySelector('dialog[open]'));
}
