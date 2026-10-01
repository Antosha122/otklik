import { $, esc, fmtTime, badge, toast } from '../core/dom.js';
import { api } from '../core/api.js';
import { ruStatus, ruRole, ruGroup, ruAppType } from '../core/i18n.js';
import { registerActions } from '../core/actions.js';
import { loadCategories } from '../categories.js';
import { pagerHTML } from './lists.js';

const shortId = (s) => (s ? String(s).slice(0, 8) : '—');

// Текущая страница списка «Все обращения» на панели админа.
let adPage = 1;

export async function loadStats() {
  try {
    const q = new URLSearchParams();
    if ($('adFrom') && $('adFrom').value) q.set('from', $('adFrom').value);
    if ($('adTo') && $('adTo').value) q.set('to', $('adTo').value);
    const s = await api('GET', '/api/admin/stats' + (q.toString() ? '?' + q.toString() : ''));
    const fmtNum = (v, suffix) => (v == null ? '—' : (Math.round(v * 10) / 10) + (suffix || ''));
    const fmtDay = (v) => (v ? new Date(v).toLocaleDateString('ru-RU') : '…');
    const period = (s.period_from || s.period_to)
      ? fmtDay(s.period_from) + ' — ' + fmtDay(s.period_to) : 'за всё время';
    const row = (l, v) => `<div class="kv"><b>${l}</b><span>${v}</span></div>`;
    const workload = (s.workload || []).map((w) => {
      const who = `${esc(w.login)} (${esc(ruRole(w.role))})`;
      if (w.role === 'operator') return row(who, 'назначил обращений: ' + w.assigned);
      return row(who, 'взял: ' + w.assigned + ' · в работе: ' + w.active + ' · завершено: ' + w.completed +
        ' · ср. решение: ' + fmtNum(w.avg_resolution_hours, ' ч'));
    }).join('') || 'нет данных';
    $('adStats').innerHTML =
      row('Период', period) +
      row('Всего обращений', s.total) +
      row('В работе', s.active + ' (срочных: ' + s.urgent_active + ')') +
      row('Новых за 7 / 30 дней', s.last_7_days + ' / ' + s.last_30_days) +
      row('Завершено', s.resolved) +
      row('Доля срочных', fmtNum(s.urgent_share_pct, '%')) +
      row('Доля возвратов на доработку', fmtNum(s.return_share_pct, '%')) +
      row('Ср. время до принятия оператором', fmtNum(s.avg_assign_minutes, ' мин')) +
      row('Ср. время до первого ответа', fmtNum(s.avg_first_response_minutes, ' мин')) +
      row('Среднее время решения', fmtNum(s.avg_resolution_hours, ' ч')) +
      '<h3>По статусам</h3>' +
      (s.by_status || []).map((r) => row(esc(ruStatus(r.label)), r.count)).join('') +
      '<h3>По типам заявителей</h3>' +
      (s.by_applicant_type || []).map((r) => row(esc(ruAppType(r.label)), r.count)).join('') +
      '<h3>По категориям</h3>' +
      (s.by_category || []).map((r) => row(esc(r.label), r.count)).join('') +
      '<h3>По специальностям</h3>' +
      (s.by_specialist_group || []).map((r) => row(r.label === 'free' ? 'свободная форма' : esc(ruGroup(r.label)), r.count)).join('') +
      '<h3>Нагрузка по сотрудникам</h3>' + workload;
  } catch (e) { $('adStats').textContent = e.message; }
}

export async function loadAdminAppeals() {
  try {
    const r = await api('GET', '/api/admin/appeals?page=' + adPage);
    const list = r.appeals || [];
    $('adAppeals').innerHTML = (list.length
      ? list.map((a) =>
        `<div class="item" data-action="open-detail" data-arg="${esc(a.id)}">
        <div class="l1"><span class="cat">${esc(a.category_name || 'Свободный текст')}</span>${badge(a.status, ruStatus(a.status))}
        ${a.no_expert_in_group ? badge('crisis', 'нет специалистов группы') : ''}
        ${a.group_overloaded ? badge('transfer', 'группа перегружена') : ''}</div>
        <div class="l2"><span>#${shortId(a.id)}</span><span>${fmtTime(a.created_at)}</span></div>
      </div>`).join('')
      : '<div class="note" style="margin-top:8px">пусто</div>') +
      pagerHTML('admin', r.page, r.total_pages);
  } catch (e) { $('adAppeals').innerHTML = '<div class="note">' + esc(e.message) + '</div>'; }
}

export async function loadAdminSettings() {
  try {
    const s = await api('GET', '/api/admin/settings');
    $('adLimit').value = s.expert_active_limit;
    $('adMaxReturns').value = s.max_returns;
    $('adNoRespDays').value = s.no_response_days;
  } catch (e) { toast(e.message); }
}

async function adminSaveSettings() {
  try {
    const lim = parseInt($('adLimit').value, 10);
    if (!lim || lim < 1 || lim > 100) { toast('Лимит специалистов — целое число от 1 до 100'); return; }
    const mr = parseInt($('adMaxReturns').value, 10);
    if (isNaN(mr) || mr < 0 || mr > 10) { toast('Лимит возвратов — целое число от 0 до 10'); return; }
    const days = parseInt($('adNoRespDays').value, 10);
    if (!days || days < 1 || days > 90) { toast('Дней до автозакрытия — целое число от 1 до 90'); return; }
    const s = await api('PUT', '/api/admin/settings', {
      expert_active_limit: lim, max_returns: mr, no_response_days: days
    });
    toast('Настройки сохранены: лимит ' + s.expert_active_limit +
      ', возвратов ' + s.max_returns + ', автозакрытие ' + s.no_response_days + ' дн.');
    loadAdminAppeals(); // подсветка «группа перегружена» пересчитывается
  } catch (e) { toast(e.message); }
}

export async function loadAdminUsers() {
  try {
    const users = (await api('GET', '/api/admin/users')).users || [];
    $('adUsers').innerHTML = users.map((u) => `<div class="item" style="cursor:default">
      <div class="l1"><span class="cat">${esc(u.login)} ${u.active ? '✅' : '⛔'}</span>
        <button class="ghost small" data-action="admin-toggle-user" data-arg="${esc(u.id)}" data-active="${!u.active ? 1 : 0}">${u.active ? 'Отключить' : 'Включить'}</button></div>
      <div class="l2"><span>${esc(ruRole(u.role))}</span><span>${esc(u.specialist_group ? ruGroup(u.specialist_group) : '—')}</span></div>
    </div>`).join('');
  } catch (e) { toast(e.message); }
}

async function adminToggleUser(id, el) {
  try {
    await api('PATCH', '/api/admin/users/' + id, { active: el.dataset.active === '1' });
    loadAdminUsers();
  } catch (e) { toast(e.message); }
}

async function adminCreateUser() {
  try {
    await api('POST', '/api/admin/users', {
      login: $('nuLogin').value.trim(), password: $('nuPass').value,
      role: $('nuRole').value, specialist_group: $('nuGroup').value
    });
    toast('Пользователь создан');
    loadAdminUsers();
  } catch (e) { toast(e.message); }
}

export async function loadAdminCats() {
  try {
    const cats = (await api('GET', '/api/admin/categories')).categories || [];
    $('adCats').innerHTML = cats.map((c) => `<div class="item" style="cursor:default">
      <div class="l1"><span class="cat">${esc(c.name)} ${c.active ? '✅' : '⛔'}</span>
        <button class="ghost small" data-action="admin-toggle-cat" data-arg="${esc(c.id)}" data-active="${!c.active ? 1 : 0}">${c.active ? 'Скрыть' : 'Включить'}</button></div>
      <div class="l2"><span>${esc(ruGroup(c.specialist_group))}</span><span>${c.free_form ? 'свободная форма' : 'фиксированная'}</span></div>
    </div>`).join('');
    loadCategories();
  } catch (e) { toast(e.message); }
}

async function adminToggleCat(id, el) {
  try {
    await api('PATCH', '/api/admin/categories/' + id, { active: el.dataset.active === '1' });
    loadAdminCats();
  } catch (e) { toast(e.message); }
}

async function adminCreateCategory() {
  try {
    await api('POST', '/api/admin/categories', {
      name: $('ncName').value.trim(), specialist_group: $('ncGroup').value, free_form: $('ncFree').value === 'true'
    });
    toast('Категория создана');
    loadAdminCats();
  } catch (e) { toast(e.message); }
}

export async function loadComplaints() {
  try {
    const cs = (await api('GET', '/api/admin/complaints')).complaints || [];
    $('adComplaints').innerHTML = cs.map((c) =>
      `<div style="margin:8px 0;padding:12px;border:1px solid var(--line);border-radius:12px;overflow-wrap:anywhere">
       <b>Обращение #${shortId(c.appeal_id)}</b> · ${fmtTime(c.created_at)}<br>${esc(c.text)}</div>`).join('') || 'жалоб нет';
  } catch (e) { $('adComplaints').textContent = e.message; }
}

registerActions({
  'reload-admin-appeals': () => { adPage = 1; loadAdminAppeals(); },
  'pg-admin': (p) => { adPage = Math.max(1, Number(p) || 1); loadAdminAppeals(); },
  'admin-save-settings': adminSaveSettings,
  'admin-toggle-user': adminToggleUser,
  'admin-create-user': adminCreateUser,
  'admin-toggle-cat': adminToggleCat,
  'admin-create-category': adminCreateCategory,
  'reload-complaints': loadComplaints,
  'reload-stats': loadStats,
});
