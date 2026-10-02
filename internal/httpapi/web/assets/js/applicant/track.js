import { $, esc, fmtTime, toast, showErr, hideErr } from '../core/dom.js';
import { api } from '../core/api.js';
import { registerActions } from '../core/actions.js';
import { t } from './tone.js';

let lastTrack = '';

export const rememberTrack = (t) => { lastTrack = t; };

export const lastTrackNumber = () => lastTrack;

// Прямая ссылка на обращение — отдельный эндпоинт /appeal с трек-номером в параметре.
const trackURL = (t) => location.origin + '/appeal?track=' + encodeURIComponent(t);

// --- Приватность: трек-номер открывает переписку, поэтому по умолчанию НИЧЕГО
// не запоминаем на устройстве. Список номеров в localStorage — риск для общего
// (семейного/школьного) компьютера: номера писались автоматически раньше.
// Теперь сохранение — явный opt-in чекбоксом на странице /track.
const STORE_KEY = 'otklik_tracks';
const REMEMBER_KEY = 'otklik_remember_tracks';

export function rememberEnabled() {
  return localStorage.getItem(REMEMBER_KEY) === '1';
}

export function setRemember(on) {
  localStorage.setItem(REMEMBER_KEY, on ? '1' : '0');
  if (!on) localStorage.removeItem(STORE_KEY);
  renderSaved();
}

export function saveTrack(track) {
  if (!rememberEnabled()) return; // приватность по умолчанию: не пишем ничего
  const saved = JSON.parse(localStorage.getItem(STORE_KEY) || '[]');
  if (saved.some((s) => s.track === track)) return;
  saved.unshift({ track, created: new Date().toISOString() });
  localStorage.setItem(STORE_KEY, JSON.stringify(saved.slice(0, 10)));
  renderSaved();
}

function eraseTracks() {
  localStorage.removeItem(STORE_KEY);
  toast('Сохранённые номера стёрты');
  renderSaved();
}

function copyTrack() {
  const t = lastTrack || $('apTrack').textContent.trim();
  if (!t) return;
  const done = () => toast('Трек-номер скопирован');
  if (navigator.clipboard && navigator.clipboard.writeText) {
    navigator.clipboard.writeText(t).then(done).catch(() => fallbackCopy(t, done));
  } else fallbackCopy(t, done);
}

function fallbackCopy(t, done) {
  const ta = document.createElement('textarea');
  ta.value = t;
  ta.style.position = 'fixed';
  ta.style.opacity = '0';
  document.body.appendChild(ta);
  ta.select();
  try { document.execCommand('copy'); done(); }
  catch (e) { toast('Скопируйте номер вручную: ' + t); }
  ta.remove();
}

function downloadTrack() {
  const t = lastTrack || $('apTrack').textContent.trim();
  if (!t) return;
  const txt =
    'Сервис «Отклик» — трек-номер обращения\n\n' +
    t + '\n\n' +
    'Сохрани этот файл или распечатай его.\n' +
    'Открыть своё обращение можно по ссылке:\n' + trackURL(t) + '\n' +
    'или на сайте сервиса: «Вход по трек-номеру».\n' +
    'Сервис анонимный: трек-номер — единственный способ вернуться к обращению.\n' +
    'ВНИМАНИЕ: если потеряешь номер — восстановить его будет невозможно!\n' +
    'Детский телефон доверия: 8-800-2000-122 (бесплатно, круглосуточно).';
  const a = document.createElement('a');
  a.href = URL.createObjectURL(new Blob(['\ufeff' + txt], { type: 'text/plain;charset=utf-8' }));
  a.download = 'Отклик ' + t + '.txt';
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(a.href), 5000);
}

export function renderSaved() {
  const box = $('savedTracks'); // список есть только на странице /track
  if (!box) return;
  bindRememberToggle();
  if (!rememberEnabled()) {
    // Запоминание выключено: стираем и список, оставшийся от старой версии
    // (до opt-in), — на общем устройстве он не должен переживать визит.
    localStorage.removeItem(STORE_KEY);
    box.textContent = 'запоминание выключено — номера в этом браузере не сохраняются';
    const btn = $('eraseTracksBtn');
    if (btn) btn.classList.add('hidden');
    return;
  }
  const saved = JSON.parse(localStorage.getItem(STORE_KEY) || '[]');
  box.innerHTML = saved.length
    ? saved.map((s) => `<div style="margin:6px 0"><a href="#" data-action="use-track" data-arg="${esc(s.track)}">${esc(s.track)}</a> <span class="note">${fmtTime(s.created)}</span></div>`).join('')
    : 'пусто';
  const btn = $('eraseTracksBtn');
  if (btn) btn.classList.toggle('hidden', !saved.length);
}

// Чекбокс «запоминать» есть только на /track; вешаем обработчик один раз.
function bindRememberToggle() {
  const cb = $('trkRemember');
  if (!cb || cb.dataset.bound) return;
  cb.dataset.bound = '1';
  cb.checked = rememberEnabled();
  cb.addEventListener('change', () => setRemember(cb.checked));
}

function useTrack(t) {
  $('trkInput').value = t;
  verifyTrack();
}

export async function verifyTrack() {
  hideErr('trkErr');
  const tn = $('trkInput').value.trim();
  if (!tn) { showErr('trkErr', new Error(t('Вставь трек-номер обращения — он в файле-памятке или в сохранённой ссылке', 'Вставьте трек-номер обращения — он в файле-памятке или в сохранённой ссылке'))); return; }
  try {
    await api('POST', '/api/appeals/track', { track_number: tn });
    saveTrack(tn);
    location.href = '/appeal'; // обращение живёт на отдельном эндпоинте
  } catch (e) {
    showErr('trkErr', e);
  }
}

registerActions({
  'copy-track': copyTrack,
  'download-track': downloadTrack,
  'verify-track': verifyTrack,
  'use-track': useTrack,
  'erase-tracks': eraseTracks,
});
