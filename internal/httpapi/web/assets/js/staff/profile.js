import { $, esc, fmtTime, kv, toast } from '../core/dom.js';
import { api } from '../core/api.js';
import { ruRole, ruGroup, ruStatus, ruPrio, ruEvent } from '../core/i18n.js';
import { registerActions } from '../core/actions.js';
import { roleHome } from './state.js';
import { guardPage } from './index.js';

// Значение события журнала локализуем по типу: статус/приоритет — словарями.
function eventValue(e) {
  if (!e.new_value) return '';
  if (e.event_type === 'status') return ruStatus(e.new_value);
  if (e.event_type === 'priority') return ruPrio(e.new_value);
  return e.new_value;
}

async function loadProfile() {
  try {
    const p = await api('GET', '/api/profile');

    $('prInfo').innerHTML =
      kv('Логин', esc(p.user.login)) +
      kv('Роль', esc(ruRole(p.user.role))) +
      (p.user.role === 'expert' ? kv('Специализация', esc(ruGroup(p.user.specialist_group))) : '') +
      kv('В системе с', fmtTime(p.user.created_at)) +
      kv('Статус учётной записи', p.user.active ? 'активна' : 'деактивирована');

    if (p.workload) {
      $('prWorkloadCard').classList.remove('hidden');
      $('prWorkload').innerHTML =
        kv('Активных обращений', `${p.workload.active} из ${p.workload.limit}`) +
        (p.workload.overload
          ? '<p class="err-box">Достигнут лимит активных обращений — новые обращения назначаться не будут.</p>'
          : '');
    }

    if (p.stats) {
      $('prStatsCard').classList.remove('hidden');
      const s = p.stats;
      const row = (l, v) => `<div class="kv"><b>${l}</b><span>${v}</span></div>`;
      const avg = s.avg_resolution_hours != null ? (Math.round(s.avg_resolution_hours * 10) / 10) + ' ч' : '—';
      $('prStats').innerHTML = s.role === 'operator'
        ? row('Назначено обращений', s.assigned) +
          row('В работе из назначенных', s.active) +
          row('Завершено из назначенных', s.resolved) +
          row('Отклонено мной', s.rejected)
        : row('Активных обращений', s.active) +
          row('Завершено', s.resolved) +
          row('Опубликовано рекомендаций', s.recommendations) +
          row('Среднее время решения', avg);
    }

    const evs = p.recent_events || [];
    $('prEvents').innerHTML = evs.length
      ? evs.map((e) => {
          const v = eventValue(e);
          return `<div class="kv"><b>${fmtTime(e.created_at)}</b><span>${esc(ruEvent(e.event_type))}` +
            `${v ? ' → ' + esc(v) : ''} · <a href="/detail/${esc(e.appeal_id)}">карточка</a></span></div>`;
        }).join('')
      : 'пока нет действий';
  } catch (e) {
    $('prInfo').textContent = e.message;
  }
}

// Выгрузка CSV с фильтрами. Охват зависит от роли:
// админ — все обращения, оператор — где он назначал/отклонял, специалист — свои.
function exportCsv() {
  const q = new URLSearchParams();
  if ($('exFrom') && $('exFrom').value) q.set('from', $('exFrom').value);
  if ($('exTo') && $('exTo').value) q.set('to', $('exTo').value);
  if ($('exStatus') && $('exStatus').value) q.set('status', $('exStatus').value);
  if ($('exPriority') && $('exPriority').value) q.set('priority', $('exPriority').value);
  if ($('exGroup') && $('exGroup').value) q.set('group', $('exGroup').value);
  if ($('exCrisis') && $('exCrisis').value) q.set('crisis', $('exCrisis').value);
  const qs = q.toString();
  window.open('/api/export/appeals' + (qs ? '?' + qs : ''), '_blank');
  toast('Выгрузка сформирована — файл скачивается');
}

// Подсказка об охвате выгрузки: фильтры уточняют, роль ограничивает.
function exportScopeHint(role) {
  const scope = role === 'admin'
    ? 'выгружаются все обращения программы'
    : role === 'operator'
      ? 'выгружаются только обращения, с которыми вы работали (назначали специалиста или отклоняли)'
      : 'выгружаются только ваши обращения';
  const el = $('exScope');
  if (el) el.textContent = 'Охват: ' + scope + '. Файл откроется в Excel (UTF-8, разделитель — запятая).';
  // Специалисту фильтр специальности не нужен — его обращения и так одной группы.
  const g = $('exGroup');
  if (g && role === 'expert') g.disabled = true;
}

// Страница /profile — личный кабинет любого сотрудника (оператор/эксперт/админ).
export async function profileInit() {
  const me = await guardPage(['operator', 'expert', 'admin']);
  if (!me) return;
  const back = $('navHomeLink');
  if (back) back.href = roleHome(me.role);
  exportScopeHint(me.role);
  await loadProfile();
}

registerActions({ 'reload-profile': loadProfile, 'export-csv': exportCsv });
