import { $, esc, fmtTime, badge, toast } from '../core/dom.js';
import { api } from '../core/api.js';
import { ruStatus, ruPrio } from '../core/i18n.js';
import { registerActions } from '../core/actions.js';
import { staffState } from './state.js';

export function appealItem(a, extra) {
  return `<div class="item${a.priority === 'urgent' ? ' urgent' : ''}" data-action="open-detail" data-arg="${esc(a.id)}">
    <div class="l1"><span class="cat">${esc(a.category_name || 'Свободный текст')}</span>${badge(a.status, ruStatus(a.status))}${a.transfer_requested ? badge('transfer', '↔ требуется другой специалист') : ''}</div>
    <div class="l2">${badge(a.priority, ruPrio(a.priority))}${a.crisis_detected ? badge('crisis', '⚠ кризис') : ''}
      <span>#${esc(String(a.id).slice(0, 8))}</span>${extra || ''}</div>
  </div>`;
}

const fmtDur = (sec) => {
  if (sec == null) return '—';
  const h = Math.floor(sec / 3600), m = Math.round((sec % 3600) / 60);
  return h > 0 ? (m ? `${h} ч ${m} мин` : `${h} ч`) : `${Math.max(1, m)} мин`;
};

// Состояние страниц списков: фильтры сбрасывают страницу на первую.
let opAllPage = 1;
let exPage = 1;

// pagerHTML — блок «назад/вперёд» для постраничных списков (ключ pg-<name>
// регистрируется в actions, data-arg — целевая страница).
export function pagerHTML(name, page, totalPages) {
  if (!totalPages || totalPages <= 1) return '';
  return `<div class="btnrow" style="margin-top:8px">
    <button class="ghost small" data-action="pg-${name}" data-arg="${page - 1}"${page <= 1 ? ' disabled' : ''}>← назад</button>
    <span class="note">страница ${page} из ${totalPages}</span>
    <button class="ghost small" data-action="pg-${name}" data-arg="${page + 1}"${page >= totalPages ? ' disabled' : ''}>вперёд →</button>
  </div>`;
}

export async function loadQueue() {
  try {
    const q = (await api('GET', '/api/operator/queue')).appeals || [];
    const overdue = q.filter((a) => a.overdue).length;
    const cnt = $('opQueueOverdue');
    cnt.textContent = `просроченных: ${overdue}`;
    cnt.classList.toggle('hidden', overdue === 0);
    const item = (a) => appealItem(a, `<span>ожидание ${fmtDur(a.waiting_sec)}</span>${a.overdue ? badge('overdue', '⏱ просрочено') : ''}` +
      (a.no_expert_in_group ? badge('crisis', 'нет специалистов группы') : '') +
      (a.group_overloaded ? badge('transfer', 'группа перегружена') : ''));
    // ТЗ «Кризисные обращения», п.3: кризисные — отдельным блоком сверху, с отсчётом ожидания.
    const crisis = q.filter((a) => a.crisis_detected);
    const rest = q.filter((a) => !a.crisis_detected);
    $('opQueue').innerHTML =
      (crisis.length
        ? `<div class="err-box" style="margin:8px 0"><b>⚠ Кризисные — требуют немедленной реакции</b>` +
          crisis.map((a) => item(a)).join('') + '</div>'
        : '') +
      (rest.length
        ? rest.map((a) => item(a)).join('')
        : (crisis.length ? '' : '<div class="note" style="margin-top:8px">очередь пуста</div>'));
  } catch (e) { toast(e.message); }
}

export async function loadOpAll() {
  try {
    const st = $('opFilterStatus').value;
    const qs = new URLSearchParams({ page: String(opAllPage) });
    if (st) qs.set('status', st);
    const r = await api('GET', '/api/operator/appeals?' + qs.toString());
    const list = r.appeals || [];
    $('opAll').innerHTML = (list.length
      ? list.map((a) =>
        appealItem(a, `<span>${esc(a.assigned_expert || 'не назначен')}</span><span>обновлено ${fmtTime(a.updated_at)}</span>` +
          (a.no_reply_sec != null && a.no_reply_sec > 24 * 3600 ? badge('overdue', `⏱ без ответа ${fmtDur(a.no_reply_sec)}`) : ''))).join('')
      : '<div class="note" style="margin-top:8px">нет обращений</div>') +
      pagerHTML('op-all', r.page, r.total_pages);
  } catch (e) { toast(e.message); }
}

export async function loadExpert() {
  try {
    const qs = new URLSearchParams({ page: String(exPage) });
    if ($('exFilterStatus').value) qs.set('status', $('exFilterStatus').value);
    if ($('exFilterPriority').value) qs.set('priority', $('exFilterPriority').value);
    if ($('exFilterCategory').value) qs.set('category', $('exFilterCategory').value);
    const r = await api('GET', '/api/expert/appeals?' + qs.toString());
    const list = r.appeals || [];
    $('exList').innerHTML = (list.length
      ? list.map((a) => appealItem(a)).join('')
      : '<div class="note" style="margin-top:8px">нет обращений</div>') +
      pagerHTML('expert', r.page, r.total_pages);
  } catch (e) { toast(e.message); }
}

export function bindListFilters() {
  // Фильтры живут на разных страницах — вешаем только те, что есть в DOM.
  if ($('opFilterStatus')) $('opFilterStatus').addEventListener('change', () => { opAllPage = 1; loadOpAll(); });
  const exFilters = ['exFilterStatus', 'exFilterPriority', 'exFilterCategory'];
  if (exFilters.some((id) => $(id))) {
    exFilters.forEach((id) => { if ($(id)) $(id).addEventListener('change', () => { exPage = 1; loadExpert(); }); });
  }
  if ($('exFilterCategory')) {
    api('GET', '/api/categories').then((r) => {
      $('exFilterCategory').innerHTML = '<option value="">любая</option>' +
        (r.categories || []).map((c) => `<option value="${esc(c.name)}">${esc(c.name)}</option>`).join('');
    }).catch(() => {});
  }
}

export async function loadMyStats() {
  const me = staffState.me;
  if (!me || (me.role !== 'operator' && me.role !== 'expert')) return;
  const el = me.role === 'expert' ? $('exMyStats') : $('opMyStats');
  try {
    const s = await api('GET', '/api/mystats');
    const row = (l, v) => `<div class="kv"><b>${l}</b><span>${v}</span></div>`;
    const avg = s.avg_resolution_hours != null ? (Math.round(s.avg_resolution_hours * 10) / 10) + ' ч' : '—';
    el.innerHTML = me.role === 'operator'
      ? row('Назначено обращений', s.assigned) +
        row('В работе из назначенных', s.active) +
        row('Завершено из назначенных', s.resolved) +
        row('Отклонено мной', s.rejected)
      : row('Активных обращений', s.active) +
        row('Завершено', s.resolved) +
        row('Опубликовано рекомендаций', s.recommendations) +
        row('Среднее время решения', avg);
  } catch (e) { el.textContent = e.message; }
}

export async function loadOpComplaints() {
  try {
    const cs = (await api('GET', '/api/operator/complaints')).complaints || [];
    $('opComplaints').innerHTML = cs.map((c) =>
      `<div style="margin:8px 0;padding:12px;border:1px solid var(--line);border-radius:12px;overflow-wrap:anywhere">
       <b>Обращение #${esc(String(c.appeal_id).slice(0, 8))}</b> · ${fmtTime(c.created_at)}<br>${esc(c.text)}</div>`).join('') || 'жалоб нет';
  } catch (e) { $('opComplaints').textContent = e.message; }
}

registerActions({
  'open-detail': (id) => { location.href = '/detail/' + encodeURIComponent(id); },
  'reload-queue': loadQueue,
  'reload-op-all': () => { opAllPage = 1; loadOpAll(); },
  'reload-expert': () => { exPage = 1; loadExpert(); },
  'reload-my-stats': loadMyStats,
  'reload-op-complaints': loadOpComplaints,
  'pg-op-all': (p) => { opAllPage = Math.max(1, Number(p) || 1); loadOpAll(); },
  'pg-expert': (p) => { exPage = Math.max(1, Number(p) || 1); loadExpert(); },
});
