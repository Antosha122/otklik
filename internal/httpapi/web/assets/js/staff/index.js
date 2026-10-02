import { $ } from '../core/dom.js';
import { api } from '../core/api.js';
import { ruRole } from '../core/i18n.js';
import { loadCategories } from '../categories.js';
import { initStaffAuth, promptPasswordChange } from './auth.js';
import { loadQueue, loadOpAll, loadExpert, bindListFilters, loadMyStats, loadOpComplaints } from './lists.js';
import { openDetailFromURL } from './detail.js';
import { loadAdminAppeals, loadAdminUsers, loadAdminCats, loadStats, loadAdminSettings, loadComplaints, loadCrisisMarkers, bindAdminFilters } from './admin.js';
import { staffState, roleHome } from './state.js';

function bindAuthHooks() {
  initStaffAuth({
    afterLogin: () => { location.href = roleHome(staffState.me && staffState.me.role); },
    afterLogout: () => { location.href = '/login'; },
  });
}

function paintWhoami(me) {
  $('whoami').textContent = `${me.login} (${ruRole(me.role)})`;
  const hello = $('stHello');
  if (hello) hello.textContent = `${me.login} — ${ruRole(me.role)}`;
}

async function fetchMe() {
  try {
    const r = await api('GET', '/api/me');
    if (r.user_id && r.role) return r;
  } catch (e) { /* нет сессии — покажем форму входа */ }
  return null;
}

// Гард страницы: без сессии — на вход, с чужой ролью — на свою домашнюю страницу.
// С несменённым демо-паролем — на /login, там же предлагается смена.
// Экспортируется для страницы личного кабинета (staff/profile.js).
export async function guardPage(roles) {
  const me = await fetchMe();
  if (!me) {
    location.replace('/login');
    return null;
  }
  if (me.must_change_password) {
    location.replace('/login');
    return null;
  }
  if (!roles.includes(me.role)) {
    location.replace(roleHome(me.role));
    return null;
  }
  staffState.me = me;
  paintWhoami(me);
  bindAuthHooks();
  return me;
}

// Страница /login (и /): при живой сессии сразу уводит на панель по роли;
// если демо-пароль ещё не сменён — сначала требует смену.
export async function loginInit() {
  bindAuthHooks();
  const me = await fetchMe();
  if (me && me.must_change_password) {
    staffState.me = me;
    const changed = await promptPasswordChange(null);
    if (!changed) return;
  }
  if (me) location.replace(roleHome(me.role));
}

// Страница /operator — панель оператора (админу тоже доступна).
export async function operatorInit() {
  const me = await guardPage(['operator', 'admin']);
  if (!me) return;
  const navAdmin = $('navAdminLink');
  if (navAdmin) navAdmin.classList.toggle('hidden', me.role !== 'admin');
  bindListFilters();
  await loadCategories(); // селект категорий в карточке обращения
  loadQueue();
  loadOpAll();
  loadOpComplaints();
  loadMyStats();
}

// Страница /expert — панель специалиста.
export async function expertInit() {
  const me = await guardPage(['expert']);
  if (!me) return;
  bindListFilters();
  await loadCategories();
  loadExpert();
  loadMyStats();
}

// Страница /admin — панель администратора.
export async function adminInit() {
  const me = await guardPage(['admin']);
  if (!me) return;
  await loadCategories();
  bindAdminFilters();
  loadAdminAppeals();
  loadAdminUsers();
  loadAdminCats();
  loadStats();
  loadAdminSettings();
  loadComplaints();
  loadCrisisMarkers();
}

// Страница /detail/{id} — карточка обращения.
export async function detailInit() {
  const me = await guardPage(['operator', 'expert', 'admin']);
  if (!me) return;
  await loadCategories(); // селект смены категории
  await openDetailFromURL();
}
