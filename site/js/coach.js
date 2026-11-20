// The coach chat: ask Claude about your data. Claude looks it up with the
// same tools as the MCP server (only what your role sees) and the answer
// streams in. Threads are yours alone; the server keeps them
// (cmd/eumetria/coach.go). Opened from the top bar, or More on phones.

import { $, h } from './dom.js';
import { openDialog } from './dialog.js';
import { api, account, reason } from './account.js';
import { renderMarkdown } from './markdown.js';
import { dfShort, LOCALE } from './format.js';
import { t } from './i18n.js';

let win = null;

// canCoach: the role may ask the coach.
export const canCoach = () => !!account.user?.scopes?.includes('use.coach');

// showCoach shows the coach's buttons to those who may ask it.
export function showCoach() {
  const btn = $('#coach-btn');
  btn.hidden = !canCoach();
  btn.onclick = () => openCoach(btn);
  const more = $('#more-coach');
  if (more) more.hidden = !canCoach();
}

const EXAMPLES = () => [
  t('How did I sleep this week compared to last week?'),
  t('Where does my protein come from?'),
  t('How does training affect my sleep?'),
  t('How is my cut going?'),
];

// What a tool call looked at, as a short line: "sleep · 9 Nov – 20 Nov".
const TOOL_NAMES = () => ({
  overview: t('overview'), daily_metrics: t('daily numbers'), entries: t('foods logged'), food_sources: t('food sources'),
  workouts: t('workouts'), sleep: t('sleep'), body: t('weigh-ins'), checkins: t('check-ins'), lifts: t('lifts'),
  sessions: t('training sessions'), wods: t('benchmark workouts'), phases: t('phases'),
});
const day = (s) => (/^\d{4}-\d{2}-\d{2}$/.test(s || '') ? dfShort.format(new Date(s + 'T00:00:00Z')) : '');
export function toolLine(name, args = {}) {
  const parts = [TOOL_NAMES()[name] || name];
  if (args.date) parts.push(day(args.date));
  else if (args.from || args.to) parts.push([day(args.from), day(args.to) || t('today')].filter(Boolean).join(' – '));
  if (args.nutrient) parts.push(args.nutrient);
  if (args.lift || args.name || args.meal) parts.push(args.lift || args.name || args.meal);
  return parts.filter(Boolean).join(' · ');
}

const ERRORS = () => ({
  billing: t('The Anthropic credit is used up. An admin can buy more at console.anthropic.com.'),
  refused: t('Claude declined to answer this one. Try asking differently.'),
  too_long: t('The answer got too long. Try a narrower question.'),
  timeout: t('That took too long. Try again.'),
  busy: t('Another question of yours is still being answered.'),
  coach_off: t('The coach is switched off.'),
  bad_question: t('Write a question of up to 2000 characters.'),
});

export async function openCoach(returnFocus) {
  if (win) return;
  const close = h('button', { class: 'sheet-close', type: 'button', 'aria-label': t('Close') }, '×');
  const fresh = h('button', { class: 'pill-btn', type: 'button' }, t('New chat'));
  const listBtn = h('button', { class: 'pill-btn coach-list-btn', type: 'button', 'aria-expanded': 'false' }, t('Chats'));
  const head = h('div', { class: 'sheet-head wo-head' });
  const titles = h('div', { class: 'wo-titles' });
  titles.append(h('h2', { id: 'coach-title' }, t('Coach')), h('p', { class: 'muted' }, t('Ask about your data. Written by Claude.')));
  head.append(titles, listBtn, fresh, close);
  const side = h('nav', { class: 'coach-side', 'aria-label': t('Chats') });
  const log = h('div', { class: 'coach-log', 'aria-live': 'polite' });
  const input = h('textarea', { class: 'field coach-input', rows: '1', maxlength: '2000', placeholder: t('Ask about your sleep, food, training…'), 'aria-label': t('Your question') });
  const send = h('button', { class: 'pill-btn coach-send', type: 'submit' }, t('Ask'));
  const form = h('form', { class: 'coach-form' });
  form.append(input, send);
  const main = h('div', { class: 'coach-main' });
  main.append(log, form);
  const body = h('div', { class: 'coach-body' });
  body.append(side, main);
  const panel = h('div', { class: 'wo-panel coach-panel' });
  panel.append(head, body);
  win = openDialog({ cls: 'wo-overlay', labelledBy: 'coach-title', content: [panel], returnFocus, onClose: () => { win = null; } });
  close.onclick = () => win?.close();

  let thread = 0, asking = false, threads = [];
  const showList = (on) => {
    body.classList.toggle('list-open', on);
    listBtn.setAttribute('aria-expanded', String(on));
  };
  listBtn.onclick = () => showList(!body.classList.contains('list-open'));

  const drawSide = () => {
    side.replaceChildren();
    if (!threads.length) return side.append(h('p', { class: 'muted coach-none' }, t('No chats yet.')));
    for (const th of threads) {
      const row = h('div', { class: 'coach-thread' + (th.id === thread ? ' current' : '') });
      const open = h('button', { class: 'coach-thread-open', type: 'button' });
      open.append(h('span', { class: 'txt' }, th.title || t('Chat')), h('span', { class: 'muted' }, dfShort.format(new Date(th.updated_at))));
      open.onclick = () => { showList(false); load(th.id); };
      const del = h('button', { class: 'icon-btn coach-del', type: 'button', 'aria-label': t('Delete chat'), title: t('Delete chat') }, '×');
      del.onclick = async () => {
        if (!confirm(t('Delete this chat?'))) return;
        const res = await api('coach/threads/' + th.id, 'DELETE').catch(() => null);
        if (!res?.ok) return;
        threads = threads.filter((x) => x.id !== th.id);
        if (thread === th.id) start();
        drawSide();
      };
      row.append(open, del);
      side.append(row);
    }
  };

  const bubble = (role) => {
    const b = h('div', { class: 'coach-msg ' + role });
    log.append(b);
    return b;
  };
  const toolsBox = (b) => b.querySelector('.coach-tools') || b.appendChild(h('ul', { class: 'coach-tools', 'aria-label': t('Looked at') }));
  const addTool = (b, name, args) => toolsBox(b).append(h('li', {}, toolLine(name, args)));
  const footer = (b, m) => {
    const bits = [t('Written by Claude')];
    if (m.cost_usd && account.user?.scopes?.includes('admin')) bits.push(`$${m.cost_usd.toLocaleString(LOCALE, { maximumFractionDigits: 3 })}`);
    b.append(h('p', { class: 'muted coach-by' }, bits.join(' · ')));
  };
  const answer = (b, m) => {
    b.replaceChildren();
    for (const tc of m.tools || []) addTool(b, tc.name, tc.args);
    if (m.status === 'failed') {
      b.classList.add('failed');
      b.append(h('p', {}, ERRORS()[m.error] || t('That didn’t work. Try again.')));
      return;
    }
    b.append(renderMarkdown(m.text || ''));
    footer(b, m);
  };
  const scroll = () => { log.scrollTop = log.scrollHeight; };

  const start = () => {
    thread = 0;
    log.replaceChildren();
    const intro = h('div', { class: 'coach-intro' });
    intro.append(h('p', {}, t('Ask anything about your nutrition, training, sleep and recovery. The coach looks at your data before it answers; it doesn’t diagnose.')));
    const ex = h('div', { class: 'coach-examples' });
    for (const q of EXAMPLES()) {
      const b = h('button', { class: 'pill-btn', type: 'button' }, q);
      b.onclick = () => { input.value = q; form.requestSubmit(); };
      ex.append(b);
    }
    intro.append(ex);
    log.append(intro);
    drawSide();
    input.focus();
  };

  const load = async (id) => {
    const res = await api('coach/threads/' + id).catch(() => null);
    if (!res?.ok) return;
    const data = await res.json();
    thread = id;
    log.replaceChildren();
    for (const m of data.messages) {
      const b = bubble(m.role);
      if (m.role === 'user') b.append(h('p', {}, m.text));
      else answer(b, m);
    }
    drawSide();
    scroll();
  };

  form.onsubmit = async (e) => {
    e.preventDefault();
    const text = input.value.trim();
    if (!text || asking) return;
    asking = true;
    send.disabled = true;
    log.querySelector('.coach-intro')?.remove();
    bubble('user').append(h('p', {}, text));
    const b = bubble('assistant');
    b.classList.add('writing');
    const typing = h('p', { class: 'muted coach-wait' }, t('Looking at your data…'));
    b.append(typing);
    scroll();
    input.value = '';
    let raw = '';
    const md = h('div');
    try {
      const res = await fetch('api/coach/ask', {
        method: 'POST', credentials: 'same-origin', cache: 'no-store',
        headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ thread, text }),
      });
      if (!res.ok) {
        const why = await reason(res);
        b.classList.add('failed');
        b.replaceChildren(h('p', {}, ERRORS()[why] || t('That didn’t work. Try again.')));
        return;
      }
      const reader = res.body.getReader();
      const dec = new TextDecoder();
      let buf = '';
      for (;;) {
        const { value, done } = await reader.read();
        if (done) break;
        buf += dec.decode(value, { stream: true });
        let cut;
        while ((cut = buf.indexOf('\n\n')) >= 0) {
          const chunk = buf.slice(0, cut);
          buf = buf.slice(cut + 2);
          const ev = chunk.match(/^event: (.*)$/m)?.[1];
          const data = chunk.match(/^data: (.*)$/m)?.[1];
          if (!ev || !data) continue;
          const v = JSON.parse(data);
          if (ev === 'thread') {
            if (!thread) {
              thread = v.id;
              threads.unshift({ id: v.id, title: text.slice(0, 80), updated_at: new Date().toISOString() });
              drawSide();
            }
          } else if (ev === 'tool') {
            addTool(b, v.tool, v.args);
          } else if (ev === 'text') {
            raw += v.text;
            typing.remove();
            md.replaceChildren(renderMarkdown(raw));
            if (!md.isConnected) b.append(md);
          } else if (ev === 'done') {
            b.classList.remove('writing');
            answer(b, v);
          } else if (ev === 'error') {
            b.classList.add('failed');
            b.replaceChildren(h('p', {}, t('That didn’t work. Try again.')));
          }
          scroll();
        }
      }
    } catch {
      // The connection broke; the server still finishes the answer.
      b.classList.add('failed');
      b.replaceChildren(h('p', {}, t('The connection broke. The answer is still being written: open this chat again in a moment.')));
    } finally {
      b.classList.remove('writing');
      asking = false;
      send.disabled = false;
      scroll();
    }
  };
  input.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
      e.preventDefault();
      form.requestSubmit();
    }
  });
  fresh.onclick = () => { showList(false); start(); };

  const res = await api('coach').catch(() => null);
  if (!win) return;
  if (!res?.ok) {
    log.replaceChildren(h('p', { class: 'muted' }, t('This could not be loaded.')));
    form.hidden = true;
    return;
  }
  const home = await res.json();
  threads = home.threads || [];
  if (!home.ready) {
    log.replaceChildren(h('p', { class: 'muted coach-intro' }, account.user?.scopes?.includes('admin')
      ? t('The coach is off. Switch it on under Settings → AI (it needs an Anthropic API key).')
      : t('The coach isn’t set up yet. Ask an admin.')));
    form.hidden = true;
    drawSide();
    return;
  }
  start();
}
