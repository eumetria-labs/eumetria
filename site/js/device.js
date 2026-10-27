// The Fitbit tracker in the page header: a small watch whose face fills with
// the battery level, next to how long ago it last synced. Missing workouts
// or sleep usually just mean the watch has not synced yet.

import { LOCALE, reviewNotices, syncAgo, syncNotices } from './format.js';
import { $, svg, showTip, hideTip } from './dom.js';
import { store } from './state.js';
import { t } from './i18n.js';

const dtf = new Intl.DateTimeFormat(LOCALE, { weekday: 'short', hour: '2-digit', minute: '2-digit' });

export function renderNotices(data) {
  const el = $('#sync-notice');
  // Credit warnings only for the site owner (devices with the admin token).
  const notes = [...syncNotices(data), ...(store.get('admin-token') ? reviewNotices(data) : [])];
  el.hidden = !notes.length;
  el.textContent = notes.join(' ');
}

// Low battery or a sync older than a day gets the warning color.
export const deviceWarn = (d, now = Date.now()) =>
  (d.battery > 0 && d.battery <= 20) || /low|empty/i.test(d.battery_status || '') || (d.last_sync && now - new Date(d.last_sync) > 864e5);

export function renderDevice(device) {
  const btn = $('#device');
  btn.hidden = !device;
  if (!device) return;
  const level = Math.max(0, Math.min(100, device.battery || 0));
  const ago = device.last_sync ? syncAgo(device.last_sync) : '';
  const battery = device.battery ? `${Math.round(level)} %` : device.battery_status || '';
  const label = [device.name || 'Fitbit', battery && t('battery {level}', { level: battery }), ago && t('synced {ago}', { ago })].filter(Boolean).join(', ');
  btn.setAttribute('aria-label', label);
  btn.title = label;
  btn.classList.toggle('warn', !!deviceWarn(device));

  // Watch: strap above and below a rounded case; the face fills from the
  // bottom with the battery level.
  const icon = svg('svg', { viewBox: '0 0 24 24', width: 20, height: 20, 'aria-hidden': 'true', class: 'watch' });
  icon.append(
    svg('path', { d: 'M8.5 5.5 9.3 2h5.4l.8 3.5M8.5 18.5l.8 3.5h5.4l.8-3.5', class: 'watch-strap' }),
    svg('rect', { x: 6, y: 5.5, width: 12, height: 13, rx: 3.5, class: 'watch-case' }),
  );
  const faceH = 9 * (level / 100);
  if (device.battery) icon.append(svg('rect', { x: 8, y: 7.5 + 9 - faceH, width: 8, height: faceH, rx: 1.5, class: 'watch-level' }));
  const text = document.createElement('span');
  text.className = 'device-text';
  text.textContent = [device.battery ? `${Math.round(level)}%` : '', ago].filter(Boolean).join(' · ');
  btn.replaceChildren(icon, text);

  btn.onclick = () => {
    const r = btn.getBoundingClientRect();
    if (btn._open) { hideTip(); btn._open = false; return; }
    showTip(r.left, r.bottom + 40, device.name || 'Fitbit', [
      battery && { cls: 'rect batt', value: battery, label: t('battery') },
      device.last_sync && { value: dtf.format(new Date(device.last_sync)), label: t('last sync ({ago})', { ago }) },
    ].filter(Boolean));
    btn._open = true;
  };
  btn.onblur = () => { hideTip(); btn._open = false; };
}
