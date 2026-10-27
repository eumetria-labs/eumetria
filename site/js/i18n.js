// Translation: the page is English unless the visitor picks German (footer,
// or "#lang=de" in a link). The code is written in English and every text it
// shows goes through t(), which looks it up in the German dictionary
// (de.js); anything missing stays English. tests/i18n.test.js checks that
// every text has its German entry.
//
// Numbers are written the German way in both languages (1.000 kcal, 78,6 kg,
// NUM_LOCALE in format.js); only words and dates change.
//
// Switching stores the choice and reloads the page, so modules can translate
// their texts once when they load. No DOM access, so Node can test this file.

import { DE } from './de.js';

const LANGS = ['en', 'de'];
const STORE_KEY = 'lang';

function initial() {
  try {
    const fromLink = new URLSearchParams(globalThis.location?.hash.slice(1)).get('lang');
    if (LANGS.includes(fromLink)) return fromLink;
    const saved = globalThis.localStorage?.getItem(STORE_KEY);
    if (LANGS.includes(saved)) return saved;
  } catch { /* no storage or location (Node, private mode) */ }
  return 'en';
}

export const LANG = initial();
// Dates, weekdays and "5 minutes ago" in the page's language.
export const DATE_LOCALE = LANG === 'de' ? 'de-DE' : 'en-US';

const nf = new Intl.NumberFormat('de-DE', { maximumFractionDigits: 1 });

// In German "Breakfast" comes back as "Frühstück", "{n} days" with n = 3 as "3 Tage".
// A key may carry a context after "|" when one English text needs two
// German ones: "Lunch|tab" reads "Lunch" in English, and German has room
// for "Mittagessen" in a title but only "Mittag" on a tab.
export function t(text, vars, lang = LANG) {
  const out = (lang === 'de' && DE[text]) || text.split('|')[0];
  return vars ? out.replace(/\{(\w+)\}/g, (m, k) => (k in vars ? String(vars[k]) : m)) : out;
}

// One or many: tn(3, '{n} day', '{n} days') → '3 days'. n is formatted like
// every other number on the page unless vars bring their own.
export function tn(n, one, other, vars, lang = LANG) {
  return t(n === 1 ? one : other, { n: nf.format(n), ...vars }, lang);
}

export function setLang(lang) {
  try { localStorage.setItem(STORE_KEY, lang); } catch { /* storage unavailable */ }
  // A language in the link would win over the choice; drop it.
  const params = new URLSearchParams(location.hash.slice(1));
  params.delete('lang');
  location.hash = params.toString();
  location.reload();
}

// The static texts of index.html: every text node and aria-label, title
// and placeholder attribute the dictionary knows, translated once at start.
export const PAGE_ATTRS = ['aria-label', 'title', 'placeholder'];
export function translatePage(root = document.body) {
  document.documentElement.lang = LANG;
  if (LANG === 'en') return;
  const walk = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
  for (let n = walk.nextNode(); n; n = walk.nextNode()) {
    const text = n.nodeValue.trim().replace(/\s+/g, ' ');
    if (DE[text]) n.nodeValue = n.nodeValue.replace(/\S[\s\S]*\S|\S/, DE[text]);
  }
  for (const el of root.querySelectorAll(PAGE_ATTRS.map((a) => `[${a}]`).join(','))) {
    for (const a of PAGE_ATTRS) {
      const v = el.getAttribute(a);
      if (v && DE[v]) el.setAttribute(a, DE[v]);
    }
  }
}
