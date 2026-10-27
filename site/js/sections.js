// Section links in the sticky header: each jumps to its part of the page,
// and the part in view is marked. Sections without visible cards (no Fitbit:
// no Sleep, no Training) lose their link.

import { $ } from './dom.js';
import { reduceMotion } from './state.js';

const nav = $('#sections');
const links = [...nav.querySelectorAll('a')];
const cardsOf = (a) => a.dataset.cards.split(' ').map((id) => document.getElementById(id)).filter((el) => el && !el.hidden);

// After a click the clicked link stays marked until the user scrolls
// themselves: sections near the end can't scroll up to the header.
let pinned = null;

export function initSections() {
  nav.addEventListener('click', (e) => {
    const a = e.target.closest('a');
    // Ctrl/Cmd/middle click still opens the anchor in a new tab.
    if (!a || e.ctrlKey || e.metaKey || e.shiftKey || e.button !== 0) return;
    // The hash holds the view (range, open day, …), not the section.
    e.preventDefault();
    const target = cardsOf(a)[0];
    if (!target) return;
    if (target.tagName === 'DETAILS') target.open = true;
    // Targets stop just below the header as it will be once scrolled.
    document.documentElement.style.setProperty('--top-h', compactHeight() + 'px');
    target.scrollIntoView({ behavior: reduceMotion.matches ? 'auto' : 'smooth', block: 'start' });
    // Keyboard users continue from the section they jumped to.
    const focus = target.tagName === 'DETAILS' ? target.querySelector('summary') : target;
    if (focus !== target || target.hasAttribute('tabindex')) focus.focus({ preventScroll: true });
    else { target.tabIndex = -1; target.focus({ preventScroll: true }); }
    pinned = a;
    setCurrent(a);
  });
  const unpin = () => { pinned = null; };
  for (const type of ['wheel', 'touchstart', 'pointerdown']) addEventListener(type, unpin, { passive: true });
  addEventListener('keydown', (e) => { if (!nav.contains(e.target)) unpin(); });
  let queued = false;
  const schedule = () => {
    if (queued) return;
    queued = true;
    requestAnimationFrame(() => { queued = false; update(); });
  };
  addEventListener('scroll', schedule, { passive: true });
  addEventListener('resize', schedule);
}

// The header's height once it is stuck and compact (smaller title, no
// "Updated" line). Measured with transitions off, and switched back before
// they are on again, so nothing animates.
function compactHeight() {
  const top = $('#top');
  if (top.classList.contains('stuck')) return top.offsetHeight;
  top.classList.add('stuck', 'measuring');
  const h = top.offsetHeight;
  top.classList.remove('stuck');
  void top.offsetHeight;
  top.classList.remove('measuring');
  return h;
}

// After each render: show the links whose cards are on the page.
export function syncSections() {
  for (const a of links) a.hidden = !cardsOf(a).length;
  nav.hidden = false;
  update();
}

function update() {
  if (pinned) return;
  // Scrolling can come before the first render (or during one), when a link
  // is not hidden yet but its cards are: only links with a card count.
  const shown = links.map((a) => [a, cardsOf(a)[0]]).filter(([a, card]) => !a.hidden && card);
  if (!shown.length) return;
  // The section whose first card has passed just below the header; at the
  // end of the page, the last one in view.
  const line = $('#top').offsetHeight + 80;
  const tops = shown.map(([, card]) => card.getBoundingClientRect().top);
  let k = tops.findLastIndex((t) => t <= line);
  if (scrollY > 0 && innerHeight + scrollY >= document.documentElement.scrollHeight - 2) k = tops.findLastIndex((t) => t < innerHeight);
  setCurrent(shown[Math.max(0, k)][0]);
}

function setCurrent(cur) {
  if (cur.getAttribute('aria-current') === 'true') return;
  for (const a of links) {
    if (a === cur) a.setAttribute('aria-current', 'true');
    else a.removeAttribute('aria-current');
  }
  // On phones the row scrolls sideways; keep the marked link in it.
  if (nav.scrollWidth > nav.clientWidth) {
    nav.scrollTo({ left: cur.offsetLeft - (nav.clientWidth - cur.offsetWidth) / 2, behavior: reduceMotion.matches ? 'auto' : 'smooth' });
  }
}
