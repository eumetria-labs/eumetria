// The "Daily data" card: the days of the selected range as a table that can
// be searched, filtered and sorted, and the export of any date range as a
// ZIP of CSV or JSON files.

import { dfShort, fmt, fmtDuration, hasDetail, logged, parse } from './format.js';
import { $, download, h } from './dom.js';
import { state, store } from './state.js';
import { openDay } from './sheet.js';
import { t, tn } from './i18n.js';
import { DAY_FILTERS, filterDays, sortBy } from './query.js';
import { exportFiles } from './export.js';
import { zip } from './zip.js';
import { printReport } from './report.js';

const dash = (v, digits = 0) => (v ? fmt(v, digits) : '–');

// [key, header, cell text, sort value]
const COLS = [
  ['date', t('Date'), (r) => r.date, (r) => r.date],
  ['energy', t('Calories'), (r) => fmt(r.energy), (r) => r.energy],
  ['goal', t('Goal'), (r) => fmt(r.energy_goal), (r) => r.energy_goal],
  ['burned', t('Burned'), (r) => dash(r.activity_energy), (r) => r.activity_energy],
  ['protein', t('Protein g'), (r) => fmt(r.protein), (r) => r.protein],
  ['carb', t('Carbs g'), (r) => fmt(r.carb), (r) => r.carb],
  ['fat', t('Fat g'), (r) => fmt(r.fat), (r) => r.fat],
  ['steps', t('Steps'), (r) => fmt(r.steps), (r) => r.steps],
  ['weight', t('Weight kg'), (r) => (r.weight != null ? fmt(r.weight, 1) : '–'), (r) => r.weight],
];
const HEALTH_COLS = [
  ['sleep', t('Sleep'), (r) => (r.sleep ? fmtDuration(r.sleep.asleep) : '–'), (r) => r.sleep?.asleep],
  ['rhr', t('Resting HR'), (r) => dash(r.rhr), (r) => r.rhr],
  ['sleep_hr', t('Sleep HR'), (r) => dash(r.sleep_hr), (r) => r.sleep_hr],
  ['hrv', 'HRV ms', (r) => dash(r.hrv, 1), (r) => r.hrv],
  ['hrv_deep', t('Deep HRV ms'), (r) => dash(r.hrv_deep, 1), (r) => r.hrv_deep],
];

const [savedKey, savedDir] = (store.get('tableSort') || '').split(':');
let sort = { key: savedKey || 'date', desc: savedDir !== 'asc' };
let filter = DAY_FILTERS.some(([k]) => k === store.get('tableFilter')) ? store.get('tableFilter') : 'all';
let query = '';
let cols = COLS, base = [];

export function renderTable(s) {
  cols = state.data.days.some((d) => d.sleep || d.rhr) ? [...COLS, ...HEALTH_COLS] : COLS;
  if (!cols.some(([k]) => k === sort.key)) sort = { key: 'date', desc: true };
  base = s.rows.filter((r) => logged(r) || r.weight != null || r.steps || r.sleep || r.rhr);
  initTools();
  drawHead();
  drawBody();
  renderExport(s);
}

let toolsReady = false;

function initTools() {
  if (toolsReady) return;
  toolsReady = true;
  const sel = $('#table-filter');
  sel.replaceChildren(...DAY_FILTERS.map(([k, label]) => h('option', { value: k }, label)));
  sel.value = filter;
  sel.onchange = () => {
    filter = sel.value;
    store.set('tableFilter', filter);
    drawBody();
  };
  $('#table-q').oninput = (e) => {
    query = e.target.value;
    drawBody();
  };
  initExport();
}

function drawHead() {
  const tr = h('tr');
  for (const [key, label] of cols) {
    const on = sort.key === key;
    const th = h('th', { scope: 'col', 'aria-sort': on ? (sort.desc ? 'descending' : 'ascending') : 'none' });
    const b = h('button', { class: 'th-sort' + (on ? ' on' : ''), type: 'button', title: t('Sort by: {column}', { column: label }) }, label);
    b.append(h('span', { class: 'sort-ind', 'aria-hidden': 'true' }, on ? (sort.desc ? '▼' : '▲') : '▼'));
    // Numbers start with the highest, dates with the newest; a second
    // click turns the order around.
    b.onclick = () => {
      sort = on ? { key, desc: !sort.desc } : { key, desc: true };
      store.set('tableSort', `${sort.key}:${sort.desc ? 'desc' : 'asc'}`);
      drawHead();
      drawBody();
    };
    th.append(b);
    tr.append(th);
  }
  const thead = h('thead');
  thead.append(tr);
  const table = $('#table');
  if (table.tHead) table.tHead.replaceWith(thead);
  else table.prepend(thead);
}

function drawBody() {
  const value = cols.find(([k]) => k === sort.key)[3];
  const rows = sortBy(filterDays(base, filter, query), value, sort.desc);
  const tbody = h('tbody');
  for (const r of rows) {
    const row = h('tr');
    cols.forEach(([, , text], ci) => {
      const td = h('td', {}, text(r));
      if (ci === 0 && hasDetail(r)) {
        td.classList.add('date-link');
        td.setAttribute('role', 'button');
        td.setAttribute('tabindex', '0');
        td.addEventListener('click', () => openDay(r.date));
        td.addEventListener('keydown', (e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); openDay(r.date); } });
      }
      row.append(td);
    });
    tbody.append(row);
  }
  if (!rows.length) {
    const td = h('td', { colspan: cols.length, class: 'empty-row' }, base.length ? 'No days match.' : 'Nothing recorded in this range.');
    const tr = h('tr');
    tr.append(td);
    tbody.append(tr);
  }
  const table = $('#table');
  if (table.tBodies[0]) table.tBodies[0].replaceWith(tbody);
  else table.append(tbody);
  const n = (k) => tn(k, '{n} day', '{n} days');
  $('#table-count').textContent = rows.length === base.length ? n(rows.length) : t('{shown} of {all}', { shown: fmt(rows.length), all: n(base.length) });
}

/* ---------- export ---------- */

// The export follows the selected range until its dates are changed by hand.
let touched = false, lastRange = null;

function initExport() {
  const from = $('#export-from'), to = $('#export-to');
  from.oninput = to.oninput = () => { touched = true; };
  // The chosen dates in order, or null while one is empty.
  const range = () => (from.value && to.value ? [from.value, to.value].sort() : null);
  $('#export').onsubmit = (e) => {
    e.preventDefault();
    const r = range();
    if (!r) return;
    const format = $('#export-format').value;
    download(`eumetria-${r[0]}-to-${r[1]}-${format}.zip`, 'application/zip', zip(exportFiles(state.data, r[0], r[1], format)));
  };
  // The report covers the same dates as the export.
  $('#report-print').onclick = () => {
    const r = range();
    if (r) printReport(...r);
  };
}

function renderExport(s) {
  const from = $('#export-from'), to = $('#export-to');
  const days = state.data.days;
  for (const el of [from, to]) {
    el.min = days[0].date;
    el.max = days.at(-1).date;
  }
  const range = `${state.range} ${state.from} ${state.to}`;
  if (!touched || range !== lastRange) {
    touched = false;
    from.value = s.rows[0].date;
    to.value = s.rows.at(-1).date;
  }
  lastRange = range;
  $('#export').title = t('Default: the selected range ({from} – {to})', { from: dfShort.format(parse(s.rows[0].date)), to: dfShort.format(parse(s.rows.at(-1).date)) });
}
