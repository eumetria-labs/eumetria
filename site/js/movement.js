// The Movement card: steps, calories burned by activities and Fitbit's
// active zone minutes, one chart below the other.

import { actLabel, actName, activityDetail, avgOf, compact, fmt, mean } from './format.js';
import { $ } from './dom.js';
import { can, state } from './state.js';
import { barChart, bucketTitle, drawStacks, timeChart } from './charts.js';
import { t } from './i18n.js';

export function renderMovement(s) {
  const { buckets, weekly, complete } = s;
  // Without view.activity only Fitbit's zone minutes remain (renderZoneMinutes).
  const hasSteps = can('view.activity');
  $('#steps-part').hidden = !hasSteps;
  $('#movement-card').hidden = !hasSteps && !state.data.days.some((d) => d.azm);
  $('#steps-sub').textContent = weekly ? t('weekly averages') : '';
  if (!hasSteps) return void ($('#activity-part').hidden = true);
  const goal = state.data.goals?.steps;
  const steps = buckets.map((b) => avgOf(b.rows, 'steps'));
  const burned = buckets.map((b) => avgOf(b.rows, 'activity_energy'));
  const hasActivity = state.data.days.some((d) => d.activity_energy > 0);
  const hasAzm = state.data.days.some((d) => d.azm);
  const avgSteps = avgOf(complete, 'steps');
  // Days without activities count as 0 kcal burned, not as missing.
  const avgBurn = mean(complete.map((r) => r.activity_energy || 0));
  $('#steps-sub').textContent = t('Daily average {steps} steps', { steps: fmt(avgSteps) })
    + (hasActivity ? ' · ' + t('{kcal} kcal burned', { kcal: fmt(avgBurn) }) : '') + (weekly ? ' · ' + t('weekly averages') : '');

  barChart($('#chart-steps'), s, {
    aria: t('Steps per day'),
    series: { cls: 's3', label: t('steps'), values: steps, unit: '' },
    goal: goal ? { value: goal, label: t('Goal {steps}', { steps: compact.format(goal) }) } : null,
    // Only the bottom chart of the card labels the dates.
    noXLabels: hasActivity || hasAzm,
  });
  $('#activity-part').hidden = !hasActivity;
  if (!hasActivity) return;
  barChart($('#chart-activity'), s, {
    aria: t('Calories burned by activities per day'),
    series: { cls: 's3', label: t('burned'), values: burned, unit: 'kcal' },
    noXLabels: hasAzm,
    extra: (i) => buckets[i].rows.flatMap((r) => r.activities || []).map((a) => {
      const detail = activityDetail(a, ', ');
      const name = actLabel(actName(a.name));
      return { label: detail ? `${name} (${detail})` : name, value: fmt(a.energy) + ' kcal' };
    }),
  });
}

// Fitbit's active zone minutes per day, stacked by zone. The reference line
// is the WHO recommendation of 150 minutes a week, as a daily share; Fitbit
// already counts cardio and peak minutes double, as the WHO does.
const AZM_ZONES = [['z2', t('fat burn')], ['z3', t('cardio')], ['z4', t('peak')]];

export function renderZoneMinutes(s) {
  const { buckets, weekly, complete } = s;
  const has = state.data.days.some((d) => d.azm);
  $('#azm-part').hidden = !has;
  if (!has) return;
  const vals = buckets.map((b) => {
    const days = b.rows.filter((r) => r.azm);
    return days.length ? [0, 1, 2].map((k) => mean(days.map((r) => r.azm[k]))) : null;
  });
  const days = complete.filter((r) => r.azm);
  const perWeek = days.length ? (days.reduce((a, r) => a + r.azm[0] + r.azm[1] + r.azm[2], 0) / days.length) * 7 : null;
  const sub = $('#steps-sub');
  if (perWeek != null) sub.textContent = [sub.textContent, t('{min} active zone min/week', { min: fmt(perWeek) })].filter(Boolean).join(' · ');
  const ref = 150 / 7;
  const max = Math.max(ref, ...vals.map((v) => (v ? v[0] + v[1] + v[2] : 0)));
  timeChart($('#chart-azm'), {
    aria: t('Active zone minutes per day by heart rate zone'),
    kind: 'band',
    empty: !vals.some(Boolean),
    n: buckets.length,
    dates: buckets.map((b) => b.start),
    y: [0, max * 1.08],
    goal: { value: ref, label: t('WHO 150 min/week') },
    draw(g, { x, y, bw }) { drawStacks(g, vals, AZM_ZONES.map(([c]) => c), x, y, bw); },
    tip(i) {
      const v = vals[i];
      if (!v) return { title: bucketTitle(buckets[i], weekly), rows: [{ value: t('Not recorded'), label: '' }] };
      return {
        title: bucketTitle(buckets[i], weekly),
        rows: [
          { value: `${fmt(v[0] + v[1] + v[2])} min`, label: weekly ? t('avg per day') : t('active zone minutes') },
          ...AZM_ZONES.map(([c, l], k) => v[k] > 0 && { cls: 'rect ' + c, value: `${fmt(v[k])} min`, label: l }).reverse(),
        ],
      };
    },
    onPick: weekly ? null : (i) => buckets[i].rows[0],
    pickTab: 'activities',
  });
}
