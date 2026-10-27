// The weekly review as a coach talking to you: an avatar and speech bubbles
// (the opening line, then one bubble per topic). Collapsed it shows only the
// opening line as a teaser; a click opens the rest.

import { LOCALE, dfShort, parse, addDays } from './format.js';
import { $, h } from './dom.js';
import { state, reduceMotion } from './state.js';
import { parseReview } from './reviewtext.js';
import { LANG, t } from './i18n.js';

let shown = 0; // index into data.reviews (newest first)
let lastFirst = null;
let open = false;

const dfFull = new Intl.DateTimeFormat(LOCALE, { month: 'short', day: 'numeric', year: 'numeric', timeZone: 'UTC' });

// messages groups the blocks into bubbles: everything before the first
// heading, then each heading with what follows it.
export function messages(blocks) {
  const out = [];
  for (const b of blocks) {
    if (b.type === 'h' || !out.length) out.push([]);
    out.at(-1).push(b);
  }
  return out;
}

export function renderReview(animate = false) {
  const card = $('#review-card');
  const reviews = state.data.reviews || [];
  card.hidden = !reviews.length;
  if (card.hidden) return;
  // A new review arrived: show it, as a teaser again.
  if (reviews[0].week !== lastFirst) { shown = 0; lastFirst = reviews[0].week; open = false; }
  shown = Math.min(shown, reviews.length - 1);
  const r = reviews[shown];
  const monday = parse(r.week);
  $('#review-week').textContent = t('week of {from} – {to}', { from: dfShort.format(monday), to: dfFull.format(addDays(monday, 6)) });

  const prev = $('#review-prev'), next = $('#review-next');
  prev.disabled = shown >= reviews.length - 1;
  next.disabled = shown === 0;
  prev.onclick = () => { shown++; renderReview(); };
  next.onclick = () => { shown--; renderReview(); };

  // German when the page is German and the review was written in both.
  const msgs = messages(parseReview(LANG === 'de' && r.text_de ? r.text_de : r.text));
  const list = open ? msgs : msgs.slice(0, 1);
  const body = $('#review-body');
  body.replaceChildren(...list.map((m, i) => {
    const bubble = h('div', { class: 'coach-bubble' + (!open && i === 0 ? ' teaser' : '') });
    bubble.append(...m.map(block));
    // When opening, the new bubbles come in one after the other.
    if (animate && i > 0 && !reduceMotion.matches) {
      bubble.classList.add('enter');
      bubble.style.setProperty('--d', `${(i - 1) * 0.07}s`);
    }
    if (!open && i === 0) bubble.onclick = toggle;
    return bubble;
  }));
  const more = $('#review-more');
  more.hidden = msgs.length < 2;
  more.textContent = open ? t('Show less') : t('Read the whole review ›');
  more.setAttribute('aria-expanded', String(open));
  more.onclick = toggle;
  const note = $('#review-note');
  note.hidden = !open;
  note.textContent = t("Written by Claude ({model}) from this dashboard's data. An observation, not medical advice.", { model: r.model });
}

function toggle() {
  open = !open;
  renderReview(open);
  if (!open) $('#review-card').scrollIntoView({ block: 'nearest', behavior: reduceMotion.matches ? 'auto' : 'smooth' });
}

function parts(el, ps) {
  for (const p of ps) el.append(p.bold ? h('strong', {}, p.text) : document.createTextNode(p.text));
  return el;
}

function block(b) {
  if (b.type === 'h') return parts(h('h3'), b.parts);
  if (b.type === 'ul') {
    const ul = h('ul');
    for (const item of b.items) ul.append(parts(h('li'), item));
    return ul;
  }
  return parts(h('p'), b.parts);
}
