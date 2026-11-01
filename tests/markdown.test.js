import { test } from 'node:test';
import assert from 'node:assert/strict';
import { inline, noteLines, parseMarkdown, renderMarkdown, safeLink } from '../site/js/markdown.js';

// A document that records what is made, so the tests can see every element
// and attribute a note ends up with.
function fakeDoc() {
  const made = [];
  const node = (tag) => {
    const n = {
      tag, attrs: {}, kids: [], textContent: null,
      setAttribute(k, v) { this.attrs[k] = v; },
      append(...more) { this.kids.push(...more); },
      get innerHTML() { throw new Error('innerHTML must not be used'); },
      set innerHTML(v) { throw new Error('innerHTML must not be used'); },
    };
    made.push(n);
    return n;
  };
  return {
    made,
    createElement: node,
    createTextNode: (text) => ({ text }),
  };
}

const render = (text) => {
  const doc = fakeDoc();
  return { root: renderMarkdown(text, doc), made: doc.made };
};

// What a reader sees: every text of an element tree, in order.
const shown = (n) => n.text ?? (n.textContent != null ? n.textContent : n.kids.map(shown).join(''));

test('inline marks', () => {
  assert.deepEqual(inline('a **b** c'), [{ text: 'a ' }, { bold: [{ text: 'b' }] }, { text: ' c' }]);
  assert.deepEqual(inline('*a* and _b_'), [{ em: [{ text: 'a' }] }, { text: ' and ' }, { em: [{ text: 'b' }] }]);
  assert.deepEqual(inline('`**raw**`'), [{ code: '**raw**' }]);
  assert.deepEqual(inline('**a *b* c**'), [{ bold: [{ text: 'a ' }, { em: [{ text: 'b' }] }, { text: ' c' }] }]);
  assert.deepEqual(inline('*a **b** c*'), [{ em: [{ text: 'a ' }, { bold: [{ text: 'b' }] }, { text: ' c' }] }]);
  assert.deepEqual(inline('***both***'), [{ bold: [{ em: [{ text: 'both' }] }] }]);
  assert.deepEqual(inline('[**x**](https://a.example/)'), [{ link: 'https://a.example/', parts: [{ bold: [{ text: 'x' }] }] }]);
  assert.deepEqual(inline('\\*no\\* \\`code\\`'), [{ text: '*no* `code`' }]);
});

test('what only looks like a mark stays text', () => {
  for (const s of ['3*10 and 4*12', 'snake_case_name', '2 * 3 * 4', 'a ** b', '**unclosed', '*unclosed', '`unclosed', '[no link]', '[a](b c)', '12/10 Cal @ 24" Box', '**', '****', '``', 'x * y*', '[](https://a.example/)']) {
    assert.deepEqual(inline(s), [{ text: s }], s);
  }
});

test('the note from the feature request', () => {
  const note = `# Warm-Up
**3 Rounds:**
- 1:00 Erg
- 6 Scapular Pull-Ups
- 3/3 Single-Arm Shoulder Press @ 8 kg

# Skill Accumulation
**Rowing Intervals — Easy Pace**
- 6 × (20 sec Row / 20 sec Easy)

# WOD
**4 Rounds — 3:00 ON / 1:00 OFF (Intermediate)**

- 12/10 Cal Row
- 10 Box Step-Overs @ 24" Box
- Max Double DB Push Press @ 10 kg each for the remaining time

**Score:** Total Double Dumbbell Push Press reps across all 4 rounds.`;
  const blocks = parseMarkdown(note);
  assert.deepEqual(blocks.map((b) => b.type), ['h', 'p', 'ul', 'h', 'p', 'ul', 'h', 'p', 'ul', 'p']);
  assert.deepEqual(blocks[1].parts, [{ bold: [{ text: '3 Rounds:' }] }]);
  assert.equal(blocks[2].items.length, 3);
  assert.deepEqual(blocks[8].items[0], [{ text: '12/10 Cal Row' }]);
  assert.deepEqual(blocks[9].parts, [{ bold: [{ text: 'Score:' }] }, { text: ' Total Double Dumbbell Push Press reps across all 4 rounds.' }]);
});

test('blocks', () => {
  assert.deepEqual(parseMarkdown(''), []);
  assert.deepEqual(parseMarkdown(null), []);
  assert.deepEqual(parseMarkdown('one\ntwo\n\nthree'), [
    { type: 'p', parts: [{ text: 'one' }, { br: true }, { text: 'two' }] },
    { type: 'p', parts: [{ text: 'three' }] },
  ]);
  assert.deepEqual(parseMarkdown('### Three\n#nope'), [
    { type: 'h', level: 3, parts: [{ text: 'Three' }] },
    { type: 'p', parts: [{ text: '#nope' }] },
  ]);
  assert.deepEqual(parseMarkdown('3. c\n4. d'), [{ type: 'ol', start: 3, items: [[{ text: 'c' }], [{ text: 'd' }]] }]);
  // Blank lines between items keep one list; another kind starts a new one.
  assert.deepEqual(parseMarkdown('- a\n\n- b\n1. c').map((b) => [b.type, b.items.length]), [['ul', 2], ['ol', 1]]);
  assert.deepEqual(parseMarkdown('- a\n  goes on').at(0).items, [[{ text: 'a' }, { text: ' ' }, { text: 'goes on' }]]);
  assert.deepEqual(parseMarkdown('- a\ntext').map((b) => b.type), ['ul', 'p']);
  assert.equal(parseMarkdown('a\r\nb')[0].parts.length, 3);
});

test('rendering makes only the elements it should', () => {
  const { root } = render('# One\n## Two\n### Three\n#### Four\ntext\nmore\n\n**b** *i* `c`\n\n- x\n\n7. y');
  assert.equal(root.attrs.class, 'md');
  assert.deepEqual(root.kids.map((k) => k.tag), ['h4', 'h5', 'h6', 'h6', 'p', 'p', 'ul', 'ol']);
  assert.deepEqual(root.kids[4].kids.map((k) => k.tag ?? 'text'), ['text', 'br', 'text']);
  assert.deepEqual(root.kids[5].kids.filter((k) => k.tag).map((k) => k.tag), ['strong', 'em', 'code']);
  assert.equal(root.kids[7].attrs.start, '7');
});

test('links are http(s) only and leak nothing', () => {
  const { made } = render('[home](https://example.org/a?b=1#c)');
  const a = made.find((n) => n.tag === 'a');
  assert.deepEqual(a.attrs, { href: 'https://example.org/a?b=1#c', target: '_blank', rel: 'noopener noreferrer' });
  assert.equal(safeLink('HTTP://Example.org'), 'http://example.org/');
  for (const href of ['javascript:alert(1)', 'JaVaScRiPt:alert(1)', 'java\tscript:alert(1)', 'data:text/html,x', 'vbscript:x', 'file:///etc/passwd', 'ftp://a', '//evil.example', '/relative', 'blob:x', 'mailto:a@b']) {
    assert.equal(safeLink(href), null, href);
  }
});

// The usual payloads. Whatever they try, the only elements are the note's
// own, the only attributes the ones the renderer sets, and what was typed
// is still there to read.
const ALLOWED = new Set(['div', 'p', 'h4', 'h5', 'h6', 'ul', 'ol', 'li', 'strong', 'em', 'code', 'pre', 'br', 'a']);
const PAYLOADS = [
  '<script>alert(1)</script>',
  '<img src=x onerror=alert(1)>',
  '<svg onload=alert(1)>',
  '<iframe src="javascript:alert(1)"></iframe>',
  '"><script>alert(1)</script>',
  '[x](javascript:alert(1))',
  '[x](JAVASCRIPT:alert(1))',
  '[x](data:text/html;base64,PHNjcmlwdD4=)',
  '[x](vbscript:msgbox(1))',
  '[x](javascript&colon;alert(1))',
  '[x]( javascript:alert(1))',
  '[<img src=x onerror=alert(1)>](https://example.org/)',
  '[x](https://example.org/"onmouseover="alert(1))',
  '![x](https://example.org/tracker.png)',
  '**<b onclick=alert(1)>bold</b>**',
  '`<script>alert(1)</script>`',
  '# <script>alert(1)</script>',
  '- <script>alert(1)</script>',
  '&lt;script&gt;alert(1)&lt;/script&gt;',
  '<style>body{display:none}</style>',
  '<a href="javascript:alert(1)">x</a>',
];

test('no payload gets through', () => {
  for (const p of PAYLOADS) {
    const { root, made } = render(p);
    for (const n of made) {
      assert.ok(ALLOWED.has(n.tag), `${p}: made <${n.tag}>`);
      for (const k of Object.keys(n.attrs)) assert.ok(['class', 'href', 'target', 'rel', 'start'].includes(k), `${p}: attribute ${k}`);
      if (n.tag === 'a') assert.match(n.attrs.href, /^https:\/\/example\.org\//, p);
    }
    // No text is lost or turned into markup: angle brackets are still text.
    if (p.includes('<') && !p.startsWith('[<img')) assert.ok(shown(root).includes('<'), `${p}: shows ${shown(root)}`);
  }
});

const SRC = 'note-images/0123456789abcdef0123456789abcdef.jpg';

test('an uploaded image is an image', () => {
  assert.deepEqual(inline(`a ![leg day](${SRC}) b`), [{ text: 'a ' }, { img: SRC, alt: 'leg day' }, { text: ' b' }]);
  const { made } = render(`![](${SRC})`);
  const img = made.find((n) => n.tag === 'img');
  assert.deepEqual(img.attrs, { src: SRC, alt: '', loading: 'lazy', decoding: 'async' });
});

test('any other image stays text, not even a link', () => {
  for (const s of [
    '![x](https://example.org/tracker.png)', '![x](//example.org/a.jpg)', '![x](data:image/svg+xml,<svg onload=alert(1)>)',
    '![x](javascript:alert(1))', '![x](note-images/../app.db)', '![x](note-images/x.jpg)', `![x](/${SRC})`, `![x](${SRC}?a=1)`,
    `![x](${SRC}" onerror="alert(1))`, `![x](${SRC.replace('.jpg', '.svg')})`, `![x](${SRC.toUpperCase()})`,
  ]) {
    const { root, made } = render(s);
    assert.ok(!made.some((n) => n.tag === 'img' || n.tag === 'a'), s);
    assert.ok(shown(root).startsWith('!['), s);
  }
});

test('code blocks', () => {
  assert.deepEqual(parseMarkdown('before\n```\n  **raw** <b>\n\n- not a list\n```\nafter'), [
    { type: 'p', parts: [{ text: 'before' }] },
    { type: 'pre', lines: ['  **raw** <b>', '', '- not a list'] },
    { type: 'p', parts: [{ text: 'after' }] },
  ]);
  // With a language, and left open to the end of the note.
  assert.deepEqual(parseMarkdown('```js\nlet a;'), [{ type: 'pre', lines: ['let a;'] }]);
  assert.deepEqual(parseMarkdown('- a\n```\nb\n```\n- c').map((b) => b.type), ['ul', 'pre', 'ul']);
  // Inline code on one line stays inline code.
  assert.equal(parseMarkdown('```inline```')[0].type, 'p');
  const { root, made } = render('```\n<script>alert(1)</script>\n```');
  assert.deepEqual(root.kids.map((k) => k.tag), ['pre']);
  assert.equal(root.kids[0].kids[0].tag, 'code');
  assert.equal(root.kids[0].kids[0].textContent, '<script>alert(1)</script>');
  assert.ok(!made.some((n) => n.tag === 'script'));
});

test('a long note with nothing but marks stays quick', () => {
  const size = 20 << 10;
  for (const piece of ['*', '**', '_', '`', '```\n', '!', '![', '![](', '[', '[]', '[a](', '*a', '**a *b ', '\\', '# ', '- ', '[a](b) ', '*a* ']) {
    const text = piece.repeat(Math.ceil(size / piece.length)).slice(0, size);
    const from = performance.now();
    parseMarkdown(text);
    assert.ok(performance.now() - from < 500, `${JSON.stringify(piece)} took ${performance.now() - from} ms`);
  }
});

test('deep nesting stops at a limit', () => {
  const text = '*a '.repeat(2000) + 'b' + ' c*'.repeat(2000);
  const depth = (parts) => Math.max(0, ...parts.map((p) => (p.em || p.bold ? 1 + depth(p.em || p.bold) : 0)));
  assert.ok(depth(inline(text)) <= 7);
});

test('a note as plain lines', () => {
  assert.deepEqual(noteLines('# Warm-Up\n**3 Rounds:**\nnext\n\n- 1:00 *Erg*\n\n3. `six`\n4. [seven](https://a.example/)'), [
    { text: 'Warm-Up', head: true },
    { text: '3 Rounds:', head: false },
    { text: 'next', head: false },
    { text: '• 1:00 Erg' },
    { text: '3. six' },
    { text: '4. seven' },
  ]);
  assert.deepEqual(noteLines('![](note-images/0123456789abcdef0123456789abcdef.jpg)\n\ntext\n\n```\nraw\n```'), [{ text: 'text', head: false }, { text: 'raw' }]);
});
