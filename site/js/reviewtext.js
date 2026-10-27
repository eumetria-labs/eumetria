// The weekly review arrives as Markdown from Claude. Only what it uses is
// understood: paragraphs, headings ("## …" or a line that is all bold),
// bullet points and **bold**. The result is plain data the page turns into
// elements with textContent, so nothing in the text can become HTML. No DOM
// access, so Node can test this file.

// Models sometimes write HTML escapes; the page shows text, so decode them.
const decode = (s) => s.replace(/&(amp|lt|gt|quot|#39);/g, (_, e) => ({ amp: '&', lt: '<', gt: '>', quot: '"', '#39': "'" })[e]);

// inline splits "**a** b" into [{ text: 'a', bold: true }, { text: ' b' }].
export function inline(s) {
  const out = [];
  const re = /\*\*(.+?)\*\*/g;
  let at = 0;
  for (let m; (m = re.exec(s));) {
    if (m.index > at) out.push({ text: decode(s.slice(at, m.index)) });
    out.push({ text: decode(m[1]), bold: true });
    at = re.lastIndex;
  }
  if (at < s.length) out.push({ text: decode(s.slice(at)) });
  return out;
}

// parseReview turns the text into blocks: { type: 'h' | 'p', parts } and
// { type: 'ul', items: [parts] }.
export function parseReview(text) {
  const blocks = [];
  let para = null, list = null;
  const end = () => { para = null; list = null; };
  for (const raw of (text || '').split('\n')) {
    const line = raw.trim();
    if (!line) { end(); continue; }
    const heading = line.match(/^#{1,6}\s+(.*)$/) || line.match(/^\*\*([^*]+)\*\*:?$/);
    const bullet = line.match(/^[-*•]\s+(.*)$/);
    if (heading) {
      end();
      blocks.push({ type: 'h', parts: inline(heading[1].replace(/\*\*/g, '')) });
    } else if (bullet) {
      para = null;
      if (!list) { list = { type: 'ul', items: [] }; blocks.push(list); }
      list.items.push(inline(bullet[1]));
    } else if (para) {
      para.parts.push({ text: ' ' }, ...inline(line));
    } else {
      list = null;
      para = { type: 'p', parts: inline(line) };
      blocks.push(para);
    }
  }
  return blocks;
}
