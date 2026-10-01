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

// --- Двухфакторная аутентификация (TOTP) ---------------------------------
// Флоу: setup выдаёт секрет для приложения-аутентификатора, enable
// подтверждает его действующим кодом, после чего логин требует код.
// Отключение — только с текущим паролем.

let totpSetupSecret = ''; // секрет этапа настройки (не включён, пока нет confirm)
let totpOtpauth = '';     // otpauth://-ссылка того же этапа настройки

export async function loadTotp() {
  const el = $('prTotp');
  if (!el) return;
  try {
    const st = await api('GET', '/api/auth/totp');
    // 2FA отключена на сервере (TOTP_ENABLED!=1) — весь блок скрываем.
    if (st.available === false) { $('prTotpCard').classList.add('hidden'); return; }
    $('prTotpCard').classList.remove('hidden');
    // 2FA включена — незавершённая настройка больше неактуальна.
    if (st.enabled) totpSetupSecret = '';
    if (st.enabled) {
      el.innerHTML =
        '<p><b>Включена.</b> При входе требуется код из приложения-аутентификатора.</p>' +
        '<button class="ghost small" data-action="totp-disable">Отключить 2FA</button>' +
        '<p class="note">Понадобится текущий пароль. Потеряли телефон с приложением — ' +
        '2FA снимет администратор (см. README).</p>';
    } else if (totpSetupSecret) {
      el.innerHTML =
        '<p><b>Шаг 2 из 2.</b> Откройте приложение-аутентификатор (Google Authenticator, ' +
        'Aegis, FreeOTP…) и добавьте запись по секрету ниже (ввод вручную) или по ссылке:</p>' +
        `<p style="font-family:monospace;font-size:16px;overflow-wrap:anywhere">${esc(totpSetupSecret)}</p>` +
        `<p style="overflow-wrap:anywhere"><a href="${esc(totpOtpauth)}">${esc(totpOtpauth)}</a></p>` +
        '<p>Затем введите текущий код и подтвердите — с этого момента вход требует код:</p>' +
        '<div class="row"><div><label>Код из приложения</label><input type="text" id="totpCode" inputmode="numeric" maxlength="6" placeholder="123456" style="letter-spacing:4px"></div></div>' +
        '<div class="btnrow" style="margin-top:8px"><button class="act small" data-action="totp-enable">Подтвердить код</button>' +
        '<button class="ghost small" data-action="totp-refresh">Отмена</button></div>';
    } else {
      el.innerHTML =
        '<p><b>Выключена.</b> Вход — только по паролю. Для учётных записей с доступом ' +
        'к персональным данным детей рекомендуется включить второй фактор.</p>' +
        '<button class="act small" data-action="totp-setup">Включить 2FA</button>';
    }
  } catch (e) { el.textContent = e.message; }
}

async function totpSetup() {
  try {
    const r = await api('POST', '/api/auth/totp/setup');
    totpSetupSecret = r.secret;
    totpOtpauth = r.otpauth_url || '';
    toast('Секрет создан — добавьте его в приложение и подтвердите кодом');
    await loadTotp();
  } catch (e) { toast(e.message); }
}

async function totpEnable() {
  const code = $('totpCode') ? $('totpCode').value.trim() : '';
  if (code.length !== 6) { toast('Код — 6 цифр из приложения'); return; }
  try {
    await api('POST', '/api/auth/totp/enable', { code });
    totpSetupSecret = '';
    totpOtpauth = '';
    toast('2FA включена: при следующем входе потребуется код');
    await loadTotp();
  } catch (e) { toast(e.message); }
}

// Мини-диалог пароля на DOM (window.prompt блокируется частью WebView).
function passwordPrompt() {
  return new Promise((resolve) => {
    const done = (v) => { overlay.remove(); resolve(v); };
    const overlay = document.createElement('div');
    overlay.style.cssText = 'position:fixed;inset:0;background:rgba(15,23,42,.55);display:flex;align-items:center;justify-content:center;z-index:100;padding:16px';
    const card = document.createElement('div');
    card.className = 'card';
    card.style.cssText = 'max-width:380px;width:100%;margin:0';
    const title = document.createElement('h2');
    title.textContent = 'Отключение 2FA';
    const sub = document.createElement('p');
    sub.className = 'login-sub';
    sub.textContent = 'Введите текущий пароль для подтверждения.';
    const row = document.createElement('div');
    const label = document.createElement('label');
    label.textContent = 'Пароль';
    const input = document.createElement('input');
    input.type = 'password';
    input.autocomplete = 'current-password';
    row.append(label, input);
    const form = document.createElement('form');
    form.onsubmit = (e) => { e.preventDefault(); done(input.value); };
    const submit = document.createElement('button');
    submit.type = 'submit';
    submit.className = 'act';
    submit.textContent = 'Отключить';
    const cancel = document.createElement('button');
    cancel.type = 'button';
    cancel.textContent = 'Отмена';
    cancel.style.cssText = 'display:block;width:100%;margin-top:10px;background:none;border:none;color:#6b7280;font-size:14.5px;padding:8px;cursor:pointer';
    cancel.onclick = () => done(null);
    form.append(row, submit, cancel);
    card.append(title, sub, form);
    overlay.append(card);
    overlay.onclick = (e) => { if (e.target === overlay) done(null); };
    document.body.append(overlay);
    input.focus();
  });
}

async function totpDisable() {
  const pass = await passwordPrompt();
  if (!pass) return;
  try {
    await api('POST', '/api/auth/totp/disable', { password: pass });
    toast('2FA отключена');
    await loadTotp();
  } catch (e) { toast(e.message); }
}

// Страница /profile — личный кабинет любого сотрудника (оператор/эксперт/админ).
export async function profileInit() {
  const me = await guardPage(['operator', 'expert', 'admin']);
  if (!me) return;
  const back = $('navHomeLink');
  if (back) back.href = roleHome(me.role);
  exportScopeHint(me.role);
  await loadProfile();
  await loadTotp();
}

registerActions({
  'reload-profile': loadProfile,
  'export-csv': exportCsv,
  'totp-refresh': loadTotp,
  'totp-setup': totpSetup,
  'totp-enable': totpEnable,
  'totp-disable': totpDisable,
});
