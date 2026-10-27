// "Save as image" on every card with charts: the card's title, legend and
// charts redrawn as one SVG in the current theme and saved as a PNG at twice
// the size. Styles are copied from the page, since an SVG drawn as an image
// sees no stylesheet; the font is embedded for the same reason (share.js).

import { dfShort, parse } from './format.js';
import { download, h, svg, textWidth } from './dom.js';
import { state } from './state.js';
import { rangeDays } from './query.js';
import { t } from './i18n.js';
import { fontFace, toPng } from './share.js';

const PROPS = ['fill', 'fill-opacity', 'stroke', 'stroke-width', 'stroke-dasharray', 'stroke-dashoffset', 'stroke-linecap', 'stroke-linejoin', 'stroke-opacity',
  'opacity', 'font-size', 'font-weight', 'font-family', 'font-variant-numeric', 'letter-spacing', 'text-transform', 'paint-order', 'stop-color', 'stop-opacity'];
const PAD = 24;

export function addSnapButtons() {
  for (const card of document.querySelectorAll('.card')) {
    const title = card.querySelector('.card-head h2');
    if (!title || !card.querySelector('.chart') || card.querySelector('.snap-btn')) continue;
    const b = h('button', { class: 'snap-btn', type: 'button', 'aria-label': t('Save as image'), title: t('Save as image') });
    const icon = svg('svg', { viewBox: '0 0 24 24', width: 15, height: 15, 'aria-hidden': 'true' });
    icon.append(svg('path', { d: 'M12 4v11m0 0-4.5-4.5M12 15l4.5-4.5M5 19h14', fill: 'none', stroke: 'currentColor', 'stroke-width': 2, 'stroke-linecap': 'round', 'stroke-linejoin': 'round' }));
    b.append(icon);
    b.onclick = async () => {
      b.disabled = true;
      try {
        await snap(card);
      } catch (err) {
        console.error('saving the chart as an image failed', err);
      } finally {
        b.disabled = false;
      }
    };
    title.parentElement.classList.add('has-snap');
    title.after(b);
  }
}

// A copy of a chart's SVG with every element's look written onto it.
function inlined(src) {
  const copy = src.cloneNode(true);
  const from = [src, ...src.querySelectorAll('*')], to = [copy, ...copy.querySelectorAll('*')];
  from.forEach((el, i) => {
    const cs = getComputedStyle(el);
    if (cs.visibility === 'hidden' || cs.display === 'none') { to[i].remove(); return; }
    to[i].setAttribute('style', PROPS.map((p) => `${p}:${cs.getPropertyValue(p)}`).join(';'));
    to[i].removeAttribute('class');
  });
  copy.removeAttribute('tabindex');
  return copy;
}

// A legend's entries as SVG at (x, y), wrapping at width; returns the height.
function legend(g, items, x, y, width, css) {
  let cx = x, cy = y;
  for (const li of items) {
    const key = li.querySelector('.key, .rp-key');
    const label = li.textContent.trim();
    const kw = key ? Math.max(8, key.offsetWidth) : 0;
    const w = kw + 6 + textWidth(label, 12) + 16;
    if (cx > x && cx + w > x + width) { cx = x; cy += 18; }
    if (key) {
      const ks = getComputedStyle(key);
      const dashed = ks.borderTopStyle === 'dashed' || ks.borderTopStyle === 'dotted';
      if (dashed) g.append(svg('line', { x1: cx, x2: cx + kw, y1: cy - 4, y2: cy - 4, stroke: ks.borderTopColor, 'stroke-width': 2, 'stroke-dasharray': ks.borderTopStyle === 'dotted' ? '1 3' : '4 3' }));
      else g.append(svg('rect', { x: cx, y: cy - 4 - Math.max(2, key.offsetHeight) / 2, width: kw, height: Math.max(2, key.offsetHeight), rx: Math.min(3, key.offsetHeight / 2), fill: ks.backgroundColor, opacity: ks.opacity }));
    }
    g.append(svg('text', { x: cx + kw + 6, y: cy, style: `font-size:12px;fill:${css.ink2}` }, label));
    cx += w;
  }
  return cy - y + 18;
}

async function snap(card) {
  const root = getComputedStyle(document.documentElement);
  const css = { surface: root.getPropertyValue('--surface'), ink: root.getPropertyValue('--ink'), ink2: root.getPropertyValue('--ink-2'), muted: root.getPropertyValue('--muted') };
  const charts = [...card.querySelectorAll('.chart')].filter((c) => c.offsetParent && c.querySelector('svg'));
  if (!charts.length) return;
  const W = Math.max(...charts.map((c) => c.querySelector('svg').viewBox.baseVal.width)) + PAD * 2;
  const body = svg('g');
  let y = PAD + 18;
  const text = (str, size, weight, fill, dy = 0) => {
    body.append(svg('text', { x: PAD, y: y + dy, style: `font-size:${size}px;font-weight:${weight};fill:${fill}` }, str));
  };
  text(card.querySelector('.card-head h2').textContent, 18, 700, css.ink);
  const sub = card.querySelector('.card-head p.muted')?.textContent;
  if (sub) { y += 20; text(sub, 12.5, 400, css.muted); }
  const headLegend = [...card.querySelectorAll('.card-head .legend li')].filter((li) => !li.hidden);
  if (headLegend.length) y += 10 + legend(body, headLegend, PAD, y + 22, W - PAD * 2, css);
  y += 16;
  for (const host of charts) {
    // A chart's own heading and legend, when its part of the card has them.
    const part = host.parentElement !== card ? host.parentElement : null;
    const mini = part?.querySelector('.mini-title');
    if (mini) { y += 14; text(mini.textContent.trim(), 13, 600, css.ink); y += 4; }
    const partLegend = part ? [...part.querySelectorAll('.legend li')].filter((li) => !li.hidden) : [];
    if (partLegend.length) y += legend(body, partLegend, PAD, y + 16, W - PAD * 2, css);
    const src = host.querySelector('svg');
    const vb = src.viewBox.baseVal;
    const copy = inlined(src);
    copy.setAttribute('x', PAD);
    copy.setAttribute('y', y);
    copy.setAttribute('width', vb.width);
    copy.setAttribute('height', vb.height);
    body.append(copy);
    y += vb.height + 8;
  }
  // Footer: where it comes from and the range shown.
  const days = state.data.days;
  const { start, end } = rangeDays(state.range, parse(days[0].date), parse(days.at(-1).date), state.from, state.to);
  y += 14;
  text(`Eumetria · ${dfShort.format(start)} – ${dfShort.format(end)} ${end.getUTCFullYear()}`, 11, 500, css.muted);
  const H = y + PAD - 6;

  const out = svg('svg', { xmlns: 'http://www.w3.org/2000/svg', viewBox: `0 0 ${W} ${H}`, width: W * 2, height: H * 2, style: `font-family:${root.getPropertyValue('--font')}` });
  const style = svg('style');
  style.textContent = await fontFace();
  out.append(style, svg('rect', { width: W, height: H, rx: 18, fill: css.surface }), body);

  const blob = await toPng(out, W, H);
  download(`eumetria-${card.id.replace(/-card$/, '')}-${state.today || ''}.png`, 'image/png', blob);
}
