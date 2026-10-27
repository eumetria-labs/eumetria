// Share cards: a meal, a recipe, a day, a workout, a night or the weight
// chart as a picture for the phone's share sheet (Instagram, WhatsApp,
// Signal, …). No link and no upload: the picture is drawn here, from the
// data the page already has.
//
// A card is drawn as one SVG at half its size (540 px wide) and turned into
// a PNG at twice that, so it can reuse the page's chart code and CSS sizes.
// An SVG drawn as an image sees no stylesheet and loads nothing, so it
// carries the page's rules, the chosen style's colours, the font and every
// photo itself. The cards themselves are built in cards.js.

import { fitBlocks } from './format.js';
import { download, h, svg } from './dom.js';
import { store } from './state.js';
import { openDialog } from './dialog.js';
import { t } from './i18n.js';

export const W = 540;
export const PAD = 28;
// Post 4:5 and story 9:16. Instagram covers the top and bottom of a story
// with its own bars, so a story keeps its content clear of them.
const FORMATS = { post: { h: 675, safe: PAD }, story: { h: 960, safe: 104 } };
const HEAD = 66, FOOT = 22, GAP = 14;

/* ---------- layout ---------- */

// fitBlocks (format.js) shares the height among a card's blocks.

// A text cut to maxW px with an ellipsis, measured in the card's font.
let measure = null;
export function fitText(text, px, weight, maxW) {
  measure ??= document.createElement('canvas').getContext('2d');
  measure.font = `${weight} ${px}px Inter, system-ui, sans-serif`;
  if (measure.measureText(text).width <= maxW) return text;
  let lo = 0, hi = text.length;
  while (lo < hi) {
    const mid = Math.ceil((lo + hi) / 2);
    if (measure.measureText(text.slice(0, mid).trimEnd() + '…').width <= maxW) lo = mid;
    else hi = mid - 1;
  }
  return text.slice(0, lo).trimEnd() + '…';
}

export function textW(text, px, weight) {
  measure ??= document.createElement('canvas').getContext('2d');
  measure.font = `${weight} ${px}px Inter, system-ui, sans-serif`;
  return measure.measureText(text).width;
}

/* ---------- blocks ---------- */

// The card's own look, on top of the page's rules. Colours are the page's
// tokens, so both styles follow from data-theme on the card.
const CARD_CSS = `
svg { font-family: var(--font); font-variant-numeric: tabular-nums; }
.c-bg { fill: var(--surface); }
.c-emoji { font-family: "Apple Color Emoji", "Segoe UI Emoji", "Noto Color Emoji", sans-serif; }
.c-title { font-size: 24px; font-weight: 700; letter-spacing: -0.02em; fill: var(--ink); }
.c-sub { font-size: 13px; fill: var(--muted); }
.c-tile { fill: var(--surface-2); }
.c-label { font-size: 11px; font-weight: 500; fill: var(--ink-2); }
.c-big { font-size: 21px; font-weight: 700; letter-spacing: -0.02em; fill: var(--ink); }
.c-unit { font-size: 12px; font-weight: 500; fill: var(--muted); }
.c-head { font-size: 13px; font-weight: 650; fill: var(--ink); }
.c-name { font-size: 14px; fill: var(--ink); }
.c-note { font-size: 12.5px; fill: var(--muted); }
.c-val { font-size: 14px; font-weight: 600; fill: var(--ink); }
.c-more { font-size: 12.5px; font-weight: 500; fill: var(--muted); }
.c-rule { stroke: var(--grid); stroke-width: 1; }
.c-track { fill: var(--grid); }
.c-brand { font-size: 12px; font-weight: 650; fill: var(--ink-2); }
.c-photo-edge { fill: none; stroke: var(--border); stroke-width: 1; }
.c-route { fill: none; stroke-width: 3.5; stroke-linecap: round; stroke-linejoin: round; stroke: var(--s1); }
.c-route-end { stroke: var(--surface); stroke-width: 2; }
.c-fill.s1, .c-route.z0 { fill: var(--s1); } .c-fill.s2 { fill: var(--s2); } .c-fill.s3 { fill: var(--s3); }
.c-fill.over { fill: var(--over); } .c-fill.ink { fill: var(--ink); } .c-fill.ink2 { fill: var(--ink-2); }
.c-fill.z1 { fill: var(--z1); } .c-fill.z2 { fill: var(--z2); } .c-fill.z3 { fill: var(--z3); } .c-fill.z4 { fill: var(--z4); }
.c-route.z1 { stroke: var(--z1); } .c-route.z2 { stroke: var(--z2); } .c-route.z3 { stroke: var(--z3); } .c-route.z4 { stroke: var(--z4); }
.c-fill.st-deep { fill: var(--st-deep); } .c-fill.st-light { fill: var(--st-light); } .c-fill.st-rem { fill: var(--st-rem); }
.c-fill.st-awake { fill: var(--st-awake); } .c-fill.st-restless { fill: var(--st-restless); }
.c-glow.s1 { stop-color: var(--s1); } .c-glow.s2 { stop-color: var(--s2); } .c-glow.s3 { stop-color: var(--s3); }
.c-glow.z3 { stop-color: var(--z3); } .c-glow.st-rem { stop-color: var(--st-rem); }
`;

const X = PAD, CW = W - PAD * 2;

const label = (x, y, text, cls, attrs = {}) => svg('text', { x, y, class: cls, ...attrs }, text);

// A number with its unit: "2.359 kcal".
function valueText(x, y, value, unit, cls = 'c-big', unitCls = 'c-unit') {
  const el = svg('text', { x, y, class: cls });
  el.append(value);
  if (unit && value !== '–') el.append(svg('tspan', { class: unitCls, dx: 3 }, unit));
  return el;
}

// Key numbers as tiles, up to four in a row: [[label, value, unit]].
export function tiles(items) {
  items = items.filter(Boolean);
  if (!items.length) return null;
  const th = 58, gap = 8;
  const width = (cols) => (CW - gap * (cols - 1)) / cols;
  // As many in a row as there are (four at most, three for five or six),
  // fewer when a value would not fit ("8 h 11 min").
  const need = Math.max(...items.map(([, value, unit]) => textW(value, 21, 700) + (unit && value !== '–' ? textW(unit, 12, 500) + 3 : 0))) + 24;
  let cols = items.length === 4 || items.length > 6 ? 4 : Math.min(3, items.length);
  while (cols > 2 && width(cols) < need) cols = cols === 4 && items.length === 4 ? 2 : cols - 1;
  const rows = Math.ceil(items.length / cols), tw = width(cols);
  return {
    h: rows * th + (rows - 1) * gap,
    draw(g, y) {
      items.forEach(([name, value, unit], i) => {
        const tx = X + (i % cols) * (tw + gap), ty = y + Math.floor(i / cols) * (th + gap);
        g.append(svg('rect', { x: tx, y: ty, width: tw, height: th, rx: 12, class: 'c-tile' }));
        g.append(label(tx + 12, ty + 20, fitText(name, 11, 500, tw - 24), 'c-label'));
        const unitW = unit && value !== '–' ? textW(unit, 12, 500) + 3 : 0;
        g.append(valueText(tx + 12, ty + 45, fitText(value, 21, 700, tw - 24 - unitW), unit));
      });
    },
  };
}

// A bar split into parts, with each part's name and value below:
// [{ cls, label, value, share }] with shares adding up to 1.
export function splitBar(parts, title) {
  parts = parts.filter((p) => p.share > 0);
  const top = title ? 20 : 0;
  return {
    h: top + 44,
    draw(g, y) {
      if (title) g.append(label(X, y + 12, title, 'c-head'));
      let px = X;
      const clip = 'split-' + Math.random().toString(36).slice(2, 8);
      const cp = svg('clipPath', { id: clip });
      cp.append(svg('rect', { x: X, y: y + top, width: CW, height: 10, rx: 5 }));
      const defs = svg('defs');
      defs.append(cp);
      const bar = svg('g', { 'clip-path': `url(#${clip})` });
      for (const p of parts) {
        const pw = p.share * CW;
        bar.append(svg('rect', { x: px, y: y + top, width: Math.max(0, pw - 2), height: 10, class: 'c-fill ' + p.cls }));
        px += pw;
      }
      g.append(defs, bar);
      // The names spread evenly under the bar.
      const cw = CW / parts.length;
      parts.forEach((p, i) => {
        const lx = X + i * cw;
        g.append(svg('circle', { cx: lx + 4, cy: y + top + 30, r: 4, class: 'c-fill ' + p.cls }));
        const el = svg('text', { x: lx + 13, y: y + top + 34, class: 'c-note' });
        el.append(svg('tspan', { class: 'c-val', 'font-size': 13 }, p.value + ' '), fitText(p.label, 12.5, 400, cw - 20 - textW(p.value + ' ', 13, 600)));
        g.append(el);
      });
    },
  };
}

// Progress toward a goal: "Eaten 2.359 of 2.593 kcal" over a bar.
export function meterBlock(name, value, goal, unit) {
  return {
    h: 40,
    draw(g, y) {
      const el = svg('text', { x: X, y: y + 14, class: 'c-head' });
      el.append(name + '  ', svg('tspan', { class: 'c-val' }, value), svg('tspan', { class: 'c-note' }, goal ? ' ' + t('of {goal}', { goal }) + ' ' + unit : ' ' + unit));
      g.append(el);
      const frac = goal ? Math.min(1, parseNum(value) / parseNum(goal)) : 0;
      g.append(svg('rect', { x: X, y: y + 26, width: CW, height: 10, rx: 5, class: 'c-track' }));
      if (frac > 0) g.append(svg('rect', { x: X, y: y + 26, width: Math.max(10, frac * CW), height: 10, rx: 5, class: 'c-fill ' + (parseNum(value) > parseNum(goal) * 1.05 ? 'over' : 's1') }));
    },
  };
}
// "2.359" back to 2359.
const parseNum = (s) => Number(String(s).replace(/\./g, '').replace(',', '.'));

// Rows of name, note and value, under an optional heading; cut to the rows
// that fit, the last one then saying how many more there are.
// rows: [{ icon, key (a colour class), name, note, value }]
export function list(rows, title, { rowH = 30, minRows = 3 } = {}) {
  const top = title ? 22 : 0;
  return {
    h: top + rows.length * rowH,
    min: top + Math.min(rows.length, minRows) * rowH,
    draw(g, y, height) {
      if (title) g.append(label(X, y + 12, title, 'c-head'));
      const fits = Math.max(1, Math.floor((height - top + 1) / rowH));
      const shown = rows.length > fits ? rows.slice(0, fits - 1) : rows;
      shown.forEach((r, i) => {
        const ry = y + top + i * rowH, base = ry + rowH / 2 + 5;
        if (i) g.append(svg('line', { x1: X, x2: X + CW, y1: ry, y2: ry, class: 'c-rule' }));
        let nx = X;
        if (r.icon) { g.append(label(nx, base, r.icon, 'c-emoji', { 'font-size': 15 })); nx += 24; }
        if (r.key) { g.append(svg('rect', { x: nx, y: base - 9, width: 10, height: 10, rx: 3, class: 'c-fill ' + r.key })); nx += 18; }
        const valW = r.value ? textW(r.value, 14, 600) + 12 : 0;
        const room = X + CW - valW - nx;
        const noteW = r.note ? Math.min(textW(r.note, 12.5, 400), room * 0.45) : 0;
        g.append(label(nx, base, fitText(r.name, 14, 400, room - noteW - (noteW ? 10 : 0)), 'c-name'));
        if (r.note) g.append(label(X + CW - valW, base, fitText(r.note, 12.5, 400, noteW), 'c-note', { 'text-anchor': 'end' }));
        if (r.value) g.append(label(X + CW, base, r.value, 'c-val', { 'text-anchor': 'end' }));
      });
      if (shown.length < rows.length) {
        const ry = y + top + shown.length * rowH;
        g.append(svg('line', { x1: X, x2: X + CW, y1: ry, y2: ry, class: 'c-rule' }));
        g.append(label(X, ry + rowH / 2 + 5, t('+ {n} more', { n: rows.length - shown.length }), 'c-more'));
      }
    },
  };
}

// A photo, cropped to fill its box. img is from photoData().
export function photo(img, { h: ph = 250, min = 130, max = 380 } = {}) {
  return {
    h: ph, min, max, optional: true,
    draw(g, y, height) {
      const id = 'ph-' + Math.random().toString(36).slice(2, 8);
      const cp = svg('clipPath', { id });
      cp.append(svg('rect', { x: X, y, width: CW, height, rx: 16 }));
      const defs = svg('defs');
      defs.append(cp);
      g.append(defs, svg('image', { href: img.url, x: X, y, width: CW, height, preserveAspectRatio: 'xMidYMid slice', 'clip-path': `url(#${id})` }));
      g.append(svg('rect', { x: X + 0.5, y: y + 0.5, width: CW - 1, height: height - 1, rx: 16, class: 'c-photo-edge' }));
    },
  };
}

// Up to four photos side by side.
export function photoStrip(imgs) {
  imgs = imgs.slice(0, 4);
  const gap = 8, pw = (CW - gap * (imgs.length - 1)) / imgs.length;
  return {
    h: Math.min(140, pw), min: 84, optional: true,
    draw(g, y, height) {
      imgs.forEach((img, i) => {
        const px = X + i * (pw + gap), id = 'ps-' + i + Math.random().toString(36).slice(2, 6);
        const cp = svg('clipPath', { id });
        cp.append(svg('rect', { x: px, y, width: pw, height, rx: 12 }));
        const defs = svg('defs');
        defs.append(cp);
        g.append(defs, svg('image', { href: img.url, x: px, y, width: pw, height, preserveAspectRatio: 'xMidYMid slice', 'clip-path': `url(#${id})` }));
      });
    },
  };
}

// A chart drawn by draw(g, box) into the content width at the height it gets.
export function chart(draw, { h: ch = 200, min = 130, max = 300, title, note } = {}) {
  const top = title ? 22 : 0;
  return {
    h: ch + top, min: min + top, max: max + top,
    draw(g, y, height) {
      if (title) g.append(label(X, y + 12, title, 'c-head'));
      if (note) g.append(label(X + CW, y + 12, note, 'c-note', { 'text-anchor': 'end' }));
      const inner = svg('g', { transform: `translate(${X},${y + top})` });
      draw(inner, { w: CW, h: height - top });
      g.append(inner);
    },
  };
}

// A row of legend keys: [[cls, label]] with cls a .c-fill colour or 'dash'.
export function legendRow(items) {
  return {
    h: 16,
    draw(g, y) {
      let lx = X;
      for (const [cls, name] of items) {
        if (cls === 'dash') g.append(svg('line', { x1: lx, x2: lx + 12, y1: y + 8, y2: y + 8, class: 'goal-line' }));
        else if (cls === 'dot') g.append(svg('circle', { cx: lx + 5, cy: y + 8, r: 4, class: 'mark-dot s1-soft' }));
        else g.append(svg('rect', { x: lx, y: y + 6.5, width: 12, height: 3, rx: 1.5, class: 'c-fill ' + cls }));
        g.append(label(lx + 17, y + 12, name, 'c-note'));
        lx += 17 + textW(name, 12.5, 400) + 16;
      }
    },
  };
}

/* ---------- drawing a card ---------- */

// The page's rules for the card: every plain rule of style.css (no @media,
// so no dark mode from the system and no print rules). The colour tokens
// come with them: :root is the light style, :root[data-theme="dark"] the
// dark one, and the card's <svg> is its own :root.
let pageCss = null;
function pageRules() {
  if (pageCss == null) {
    const sheet = [...document.styleSheets].find((s) => s.href?.endsWith('style.css'));
    try {
      pageCss = [...(sheet?.cssRules || [])].filter((r) => r instanceof CSSStyleRule).map((r) => r.cssText).join('\n');
    } catch {
      pageCss = '';
    }
  }
  return pageCss;
}

// The font as CSS for an SVG drawn as an image, loaded once.
let fontCss = null;
export async function fontFace() {
  if (fontCss == null) {
    try {
      const res = await fetch('fonts/inter-latin-var.woff2');
      if (!res.ok) throw new Error(res.status);
      const buf = new Uint8Array(await res.arrayBuffer());
      let bin = '';
      for (let i = 0; i < buf.length; i += 0x8000) bin += String.fromCharCode(...buf.subarray(i, i + 0x8000));
      fontCss = `@font-face{font-family:"Inter";src:url(data:font/woff2;base64,${btoa(bin)}) format("woff2");font-weight:100 900}`;
    } catch {
      fontCss = ''; // the system font then
    }
  }
  return fontCss;
}

// A photo as a data URL, at most max px on its long side so the card stays
// small. Null when it cannot be loaded.
const photos = new Map();
export function photoData(src, max = 1080) {
  if (!src) return Promise.resolve(null);
  if (!photos.has(src)) {
    photos.set(src, (async () => {
      // Through an <img>: the page's Content-Security-Policy lets images,
      // not fetch, load data: URLs (recipe photos may be one).
      const img = new Image();
      img.src = src;
      await img.decode();
      const w = img.naturalWidth || 1080, hgt = img.naturalHeight || 810;
      const k = Math.min(1, max / Math.max(w, hgt));
      const c = h('canvas', { width: Math.round(w * k), height: Math.round(hgt * k) });
      c.getContext('2d').drawImage(img, 0, 0, c.width, c.height);
      return { url: c.toDataURL('image/jpeg', 0.88), w: c.width, h: c.height };
    })().catch((err) => {
      console.error('loading a photo for sharing failed', src, err);
      photos.delete(src);
      return null;
    }));
  }
  return photos.get(src);
}

let markUrl = null;
async function mark() {
  if (markUrl == null) {
    try {
      const res = await fetch('icons/mark.svg');
      if (!res.ok) throw new Error(res.status);
      markUrl = 'data:image/svg+xml;charset=utf-8,' + encodeURIComponent(await res.text());
    } catch {
      markUrl = '';
    }
  }
  return markUrl;
}

// An SVG of w × h (drawn at twice that) as a PNG blob.
export async function toPng(out, w, hgt) {
  const img = new Image();
  img.src = 'data:image/svg+xml;charset=utf-8,' + encodeURIComponent(new XMLSerializer().serializeToString(out));
  await img.decode();
  // Safari may draw an SVG image before its embedded font is ready; a frame
  // later it has it.
  await new Promise((ok) => requestAnimationFrame(() => requestAnimationFrame(ok)));
  // Twice the size for sharp text; a canvas too large for the browser gives
  // no image, so then the plain size.
  for (const scale of [2, 1]) {
    const canvas = h('canvas', { width: w * scale, height: hgt * scale });
    canvas.getContext('2d').drawImage(img, 0, 0, w * scale, hgt * scale);
    const blob = await new Promise((ok) => canvas.toBlob(ok, 'image/png'));
    if (blob) return blob;
  }
  throw new Error('the browser could not draw the image');
}

// card: { icon, title, sub, accent (a colour class), blocks }.
export async function renderCard(card, { format = 'post', theme = 'light' } = {}) {
  const { h: H, safe } = FORMATS[format] || FORMATS.post;
  const out = svg('svg', { xmlns: 'http://www.w3.org/2000/svg', viewBox: `0 0 ${W} ${H}`, width: W * 2, height: H * 2, 'data-theme': theme });
  const style = svg('style');
  style.textContent = (await fontFace()) + pageRules() + CARD_CSS;
  out.append(style);

  // The background, with a soft glow of the card's colour in its corner.
  const defs = svg('defs');
  const glow = svg('radialGradient', { id: 'c-glow', cx: 1, cy: 0, r: 0.9 });
  glow.append(svg('stop', { offset: 0, class: 'c-glow ' + (card.accent || 's1'), 'stop-opacity': theme === 'dark' ? 0.28 : 0.2 }),
    svg('stop', { offset: 1, class: 'c-glow ' + (card.accent || 's1'), 'stop-opacity': 0 }));
  defs.append(glow);
  out.append(defs, svg('rect', { width: W, height: H, class: 'c-bg' }), svg('rect', { width: W, height: H * 0.6, fill: 'url(#c-glow)' }));

  // Header and blocks as one group, centred between the top and the footer.
  const footY = H - safe;
  const avail = footY - FOOT - GAP - safe - HEAD;
  const fitted = fitBlocks(card.blocks.filter(Boolean), avail, GAP);
  const used = HEAD + fitted.reduce((a, f) => a + f.h, 0) + GAP * Math.max(0, fitted.length - 1);
  // A post starts at the top; a story centres it, clear of Instagram's bars.
  let y = safe + (format === 'story' ? Math.max(0, (footY - FOOT - GAP - safe - used) / 2) : 0);

  const g = svg('g');
  const iconW = card.icon ? 38 : 0;
  if (card.icon) g.append(label(X, y + 28, card.icon, 'c-emoji', { 'font-size': 26 }));
  g.append(label(X + iconW, y + 28, fitText(card.title, 24, 700, CW - iconW), 'c-title'));
  if (card.sub) g.append(label(X, y + 52, fitText(card.sub, 13, 400, CW), 'c-sub'));
  y += HEAD;
  for (const f of fitted) {
    f.block.draw(g, y, f.h);
    y += f.h + GAP;
  }
  out.append(g);

  // The footer: where it comes from.
  const m = await mark();
  if (m) out.append(svg('image', { href: m, x: X, y: footY - 18, width: 18, height: 18 }));
  out.append(label(X + (m ? 25 : 0), footY - 4.5, 'Eumetria', 'c-brand'));
  if (card.source) out.append(label(X + CW, footY - 4.5, card.source, 'c-note', { 'text-anchor': 'end', 'font-size': 11 }));
  return toPng(out, W, H);
}

/* ---------- the share window ---------- */

// The share icon (a box with an arrow out of it) on a round button.
export function shareButton(onClick, cls = 'sheet-close') {
  const b = h('button', { class: cls + ' share-btn', type: 'button', 'aria-label': t('Share as image'), title: t('Share as image') });
  const icon = svg('svg', { viewBox: '0 0 24 24', width: 17, height: 17, 'aria-hidden': 'true' });
  icon.append(svg('path', { d: 'M12 3v12m0-12L7.5 7.5M12 3l4.5 4.5M7 11H6a2 2 0 0 0-2 2v6a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-6a2 2 0 0 0-2-2h-1', fill: 'none', stroke: 'currentColor', 'stroke-width': 2, 'stroke-linecap': 'round', 'stroke-linejoin': 'round' }));
  b.append(icon);
  b.addEventListener('click', () => onClick(b));
  return b;
}

const currentTheme = () => document.documentElement.dataset.theme || (matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light');

// A row of choices, one of them on; returns the row.
function choices(name, items, value, onPick) {
  const row = h('div', { class: 'seg share-seg', role: 'radiogroup', 'aria-label': name });
  for (const [key, text] of items) {
    const b = h('button', { type: 'button', role: 'radio', 'aria-checked': String(key === value) }, text);
    b.onclick = () => {
      row.querySelectorAll('button').forEach((x) => x.setAttribute('aria-checked', String(x === b)));
      onPick(key);
    };
    row.append(b);
  }
  return row;
}

let win = null;

// The share window: what to share (options: [{ key, label, name, route,
// build(opts) → card }]), post or story, light or dark, and for a workout
// with a route whether to show it. The picture is made before Share is
// tapped, so the tap still counts as the gesture the share sheet needs.
export function openShare(options, current, returnFocus) {
  closeShare(true);
  const prefs = { format: store.get('share-format') || 'post', theme: currentTheme(), route: false };
  let opt = options.find((o) => o.key === current) || options[0];
  let blob = null, run = 0;

  const close = h('button', { class: 'sheet-close', type: 'button', 'aria-label': t('Close') }, '×');
  const head = h('div', { class: 'sheet-head' });
  head.append(h('h2', { id: 'share-title' }, t('Share as image')), close);

  const controls = h('div', { class: 'share-controls' });
  if (options.length > 1) {
    const what = h('div', { class: 'share-what', role: 'radiogroup', 'aria-label': t('What to share') });
    for (const o of options) {
      const b = h('button', { type: 'button', role: 'radio', class: 'share-chip', 'aria-checked': String(o === opt) }, o.label);
      b.onclick = () => {
        what.querySelectorAll('button').forEach((x) => x.setAttribute('aria-checked', String(x === b)));
        opt = o;
        update();
      };
      what.append(b);
    }
    controls.append(what);
  }
  const row = h('div', { class: 'share-row' });
  row.append(
    choices(t('Format'), [['post', t('Post')], ['story', t('Story')]], prefs.format, (v) => { prefs.format = v; store.set('share-format', v); update(); }),
    choices(t('Style'), [['light', t('Light|style')], ['dark', t('Dark')]], prefs.theme, (v) => { prefs.theme = v; update(); }),
  );
  const routeBox = h('input', { type: 'checkbox' });
  const routeLabel = h('label', { class: 'share-check' });
  routeLabel.append(routeBox, ' ' + t('Show route'));
  routeBox.onchange = () => { prefs.route = routeBox.checked; update(); };
  row.append(routeLabel);
  controls.append(row);

  const img = h('img', { class: 'share-img', alt: '' });
  const preview = h('div', { class: 'share-preview' });
  preview.append(img);
  const status = h('p', { class: 'share-status muted', role: 'status' });

  const actions = h('div', { class: 'share-actions' });
  const canShareFiles = !!navigator.canShare?.({ files: [new File([''], 'x.png', { type: 'image/png' })] });
  const shareBtn = h('button', { type: 'button', class: 'pill-btn share-main' }, t('Share|button'));
  const saveBtn = h('button', { type: 'button', class: 'pill-btn' }, t('Save image'));
  const copyBtn = h('button', { type: 'button', class: 'pill-btn' }, t('Copy image'));
  if (canShareFiles) actions.append(shareBtn);
  actions.append(saveBtn);
  if (globalThis.ClipboardItem && navigator.clipboard?.write) actions.append(copyBtn);
  const filename = () => `eumetria-${opt.name}.png`;

  shareBtn.onclick = async () => {
    if (!blob) return;
    try {
      await navigator.share({ files: [new File([blob], filename(), { type: 'image/png' })] });
    } catch (err) {
      if (err.name !== 'AbortError') {
        console.error('sharing failed', err);
        status.textContent = t('Sharing did not work; save the image instead.');
      }
    }
  };
  saveBtn.onclick = () => { if (blob) download(filename(), 'image/png', blob); };
  copyBtn.onclick = async () => {
    if (!blob) return;
    try {
      await navigator.clipboard.write([new ClipboardItem({ 'image/png': blob })]);
      status.textContent = t('Image copied');
    } catch (err) {
      console.error('copying the image failed', err);
      status.textContent = t('Copying did not work; save the image instead.');
    }
  };

  async function update() {
    const mine = ++run;
    routeLabel.hidden = !opt.route;
    preview.dataset.format = prefs.format;
    preview.classList.add('busy');
    actions.querySelectorAll('button').forEach((b) => { b.disabled = true; });
    status.textContent = '';
    try {
      const card = await opt.build({ route: prefs.route });
      const png = await renderCard(card, prefs);
      if (mine !== run) return;
      blob = png;
      // A data URL: the page's Content-Security-Policy allows no blob: images.
      img.src = await dataUrl(png);
      if (mine !== run) return;
      img.alt = t('Preview: {title}', { title: card.title });
      actions.querySelectorAll('button').forEach((b) => { b.disabled = false; });
    } catch (err) {
      if (mine !== run) return;
      console.error('drawing the share image failed', err);
      blob = null;
      status.textContent = t('The image could not be drawn.');
    } finally {
      if (mine === run) preview.classList.remove('busy');
    }
  }

  const panel = h('div', { class: 'wo-panel share-panel' });
  const body = h('div', { class: 'wo-body share-body' });
  body.append(controls, preview, status, actions);
  panel.append(head, body);
  win = openDialog({
    cls: 'wo-overlay', labelledBy: 'share-title', content: [panel], returnFocus,
    onClose: () => { run++; },
  });
  close.onclick = () => closeShare();
  update();
  close.focus();
}

const dataUrl = (blob) => new Promise((ok, fail) => {
  const r = new FileReader();
  r.onload = () => ok(r.result);
  r.onerror = () => fail(r.error);
  r.readAsDataURL(blob);
});

export function closeShare(instant) {
  win?.close(instant);
  win = null;
}
