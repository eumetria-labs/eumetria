// The Food quality card: how the range's calories split by NOVA processing
// group and by Nutri-Score (Open Food Facts), the ultra-processed foods that
// contribute most and the additives that come up.

import { fmt } from './format.js';
import { $, h, showTip, hideTip } from './dom.js';
import { state } from './state.js';
import { foodName, hasHistory, historyLink, openFood } from './history.js';
import { t, tn } from './i18n.js';
import { NOVA, NUTRI, foodQuality, novaStep, nutriStep, offURL } from './quality.js';

const pct = (v) => (v > 0 && v < 0.005 ? '<1%' : `${fmt(v * 100)}%`);

export function renderFoodQuality(s) {
  const card = $('#quality-card');
  const products = state.data.products || {};
  card.hidden = !Object.keys(products).length;
  if (card.hidden) return;
  const q = foodQuality(s.rows, products, state.data.recipes || {});
  const days = s.rows.filter((r) => r.foods?.length).length || 1;
  $('#quality-sub').textContent = q.total
    ? t('Share of logged calories in this range · {pct} of them have Open Food Facts data', { pct: pct(q.coverage) })
    : t('No foods logged in this range');

  const unknown = (k) => ({
    key: k, step: k, short: '', label: k === 'unrated' ? t('Not rated') : t('No barcode'),
    note: k === 'unrated' ? t('Known to Open Food Facts, but without this rating') : t('Loose foods (fruit, vegetables) and foods Open Food Facts doesn’t know'),
  });
  bar($('#quality-nova'), t('Processing (NOVA)'), q, days, [
    ...NOVA.map(([n, label]) => ({ key: n, step: `q${novaStep(n)}`, short: String(n), label: `${n} · ${label}` })),
    unknown('unrated'), unknown('none'),
  ], q.nova);
  bar($('#quality-nutri'), 'Nutri-Score', q, days, [
    ...NUTRI.map((g) => ({ key: g, step: `q${nutriStep(g)}`, short: g.toUpperCase(), label: g.toUpperCase() })),
    unknown('unrated'), unknown('none'),
  ], q.nutri);
  lists(q, days);
}

// One 100 % stacked bar with its legend; the legend holds every value as
// text, so nothing depends on color alone.
function bar(host, title, q, days, segs, shares) {
  const track = h('div', { class: 'q-bar', role: 'img', 'aria-label': `${title}: ` + segs.filter((g) => shares[g.key] > 0).map((g) => `${g.label} ${pct(shares[g.key])}`).join(', ') });
  const legend = h('ul', { class: 'legend q-legend' });
  for (const g of segs) {
    const v = shares[g.key] || 0;
    if (v > 0) {
      const seg = h('div', { class: `q-seg ${g.step}` });
      seg.style.flexGrow = v;
      if (v >= 0.06 && g.short) seg.textContent = g.short;
      const tip = (e) => showTip(e.clientX, e.clientY, `${title} ${g.short || g.label}`, [
        { cls: `rect ${g.step}`, value: pct(v), label: g.short ? g.label.replace(/^\S+ · /, '') : g.note },
        { value: `${fmt((v * q.total) / days)} kcal`, label: t('per logged day') },
      ]);
      seg.addEventListener('pointerenter', tip);
      seg.addEventListener('pointermove', tip);
      seg.addEventListener('pointerleave', hideTip);
      track.append(seg);
    }
    if (v > 0 || g.short) {
      const li = h('li', { title: g.note || '' });
      li.append(h('span', { class: `key q-key ${g.step}` }), document.createTextNode(`${g.label} `), h('b', {}, pct(v)));
      legend.append(li);
    }
  }
  host.replaceChildren(h('h3', { class: 'mini-title' }, title), q.total ? track : h('p', { class: 'empty-tab' }, t('Nothing logged')), legend);
}

function lists(q, days) {
  const ultra = q.ultra.slice(0, 6);
  const left = h('div');
  left.append(h('h3', { class: 'mini-title' }, t('Most calories from ultra-processed foods')));
  if (ultra.length) {
    const max = ultra[0].kcal;
    const list = h('div', { class: 'hbars' });
    for (const u of ultra) {
      const row = h('div', { class: 'hbar' });
      // Logged foods open their history (its title links to Open Food
      // Facts), like the rows of Most logged foods; recipe ingredients link
      // to their product page directly.
      const linked = hasHistory(u.name);
      const url = !linked && offURL(u.x);
      const name = h(url ? 'a' : 'span', { class: 'name', title: u.name });
      if (url) { name.href = url; name.target = '_blank'; name.rel = 'noopener noreferrer'; }
      name.append(h('span', { class: 'txt' }, u.name));
      const val = h('span', { class: 'val' }, `${fmt(u.kcal / days)} kcal`);
      val.append(h('small', {}, t('per day') + ` · ${fmt(u.n)}×`));
      const track = h('div', { class: 'track' });
      const fill = h('i', { class: 'q5' });
      fill.style.width = `${(u.kcal / max) * 100}%`;
      track.append(fill);
      row.append(name, val, track);
      list.append(linked ? historyLink(row, u.name, openFood) : row);
    }
    left.append(list);
  } else left.append(h('p', { class: 'empty-tab' }, t('None in this range')));

  const right = h('div');
  right.append(h('h3', { class: 'mini-title' }, t('Additives')));
  if (q.additives.length) {
    const list = h('ul', { class: 'q-additives' });
    for (const a of q.additives.slice(0, 8)) {
      const li = h('li', { title: a.foods.join(', ') });
      const head = h('span');
      // Open Food Facts explains each additive on a page of its own.
      head.append(h('a', { href: `https://world.openfoodfacts.org/additive/${a.code.toLowerCase()}`, target: '_blank', rel: 'noopener noreferrer' }, a.code),
        document.createTextNode(' · ' + tn(a.foods.length, '{n} food', '{n} foods')));
      const foods = h('small', { class: 'muted' });
      a.foods.slice(0, 3).forEach((n, k) => foods.append(k ? ', ' : '', foodName(n, 'q-food')));
      if (a.foods.length > 3) foods.append(', …');
      li.append(head, foods);
      list.append(li);
    }
    right.append(list);
  } else right.append(h('p', { class: 'empty-tab' }, t('None listed for the foods in this range')));
  $('#quality-lists').replaceChildren(left, right);
}
