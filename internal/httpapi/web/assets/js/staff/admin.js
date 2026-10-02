import { $, esc, fmtTime, badge, toast } from '../core/dom.js';
import { api } from '../core/api.js';
import { ruStatus, ruRole, ruGroup, ruAppType } from '../core/i18n.js';
import { registerActions } from '../core/actions.js';
import { loadCategories } from '../categories.js';
import { pagerHTML } from './lists.js';
import { kpiHTML, dailyBarsSVG, donutHTML, hbarHTML, loadbarHTML, STATUS_COLORS, CHART_COLORS } from '../core/chart.js';

const shortId = (s) => (s ? String(s).slice(0, 8) : '—');

// Текущая страница списка «Все обращения» на панели админа.
let adPage = 1;

const fmtNum = (v, suffix) => (v == null ? '—' : (Math.round(v * 10) / 10) + (suffix || ''));

// Подсветка активной кнопки быстрых периодов (null — сброс, даты введены вручную).
function markPeriod(arg) {
  document.querySelectorAll('.seg button[data-action="stats-period"]')
    .forEach((b) => b.classList.toggle('on', b.dataset.arg === arg));
}

// Быстрые периоды: N дней назад … сегодня; «всё время» очищает поля.
function setQuickPeriod(arg) {
  const to = $('adTo'), from = $('adFrom');
  if (!to || !from) return;
  if (arg === 'all') { from.value = ''; to.value = ''; }
  else {
    const d = new Date();
    to.value = d.toISOString().slice(0, 10);
    d.setDate(d.getDate() - (Number(arg) - 1));
    from.value = d.toISOString().slice(0, 10);
  }
  markPeriod(arg);
  loadStats();
}

export async function loadStats() {
  try {
    const q = new URLSearchParams();
    if ($('adFrom') && $('adFrom').value) q.set('from', $('adFrom').value);
    if ($('adTo') && $('adTo').value) q.set('to', $('adTo').value);
    const s = await api('GET', '/api/admin/stats' + (q.toString() ? '?' + q.toString() : ''));

    // KPI-плитки: ключевые метрики периода и живые (не зависящие от периода) счётчики.
    $('adKpi').innerHTML = kpiHTML([
      { label: 'Всего обращений (период)', value: s.total, hint: 'новых за 7 / 30 дней: ' + s.last_7_days + ' / ' + s.last_30_days },
      { label: 'В работе сейчас', value: s.active, hint: 'срочных: ' + s.urgent_active, tone: s.urgent_active ? 'bad' : '' },
      { label: 'Завершено (период)', value: s.resolved, tone: 'ok' },
      { label: 'Ср. время решения', value: fmtNum(s.avg_resolution_hours, ' ч') },
      { label: 'Ср. до назначения', value: fmtNum(s.avg_assign_minutes, ' мин') },
      { label: 'Ср. до первого ответа', value: fmtNum(s.avg_first_response_minutes, ' мин') },
      { label: 'Доля срочных', value: fmtNum(s.urgent_share_pct, ' %'), tone: s.urgent_share_pct >= 30 ? 'warn' : '' },
      { label: 'Возвраты на доработку', value: fmtNum(s.return_share_pct, ' %') },
    ]);

    $('adChartDaily').innerHTML = dailyBarsSVG(s.by_day || []);

    const statusItems = (s.by_status || []).map((r) => ({
      label: ruStatus(r.label), count: r.count,
      color: STATUS_COLORS[r.label] || CHART_COLORS[7],
    }));
    $('adChartStatus').innerHTML = donutHTML(statusItems);

    const groups = (s.by_specialist_group || []).map((r, i) => ({
      label: r.label === 'free' ? 'свободная форма' : ruGroup(r.label),
      count: r.count, color: CHART_COLORS[i % CHART_COLORS.length],
    }));
    $('adChartGroups').innerHTML = hbarHTML(groups);

    const cats = (s.by_category || []).slice(0, 8).map((r, i) => ({
      label: esc(r.label), count: r.count, color: CHART_COLORS[(i + 2) % CHART_COLORS.length],
    }));
    $('adChartCats').innerHTML = hbarHTML(cats);

    // Нагрузка: операторам — сколько назначили, специалистам — полоса active/limit.
    const limit = s.expert_limit || 0;
    $('adWorkload').innerHTML = (s.workload || []).map((w) => {
      if (w.role === 'operator') {
        return `<div class="wload"><span class="wload-l">${esc(w.login)} · оператор</span>` +
          `<span class="note">назначил обращений: <b>${w.assigned}</b></span></div>`;
      }
      const overload = w.active >= limit && limit > 0;
      return `<div class="wload${overload ? ' hot' : ''}">` +
        `<span class="wload-l">${esc(w.login)} · специалист</span>` +
        `<span class="wload-bar">${loadbarHTML(w.active, limit)}</span>` +
        `<span class="note">завершено ${w.completed} · ср. решение ${fmtNum(w.avg_resolution_hours, ' ч')}</span></div>`;
    }).join('') || '<div class="note">нет данных</div>';
  } catch (e) { $('adKpi').innerHTML = '<div class="note">' + esc(e.message) + '</div>'; }
}

export async function loadAdminAppeals() {
  try {
    const qs = new URLSearchParams({ page: String(adPage) });
    if ($('adFilterStatus') && $('adFilterStatus').value) qs.set('status', $('adFilterStatus').value);
    const r = await api('GET', '/api/admin/appeals?' + qs.toString());
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

// Кризисные маркеры: редактируемый словарь детектора (детектор кэширует
// словарь на минуту — новые маркеры применяются почти сразу).
export async function loadCrisisMarkers() {
  try {
    const ms = (await api('GET', '/api/admin/crisis-markers')).markers || [];
    const ruKind = (k) => (k === 'word' ? 'по границе слова' : 'подстрока');
    $('adMarkers').innerHTML = ms.map((m) => `<div class="item" style="cursor:default">
      <div class="l1"><span class="cat">${esc(m.text)}</span>
        <button class="ghost small" data-action="admin-del-marker" data-arg="${esc(m.id)}">Удалить</button></div>
      <div class="l2"><span>${esc(ruKind(m.kind))}</span></div>
    </div>`).join('') || '<div class="note">словарь пуст — детектор работает на встроенных маркерах</div>';
  } catch (e) { toast(e.message); }
}

async function adminAddMarker() {
  const text = $('cmText').value.trim();
  if (text.length < 2) { toast('Маркер должен быть не короче 2 символов'); return; }
  try {
    await api('POST', '/api/admin/crisis-markers', { kind: $('cmKind').value, text });
    $('cmText').value = '';
    toast('Маркер добавлен');
    loadCrisisMarkers();
  } catch (e) { toast(e.message); }
}

async function adminDeleteMarker(id) {
  try {
    await api('DELETE', '/api/admin/crisis-markers/' + id);
    loadCrisisMarkers();
  } catch (e) { toast(e.message); }
}

registerActions({
  'reload-admin-appeals': () => { adPage = 1; loadAdminAppeals(); },
  'pg-admin': (p) => { adPage = Math.max(1, Number(p) || 1); loadAdminAppeals(); },
  'admin-save-settings': adminSaveSettings,
  'admin-toggle-user': adminToggleUser,
  'admin-create-user': adminCreateUser,
  'admin-toggle-cat': adminToggleCat,
  'admin-create-category': adminCreateCategory,
  'admin-add-marker': adminAddMarker,
  'admin-del-marker': adminDeleteMarker,
  'reload-complaints': loadComplaints,
  'reload-stats': loadStats,
  'stats-period': setQuickPeriod,
});

// Фильтр статуса списка «Все обращения»: смена — всегда первая страница.
export function bindAdminFilters() {
  if ($('adFilterStatus')) {
    $('adFilterStatus').addEventListener('change', () => { adPage = 1; loadAdminAppeals(); });
  }
  if ($('adFrom')) $('adFrom').addEventListener('change', () => { markPeriod(null); loadStats(); });
  if ($('adTo')) $('adTo').addEventListener('change', () => { markPeriod(null); loadStats(); });
}
