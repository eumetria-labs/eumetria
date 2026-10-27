// The share cards (share.js draws them): a meal, a recipe, a whole day, a
// workout, a night's sleep and the weight chart, and the buttons that open
// the share window for them.

import { MACROS, MEALS, MEAL_ICONS, actIcon, actLabel, actName, activityDetail, addDays, dfLong, dfShort, dfMonthYear, fmt, fmtAmount, fmtDistance, fmtDuration, fmtQty, iso, mealThumb, parse, recipeShare, recipeTotals, signed } from './format.js';
import { RESTLESS, STAGES, ZONES, dayActivities, fmtSleep, speedLabel, workoutDetail, workoutIcon, workoutName, zoneOf, zoneShares } from './health.js';
import { svg } from './dom.js';
import { state } from './state.js';
import { niceTicks } from './charts.js';
import { drawWeight, forecastFor, weightRows } from './weight.js';
import { weightGoalAt, weightGoalLine, weightTrendAt } from './insights.js';
import { drawHeartRate, loadRoute } from './workout.js';
import { drawHypnogram } from './recovery.js';
import { recipeOf } from './recipe.js';
import { chart, legendRow, list, meterBlock, openShare, photo, photoData, photoStrip, shareButton, splitBar, textW, tiles } from './share.js';
import { t } from './i18n.js';

const sum = (items, k) => items.reduce((a, it) => a + (it[k] || 0), 0);

// Protein, carbs and fat as a bar split by their share of the calories.
function macroSplit(g, title) {
  const kcal = MACROS.map((m) => (g[m.key] || 0) * m.kcal);
  const total = kcal.reduce((a, b) => a + b, 0);
  if (!total) return null;
  return splitBar(MACROS.map((m, i) => ({ cls: m.cls, label: m.label, value: `${fmt(g[m.key])} g`, share: kcal[i] / total })), title);
}

/* ---------- food ---------- */

export async function mealCard(day, meal) {
  const items = (day.foods || []).filter((f) => f.m === meal);
  const name = MEALS.find(([k]) => k === meal)?.[1];
  const kcal = items.length ? sum(items, 'e') : day.meals?.[meal];
  const img = await photoData(mealThumb(day, meal, state.data.recipes));
  const macros = { protein: sum(items, 'pr'), carb: sum(items, 'c'), fat: sum(items, 'f') };
  return {
    icon: MEAL_ICONS[meal], title: name, sub: dfLong.format(parse(day.date)), accent: 's2',
    blocks: [
      img && photo(img),
      tiles([[t('Calories'), fmt(kcal), 'kcal'], ...MACROS.map((m) => [m.label, items.length ? fmt(macros[m.key]) : '–', 'g'])]),
      items.length && list(items.map((f) => ({ name: f.n, note: fmtAmount(f), value: `${fmt(f.e)} kcal` })), t('Foods')),
    ],
  };
}

export async function recipeCard(day, f) {
  const rec = recipeOf(f);
  const share = recipeShare(f, rec);
  const items = rec.items || [];
  const tot = recipeTotals(items, share);
  const meal = MEALS.find(([k]) => k === f.m)?.[1];
  const img = await photoData(rec.img);
  // Macros come from the ingredients, which are only right for the version eaten.
  const exact = items.length && !f.rs;
  return {
    icon: MEAL_ICONS[f.m] || '🍽️', title: rec.name, accent: 's2',
    sub: [meal, dfLong.format(parse(day.date)), fmtQty(f.a || 1, 'portion')].filter(Boolean).join(' · '),
    blocks: [
      img && photo(img),
      tiles([[t('Calories'), fmt(f.e), 'kcal'], ...MACROS.map((m) => [m.label, exact ? fmt(tot[m.key]) : '–', 'g'])]),
      items.length && list(items.map((it) => ({
        name: it.n, note: fmtAmount({ a: it.a * share, u: it.u, s: it.s, sq: it.sq * share }), value: `${fmt(it.e * share)} kcal`,
      })), t('Ingredients')),
    ],
  };
}

export async function dayCard(day) {
  const goal = day.energy_goal || day.energy_base;
  const meals = MEALS.filter(([k]) => day.meals?.[k] > 0 || (day.foods || []).some((f) => f.m === k));
  const imgs = (await Promise.all(meals.map(([k]) => photoData(mealThumb(day, k, state.data.recipes))))).filter(Boolean);
  const acts = dayActivities(day);
  return {
    icon: '📅', title: t('My day'), sub: dfLong.format(parse(day.date)), accent: 's1',
    blocks: [
      imgs.length && photoStrip(imgs),
      day.energy > 0 && meterBlock(t('Eaten'), fmt(day.energy), goal ? fmt(goal) : '', 'kcal'),
      tiles([
        day.activity_energy && [t('Burned'), fmt(day.activity_energy), 'kcal'],
        day.steps && [t('Steps'), fmt(day.steps), ''],
        day.sleep?.asleep && [t('Sleep'), fmtSleep(day.sleep.asleep), ''],
        day.weight && [t('Weight'), fmt(day.weight, 1), 'kg'],
      ]),
      macroSplit(day, t('Macronutrients')),
      meals.length && list(meals.map(([k, name]) => ({
        icon: MEAL_ICONS[k], name,
        note: (day.foods || []).filter((f) => f.m === k).map((f) => f.n).join(', '),
        value: `${fmt(day.meals?.[k] ?? sum((day.foods || []).filter((f) => f.m === k), 'e'))} kcal`,
      })), t('Meals'), { minRows: 4 }),
      acts.length && { ...list(acts.map(({ a, w }) => {
        const key = w && w.type && w.type !== 'OTHER' ? workoutName(w.type) : actName(a.name);
        return { icon: w ? workoutIcon(w.type) : actIcon(key), name: actLabel(key), note: a ? activityDetail(a) : workoutDetail(w), value: `${fmt(a ? a.energy : w.energy)} kcal` };
      }), t('Activities'), { minRows: 1 }), optional: true },
    ].filter(Boolean),
  };
}

/* ---------- workout ---------- */

export async function workoutCard(day, w, { route: withRoute } = {}) {
  const items = [
    [t('Duration'), w.dur ? fmtDuration(w.dur / 60) : '–', ''],
    w.dist && [t('Distance'), fmtDistance(w.dist), ''],
    w.dist && [/RUN|WALK|HIK/.test(w.type) ? t('Avg pace') : t('Avg speed'), speedLabel(w), ''],
    w.energy && [t('Calories'), fmt(w.energy), 'kcal'],
    w.hr_avg && [t('Avg heart rate'), fmt(w.hr_avg), 'bpm'],
    w.hr_max && [t('Max heart rate'), fmt(w.hr_max), 'bpm'],
    w.azm != null && [t('Zone minutes'), fmt(w.azm), 'min'],
    w.elev && [t('Elevation gain'), fmt(w.elev), 'm'],
  ].filter(Boolean).slice(0, 6);
  const route = withRoute ? await loadRoute(w) : null;
  const sub = [dfLong.format(parse(day.date)), t('started {time}', { time: w.start })];
  if (w.name && actName(w.name) !== workoutName(w.type)) sub.unshift(w.name);
  const shares = w.zones?.some((z) => z > 0) ? zoneShares(w.zones) : null;
  return {
    icon: workoutIcon(w.type), title: actLabel(workoutName(w.type)), sub: sub.join(' · '), accent: 'z3',
    blocks: [
      tiles(items),
      w.hr?.length > 1 && chart((g, box) => drawHeartRate(g, w, box.w, box.h), { h: 170, min: 100, max: 240, title: t('Heart rate') }),
      shares && splitBar(ZONES.map((z, i) => ({ cls: z.cls, label: z.label, value: `${shares[i].min} min`, share: shares[i].pct / 100 })).reverse(), t('Heart rate zones')),
      route?.length > 1 && chart((g, box) => drawRoute(g, route, w, box), { h: 220, min: 110, max: 340, title: t('Route'), note: fmtDistance(w.dist || 0) }),
    ].filter(Boolean),
  };
}

// Whether a workout has a route to offer.
const hasRoute = (w) => w.route?.length > 1 || !!w.route_src;

// The GPS track as a line, without a map, scaled to fit the box; coloured
// by heart rate zone when the points carry heart rate.
function drawRoute(g, route, w, { w: bw, h: bh }) {
  const lat0 = route.reduce((a, p) => a + p[0], 0) / route.length;
  const k = Math.cos((lat0 * Math.PI) / 180);
  const px = route.map((p) => [p[1] * k, -p[0]]);
  const xs = px.map((p) => p[0]), ys = px.map((p) => p[1]);
  const x0 = Math.min(...xs), y0 = Math.min(...ys);
  const sw = Math.max(...xs) - x0 || 1e-9, sh = Math.max(...ys) - y0 || 1e-9;
  const pad = 10, s = Math.min((bw - pad * 2) / sw, (bh - pad * 2) / sh);
  const ox = (bw - sw * s) / 2, oy = (bh - sh * s) / 2;
  const at = (i) => [ox + (px[i][0] - x0) * s, oy + (px[i][1] - y0) * s];
  g.append(svg('rect', { width: bw, height: bh, rx: 16, class: 'c-tile' }));
  // Runs of the same zone, each sharing its end point with the next.
  let d = '', zone = null;
  const flush = () => {
    if (d) g.append(svg('path', { d, class: 'c-route z' + (zone + 1) }));
    d = '';
  };
  route.forEach((p, i) => {
    const z = w.limits && p[4] > 0 ? zoneOf(p[4], w.limits) : -1;
    const [x, y] = at(i);
    if (z !== zone && i) {
      d += `L${x.toFixed(1)},${y.toFixed(1)}`;
      flush();
    }
    if (z !== zone || !i) d += `M${x.toFixed(1)},${y.toFixed(1)}`;
    else d += `L${x.toFixed(1)},${y.toFixed(1)}`;
    zone = z;
  });
  flush();
  const [sx, sy] = at(0), [ex, ey] = at(route.length - 1);
  g.append(svg('circle', { cx: sx, cy: sy, r: 5, class: 'c-fill ink c-route-end' }), svg('circle', { cx: ex, cy: ey, r: 5, class: 'c-fill ink2 c-route-end' }));
}

/* ---------- sleep ---------- */

export function sleepCard(day) {
  const sl = day.sleep;
  const staged = (sl.deep || 0) + (sl.light || 0) + (sl.rem || 0);
  const total = staged + (sl.awake || 0);
  return {
    icon: '😴', title: t('Sleep'), sub: t('The night to {date}', { date: dfLong.format(parse(day.date)) }), accent: 'st-rem',
    blocks: [
      tiles([
        [t('Asleep'), fmtSleep(sl.asleep), ''],
        [t('In bed'), `${sl.start}–${sl.end}`, ''],
        day.hrv != null && [t('HRV'), fmt(day.hrv, 1), 'ms'],
        day.sleep_hr != null ? [t('Sleeping heart rate'), fmt(day.sleep_hr), 'bpm'] : day.rhr != null && [t('Resting heart rate'), fmt(day.rhr), 'bpm'],
      ]),
      sl.stages?.length && chart((g, box) => drawHypnogram(g, sl, box.w, box.h), { h: 170, min: 120, max: 260, title: t('Sleep stages') }),
      staged && list(STAGES.map((st) => ({ key: st.cls, name: st.label, note: `${Math.round(((sl[st.key] || 0) / total) * 100)} %`, value: fmtSleep(sl[st.key] || 0) })), '', { rowH: 28, minRows: 4 }),
      sl.restless?.length && !staged && list([{ key: RESTLESS.cls, name: RESTLESS.label, value: `${fmt(sl.restless_min)} min` }]),
    ].filter(Boolean),
  };
}

/* ---------- weight ---------- */

// The chart's frame in box (w × h): y grid with labels and about five date
// labels; returns the ctx the page's chart code draws with.
function frame(g, { w, h }, { n, dates, lo, hi, yFmt }) {
  const ticks = niceTicks(lo, hi, 4);
  const labels = ticks.map(yFmt);
  const m = { t: 22, r: 4, b: 20, l: Math.max(...labels.map((l) => textW(l, 11, 400))) + 10 };
  const pw = w - m.l - m.r, ph = h - m.t - m.b;
  const y = (v) => m.t + ph - ((v - ticks[0]) / (ticks.at(-1) - ticks[0])) * ph;
  const step = pw / n;
  const x = (i) => m.l + (i + 0.5) * step;
  const axis = svg('g', { class: 'axis' });
  ticks.forEach((v, k) => {
    axis.append(svg('line', { x1: m.l, x2: w - m.r, y1: y(v), y2: y(v), class: k ? 'gridline' : 'baseline' }));
    axis.append(svg('text', { x: m.l - 6, y: y(v) + 4, 'text-anchor': 'end' }, labels[k]));
  });
  const every = Math.max(1, Math.ceil(n / 5));
  for (let i = n - 1; i >= 0; i -= every) {
    const anchor = i === n - 1 ? 'end' : 'middle';
    axis.append(svg('text', { x: anchor === 'end' ? Math.min(w - m.r, x(i) + step / 2) : x(i), y: h - 4, 'text-anchor': anchor }, dfShort.format(dates[i])));
  }
  g.append(axis);
  return { x, y, step, top: m.t, bottom: m.t + ph, right: w - m.r };
}

export function weightCard(rows) {
  const trend = rows.map((r) => weightTrendAt(state.byDate, r.date));
  const pts = rows.map((r) => r.weight ?? null);
  const f = forecastFor(rows);
  const n = f?.n ?? rows.length;
  const ahead = f?.ahead ?? [];
  const dates = Array.from({ length: n }, (_, i) => addDays(parse(rows[0].date), i));
  const goalAt = dates.map((d) => weightGoalAt(state.data.goal_changes, iso(d), state.data.goals?.weight));
  const vals = [...pts, ...trend, ...ahead].filter((v) => v != null);
  const { goals: shown, lo, hi } = weightGoalLine(goalAt, Math.min(...vals), Math.max(...vals));
  const first = trend.find((v) => v != null), last = trend.findLast((v) => v != null);
  const goal = state.data.goals?.weight;
  const from = parse(rows[0].date), to = parse(rows.at(-1).date);
  const legend = [['dot', t('Weigh-in')], ['s1', t('7-day average')]];
  if (shown.some((v) => v != null)) legend.push(['dash', t('Goal')]);
  return {
    icon: '⚖️', title: t('Weight'), accent: 's1',
    sub: `${dfShort.format(from)} – ${dfShort.format(to)} ${to.getUTCFullYear()}`,
    blocks: [
      tiles([
        [t('Now'), last != null ? 'Ø ' + fmt(last, 1) : '–', 'kg'],
        first != null && last != null && [t('Change'), signed(last - first, 1), 'kg'],
        goal && [t('Goal'), fmt(goal, 1), 'kg'],
        f?.ol.fc?.date && [t('Goal reached'), dfMonthYear.format(parse(f.ol.fc.date)), ''],
      ]),
      chart((g, box) => {
        const ctx = frame(g, box, { n, dates, lo: lo - 0.5, hi: hi + 0.5, yFmt: (v) => fmt(v, Number.isInteger(v) ? 0 : 1) });
        drawWeight(g, ctx, { days: rows.length, trend, pts, goals: shown, marks: [], f, id: 'fade-share-weight', dot: { r: rows.length > 120 ? 2.5 : 3.5, cls: 'mark-dot s1-soft' } });
      }, { h: 300, min: 200, max: 520 }),
      legendRow(legend),
    ].filter(Boolean),
  };
}

/* ---------- buttons ---------- */

// The day window's button: the whole day, each meal, each Fitbit workout
// and the night, starting with what the open tab shows.
export function dayShareButton(day, currentTab) {
  return shareButton((btn) => {
    const options = [{ key: 'day', label: t('Whole day'), name: `day-${day.date}`, build: () => dayCard(day) }];
    for (const [k, name] of MEALS) {
      if (day.meals?.[k] > 0 || (day.foods || []).some((f) => f.m === k)) {
        options.push({ key: k, label: name, name: `${k}-${day.date}`, build: () => mealCard(day, k) });
      }
    }
    for (const w of day.workouts || []) {
      options.push({ key: 'w:' + w.id, label: actLabel(workoutName(w.type)), name: `workout-${day.date}`, route: hasRoute(w), build: (o) => workoutCard(day, w, o) });
    }
    if (day.sleep) options.push({ key: 'sleep', label: t('Sleep'), name: `sleep-${day.date}`, build: () => sleepCard(day) });
    const tab = currentTab();
    const firstWorkout = options.find((o) => o.key.startsWith('w:'))?.key;
    openShare(options, tab === 'activities' ? firstWorkout || 'day' : tab, btn);
  });
}

export function recipeShareButton(day, f) {
  return shareButton((btn) => openShare([{ key: 'recipe', label: '', name: `recipe-${day.date}`, build: () => recipeCard(day, f) }], 'recipe', btn));
}

export function workoutShareButton(day, w) {
  return shareButton((btn) => openShare([{ key: 'w', label: '', name: `workout-${day.date}`, route: hasRoute(w), build: (o) => workoutCard(day, w, o) }], 'w', btn));
}

// On the Weight card, next to "Save as image": the range the card shows.
export function addWeightShare() {
  const snap = document.querySelector('#weight-card .snap-btn');
  if (!snap || document.querySelector('#weight-card .share-btn')) return;
  snap.after(shareButton((btn) => {
    const rows = weightRows();
    if (!rows.length) return;
    openShare([{ key: 'weight', label: '', name: `weight-${rows.at(-1).date}`, build: () => weightCard(rows) }], 'weight', btn);
  }, 'snap-btn'));
}
