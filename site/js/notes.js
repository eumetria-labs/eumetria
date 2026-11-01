// Workout notes: Markdown about a Fitbit workout, written in its window. The
// server keeps them (notes.go) and sends those of the workouts the user sees;
// with edit.notes the window also adds, changes and deletes them. Text being
// written survives closing the window, until it is saved or cancelled.
// Images are pasted into the text or picked from a file; they are uploaded
// at once and embedded where the cursor is. A long note shows its start,
// with a button for the rest.

import { LOCALE } from './format.js';
import { h, tabKeys } from './dom.js';
import { openLightbox } from './lightbox.js';
import { account, api, reason } from './account.js';
import { can, state } from './state.js';
import { noteLines, renderMarkdown } from './markdown.js';
import { imageForUpload } from './upload.js';
import { t } from './i18n.js';

const dfNote = new Intl.DateTimeFormat(LOCALE, { day: 'numeric', month: 'short', year: 'numeric' });

const drafts = new Map(); // workout id → the text being written

// The longest side of an uploaded image: enough for a note.
const IMAGE_SIDE = 1920;

// text is the note without its Markdown, for search and for folded notes.
export const withText = (n) => ({ ...n, text: noteLines(n.markdown).map((l) => l.text).join('\n') });

export const noteOf = (id) => state.notes.get(id);

// The notes that go with data. Offline, or when the server doesn't answer,
// the ones already here stay, and the page knows the list may be incomplete.
export async function loadNotes(data) {
  if (!data.scopes?.includes('view.notes')) return state.notes.clear();
  const res = await api('notes').catch(() => null);
  if (!res?.ok) {
    console.error('loading the notes failed', res?.status);
    return;
  }
  state.notes = new Map((await res.json()).map((n) => [n.workout_id, withText(n)]));
  state.notesLoaded = true;
}

// Without a second factor the server refuses a role that needs one.
function mayEdit() {
  const u = account.user;
  return !!u?.scopes?.includes('edit.notes') && !u.needs_second_factor;
}

const byline = (n) => t('by {name}, {date}', { name: n.updated_by, date: dfNote.format(new Date(n.updated_at)) });

// A workout's note as its window shows it, or null when there is nothing to
// show or add.
export function noteSection(w) {
  if (!can('view.notes') || (!noteOf(w.id) && !mayEdit())) return null;
  const sec = h('section', { class: 'wo-sec wo-note' });
  const show = () => sec.replaceChildren(...noteView(w, show, edit));
  const edit = () => sec.replaceChildren(...noteEditor(w, show));
  if (drafts.has(w.id)) edit();
  else show();
  return sec;
}

function noteView(w, show, edit) {
  const n = noteOf(w.id);
  const button = (text, onClick) => {
    const b = h('button', { class: 'pill-btn', type: 'button' }, text);
    b.onclick = onClick;
    return b;
  };
  // Without the list this workout may well have a note, which adding one
  // would overwrite.
  if (!n && !state.notesLoaded) return [h('p', { class: 'muted' }, t('The notes could not be loaded.'))];
  if (!n) return [button(t('Add note'), edit)];
  const head = h('div', { class: 'wo-sec-head' });
  const by = h('span', { class: 'muted note-by' });
  if (n.updated_by_avatar) by.append(h('img', { class: 'note-avatar', src: n.updated_by_avatar, alt: '' }));
  by.append(byline(n));
  head.append(h('h3', {}, t('Note')), by);
  const parts = [head, ...collapsible(noteBody(n))];
  if (mayEdit()) {
    const row = h('div', { class: 'sec-row note-actions' });
    const error = h('p', { class: 'lock-error', role: 'alert', hidden: '' });
    const del = button(t('Delete'), async () => {
      if (!confirm(t('Delete this note?'))) return;
      del.disabled = true;
      error.hidden = true;
      const res = await api('notes/' + encodeURIComponent(w.id), 'DELETE').catch(() => null);
      if (res?.ok || res?.status === 404) {
        state.notes.delete(w.id);
        return show();
      }
      del.disabled = false;
      error.textContent = res ? t('That didn’t work. Try again.') : t('Server not reachable.');
      error.hidden = false;
    });
    row.append(button(t('Edit'), edit), del);
    parts.push(row, error);
  }
  return parts;
}

// A note's text, its images opening full size.
function noteBody(n) {
  const md = renderMarkdown(n.markdown);
  const imgs = [...md.querySelectorAll('img')];
  const photos = imgs.map((img) => ({ src: img.getAttribute('src'), caption: img.alt || t('Note') }));
  imgs.forEach((img, i) => {
    img.tabIndex = 0;
    img.setAttribute('role', 'button');
    img.setAttribute('aria-label', img.alt || t('Open image'));
    img.onclick = () => openLightbox(photos, i, img);
    img.onkeydown = (e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); img.click(); } };
  });
  return md;
}

// md cut to a few lines' height when it is longer, with a button for the
// rest. Measured whenever its size changes: images arrive later.
function collapsible(md) {
  const box = h('div', { class: 'note-clamp' });
  box.append(md);
  const more = h('button', { class: 'note-more', type: 'button', 'aria-expanded': 'false', hidden: '' }, t('Show more'));
  let open = false;
  new ResizeObserver(() => {
    if (open) return;
    const long = box.scrollHeight > box.clientHeight + 1;
    more.hidden = !long;
    box.classList.toggle('cut', long);
  }).observe(md);
  more.onclick = () => {
    open = !open;
    box.classList.toggle('open', open);
    box.classList.toggle('cut', !open);
    more.textContent = open ? t('Show less') : t('Show more');
    more.setAttribute('aria-expanded', String(open));
  };
  return [box, more];
}

// The server's reasons for refusing a note, in the page's words. nginx
// answers a note it finds too long itself, without one.
async function refused(res) {
  const why = await reason(res);
  if (res.status === 413 || why === 'too_long') return t('That note is too long: 20 KB at most.');
  if (why === 'empty') return t('Write something first, or delete the note.');
  if (why === 'not_found') return t('This workout is no longer there.');
  if (res.status === 401) return t('You are signed out. Copy your text and reload the page.');
  return t('That didn’t work. Try again.');
}

async function refusedImage(res) {
  const why = await reason(res);
  if (res.status === 413 || why === 'too_long') return t('That image is too large.');
  if (why === 'bad_image') return t('That image can’t be used.');
  return refused(res);
}

function noteEditor(w, done) {
  const area = h('textarea', { class: 'field note-input', rows: 12, 'aria-label': t('Note in Markdown'), spellcheck: 'true' });
  area.value = drafts.get(w.id) ?? noteOf(w.id)?.markdown ?? '';
  area.oninput = () => drafts.set(w.id, area.value);

  const preview = h('div', { class: 'note-preview', hidden: '' });
  const tabs = h('div', { class: 'tabs note-tabs', role: 'tablist', 'aria-label': t('Note') });
  const tab = (label, on, pick) => {
    const b = h('button', { role: 'tab', type: 'button', 'aria-selected': String(on), tabindex: on ? '0' : '-1' }, label);
    b.onclick = () => {
      for (const x of tabs.children) {
        x.setAttribute('aria-selected', String(x === b));
        x.tabIndex = x === b ? 0 : -1;
      }
      pick();
    };
    return b;
  };
  tabs.append(
    tab(t('Write'), true, () => { area.hidden = false; preview.hidden = true; area.focus(); }),
    tab(t('Preview'), false, () => {
      preview.replaceChildren(area.value.trim() ? renderMarkdown(area.value) : h('p', { class: 'empty-tab' }, t('Nothing to show yet.')));
      area.hidden = true;
      preview.hidden = false;
    }),
  );
  tabs.addEventListener('keydown', (e) => tabKeys(e, tabs));

  const error = h('p', { class: 'lock-error', role: 'alert', hidden: '' });
  const status = h('p', { class: 'muted note-status', role: 'status' });
  const save = h('button', { class: 'pill-btn', type: 'submit' }, t('Save'));
  const cancel = h('button', { class: 'pill-btn', type: 'button' }, t('Cancel'));
  cancel.onclick = () => {
    drafts.delete(w.id);
    done();
  };
  const picker = h('input', { type: 'file', accept: 'image/*', multiple: '', hidden: '' });
  const addImage = h('button', { class: 'pill-btn', type: 'button' }, t('Add image'));
  addImage.onclick = () => picker.click();
  const row = h('div', { class: 'sec-row note-actions' });
  row.append(save, cancel, addImage, picker);

  // Each image goes up on its own and lands where the cursor is by then,
  // on a line of its own.
  let uploading = 0;
  const upload = async (files) => {
    uploading += files.length;
    save.disabled = true;
    error.hidden = true;
    status.textContent = t('Uploading image…');
    for (const file of files) {
      try {
        const res = await api(`notes/${encodeURIComponent(w.id)}/images`, 'POST', { image: await imageForUpload(file, IMAGE_SIDE) });
        if (!res.ok) throw Object.assign(new Error(), { res });
        const { src } = await res.json();
        const at = area.selectionStart;
        const before = area.value[at - 1];
        area.setRangeText(`${before && before !== '\n' ? '\n' : ''}![](${src})\n`, at, area.selectionEnd, 'end');
        area.oninput();
      } catch (err) {
        error.textContent = err.res ? await refusedImage(err.res) : err instanceof TypeError ? t('Server not reachable.') : t('That image can’t be used.');
        error.hidden = false;
      }
      uploading--;
    }
    if (!uploading) {
      status.textContent = '';
      save.disabled = false;
    }
  };
  picker.onchange = () => {
    upload([...picker.files]);
    picker.value = '';
  };
  // What holds text as well, like cells of a spreadsheet, pastes as text.
  area.addEventListener('paste', (e) => {
    const files = [...e.clipboardData.files].filter((f) => f.type.startsWith('image/'));
    if (!files.length || e.clipboardData.getData('text/plain')) return;
    e.preventDefault();
    upload(files);
  });

  const form = h('form', { class: 'note-form' });
  form.append(tabs, area, preview, status, error, row);
  form.onsubmit = async (e) => {
    e.preventDefault();
    save.disabled = true;
    error.hidden = true;
    try {
      const res = await api('notes/' + encodeURIComponent(w.id), 'PUT', { markdown: area.value });
      if (res.ok) {
        state.notes.set(w.id, withText(await res.json()));
        drafts.delete(w.id);
        return done();
      }
      error.textContent = await refused(res);
    } catch {
      error.textContent = t('Server not reachable.');
    }
    error.hidden = false;
    save.disabled = false;
  };
  queueMicrotask(() => area.focus());

  const head = h('div', { class: 'wo-sec-head' });
  head.append(h('h3', {}, t('Note')));
  return [head, form];
}

// A note folded to its first line, for lists of workouts.
export function noteFold(n) {
  const fold = h('details', { class: 'note-fold' });
  fold.append(h('summary', {}, n.text.split('\n')[0] || t('Note')), noteBody(n));
  return fold;
}
