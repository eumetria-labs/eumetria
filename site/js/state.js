// Shared page state. Modules read and update these fields directly.

export const state = {
  data: null,
  byDate: new Map(),
  range: '30', // '7', '30', '90', '365', 'all' or 'custom'
  from: null, // first and last day of a custom range, ISO dates
  to: null,
  today: '', // "today" in the sync's time zone; its partial totals stay out of averages
  animateNext: true, // entrance animations run on first render, new data and range changes
};

export const reduceMotion = matchMedia('(prefers-reduced-motion: reduce)');

export const store = {
  get(k) { try { return localStorage.getItem(k); } catch { return null; } },
  set(k, v) { try { localStorage.setItem(k, v); } catch { /* storage unavailable */ } },
};
