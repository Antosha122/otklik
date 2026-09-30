// Все запросы ходят на свой источник и аутентифицируются HttpOnly-кукой
// (otklik_staff / otklik_applicant) — токен в sessionStorage не хранится.
// Любая ошибка, вылетающая наружу, уже переведена на русский (humanError).
import { humanError } from './errors.js';

export async function api(method, path, body) {
  const opt = { method, headers: {} };
  if (body !== undefined) {
    opt.headers['Content-Type'] = 'application/json; charset=utf-8';
    opt.body = JSON.stringify(body);
  }
  let res;
  try {
    res = await fetch(path, opt);
  } catch (e) {
    // Сеть недоступна / сервер не отвечает — fetch бросает TypeError('Failed to fetch').
    throw new Error('Нет соединения с сервером — проверьте подключение к сети');
  }
  let data = null;
  try { data = await res.json(); } catch (e) { /* пустое тело — это нормально */ }
  if (!res.ok) throw new Error(humanError(data && data.error, res.status));
  return data;
}
