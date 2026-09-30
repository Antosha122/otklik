import { $, showErr, hideErr, toast } from '../core/dom.js';
import { api } from '../core/api.js';
import { registerActions } from '../core/actions.js';
import { staffState } from './state.js';

let hooks = { afterLogin: null, afterLogout: null };

export function initStaffAuth(h) {
  hooks = h;
}

export async function login() {
  hideErr('stErr');
  const login = $('stLogin').value.trim();
  const pass = $('stPass').value;
  // Пустые поля отсекаем на клиенте: автозаполнение браузера не всегда
  // успевает попасть в DOM до клика, а сервер в этом случае отвечает 400.
  if (!login || !pass) {
    showErr('stErr', new Error('Введите логин и пароль'));
    return;
  }
  try {
    // Сессия живёт в HttpOnly-куке; тело ответа токен не содержит.
    const me = await api('POST', '/api/auth/login', { login, password: pass });
    staffState.me = me;
    // Демо-пароль обязан быть сменён при первом входе — до смены API закрыт (403).
    if (me.must_change_password) {
      const changed = await promptPasswordChange(pass);
      if (!changed) return; // отмена/ошибка — уже разлогинены
    }
    if (hooks.afterLogin) await hooks.afterLogin();
  } catch (e) {
    showErr('stErr', e);
  }
}

export async function logout() {
  try { await api('POST', '/api/auth/logout'); } catch (e) { /* сессия могла истечь */ }
  staffState.me = null;
  if (hooks.afterLogout) await hooks.afterLogout();
}

// Диалог смены пароля: модалка на DOM вместо window.prompt — системные
// промпты блокируются частью браузеров/WebView («prompt() is not supported»),
// из-за чего принудительная смена демо-пароля ломалась.
// resolve {current, next} или null при отмене.
function passwordDialog({ prefill = '', forced = false } = {}) {
  return new Promise((resolve) => {
    const done = (val) => { overlay.remove(); resolve(val); };

    const overlay = document.createElement('div');
    overlay.style.cssText =
      'position:fixed;inset:0;background:rgba(15,23,42,.55);display:flex;' +
      'align-items:center;justify-content:center;z-index:100;padding:16px';

    const card = document.createElement('div');
    card.className = 'card';
    card.style.cssText = 'max-width:420px;width:100%;margin:0';

    const title = document.createElement('h2');
    title.textContent = forced ? 'Смена пароля по умолчанию' : 'Смена пароля';
    const sub = document.createElement('p');
    sub.className = 'login-sub';
    sub.textContent = forced
      ? 'Пароль по умолчанию известен всем — задайте собственный, минимум 8 символов, чтобы продолжить работу.'
      : 'Введите текущий пароль и новый (минимум 8 символов).';

    const mkField = (labelText, value, autocomplete) => {
      const row = document.createElement('div');
      const label = document.createElement('label');
      label.textContent = labelText;
      const input = document.createElement('input');
      input.type = 'password';
      input.value = value || '';
      input.autocomplete = autocomplete;
      row.append(label, input);
      return { row, input };
    };
    const cur = mkField('Текущий пароль', prefill, 'current-password');
    const next = mkField('Новый пароль', '', 'new-password');
    const again = mkField('Повторите новый пароль', '', 'new-password');

    const err = document.createElement('div');
    err.className = 'err-box hidden';

    const form = document.createElement('form');
    form.onsubmit = (e) => {
      e.preventDefault();
      if (!cur.input.value) { err.textContent = 'Введите текущий пароль'; err.classList.remove('hidden'); return; }
      if (next.input.value.length < 8) { err.textContent = 'Новый пароль — минимум 8 символов'; err.classList.remove('hidden'); return; }
      if (next.input.value !== again.input.value) { err.textContent = 'Пароли не совпадают'; err.classList.remove('hidden'); return; }
      done({ current: cur.input.value, next: next.input.value });
    };

    const submit = document.createElement('button');
    submit.type = 'submit';
    submit.className = 'act';
    submit.textContent = 'Сменить пароль';
    const cancel = document.createElement('button');
    cancel.type = 'button';
    cancel.textContent = forced ? 'Выйти' : 'Отмена';
    cancel.style.cssText =
      'display:block;width:100%;margin-top:10px;background:none;border:none;' +
      'color:#6b7280;font-size:14.5px;padding:8px;cursor:pointer';
    cancel.onclick = () => done(null);
    form.append(cur.row, next.row, again.row, err, submit, cancel);

    card.append(title, sub, form);
    overlay.append(card);
    // В принудительном режиме мимо диалога не пройти: клик по фону не закрывает.
    overlay.onclick = (e) => { if (e.target === overlay && !forced) done(null); };
    document.body.append(overlay);
    (prefill ? next.input : cur.input).focus();
  });
}

async function submitPasswordChange(current, nextPwd) {
  await api('POST', '/api/auth/password', { current_password: current, new_password: nextPwd });
  if (staffState.me) staffState.me.must_change_password = false;
  toast('Пароль изменён; другие сессии завершены');
}

// Принудительная смена демо-пароля: отмена невозможна — только выход.
// knownCurrent — пароль, только что введённый в форму входа; при заходе
// с живой сессией текущий пароль спрашивает сам диалог.
export async function promptPasswordChange(knownCurrent) {
  let prefill = knownCurrent || '';
  for (;;) {
    const res = await passwordDialog({ prefill, forced: true });
    if (!res) { await logout(); return false; }
    try {
      await submitPasswordChange(res.current, res.next);
      return true;
    } catch (e) {
      toast('Ошибка: ' + e.message);
      prefill = res.current; // неверный текущий пароль — показываем диалог снова
    }
  }
}

// Смена пароля самим сотрудником: /api/auth/password инвалидирует прочие сессии.
export async function changePassword() {
  let prefill = '';
  for (;;) {
    const res = await passwordDialog({ prefill, forced: false });
    if (!res) return;
    try {
      await submitPasswordChange(res.current, res.next);
      return;
    } catch (e) {
      toast('Ошибка: ' + e.message);
      prefill = res.current;
    }
  }
}

registerActions({
  'staff-login': login,
  'staff-logout': logout,
  'staff-change-password': changePassword,
});
