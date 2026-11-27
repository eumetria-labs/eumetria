// Updates as the page knows them (PLAN 15a), for the admin only: what the
// server's last update check found (GET /api/admin/update). An available
// update shows as a chip on the Today card and a dot on Settings → Update,
// which shows the rest (settings.js updateTab).

import { account, api } from './account.js';
import { emoji, h } from './dom.js';
import { t } from './i18n.js';

export const updates = { info: null };

const isAdmin = () => !!account.user?.scopes?.includes('admin');

// loadUpdate asks the server once more; nothing for other accounts and for
// plain files (no API).
export async function loadUpdate() {
  if (!isAdmin()) return (updates.info = null);
  const res = await api('admin/update').catch(() => null);
  updates.info = res?.ok ? await res.json() : null;
  return updates.info;
}

// setUpdate keeps an answer the Update tab got (after "Check now").
export function setUpdate(info) {
  updates.info = info;
  document.dispatchEvent(new Event('update-changed'));
}

// available is the update to install, or null: the version or edge build.
export const available = (info = updates.info) => (info?.last?.available && info.last.target) || null;

// updateLabel names a target: "1.2.0" as it is, "main-3ba126d" as the commit.
export const updateLabel = (target) => (target?.startsWith('main-') ? t('edge {commit}', { commit: target.slice(5) }) : target);

// updateChip is the Today card's chip for an available update, or null.
export function updateChip(open) {
  const target = isAdmin() && available();
  if (!target) return null;
  const b = h('button', { class: 'highlight upd-chip', type: 'button', role: 'listitem' });
  b.append(emoji('⬆️'), h('span', { class: 'hl-text' }, t('Update available: {version}', { version: updateLabel(target) })), h('b', {}, '›'));
  b.onclick = () => open(b);
  return b;
}
