// Workout notes: Markdown about a training (sessions.js), written in the
// window of one of its Fitbit workouts. The server keeps them (notes.go) and
// sends those of the workouts the user sees; state.notes has them by
// training id;
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
import { noteImages, noteLines, placeTranscript, renderMarkdown } from './markdown.js';
import { imageForUpload } from './upload.js';
import { knowTraining } from './sessions.js';
import { t } from './i18n.js';

const dfNote = new Intl.DateTimeFormat(LOCALE, { day: 'numeric', month: 'short', year: 'numeric' });

const drafts = new Map(); // workout id → the text being written

// The longest side of an uploaded image: enough for a note.
const IMAGE_SIDE = 1920;

// text is the note without its Markdown, for search and for folded notes.
export const withText = (n) => ({ ...n, text: noteLines(n.markdown).map((l) => l.text).join('\n') });

// noteOf is the note of the Fitbit workout id: its training's.
export const noteOf = (id) => {
  for (const n of state.notes.values()) if (n.workouts?.some((r) => r.source === 'fitbit' && r.id === id)) return n;
  return undefined;
};

// The notes that go with data. Offline, or when the server doesn't answer,
// the ones already here stay, and the page knows the list may be incomplete.
export async function loadNotes(data) {
  if (!data.scopes?.includes('view.notes')) return state.notes.clear();
  const res = await api('notes').catch(() => null);
  if (!res?.ok) {
    console.error('loading the notes failed', res?.status);
    return;
  }
  state.notes = new Map((await res.json()).map((n) => [n.session_id, withText(n)]));
  state.notesLoaded = true;
}

// Without a second factor the server refuses a role that needs one, except
// with -demo.
function mayEdit() {
  const u = account.user;
  return !!u?.scopes?.includes('edit.notes') && (!u.needs_second_factor || !!u.demo);
}

// An author whose account was deleted comes as "".
const byline = (n) => t('by {name}, {date}', { name: n.updated_by || t('deleted user'), date: dfNote.format(new Date(n.updated_at)) });

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
        state.notes.delete(n.session_id);
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
  if (why === 'too_many_pending') return t('Too many images wait for their note. Save the note first, or remove images you don’t use.');
  if (why === 'too_many_images') return t('That’s a lot of images for one hour. Try again later.');
  if (why === 'storage_full') return t('There’s no room for more images. Tell an admin.');
  if (why === 'busy') return t('The server is busy with other images. Try again in a moment.');
  return refused(res);
}

// What the page says when a photo couldn't be read.
function whiteboardError(body) {
  const time = (s) => new Date(s).toLocaleTimeString(LOCALE, { hour: '2-digit', minute: '2-digit' });
  return {
    ai_off: () => t('Reading photos needs Claude: an admin sets it up under Settings → AI.'),
    no_workout: () => t('Claude found no workout on that photo.'),
    limit: () => t('Claude’s limit is reached until {time}. Try again then.', { time: time(body.until) }),
    too_many_images: () => t('That’s a lot of photos for one hour. Try again later.'),
    billing: () => t('The Anthropic credit is used up. An admin can buy more at console.anthropic.com.'),
    runner_down: () => t('Claude Code can’t be reached right now. Try again later.'),
    refused: () => t('Claude declined to read this photo.'),
  }[body.error]?.() || t('That didn’t work. Try again.');
}

// noteInput is the note being written, with its tabs (Write, Preview), its
// images and their "Read the whiteboard" buttons, for the note key key (a
// Fitbit workout's id, "training-{id}", or "new" for a training not saved
// yet: notes.go). initial is the text to start with; onInput(text) follows
// it. addImage is a button for the caller's row; busy() is true while an
// image goes up, and error()/status() show the caller's messages.
export function noteInput({ key, initial = '', onInput, label = t('Note in Markdown'), rows = 12 }) {
  const area = h('textarea', { class: 'field note-input', rows, 'aria-label': label, spellcheck: 'true' });
  area.value = initial;
  area.oninput = () => onInput?.(area.value);

  const preview = h('div', { class: 'note-preview', hidden: '' });
  const tabs = h('div', { class: 'tabs note-tabs', role: 'tablist', 'aria-label': t('Note') });
  const tab = (text, on, pick) => {
    const b = h('button', { role: 'tab', type: 'button', 'aria-selected': String(on), tabindex: on ? '0' : '-1' }, text);
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
  const showError = (text) => {
    error.textContent = text || '';
    error.hidden = !text;
  };
  const picker = h('input', { type: 'file', accept: 'image/*', multiple: '', hidden: '' });
  const addImage = h('button', { class: 'pill-btn', type: 'button' }, t('Add image'));
  addImage.onclick = () => picker.click();

  // Each image goes up on its own and lands where the cursor is by then,
  // on a line of its own.
  let uploading = 0;
  const upload = async (files) => {
    uploading += files.length;
    showError('');
    status.textContent = t('Uploading image…');
    for (const file of files) {
      try {
        const res = await api(`notes/${encodeURIComponent(key())}/images`, 'POST', { image: await imageForUpload(file, IMAGE_SIDE) });
        if (!res.ok) throw Object.assign(new Error(), { res });
        const { src } = await res.json();
        const at = area.selectionStart;
        const before = area.value[at - 1];
        area.setRangeText(`${before && before !== '\n' ? '\n' : ''}![](${src})\n`, at, area.selectionEnd, 'end');
        area.oninput();
        drawPhotos();
      } catch (err) {
        showError(err.res ? await refusedImage(err.res) : err instanceof TypeError ? t('Server not reachable.') : t('That image can’t be used.'));
      }
      uploading--;
    }
    if (!uploading) status.textContent = '';
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

  // The note's photos, each with a button that has Claude write out the
  // workout on it: the text goes where the photo was, the photo to the end
  // (whiteboard.go). Nothing is saved until the note is.
  const photos = h('div', { class: 'note-photos', hidden: '' });
  const reading = new Set();
  const drawPhotos = () => {
    const srcs = noteImages(area.value);
    photos.hidden = !srcs.length;
    photos.replaceChildren(...srcs.map((src) => {
      const item = h('div', { class: 'note-photo' });
      const img = h('img', { src, alt: '', loading: 'lazy' });
      const go = h('button', { class: 'pill-btn small', type: 'button' }, reading.has(src) ? t('Reading…') : t('Read the whiteboard'));
      go.disabled = reading.has(src);
      go.onclick = () => transcribe(src);
      item.append(img, go);
      return item;
    }));
  };
  const transcribe = async (src) => {
    reading.add(src);
    drawPhotos();
    showError('');
    status.textContent = t('Claude is reading the photo…');
    try {
      const res = await api(`notes/${encodeURIComponent(key())}/transcribe`, 'POST', { src });
      if (res.ok) {
        const { markdown } = await res.json();
        area.value = placeTranscript(area.value, src, markdown);
        area.oninput();
        status.textContent = t('Written out by Claude: check it before you save.');
      } else {
        const body = await res.json().catch(() => ({}));
        status.textContent = '';
        showError(whiteboardError(body));
      }
    } catch {
      status.textContent = '';
      showError(t('Server not reachable.'));
    }
    reading.delete(src);
    drawPhotos();
  };
  let photoTimer = 0;
  area.addEventListener('input', () => {
    clearTimeout(photoTimer);
    photoTimer = setTimeout(drawPhotos, 300);
  });
  drawPhotos();

  return {
    parts: [tabs, area, preview, photos, status, error],
    addImage: [addImage, picker],
    value: () => area.value,
    // Images from elsewhere (shared to the app), uploaded as if picked.
    addFiles: (files) => upload(files),
    busy: () => uploading > 0,
    focus: () => area.focus(),
    error: showError,
  };
}

// The workout window's editor: noteInput with Save and Cancel, the text
// kept as a draft until one of them.
function noteEditor(w, done) {
  const input = noteInput({ key: () => w.id, initial: drafts.get(w.id) ?? noteOf(w.id)?.markdown ?? '', onInput: (text) => drafts.set(w.id, text) });
  const save = h('button', { class: 'pill-btn', type: 'submit' }, t('Save'));
  const cancel = h('button', { class: 'pill-btn', type: 'button' }, t('Cancel'));
  cancel.onclick = () => {
    drafts.delete(w.id);
    done();
  };
  const row = h('div', { class: 'sec-row note-actions' });
  row.append(save, cancel, ...input.addImage);

  const form = h('form', { class: 'note-form' });
  form.append(...input.parts, row);
  form.onsubmit = async (e) => {
    e.preventDefault();
    if (input.busy()) return input.error(t('Wait for the images to finish uploading.'));
    save.disabled = true;
    input.error('');
    try {
      const res = await api('notes/' + encodeURIComponent(w.id), 'PUT', { markdown: input.value() });
      if (res.ok) {
        const saved = withText(await res.json());
        state.notes.set(saved.session_id, saved);
        drafts.delete(w.id);
        // The workout's first note may have made its training.
        await knowTraining(saved.session_id);
        return done();
      }
      input.error(await refused(res));
    } catch {
      input.error(t('Server not reachable.'));
    }
    save.disabled = false;
  };
  queueMicrotask(() => input.focus());

  const head = h('div', { class: 'wo-sec-head' });
  head.append(h('h3', {}, t('Note')));
  return [head, form];
}

// saveNote saves the training's note as text says: written, changed, or
// deleted when emptied (nothing when it is as it was). It returns '' when
// done, else what went wrong, in the page's words.
export async function saveNote(trainingId, text) {
  const before = state.notes.get(trainingId)?.markdown ?? '';
  if (text.trim() === before.trim()) return '';
  const key = 'notes/training-' + trainingId;
  try {
    if (!text.trim()) {
      const res = await api(key, 'DELETE');
      if (!res.ok && res.status !== 404) return refused(res);
      state.notes.delete(trainingId);
      return '';
    }
    const res = await api(key, 'PUT', { markdown: text });
    if (!res.ok) return refused(res);
    const saved = withText(await res.json());
    state.notes.set(saved.session_id, saved);
    return '';
  } catch {
    return t('Server not reachable.');
  }
}

// mayEditNotes: whether this user writes notes.
export const mayEditNotes = mayEdit;

// A note folded to its first line, for lists of workouts.
export function noteFold(n) {
  const fold = h('details', { class: 'note-fold' });
  fold.append(h('summary', {}, n.text.split('\n')[0] || t('Note')), noteBody(n));
  return fold;
}
