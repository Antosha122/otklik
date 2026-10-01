import { $, esc, fmtTime, kv } from '../core/dom.js';
import { api } from '../core/api.js';
import { ruRole, ruGroup, ruStatus, ruPrio, ruEvent } from '../core/i18n.js';
import { registerActions } from '../core/actions.js';
import { roleHome } from './state.js';
import { guardPage } from './index.js';
import './lists.js'; // side-effect: регистрирует export-csv и пагинацию списков

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

// Страница /profile — личный кабинет любого сотрудника (оператор/эксперт/админ).
export async function profileInit() {
  const me = await guardPage(['operator', 'expert', 'admin']);
  if (!me) return;
  const back = $('navHomeLink');
  if (back) back.href = roleHome(me.role);
  await loadProfile();
}

registerActions({ 'reload-profile': loadProfile });
