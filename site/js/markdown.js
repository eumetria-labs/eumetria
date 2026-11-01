// Workout notes are Markdown people write and other people read. Only this
// much is understood: headings, bold, italic, bullet and numbered lists, line
// breaks, inline code, code blocks, links to http(s) addresses and images
// uploaded to the server. Everything else stays the text it is, HTML
// included. The parser makes plain data and the renderer turns that into
// elements with textContent, so nothing in a note can become HTML, and no
// script and no image from elsewhere can load. No DOM access but through
// the document passed in, so Node can test this file.

// Bold inside italic inside a link is fine; a note nesting without end isn't.
const MAX_DEPTH = 6;

const WORD = /[\p{L}\p{N}]/u;
const isWord = (c) => c !== undefined && WORD.test(c);
const isSpace = (c) => c === undefined || /\s/.test(c);

// A link only goes to http(s); anything else ("javascript:", "data:", a
// relative address) stays text. Returns the address or null.
export function safeLink(href) {
  try {
    const url = new URL(href);
    return url.protocol === 'http:' || url.protocol === 'https:' ? url.href : null;
  } catch {
    return null;
  }
}

// An image is one the server keeps (internal/notes, images.go). Any other
// address could tell a stranger who read the note and when.
const IMAGE = /^note-images\/[0-9a-f]{32}\.jpg$/;

// Where the marker opened at i closes, or -1. A marker opens and closes
// only at the edge of a word, so "3*10 and 4*12" and snake_case stay as
// they are. In a run of markers the closing one is the last of it, so
// "***both***" has the bold around the italic, and a single mark doesn't
// close at a pair: that's a bold inside it.
function closing(s, i, mark) {
  for (let j = i + mark.length; j < s.length; j++) {
    if (s[j] === '\\') { j++; continue; }
    if (s[j] !== mark[0]) continue;
    let end = j;
    while (s[end + 1] === mark[0]) end++;
    const run = end - j + 1;
    const fits = mark.length === 2 ? run >= 2 : run % 2 === 1;
    if (fits && !isSpace(s[j - 1]) && !isWord(s[end + 1])) return end - mark.length + 1;
    j = end;
  }
  return -1;
}

// inline turns a line into parts: { text }, { code }, { bold: parts },
// { em: parts }, { link, parts } and { img, alt }.
export function inline(s, depth = 0) {
  const out = [];
  let text = '';
  const push = (part) => {
    if (text) out.push({ text });
    text = '';
    out.push(part);
  };
  // Marks known to have no end further on, so they aren't looked for again.
  const open = new Set();
  for (let i = 0; i < s.length;) {
    const c = s[i];
    if (c === '\\' && /[\\`*_[\]()#+.!-]/.test(s[i + 1] ?? '')) {
      text += s[i + 1];
      i += 2;
      continue;
    }
    if (c === '`') {
      const end = open.has(c) ? -1 : s.indexOf('`', i + 1);
      if (end > i + 1) {
        push({ code: s.slice(i + 1, end) });
        i = end + 1;
        continue;
      }
      if (end < 0) open.add(c);
    } else if (c === '!' && s[i + 1] === '[' && !open.has('[')) {
      const close = s.indexOf(']', i + 2);
      const stop = close > 0 && s[close + 1] === '(' ? s.indexOf(')', close + 2) : -1;
      if (stop > 0 && IMAGE.test(s.slice(close + 2, stop))) {
        push({ img: s.slice(close + 2, stop), alt: s.slice(i + 2, close) });
        i = stop + 1;
        continue;
      }
    } else if (c === '[' && depth < MAX_DEPTH && s[i - 1] !== '!') { // any other image stays text
      const close = open.has(c) ? -1 : s.indexOf(']', i + 1);
      if (close < 0) open.add(c);
      const stop = close > 0 && s[close + 1] === '(' ? s.indexOf(')', close + 2) : -1;
      const href = stop > 0 ? s.slice(close + 2, stop) : '';
      const link = href && !/[\s(]/.test(href) ? safeLink(href) : null;
      if (link && close > i + 1) {
        push({ link, parts: inline(s.slice(i + 1, close), depth + 1) });
        i = stop + 1;
        continue;
      }
    } else if ((c === '*' || c === '_') && depth < MAX_DEPTH) {
      const mark = s[i + 1] === c ? c + c : c;
      if (!isSpace(s[i + mark.length]) && !isWord(s[i - 1]) && !open.has(mark)) {
        const end = closing(s, i, mark);
        if (end < 0) open.add(mark);
        else if (end > i + mark.length) {
          const parts = inline(s.slice(i + mark.length, end), depth + 1);
          push(mark.length === 2 ? { bold: parts } : { em: parts });
          i = end + mark.length;
          continue;
        }
      }
    }
    text += c;
    i++;
  }
  if (text) out.push({ text });
  return out;
}

// parseMarkdown turns a note into blocks: { type: 'h', level, parts },
// { type: 'p', parts }, { type: 'pre', lines } and { type: 'ul' | 'ol',
// start, items: [parts] }. A list may have blank lines between its items; a
// line break inside a paragraph stays one ({ br: true }). A code block
// between ``` lines keeps its lines as they are and runs to the end of the
// note when it isn't closed.
export function parseMarkdown(text) {
  const blocks = [];
  let para = null, list = null, code = null, blank = false;
  for (const raw of (text || '').split(/\r?\n/)) {
    const line = raw.trim();
    if (code) {
      if (line === '```') code = null;
      else code.lines.push(raw);
      continue;
    }
    if (/^```[^`]*$/.test(line)) {
      para = list = null;
      blank = false;
      code = { type: 'pre', lines: [] };
      blocks.push(code);
      continue;
    }
    if (!line) {
      para = null;
      blank = true;
      continue;
    }
    const heading = line.match(/^(#{1,6})\s+(.*)$/);
    const bullet = line.match(/^[-*+•]\s+(.*)$/);
    const numbered = line.match(/^(\d{1,9})[.)]\s+(.*)$/);
    if (heading) {
      para = list = null;
      blocks.push({ type: 'h', level: heading[1].length, parts: inline(heading[2]) });
    } else if (bullet || numbered) {
      const type = bullet ? 'ul' : 'ol';
      para = null;
      if (list?.type !== type) {
        list = { type, start: numbered ? Number(numbered[1]) : 1, items: [] };
        blocks.push(list);
      }
      list.items.push(inline(bullet ? bullet[1] : numbered[2]));
    } else if (list && !blank && /^\s/.test(raw)) {
      // An indented line goes on with the item above.
      list.items.at(-1).push({ text: ' ' }, ...inline(line));
    } else if (para) {
      para.parts.push({ br: true }, ...inline(line));
    } else {
      list = null;
      para = { type: 'p', parts: inline(line) };
      blocks.push(para);
    }
    blank = false;
  }
  return blocks;
}

function partsText(parts) {
  return parts.map((p) => (p.br ? '\n' : p.img ? '' : p.code ?? p.text ?? partsText(p.bold || p.em || p.parts))).join('');
}

// noteLines is a note as lines of plain text, for the share card and the
// search: [{ text, head }], with the list marks as text and without images.
export function noteLines(text) {
  const out = [];
  for (const b of parseMarkdown(text)) {
    if (b.type === 'pre') {
      for (const line of b.lines) out.push({ text: line });
    } else if (b.type === 'ul' || b.type === 'ol') {
      b.items.forEach((parts, i) => out.push({ text: (b.type === 'ul' ? '• ' : `${b.start + i}. `) + partsText(parts) }));
    } else {
      for (const line of partsText(b.parts).split('\n')) if (line.trim()) out.push({ text: line, head: b.type === 'h' });
    }
  }
  return out;
}

// renderMarkdown makes the element that shows a note. In the window of a
// workout the headings start below its own ones.
export function renderMarkdown(text, doc = document) {
  const el = (tag, content) => {
    const n = doc.createElement(tag);
    if (content != null) n.textContent = content;
    return n;
  };
  const fill = (n, parts) => {
    for (const p of parts) {
      if (p.br) n.append(el('br'));
      else if (p.code != null) n.append(el('code', p.code));
      else if (p.img) {
        const img = el('img');
        img.setAttribute('src', p.img);
        img.setAttribute('alt', p.alt);
        img.setAttribute('loading', 'lazy');
        img.setAttribute('decoding', 'async');
        n.append(img);
      }
      else if (p.bold) n.append(fill(el('strong'), p.bold));
      else if (p.em) n.append(fill(el('em'), p.em));
      else if (p.link) {
        const a = fill(el('a'), p.parts);
        a.setAttribute('href', p.link);
        a.setAttribute('target', '_blank');
        a.setAttribute('rel', 'noopener noreferrer');
        n.append(a);
      } else n.append(doc.createTextNode(p.text));
    }
    return n;
  };
  const root = el('div');
  root.setAttribute('class', 'md');
  for (const b of parseMarkdown(text)) {
    if (b.type === 'h') root.append(fill(el(`h${Math.min(b.level + 3, 6)}`), b.parts));
    else if (b.type === 'p') root.append(fill(el('p'), b.parts));
    else if (b.type === 'pre') {
      const pre = el('pre');
      pre.append(el('code', b.lines.join('\n')));
      root.append(pre);
    }
    else {
      const list = el(b.type);
      if (b.type === 'ol' && b.start !== 1) list.setAttribute('start', String(b.start));
      for (const item of b.items) list.append(fill(el('li'), item));
      root.append(list);
    }
  }
  return root;
}
