// "Save as image" and "Share as image" on every card with charts. Saving
// draws the card's title, legend and charts as one SVG in the current theme
// and saves it as a PNG at twice the size. Styles are copied from the page,
// since an SVG drawn as an image sees no stylesheet; the font is embedded for
// the same reason (share.js). Sharing opens the share window (post or story,
// light or dark) with the charts drawn again at the card's width; a card
// with a share card of its own (data-share="custom", cards.js) adds that
// instead.

import { dfShort, parse } from './format.js';
import { download, h, svg, textWidth } from './dom.js';
import { state } from './state.js';
import { rangeDays } from './query.js';
import { t } from './i18n.js';
import { PAD as SHARE_PAD, W as SHARE_W, chart as chartBlock, fontFace, openShare, shareButton, textW, toPng } from './share.js';

const PROPS = ['fill', 'fill-opacity', 'stroke', 'stroke-width', 'stroke-dasharray', 'stroke-dashoffset', 'stroke-linecap', 'stroke-linejoin', 'stroke-opacity',
  'opacity', 'font-size', 'font-weight', 'font-family', 'font-variant-numeric', 'letter-spacing', 'text-transform', 'paint-order', 'stop-color', 'stop-opacity'];
const PAD = 24;

// The charts of the card's open tab, if it has tabs.
const shownCharts = (card) => [...card.querySelectorAll('.chart')].filter((c) => !c.closest('[hidden]'));

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
    const share = card.dataset.share === 'custom' ? null
      : shareButton((btn) => openShare([{ key: 'card', label: '', name: `${card.id.replace(/-card$/, '')}-${state.today || ''}`, build: () => shareCard(card) }], 'card', btn), 'snap-btn');
    if (share) b.after(share);
    // A tab without charts has nothing to save or share.
    const sync = () => {
      b.hidden = !shownCharts(card).length;
      if (share) share.hidden = b.hidden;
    };
    card.addEventListener('panelchange', sync);
    sync();
  }
}

/* ---------- sharing ---------- */

const CW = SHARE_W - SHARE_PAD * 2;

// The range the page shows, for the footer and the share card.
function rangeText() {
  const days = state.data.days;
  const { start, end } = rangeDays(state.range, parse(days[0].date), parse(days.at(-1).date), state.from, state.to);
  return `${dfShort.format(start)} – ${dfShort.format(end)} ${end.getUTCFullYear()}`;
}

// A chart drawn again at width w (and height h, if given) and copied with
// its classes, so the share card's own styles (light or dark) colour it;
// then drawn back.
function chartAt(host, w, h) {
  if (!host._draw) return host.querySelector('svg')?.cloneNode(true);
  const old = [host.style.width, host.style.height];
  host.style.width = `${w}px`;
  if (h) host.style.height = `${h}px`;
  host._draw();
  const copy = host.querySelector('svg')?.cloneNode(true);
  [host.style.width, host.style.height] = old;
  host._draw();
  return copy;
}

// A heading over a chart.
const headingBlock = (text) => ({ h: 20, draw: (g, y) => g.append(svg('text', { x: SHARE_PAD, y: y + 14, class: 'c-head' }, text)) });

// The page's legend keys as share-card colours: a dashed or dotted key, a
// dot, or the colour class of a line or area.
const COLOURS = ['s1', 's2', 's3', 'over', 'ink', 'z1', 'z2', 'z3', 'z4', 'st-deep', 'st-light', 'st-rem', 'st-awake', 'st-restless'];
function legendItems(lis) {
  return lis.map((li) => {
    const key = li.querySelector('.key, .rp-key');
    const cls = key ? [...key.classList] : [];
    const kind = cls.includes('dash') || cls.includes('dotted') ? 'dash' : cls.includes('dot') ? 'dot' : COLOURS.find((c) => cls.includes(c)) || 'ink2';
    return [kind, li.textContent.trim()];
  });
}

// A legend that wraps onto more lines.
function legendBlock(items) {
  const rows = [];
  let x = 0;
  for (const [kind, name] of items) {
    const w = 17 + textW(name, 12.5, 400) + 16;
    if (!rows.length || (x > 0 && x + w > CW)) { rows.push([]); x = 0; }
    rows.at(-1).push([kind, name, x]);
    x += w;
  }
  return {
    h: rows.length * 18,
    draw(g, y) {
      rows.forEach((row, k) => {
        const ry = y + k * 18;
        for (const [kind, name, dx] of row) {
          const lx = SHARE_PAD + dx;
          if (kind === 'dash') g.append(svg('line', { x1: lx, x2: lx + 12, y1: ry + 8, y2: ry + 8, class: 'goal-line' }));
          else if (kind === 'dot') g.append(svg('circle', { cx: lx + 5, cy: ry + 8, r: 4, class: 'mark-dot s1-soft' }));
          else g.append(svg('rect', { x: lx, y: ry + 6.5, width: 12, height: 3, rx: 1.5, class: 'c-fill ' + kind }));
          g.append(svg('text', { x: lx + 17, y: ry + 12, class: 'c-note' }, name));
        }
      });
    },
  };
}

// The share card of a card: its title (and open tab), the range, and each
// chart shown with its heading and legend; charts after the first give way
// when the format has no room for them.
function shareCard(card) {
  const name = card.querySelector('.card-head h2').textContent.trim();
  const tab = card.querySelector(':scope > .tabs [aria-selected="true"] .tab-label')?.textContent.trim();
  const blocks = [];
  const hosts = shownCharts(card);
  const headLegend = [...card.querySelectorAll('.card-head .legend li')].filter((li) => !li.hidden);
  if (headLegend.length) blocks.push(legendBlock(legendItems(headLegend)));
  hosts.forEach((host, i) => {
    // A card's only chart gets the room of the picture.
    const copy = chartAt(host, CW, hosts.length === 1 ? 440 : 0);
    if (!copy) return;
    const part = host.parentElement !== card ? host.parentElement : null;
    const lis = part ? [...part.querySelectorAll('.legend li')].filter((li) => !li.hidden && !li.closest('.card-head')) : [];
    const title = part?.querySelector('.mini-title')?.childNodes[0]?.textContent.trim();
    if (title) blocks.push(headingBlock(title));
    if (lis.length) blocks.push(legendBlock(legendItems(lis)));
    const vb = copy.viewBox.baseVal;
    const ch = vb.height || host.clientHeight || 200;
    const block = chartBlock((g, box) => {
      copy.setAttribute('width', box.w);
      copy.setAttribute('height', box.h);
      copy.removeAttribute('tabindex');
      g.append(copy);
    }, { h: ch, min: Math.round(ch * 0.7), max: ch });
    if (i > 0) block.optional = true;
    blocks.push(block);
  });
  return { title: tab && tab !== name ? `${name} · ${tab}` : name, sub: rangeText(), accent: 's1', blocks };
}

/* ---------- saving ---------- */

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
  y += 14;
  text(`Eumetria · ${rangeText()}`, 11, 500, css.muted);
  const H = y + PAD - 6;

  const out = svg('svg', { xmlns: 'http://www.w3.org/2000/svg', viewBox: `0 0 ${W} ${H}`, width: W * 2, height: H * 2, style: `font-family:${root.getPropertyValue('--font')}` });
  const style = svg('style');
  style.textContent = await fontFace();
  out.append(style, svg('rect', { width: W, height: H, rx: 18, fill: css.surface }), body);

  const blob = await toPng(out, W, H);
  download(`eumetria-${card.id.replace(/-card$/, '')}-${state.today || ''}.png`, 'image/png', blob);
}
