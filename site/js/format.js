// Formatting and small data helpers. No DOM access, so Node can test this file.

import { DATE_LOCALE, t, tn } from './i18n.js';

// Dates and words follow the page's language (i18n.js).
export const LOCALE = DATE_LOCALE;
// Numbers are written the German way (1.000 kcal, 78,6 kg) in every language.
export const NUM_LOCALE = 'de-DE';
export const MEALS = [['breakfast', t('Breakfast')], ['lunch', t('Lunch')], ['dinner', t('Dinner')], ['snack', t('Snacks')]];
// Shorter names for tabs and the Today card's meal chips.
export const MEAL_TABS = { breakfast: t('Breakfast|tab'), lunch: t('Lunch|tab'), dinner: t('Dinner|tab'), snack: t('Snacks|tab') };
export const MEAL_ICONS = { breakfast: '🍳', lunch: '🥗', dinner: '🍝', snack: '🍎' };
// The Today card and the Macronutrients card show these in this order and colour.
export const MACROS = [
  { key: 'protein', cls: 's1', label: t('Protein'), kcal: 4 },
  { key: 'carb', cls: 's2', label: t('Carbs'), kcal: 4 },
  { key: 'fat', cls: 's3', label: t('Fat'), kcal: 9 },
];

export const nf1 = new Intl.NumberFormat(NUM_LOCALE, { minimumFractionDigits: 1, maximumFractionDigits: 1 });
const nf0 = new Intl.NumberFormat(NUM_LOCALE, { maximumFractionDigits: 0 });
const nfQty = new Intl.NumberFormat(NUM_LOCALE, { maximumFractionDigits: 1 });
const nf2 = new Intl.NumberFormat(NUM_LOCALE, { minimumFractionDigits: 2, maximumFractionDigits: 2 });
export const compact = new Intl.NumberFormat(NUM_LOCALE, { notation: 'compact', maximumFractionDigits: 1 });
export const fmt = (v, digits = 0) => (v == null || Number.isNaN(v) ? '–' : (digits ? nf1 : nf0).format(v));
export const signed = (v, digits = 0) => (v > 0 ? '+' : v < 0 ? '−' : '±') + fmt(Math.abs(v), digits);

// Dates are handled as UTC midnights so DST never shifts a day.
export const parse = (s) => new Date(s + 'T00:00:00Z');
export const iso = (d) => d.toISOString().slice(0, 10);
export const addDays = (d, n) => new Date(d.getTime() + n * 864e5);
export const dfShort = new Intl.DateTimeFormat(LOCALE, { month: 'short', day: 'numeric', timeZone: 'UTC' });
export const dfLong = new Intl.DateTimeFormat(LOCALE, { weekday: 'short', month: 'short', day: 'numeric', year: 'numeric', timeZone: 'UTC' });
export const dfDay = new Intl.DateTimeFormat(LOCALE, { weekday: 'long', month: 'long', day: 'numeric', timeZone: 'UTC' });
export const dfMonth = new Intl.DateTimeFormat(LOCALE, { month: 'short', timeZone: 'UTC' });
export const dfMonthYear = new Intl.DateTimeFormat(LOCALE, { month: 'short', year: 'numeric', timeZone: 'UTC' });

// The date that is "today" in the sync's time zone (falls back to the viewer's).
export function todayISO(timeZone, now = new Date()) {
  try {
    return new Intl.DateTimeFormat('en-CA', { timeZone: timeZone || undefined }).format(now);
  } catch {
    return new Intl.DateTimeFormat('en-CA').format(now);
  }
}

export const mean = (xs) => (xs.length ? xs.reduce((a, b) => a + b, 0) / xs.length : null);
export const logged = (r) => r?.energy > 0;
export const avgOf = (rows, key, pred = (r) => r[key] > 0) => mean(rows.filter(pred).map((r) => r[key]));
export const hasDetail = (r) => !!r && !!(r.foods?.length || r.activities?.length || r.photos?.length || logged(r) || r.workouts?.length || r.sleep);

export function fmtQty(v, unit) {
  if (unit === 'portion') return tn(v, '{n} portion', '{n} portions');
  return `${nfQty.format(v)} ${unit}`;
}
// An average of logged amounts: whole grams and millilitres ("126 g", not
// "126,3 g"); portions and other units keep one decimal.
export const fmtAvgQty = (v, unit) => fmtQty(unit === 'g' || unit === 'ml' ? Math.round(v) : v, unit);

// "20 ml", "1,5 portions", or "2 × slice · 60 g" for food entered by serving.
export function fmtAmount(f) {
  if (!f.a) return '';
  const base = fmtQty(f.a, f.u);
  return f.s && f.sq ? `${nfQty.format(f.sq)} × ${servingName(f.s)} · ${base}` : base;
}

// Yazio's serving names ("piece", "slice", "tablespoon"), in the page's language.
export const servingName = (s) => t(s.replace(/_/g, ' '));

// Recipes: the ingredients in stats.json are for the whole recipe; an entry
// of a portions ate this share of them.
export const recipeShare = (f, rec) => (f.a || 1) / (rec.portions || 1);

export function recipeTotals(items, share) {
  const sum = (k) => items.reduce((a, it) => a + (it[k] || 0), 0) * share;
  return { energy: sum('e'), protein: sum('pr'), carb: sum('c'), fat: sum('f') };
}

// The picture for a meal: its photo, or else the photo of a recipe eaten in
// it. Null when there is neither.
export function mealThumb(day, meal, recipes = {}) {
  const photo = (day?.photos || []).find((p) => p.m === meal);
  if (photo) return photo.src;
  for (const f of day?.foods || []) if (f.m === meal && recipes[f.r]?.img) return recipes[f.r].img;
  return null;
}

export function fmtDuration(mins) {
  const m = Math.round(mins);
  if (m < 60) return `${m} min`;
  return m % 60 ? `${Math.floor(m / 60)} h ${m % 60} min` : `${m / 60} h`;
}

// Exercise distances arrive in metres.
export function fmtDistance(m) {
  return m < 1000 ? `${Math.round(m)} m` : `${nf1.format(m / 1000)} km`;
}

export function activityDetail(a, sep = ' · ') {
  return [a.dur ? fmtDuration(a.dur) : '', a.dist ? fmtDistance(a.dist) : ''].filter(Boolean).join(sep);
}

// Built-in activities arrive as English keys ("linefishing"), custom ones as
// typed in the app, often German. Both become one English name, which is the
// activity's key (links, history, search); actLabel shows it translated.
const ACTIVITY_NAMES = {
  walking: 'Walking', running: 'Running', jogging: 'Jogging', cycling: 'Cycling', swimming: 'Swimming',
  rowing: 'Rowing', climbing: 'Climbing', hiking: 'Hiking', yoga: 'Yoga', pilates: 'Pilates', dancing: 'Dancing',
  linefishing: 'Fishing', fishing: 'Fishing', ropejumping: 'Jump rope', frisbee: 'Frisbee', crossfit: 'CrossFit',
  hiit: 'HIIT', strengthtraining: 'Strength training', weighttraining: 'Strength training', gardening: 'Gardening',
  krafttraining: 'Strength training', hausarbeit: 'Housework', 'aktivität': 'Activity', training: 'Training',
  'indoor-klettern': 'Indoor climbing', klettern: 'Climbing', bouldern: 'Bouldering', laufen: 'Running',
  joggen: 'Jogging', radfahren: 'Cycling', fahrradfahren: 'Cycling', schwimmen: 'Swimming', wandern: 'Hiking',
  spazieren: 'Walking', spazierengehen: 'Walking', gehen: 'Walking', gartenarbeit: 'Gardening', tanzen: 'Dancing',
  'fußball': 'Football', fussball: 'Football', rudern: 'Rowing', seilspringen: 'Jump rope', angeln: 'Fishing',
  'rückentraining': 'Back training', dehnen: 'Stretching', putzen: 'Cleaning',
};
// Every activity key actName gives, for the translation test.
export const ACTIVITY_KEYS = [...new Set(['Activity', ...Object.values(ACTIVITY_NAMES)])];
// Keyed by the English display name from actName.
const ACTIVITY_ICONS = {
  'Strength training': '🏋️', CrossFit: '🤸', Walking: '🚶', Running: '🏃', Jogging: '🏃', Cycling: '🚴',
  Swimming: '🏊', Rowing: '🚣', Climbing: '🧗', 'Indoor climbing': '🧗', Bouldering: '🧗', Hiking: '🥾',
  Yoga: '🧘', Pilates: '🧘', Stretching: '🧘', Dancing: '💃', Fishing: '🎣', 'Jump rope': '🪢', Frisbee: '🥏',
  Football: '⚽', Tennis: '🎾', HIIT: '⚡', Housework: '🧹', Cleaning: '🧹', Gardening: '🌱',
};
export const actIcon = (name) => ACTIVITY_ICONS[name] || '🏅';
// An activity's key (from actName or workoutName) as shown on the page.
export const actLabel = (key) => t(key);

export function actName(n) {
  if (!n) return 'Activity';
  const known = ACTIVITY_NAMES[n.trim().toLowerCase()];
  if (known) return known;
  const words = n.trim().replace(/[_-]+/g, ' ');
  return words.charAt(0).toUpperCase() + words.slice(1);
}

// syncEvery turns a sync interval into "every 30 minutes", "every hour", "every 3 hours".
export function syncEvery(mins) {
  if (!(mins > 0)) return '';
  if (mins % 60 === 0) return tn(mins / 60, 'every hour', 'every {n} hours');
  return tn(mins, 'every minute', 'every {n} minutes');
}

const rtfNarrow = new Intl.RelativeTimeFormat(LOCALE, { numeric: 'auto', style: 'narrow' });

// "18 min ago", "3 hr ago", "2 days ago"
export function syncAgo(iso, now = Date.now()) {
  const mins = Math.max(0, Math.round((now - new Date(iso)) / 60000));
  if (mins < 60) return rtfNarrow.format(-mins, 'minute');
  if (mins < 48 * 60) return rtfNarrow.format(-Math.round(mins / 60), 'hour');
  return rtfNarrow.format(-Math.round(mins / 1440), 'day');
}

// Warnings for the top of the page when syncing stopped working: the data is
// older than three sync intervals (a day when the interval is unknown), or
// the Fitbit sync fails. A Fitbit error that clears on the next sync is not
// worth a warning, so only one older than that limit shows, unless Google
// access must be renewed, which never fixes itself.
export function syncNotices(data, now = Date.now()) {
  const limit = data.sync_interval_minutes ? 3 * data.sync_interval_minutes * 60000 : 864e5;
  const out = [];
  if (data.generated_at && now - new Date(data.generated_at) > limit) {
    out.push(t('The last sync was {ago}, longer than expected. Syncing may be failing.', { ago: syncAgo(data.generated_at, now) }));
  }
  const hs = data.health_sync;
  if (hs?.failing) {
    const last = hs.last_success ? ' ' + t('Last Fitbit sync {ago}.', { ago: syncAgo(hs.last_success, now) }) : '';
    if (hs.auth) out.push(t("Fitbit data isn't syncing: Google access has to be renewed (eumetria -google-auth).") + last);
    else if (!hs.last_success || now - new Date(hs.last_success) > limit) out.push(t("Fitbit data isn't syncing.") + last);
  }
  return out;
}

// Warnings for the site owner about the Anthropic credit behind the weekly
// review (review_status). Visitors never see them.
export function reviewNotices(data) {
  const st = data.review_status;
  if (!st) return [];
  if (st.billing) return [t("The weekly review couldn't be written: the Anthropic credit is used up or the workspace's spend limit is reached. Buy credits in the Anthropic Console; it's tried again on every sync.")];
  if (st.credit_low) {
    const weeks = tn(st.weeks_left, 'one more weekly review', 'about {n} more weekly reviews');
    return [t('The Anthropic credit is running low: about ${usd} left, enough for {weeks}. Buy credits in the Anthropic Console before it runs out.', { usd: nf2.format(st.remaining_usd), weeks })];
  }
  return [];
}

// The heights of a share card's blocks (share.js) in avail px. A block has
// a natural height h and may shrink to min, grow to max, or be dropped when
// optional. Too tall: every block gives in proportion to what it can give, then optional
// blocks go from the end. Room to spare: blocks that can grow share it.
// Returns the blocks kept, each with its height.
export function fitBlocks(blocks, avail, gap) {
  const list = blocks.map((b) => ({ block: b, h: b.h }));
  const total = () => list.reduce((a, x) => a + x.h, 0) + gap * Math.max(0, list.length - 1);
  const over = total() - avail;
  if (over > 0) {
    const give = list.reduce((a, x) => a + x.h - (x.block.min ?? x.h), 0);
    const k = give ? Math.min(1, over / give) : 0;
    for (const x of list) x.h -= (x.h - (x.block.min ?? x.h)) * k;
  }
  for (let i = list.length - 1; i >= 0 && total() > avail; i--) if (list[i].block.optional) list.splice(i, 1);
  const spare = avail - total();
  if (spare > 0) {
    const take = list.reduce((a, x) => a + (x.block.max ?? x.h) - x.h, 0);
    const k = take ? Math.min(1, spare / take) : 0;
    for (const x of list) x.h += ((x.block.max ?? x.h) - x.h) * k;
  }
  return list.map((x) => ({ block: x.block, h: Math.floor(x.h) }));
}

// A session's browser and system from its user agent, e.g. "Safari on
// iPhone"; the raw string when neither is known.
export function deviceName(ua = '') {
  const browser = /Edg\//.test(ua) ? 'Edge' : /Firefox\//.test(ua) ? 'Firefox' : /(Chrome|CriOS)\//.test(ua) ? 'Chrome' : /Safari\//.test(ua) ? 'Safari' : '';
  const system = /iPhone/.test(ua) ? 'iPhone' : /iPad/.test(ua) ? 'iPad' : /Android/.test(ua) ? 'Android' : /Mac OS X/.test(ua) ? 'macOS' : /Windows/.test(ua) ? 'Windows' : /Linux/.test(ua) ? 'Linux' : '';
  if (browser && system) return t('{browser} on {system}', { browser, system });
  return browser || system || ua || t('Unknown device');
}
