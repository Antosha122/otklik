import { $, esc, fmtTime, badge, kv, toast } from '../core/dom.js';
import { api } from '../core/api.js';
import { humanError } from '../core/errors.js';
import { ruStatus, ruAuthor } from '../core/i18n.js';
import { createPoller } from '../core/poller.js';
import { registerActions } from '../core/actions.js';
import { applyTone, t } from './tone.js';

export const viewPoller = createPoller(loadApplicantView, 5000);

const TERMINAL = ['completed', 'rejected', 'closed_no_response'];

// --- Уведомления о новых сообщениях специалиста ---
// Два режима работы кнопки:
//  1) Web Push (если сервер включил его и браузер поддерживает PushManager):
//     уведомления приходят даже при закрытой вкладке — их доставляет
//     service worker (см. sw.js, обработчик push).
//  2) Фолбэк Notification API: системное уведомление, когда вкладка открыта,
//     но свёрнута в фон. Работает везде, где есть Notification.
let lastMsgCount = -1;
let notifyWanted = false;   // фолбэк-режим включён
let pushSubscribed = false; // Web Push-подписка активна
let pushCfg = { enabled: false, public_key: '' };

const pushSupported = () =>
  'serviceWorker' in navigator && 'PushManager' in window;

// base64url (без паддинга) → Uint8Array для applicationServerKey.
function urlB64ToUint8Array(b64) {
  const padding = '='.repeat((4 - (b64.length % 4)) % 4);
  const raw = atob((b64 + padding).replace(/-/g, '+').replace(/_/g, '/'));
  const out = new Uint8Array(raw.length);
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
  return out;
}

async function initNotifyButton() {
  const btn = $('avNotifyBtn');
  if (!btn || btn.dataset.bound) return;
  btn.dataset.bound = '1';
  if (!('Notification' in window)) {
    btn.classList.add('hidden');
    return;
  }
  btn.classList.remove('hidden'); // скрыта только если API нет вовсе
  try { pushCfg = await api('GET', '/api/push/config') || pushCfg; } catch (e) { /* сервер без push */ }
  // Уже есть живая подписка (с прошлого визита) — показываем активное состояние.
  if (pushCfg.enabled && pushSupported() && Notification.permission === 'granted') {
    try {
      const reg = await navigator.serviceWorker.getRegistration();
      const sub = reg && await reg.pushManager.getSubscription();
      if (sub && sub.endpoint) pushSubscribed = true;
    } catch (e) { /* SW не зарегистрирован — просто фолбэк-режим */ }
  }
  if (pushSubscribed) notifyWanted = false;
  syncNotifyLabel(btn);
  btn.addEventListener('click', toggleNotify);
}

async function toggleNotify() {
  const btn = $('avNotifyBtn');
  // Выключение: отписываемся от push и/или фолбэка.
  if (pushSubscribed || notifyWanted) {
    if (pushSubscribed && pushSupported()) {
      try {
        const reg = await navigator.serviceWorker.getRegistration();
        const sub = reg && await reg.pushManager.getSubscription();
        if (sub) {
          await api('DELETE', '/api/push/subscribe', { endpoint: sub.endpoint });
          await sub.unsubscribe();
        }
      } catch (e) { toast(e.message); }
    }
    pushSubscribed = false;
    notifyWanted = false;
    syncNotifyLabel(btn);
    toast(t('Уведомления выключены', 'Уведомления выключены'));
    return;
  }
  // Включение: сначала разрешение браузера.
  let perm = Notification.permission;
  if (perm !== 'granted') perm = await Notification.requestPermission();
  if (perm !== 'granted') {
    toast(t('Разрешение на уведомления не выдано', 'Разрешение на уведомления не выдано'));
    return;
  }
  // Основной путь — Web Push: уведомления и при закрытой вкладке.
  if (pushCfg.enabled && pushSupported()) {
    try {
      const reg = await navigator.serviceWorker.ready;
      const sub = await reg.pushManager.subscribe({
        userVisibleOnly: true,
        applicationServerKey: urlB64ToUint8Array(pushCfg.public_key),
      });
      const j = sub.toJSON();
      await api('POST', '/api/push/subscribe', {
        endpoint: j.endpoint,
        keys: { p256dh: j.keys.p256dh, auth: j.keys.auth },
      });
      pushSubscribed = true;
      syncNotifyLabel(btn);
      toast(t('Готово: сообщим, даже если приложение закрыто', 'Уведомления включены'));
      return;
    } catch (e) { /* браузер отказал в подписке — ниже фолбэк */ }
  }
  // Фолбэк: уведомления только пока вкладка жива.
  notifyWanted = true;
  syncNotifyLabel(btn);
  toast(t('Уведомим, если придёт ответ (пока страница открыта)', 'Уведомим о новых сообщениях'));
}

function syncNotifyLabel(btn) {
  btn.textContent = (pushSubscribed || notifyWanted)
    ? '🔕 Выключить уведомления'
    : '🔔 Уведомлять о новых сообщениях';
  btn.title = pushSubscribed
    ? 'Уведомления приходят даже при закрытом приложении'
    : 'Показывать системные уведомления о новых сообщениях';
}

function maybeNotify(msgs) {
  if (lastMsgCount < 0 || msgs.length <= lastMsgCount) return;
  // Web Push уже показал уведомление из service worker — не дублируем.
  if (pushSubscribed || !notifyWanted) { lastMsgCount = msgs.length; return; }
  const last = msgs[msgs.length - 1];
  lastMsgCount = msgs.length;
  if (!document.hidden || last.author_type === 'applicant') return;
  if (!('Notification' in window) || Notification.permission !== 'granted') return;
  try {
    const n = new Notification('Отклик — новое сообщение', {
      body: (last.text || '').slice(0, 120),
      icon: '/assets/icons/icon-192.png',
      tag: 'otklik-message',
    });
    n.onclick = () => { window.focus(); n.close(); };
  } catch (e) { /* отдельные браузеры запрещают — тихо игнорируем */ }
}

const fmtSize = (n) => n >= 1048576 ? (n / 1048576).toFixed(1) + ' МБ' : Math.max(1, Math.round(n / 1024)) + ' КБ';

export async function loadApplicantView() {
  initNotifyButton();
  let v;
  try { v = await api('GET', '/api/appeals/me/'); } catch (e) { return; }
  applyTone(v.applicant_type);
  $('apViewCard').classList.remove('hidden');
  $('avStatus').innerHTML = badge(v.status, ruStatus(v.status));
  $('avExpl').textContent = v.status_explanation || '';
  $('avExpl').classList.toggle('hidden', !v.status_explanation);
  $('avCrisis').classList.toggle('hidden', !v.crisis_help);
  if (v.crisis_help) {
    $('avCrisis').innerHTML = '<b>⚠ Кризисная помощь — позвоните прямо сейчас:</b>' +
      v.crisis_help.map((h) => {
        const digits = String(h.phone).replace(/\D/g, '');
        const tel = (digits.length === 11 && digits[0] === '8') ? '+7' + digits.slice(1) : digits;
        return `<div class="crisis-call"><div class="crisis-txt"><div class="crisis-t">${esc(h.title)}</div><div class="crisis-d">${esc(h.description || '')}</div></div>` +
          `<a class="crisis-tel" href="tel:${tel}" aria-label="Позвонить: ${esc(h.title)}">📞 ${esc(h.phone)}</a></div>`;
      }).join('');
  }
  $('avMeta').innerHTML =
    kv('Категория', esc(v.category_name || 'свободный текст')) +
    kv('Создано', fmtTime(v.created_at)) +
    kv('Возвратов на доработку', v.return_count) +
    (v.recommendation ? kv('Рекомендация', esc(v.recommendation)) : '');
  $('avDesc').textContent = v.description || '';
  const closed = TERMINAL.includes(v.status);
  $('avAppendBlock').classList.toggle('hidden', closed);
  $('avUploadBlock').classList.toggle('hidden', closed);
  $('avAttach').innerHTML = (v.attachments || []).map((a) =>
    `<div class="kv"><b>${esc(a.content_type)}</b><span><a href="/api/appeals/me/attachments/${a.id}" target="_blank" rel="noopener">открыть файл</a> · ${fmtSize(a.size)} · ${fmtTime(a.created_at)}</span></div>`
  ).join('') || '<div class="note">пока нет вложений</div>';
  $('avAnswers').innerHTML = (v.intake_answers || []).map((a) =>
    `<div class="kv"><b>${esc(a.question)}</b><span>${esc(a.answer)}</span></div>`).join('') || '<div class="note">—</div>';
  $('avHistory').innerHTML = (v.status_history || []).map((h) => {
    const st = h.status || h.to_status;
    return `<li>${badge(st, ruStatus(st))} — ${fmtTime(h.created_at || h.at)}</li>`;
  }).join('');
  $('avMsgs').innerHTML = (v.messages || []).map((m) =>
    `<div class="msg ${m.author_type === 'applicant' ? 'mine' : ''}"><div class="a">${esc(ruAuthor(m.author_type))} · ${fmtTime(m.created_at)}</div>${esc(m.text)}</div>`).join('')
    || '<div class="note">пока нет сообщений</div>';
  $('avMsgs').scrollTop = 1e9;
  maybeNotify(v.messages || []);
  $('avResultBlock').classList.toggle('hidden', v.status !== 'answer_ready');
  // ТЗ 5.1: число возвратов ограничено — прячем «Не помогло» при исчерпании лимита.
  const limitReached = v.status === 'answer_ready' && v.return_count >= (v.max_returns != null ? v.max_returns : 2);
  $('avNotHelpedBtn').classList.toggle('hidden', limitReached);
  const limNote = $('avReturnLimitNote');
  limNote.classList.toggle('hidden', !limitReached);
  limNote.textContent = limitReached
    ? t('Лимит возвратов исчерпан. Если рекомендации не помогли — оцени работу, пожалуйся оператору или напиши новое обращение',
        'Лимит возвратов исчерпан. Если рекомендации не помогли — оцените работу, отправьте жалобу оператору или напишите новое обращение')
    : '';
  $('avFeedbackBlock').classList.toggle('hidden', !(v.status === 'answer_ready' || v.status === 'completed'));
  // Жалоба доступна, когда в работе участвует специалист.
  $('avComplaintBlock').classList.toggle('hidden',
    !['assigned', 'in_progress', 'needs_clarification', 'answer_ready', 'returned'].includes(v.status));
  $('avAgainBlock').classList.toggle('hidden', !closed);
}

async function apSendMessage() {
  const t = $('avMsgText').value.trim();
  if (!t) return;
  try {
    await api('POST', '/api/appeals/me/messages', { text: t });
    $('avMsgText').value = '';
    loadApplicantView();
  } catch (e) { toast(e.message); }
}

async function apAppend() {
  const text = $('avAppendText').value.trim();
  if (text.length < 10) {
    toast(t(
      `Дополнение слишком короткое: нужно хотя бы 10 символов, а сейчас ${text.length}`,
      `Дополнение слишком короткое: нужно хотя бы 10 символов, а сейчас ${text.length}`));
    return;
  }
  try {
    await api('POST', '/api/appeals/me/append', { text });
    $('avAppendText').value = '';
    toast(t('Дополнение добавлено — специалист его увидит', 'Дополнение добавлено — специалист его увидит'));
    loadApplicantView();
  } catch (e) { toast(e.message); }
}

async function apUpload() {
  const files = [...$('avFile').files];
  if (!files.length) { toast(t('Сначала выбери файл(ы)', 'Сначала выберите файл(ы)')); return; }
  let uploaded = 0;
  for (const f of files) {
    if (f.size > 10 * 1024 * 1024) {
      toast(t(`«${f.name}» больше 10 МБ — приложить не получится`, `Файл «${f.name}» больше 10 МБ — приложить не получится`));
      continue;
    }
    const fd = new FormData();
    fd.append('file', f);
    const res = await fetch('/api/appeals/me/attachments', { method: 'POST', body: fd });
    if (!res.ok) {
      const d = await res.json().catch(() => null);
      toast(humanError(d && d.error, res.status));
      continue;
    }
    uploaded++;
  }
  $('avFile').value = '';
  if (uploaded) toast(t('Файлы прикреплены', 'Файлы прикреплены'));
  loadApplicantView();
}

async function apResult(helped) {
  const reason = helped ? '' : $('avReturnReason').value.trim();
  try {
    await api('POST', '/api/appeals/me/result', { helped, reason });
    loadApplicantView();
    toast(helped
      ? t('Спасибо! Рады, что помогло', 'Спасибо! Отмечено как полезное')
      : t('Спасибо за честный ответ — подберём другой способ помочь', 'Спасибо за отзыв — подберём другой способ помощи'));
  } catch (e) { toast(e.message); }
}

async function apFeedback() {
  try {
    await api('POST', '/api/appeals/me/feedback', { rating: +$('avRating').value, comment: $('avComment').value.trim() || null });
    toast('Оценка сохранена');
  } catch (e) { toast(e.message); }
}

async function apComplaint() {
  const text = $('avComplaintText').value.trim();
  if (text.length < 5) {
    toast(t('Опиши жалобу хотя бы парой слов', 'Опишите жалобу хотя бы парой слов'));
    return;
  }
  try {
    await api('POST', '/api/appeals/me/complaint', { text });
    $('avComplaintText').value = '';
    toast(t('Жалоба отправлена оператору — специалист её не увидит', 'Жалоба отправлена оператору — специалист её не увидит'));
  } catch (e) { toast(e.message); }
}

registerActions({
  'applicant-send-message': apSendMessage,
  'applicant-append': apAppend,
  'applicant-upload': apUpload,
  'applicant-result': (arg) => apResult(arg === '1'),
  'applicant-feedback': apFeedback,
  'applicant-complaint': apComplaint,
  'write-again': () => {
    location.href = '/new'; // новое обращение — отдельный эндпоинт
  },
});
