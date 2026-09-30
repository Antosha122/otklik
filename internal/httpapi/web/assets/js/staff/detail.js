import { $, esc, fmtTime, badge, kv, toast } from '../core/dom.js';
import { api } from '../core/api.js';
import { ruStatus, ruPrio, ruAppType, ruGroup, ruAuthor, ruEvent } from '../core/i18n.js';
import { createPoller } from '../core/poller.js';
import { registerActions } from '../core/actions.js';
import { staffState, roleHome } from './state.js';
import { loadQueue, loadOpAll, loadExpert } from './lists.js';
import { loadAdminAppeals } from './admin.js';

let detailId = null;

let expertsLoad = new Map();
const ROUTE_TERMINAL = { completed: 1, rejected: 1, closed_no_response: 1 };

const detailPoller = createPoller(() => refreshDetail(true), 5000);

// «К списку»: карточка обращения — отдельный эндпоинт, возвращаемся назад.
export function closeDetail() {
  detailPoller.stop();
  if (history.length > 1) history.back();
  else location.href = roleHome(staffState.me && staffState.me.role);
}

// Карточка открытия по URL: /detail/{id}.
export async function openDetailFromURL() {
  const id = decodeURIComponent(location.pathname.split('/').filter(Boolean).pop() || '');
  if (!id) {
    location.href = roleHome(staffState.me && staffState.me.role);
    return;
  }
  detailId = id;
  await refreshDetail();
  detailPoller.start();
}

async function refreshDetail(silent) {
  if (!detailId) return;
  let a;
  try { a = await api('GET', '/api/appeals/' + detailId + '/'); }
  catch (e) { if (!silent) toast(e.message); return; }
  const me = staffState.me;
  const isOperator = !!me && me.role === 'operator';
  const isAdmin = !!me && me.role === 'admin';
  $('dStatus').innerHTML = badge(a.status, ruStatus(a.status));
  // Кризисная пометка — только оператору и эксперту: администратор работает
  // с обращениями, но не должен видеть, что обращение кризисное.
  $('dCrisis').classList.toggle('hidden', !a.crisis_detected || isAdmin);
  $('dPriority').value = a.priority; // чтобы открытие карточки не сбрасывало приоритет на «низкий»
  // Текущая категория — в селект смены категории (если опции ещё не загружены, подхватит поллер)
  const dCat = $('dCategory');
  if (dCat && a.category_id && dCat.querySelector(`option[value="${a.category_id}"]`)) dCat.value = a.category_id;
  $('dMeta').innerHTML =
    kv('Заявитель', esc(ruAppType(a.applicant_type))) +
    kv('Категория', esc(a.category_name || 'свободный текст')) +
    kv('Приоритет', esc(ruPrio(a.priority))) +
    kv('Ответственный', esc(a.assigned_expert || 'не назначен')) +
    ((a.participants || []).filter((p) => p.participant_role === 'contributor').length
      ? kv('Соисполнители', esc((a.participants || []).filter((p) => p.participant_role === 'contributor').map((p) => p.expert_login).join(', ')))
      : '') +
    kv('Создано', fmtTime(a.created_at)) +
    kv('Обновлено', fmtTime(a.updated_at)) +
    kv('Возвратов', a.return_count) +
    kv('Запрос передачи', a.transfer_requested ? 'да' : 'нет') +
    (a.rejection_reason ? kv('Причина отклонения', esc(a.rejection_reason)) : '') +
    (!isAdmin && a.crisis_contact ? kv('Кризисный контакт', esc(a.crisis_contact)) : '');
  $('dDesc').textContent = a.description || '';
  const atts = a.attachments || [];
  $('dAttachTitle').classList.toggle('hidden', isAdmin || !atts.length);
  $('dAttach').classList.toggle('hidden', isAdmin || !atts.length);
  $('dAttach').innerHTML = atts.map((x) => {
    const size = x.size >= 1048576 ? (x.size / 1048576).toFixed(1) + ' МБ' : Math.max(1, Math.round(x.size / 1024)) + ' КБ';
    return `<div>📎 <a href="/api/appeals/${detailId}/attachments/${x.id}" target="_blank" rel="noopener">файл ${esc(x.content_type)}</a> · ${size} · ${fmtTime(x.created_at)}</div>`;
  }).join('');
  $('dAnswers').innerHTML = (a.intake_answers || []).length
    ? '<h3>Ответы анкеты</h3>' + a.intake_answers.map((x) => `<div class="kv"><b>${esc(x.question)}</b><span>${esc(x.answer)}</span></div>`).join('')
    : '';
  const sug = a.category_suggestion;
  const showSug = !!me && me.role === 'operator' && sug && sug.category_id !== a.category_id;
  $('dSuggest').classList.toggle('hidden', !showSug);
  if (showSug) {
    $('dSuggest').innerHTML =
      `Подсказка системы: похоже на «${esc(sug.category_name)}» (профиль — ${esc(sug.specialist_group)})` +
      (sug.matched_keywords && sug.matched_keywords.length ? `, маркеры: ${sug.matched_keywords.map(esc).join(', ')}` : '') +
      `. <button class="ghost small" data-action="apply-suggestion" data-arg="${esc(sug.category_id)}">Применить</button>`;
  }
  const rt = a.routing;
  const showRt = isOperator && rt && !ROUTE_TERMINAL[a.status] &&
    (!a.assigned_expert_id || a.transfer_requested);
  $('dRouting').classList.toggle('hidden', !showRt);
  if (showRt) {
    const list = (rt.experts || []).map((e3) =>
      ` ${esc(e3.login)} — ${e3.active}/${rt.limit}${e3.overload ? ' ⚠' : ''}`).join(' ·');
    let head = `Маршрутизация (по правилу): группа «${esc(rt.group_ru)}»` +
      (rt.source === 'suggestion' ? ' — по подсказке системы, категория ещё не назначена' : '') + '.';
    let tail = '';
    if (rt.no_experts) {
      tail = ' В группе нет активных специалистов — обращение осталось в очереди; требуется настройка администратора.';
    } else if (rt.all_overloaded) {
      tail = ` Все специалисты группы перегружены (лимит ${rt.limit}); наименьшая загрузка — ${esc(rt.least_loaded)}. Назначьте с учётом нагрузки либо оставьте в очереди.` +
        ` <button class="ghost small" data-action="op-assign-recommended" data-arg="${esc(rt.least_loaded)}">Назначить ${esc(rt.least_loaded)}</button>`;
    } else {
      const free = (rt.experts || []).find((e3) => e3.login === rt.free_login);
      tail = ` Свободен: ${esc(rt.free_login)} (${free ? free.active : 0}/${rt.limit}).` +
        ` <button class="ghost small" data-action="op-assign-recommended" data-arg="${esc(rt.free_login)}">Назначить рекомендуемого</button>`;
    }
    $('dRouting').innerHTML = head + (list ? ` Нагрузка:${list}.` : '') + tail;
  }
  $('dOpActions').classList.toggle('hidden', !(me && (me.role === 'operator' || me.role === 'admin')));
  $('dExActions').classList.toggle('hidden', !(me && me.role === 'expert'));
  $('opCompleteRow').classList.toggle('hidden', !isOperator);
  $('opRejectRow').classList.toggle('hidden', !isOperator);
  // Терминальный статус — архив: у завершённых/отклонённых/закрытых без
  // ответа обращений разблокировка статуса недоступна даже админу.
  const TERMINAL_STATUS = { completed: 1, rejected: 1, closed_no_response: 1 };
  $('adStatusRow').classList.toggle('hidden', !isAdmin || !!TERMINAL_STATUS[a.status]);
  $('dDescTitle').classList.toggle('hidden', isAdmin);
  $('dDesc').classList.toggle('hidden', isAdmin);
  $('dDescRestricted').classList.toggle('hidden', !isAdmin);
  const EX_HINT = {
    new: 'Обращение ещё не назначено специалисту — дождитесь назначения оператором.',
    assigned: 'Обращение назначено вам. Возьмите его в работу, когда будете готовы.',
    in_progress: 'Общайтесь с заявителем в чате. Когда помощь будет оказана — опубликуйте рекомендацию.',
    needs_clarification: 'Вы запросили уточнение — ожидаем ответа заявителя в чате.',
    answer_ready: 'Рекомендация опубликована. Обращение завершит заявитель («Помогло» / «Не помогло»); если ответа не будет — оператор закроет обращение без ответа.',
    returned: 'Заявитель отметил, что рекомендация не помогла. Обращение вернулось к оператору и будет переназначено — публикация недоступна до переназначения.',
    completed: 'Обращение завершено — действия недоступны.',
    rejected: 'Обращение отклонено — действия недоступны.',
    closed_no_response: 'Обращение закрыто без ответа заявителя.'
  };
  const TERMINAL = { completed: 1, rejected: 1, closed_no_response: 1 };
  const st = a.status;
  const noChat = !!me && (me.role === 'operator' || me.role === 'admin');
  $('dChatBody').classList.toggle('hidden', noChat);
  $('dChatRestricted').classList.toggle('hidden', !noChat);
  $('exHint').textContent = EX_HINT[st] || '';
  $('exTakeRow').classList.toggle('hidden', st !== 'assigned');
  $('exClarifyRow').classList.toggle('hidden', st !== 'in_progress');
  $('exRecRow').classList.toggle('hidden', st !== 'in_progress');
  $('exTransferRow').classList.toggle('hidden', !!TERMINAL[st]);
  $('exContribRow').classList.toggle('hidden', !!TERMINAL[st]);
  $('opCloseNoRespRow').classList.toggle('hidden', !(isOperator && (st === 'needs_clarification' || st === 'answer_ready')));
  $('opReturnRow').classList.toggle('hidden', !(isOperator && st === 'answer_ready'));
  if (me && (me.role === 'operator' || me.role === 'admin')) {
    try {
      const exps = (await api('GET', '/api/staff/experts')).experts || [];
      expertsLoad = new Map(exps.map((e2) => [e2.id, e2]));
      const cur = $('dExpert').value; // не сбрасываем выбор оператора при автополлинге
      $('dExpert').innerHTML = exps.map((e2) =>
        `<option value="${e2.id}">${esc(e2.login)} (${esc(e2.specialist_group ? ruGroup(e2.specialist_group) : '—')}) · ${e2.active_count}/${e2.limit}${e2.active_count >= e2.limit ? ' ⚠ перегружен' : ''}</option>`).join('');
      if (cur) $('dExpert').value = cur;
    } catch (e) {}
  }
  if (me && me.role === 'expert') {
    try {
      const exps = (await api('GET', '/api/staff/experts')).experts || [];
      const cur2 = $('dContributor').value;
      $('dContributor').innerHTML = exps
        .filter((e2) => e2.login !== me.login && e2.id !== a.assigned_expert_id)
        .map((e2) => `<option value="${e2.id}">${esc(e2.login)} (${esc(e2.specialist_group ? ruGroup(e2.specialist_group) : '—')})</option>`).join('');
      if (cur2) $('dContributor').value = cur2;
    } catch (e) {}
  }
  let msgs = [];
  if (!noChat) {
    try { msgs = (await api('GET', '/api/appeals/' + detailId + '/messages')).messages || []; } catch (e) {}
  }
  $('dMsgs').innerHTML = msgs.map((m) =>
    `<div class="msg"><div class="a">${esc(ruAuthor(m.author_type))}${m.author_login ? ' · ' + esc(m.author_login) : ''} · ${fmtTime(m.created_at)}</div>${esc(m.text)}</div>`).join('')
    || '<div class="note">пока нет сообщений</div>';
  $('dMsgs').scrollTop = 1e9;
  const isExpertRole = !!me && me.role === 'expert';
  $('dNotesWrap').classList.toggle('hidden', !isExpertRole);
  $('dNoteForm').classList.toggle('hidden', !isExpertRole);
  $('dNotesRestricted').classList.toggle('hidden', !(me && me.role === 'admin'));
  if (isExpertRole) {
    try {
      const notes = (await api('GET', '/api/appeals/' + detailId + '/notes')).notes || [];
      $('dNotes').innerHTML = notes.map((n) =>
        `<div class="msg"><div class="a">${esc(n.author_login || n.author || '')} · ${fmtTime(n.created_at)}</div>${esc(n.text)}</div>`).join('')
        || '<div class="note">нет заметок</div>';
    } catch (e) { $('dNotes').innerHTML = '<div class="note">заметки недоступны</div>'; }
  } else {
    $('dNotes').innerHTML = '';
  }
  api('POST', '/api/appeals/' + detailId + '/presence', { typing: $('dMsgText').value.trim().length > 0 }).catch(() => {});
  let present = [];
  try { present = (await api('GET', '/api/appeals/' + detailId + '/presence')).present || []; } catch (e) {}
  $('dPresence').classList.toggle('hidden', present.length === 0);
  if (present.length) {
    $('dPresence').textContent = 'Сейчас в карточке: ' +
      present.map((u) => u.login + (u.typing ? ' (печатает…)' : '')).join(', ');
  }
  const otherTyping = present.find((u) => u.typing && u.role === 'expert');
  const msgInp = $('dMsgText');
  msgInp.disabled = !!otherTyping;
  msgInp.placeholder = otherTyping ? otherTyping.login + ' печатает ответ заявителю…' : 'Сообщение';
  try {
    const evs = (await api('GET', '/api/appeals/' + detailId + '/events')).events || [];
    $('dEvents').innerHTML = evs.slice().reverse().map((ev) =>
      `<div style="margin:4px 0">${fmtTime(ev.created_at)} — <b>${esc(ruEvent(ev.type))}</b>${ev.initiator_login ? ' (' + esc(ev.initiator_login) + ')' : ''}${ev.reason ? ': ' + esc(ev.reason) : ''}</div>`).join('') || '—';
    const tr = evs.find((ev) => ev.type === 'transfer_requested');
    const ret = evs.find((ev) => ev.type === 'status' && ev.new_value === 'returned');
    let opHintText = '';
    if (a.transfer_requested && tr) {
      opHintText = 'Специалист запросил пересмотр назначения' + (tr.reason ? ' — ' + tr.reason : '') +
        '. Переназначьте другого эксперта: при подтверждённом запросе передачи это разрешено.';
    } else if (st === 'returned' || ret) {
      opHintText = (ret && ret.reason ? 'Причина возврата: ' + ret.reason : 'Заявитель вернул обращение без указания причины.');
    }
    $('opHint').textContent = opHintText;
    $('opHint').classList.toggle('hidden', !opHintText);
  } catch (e) { $('dEvents').textContent = 'аудит доступен оператору и админу'; }
}

async function dSendMsg() {
  const t = $('dMsgText').value.trim();
  if (!t || !detailId) return;
  try {
    await api('POST', '/api/appeals/' + detailId + '/messages', { text: t });
    $('dMsgText').value = '';
    refreshDetail();
  } catch (e) { toast(e.message); }
}

async function dSendNote() {
  const t = $('dNoteText').value.trim();
  if (!t || !detailId) return;
  try {
    await api('POST', '/api/appeals/' + detailId + '/notes', { text: t });
    $('dNoteText').value = '';
    refreshDetail();
  } catch (e) { toast(e.message); }
}

async function act(path, body, msg) {
  const me = staffState.me;
  try {
    await api('POST', '/api/appeals/' + detailId + '/' + path, body || {});
    toast(msg || 'Готово');
    await refreshDetail();
    // Списки живут на своих страницах — обновляем только те, что есть в DOM.
    if ($('opQueue')) { loadQueue(); loadOpAll(); }
    if ($('exList')) loadExpert();
    if ($('adAppeals')) loadAdminAppeals(); // панель «Все обращения»
  } catch (e) { toast(e.message); }
}

const opAssign = () => {
  const sel = expertsLoad.get($('dExpert').value);
  if (sel && sel.active_count >= sel.limit &&
      !confirm(`${sel.login} уже ведёт ${sel.active_count} обращений (лимит ${sel.limit}). Всё равно назначить?`)) return;
  act('assign', { expert_id: $('dExpert').value, reason: $('dAssignReason').value.trim() }, 'Эксперт назначен');
};
const opAssignRecommended = (login) => {
  const found = [...expertsLoad.values()].find((e2) => e2.login === login);
  if (!found) { toast('Специалист не найден, выберите вручную'); return; }
  $('dExpert').value = found.id;
  act('assign', { expert_id: found.id, reason: 'назначен по подсказке маршрутизации' }, 'Эксперт назначен');
};
const opReject = () => act('reject', { reason: $('dRejectReason').value.trim() || 'не соответствует тематике' }, 'Обращение отклонено');
const opComplete = () => act('complete', { recommendation: $('dCompleteRec').value.trim() || 'Рекомендация оператора' }, 'Обращение завершено');
const opPriority = () => act('priority', { priority: $('dPriority').value, reason: '' }, 'Приоритет изменён');
const opCategory = () => act('category', { category_id: $('dCategory').value, reason: '' }, 'Категория изменена');
const adStatus = () => {
  const reason = $('dAdminStatusReason').value.trim();
  if (reason.length < 5) { toast('Укажите причину смены статуса (минимум 5 символов)'); return; }
  return act('admin-status', { status: $('dAdminStatus').value, reason }, 'Статус изменён администратором');
};
const exTake = () => act('take', null, 'Взято в работу');
const exClarify = () => act('clarify', null, 'Запрошено уточнение');
const exRecommendation = () => act('recommendation', { recommendation: $('dRec').value.trim() }, 'Рекомендация опубликована');
const exTransfer = () => act('transfer-request', { reason: $('dTransferReason').value.trim() || 'требуется другой специалист' }, 'Запрос отправлен оператору');
const exContributor = () => act('contributors', { expert_id: $('dContributor').value }, 'Коллега подключён к обращению');
const opCloseNoResponse = () => {
  if (confirm('Закрыть обращение без ответа заявителя?')) act('close-no-response', null, 'Обращение закрыто');
};
const opReturn = () => {
  const reason = $('dReturnReason').value.trim();
  if (reason.length < 5) { toast('Укажите причину возврата (минимум 5 символов)'); return; }
  return act('return', { reason }, 'Обращение возвращено на доработку');
};
const applySuggestion = (catId) => {
  const sel = $('dCategory');
  if (catId && sel && sel.querySelector(`option[value="${catId}"]`)) sel.value = catId;
  toast('Категория подставлена — проверьте и нажмите «Сменить»');
};

registerActions({
  'close-detail': closeDetail,
  'detail-send-message': dSendMsg,
  'detail-send-note': dSendNote,
  'apply-suggestion': applySuggestion,
  'op-assign': opAssign,
  'op-assign-recommended': opAssignRecommended,
  'op-reject': opReject,
  'op-complete': opComplete,
  'op-priority': opPriority,
  'op-category': opCategory,
  'ad-status': adStatus,
  'ex-take': exTake,
  'ex-clarify': exClarify,
  'ex-recommendation': exRecommendation,
  'ex-transfer': exTransfer,
  'ex-contributor': exContributor,
  'op-close-no-response': opCloseNoResponse,
  'op-return': opReturn,
});
