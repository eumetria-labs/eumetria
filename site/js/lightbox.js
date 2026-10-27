// Full-screen photo viewer above the day sheet. Arrow keys, buttons or a
// swipe move between the day's photos; Esc, × or a click outside closes it.

import { h, emoji } from './dom.js';
import { openDialog } from './dialog.js';
import { t } from './i18n.js';

let win = null;

// photos: [{ src, caption, icon }], start: index of the photo to show first.
export function openLightbox(photos, start, returnFocus) {
  closeLightbox(true);
  let i = start;
  const many = photos.length > 1;

  const img = h('img', { alt: '' });
  const caption = h('figcaption');
  const figure = h('figure', { class: 'lb-figure' });
  figure.append(img, caption);

  const close = h('button', { class: 'lb-btn lb-close', type: 'button', 'aria-label': t('Close photo') }, '×');
  const prev = h('button', { class: 'lb-btn lb-prev', type: 'button', 'aria-label': t('Previous photo') }, '‹');
  const next = h('button', { class: 'lb-btn lb-next', type: 'button', 'aria-label': t('Next photo') }, '›');

  const content = [figure, close];
  if (many) content.push(prev, next);
  win = openDialog({ cls: 'lightbox', label: t('Photo'), content, returnFocus });
  const box = win.el;

  const show = (k) => {
    i = (k + photos.length) % photos.length;
    const p = photos[i];
    img.classList.remove('lb-in');
    img.src = p.src;
    img.alt = p.caption;
    void img.offsetWidth; // restart the zoom-in for each photo
    img.classList.add('lb-in');
    const text = h('span', { class: 'lb-text' });
    if (p.icon) text.append(emoji(p.icon));
    text.append(p.caption);
    if (many) text.append(h('span', { class: 'lb-count' }, ` · ${i + 1} / ${photos.length}`));
    const full = h('a', { href: p.src, target: '_blank', rel: 'noopener', class: 'lb-full' }, t('Open full size'));
    caption.replaceChildren(text, full);
  };

  close.onclick = () => closeLightbox();
  prev.onclick = () => show(i - 1);
  next.onclick = () => show(i + 1);
  // The space around the photo counts as outside, too.
  figure.addEventListener('click', (e) => { if (e.target === figure) closeLightbox(); });
  box.addEventListener('keydown', (e) => {
    if (many && e.key === 'ArrowLeft') { e.preventDefault(); show(i - 1); }
    else if (many && e.key === 'ArrowRight') { e.preventDefault(); show(i + 1); }
  });
  // Swipe left/right on touch screens.
  let x0 = null;
  box.addEventListener('pointerdown', (e) => { if (e.pointerType === 'touch') x0 = e.clientX; });
  box.addEventListener('pointerup', (e) => {
    if (x0 == null) return;
    const dx = e.clientX - x0;
    x0 = null;
    if (many && Math.abs(dx) > 50) show(dx < 0 ? i + 1 : i - 1);
  });

  show(i);
  close.focus();
}

export function closeLightbox(instant) {
  win?.close(instant);
  win = null;
}
