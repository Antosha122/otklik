import { $, esc, showErr, hideErr, toast } from '../core/dom.js';
import { api } from '../core/api.js';
import { registerActions } from '../core/actions.js';
import { loadCategories } from '../categories.js';
import { rememberTrack, saveTrack, lastTrackNumber } from './track.js';
import { applyTone, t } from './tone.js';

// --- Офлайн-черновик: текст обращения дороже всего терять при обрыве сети ---
// Черновик живёт только в этом браузере (localStorage), восстанавливается при
// следующем визите и стирается сразу после успешной отправки обращения.
const DRAFT_KEY = 'otklik_new_draft';
let draftTimer = null;
let pendingDraft = null;

function draftFields() {
  return {
    type: $('apType').value,
    cat: $('apCat').value,
    free: $('apFree').checked,
    desc: $('apDesc').value,
    crisis: $('apCrisis').value,
    answers: [...document.querySelectorAll('#apQuestions select')]
      .map((s) => [s.dataset.q, s.value])
      .filter(([, v]) => v),
    saved: new Date().toISOString(),
  };
}

function saveDraft() {
  clearTimeout(draftTimer);
  draftTimer = setTimeout(() => {
    const d = draftFields();
    if (!d.desc || d.desc.trim().length < 10) return; // пустышки не храним
    try { localStorage.setItem(DRAFT_KEY, JSON.stringify(d)); } catch (e) { /* quota — не беда */ }
  }, 400);
}

function clearDraft() {
  localStorage.removeItem(DRAFT_KEY);
  pendingDraft = null;
  const note = $('apDraftNote');
  if (note) note.classList.add('hidden');
}

function discardDraft() {
  clearDraft();
  $('apDesc').value = '';
  $('apCrisis').value = '';
  $('apFree').checked = false;
}

function restoreDraft() {
  let d = null;
  try { d = JSON.parse(localStorage.getItem(DRAFT_KEY) || 'null'); } catch (e) { d = null; }
  if (!d || !d.desc) return;
  pendingDraft = d;
  if (d.type) $('apType').value = d.type;
  if (d.desc) $('apDesc').value = d.desc;
  if (d.crisis) $('apCrisis').value = d.crisis;
  const note = $('apDraftNote');
  if (note) note.classList.remove('hidden');
}

// Категория и анкета подгружаются с сервера асинхронно — их значения из
// черновика подставляем после того, как select'ы заполнены.
function applyDraftLate() {
  const d = pendingDraft;
  if (!d) return;
  if (d.cat) $('apCat').value = d.cat;
  if (d.free) $('apFree').checked = true;
  (d.answers || []).forEach(([q, a]) => {
    const sel = document.querySelector(`#apQuestions select[data-q="${CSS.escape(q)}"]`);
    if (sel) sel.value = a;
  });
  $('apFree').dispatchEvent(new Event('change'));
}

function syncFree() {
  $('apCat').disabled = $('apFree').checked;
  $('apCat').parentElement.style.opacity = $('apFree').checked ? 0.5 : 1;
}

async function renderQuestions() {
  try {
    const qs = (await api('GET', '/api/intake-questions')).questions || [];
    $('apQuestions').innerHTML = qs.map((q) =>
      `<div><label>${esc(q.text)}</label>
       <select data-q="${esc(q.text)}">
         <option value="">— пропустить</option>
         ${(q.options || []).map((o) => `<option value="${esc(o)}">${esc(o)}</option>`).join('')}
       </select></div>`).join('');
  } catch (e) {
    console.error(e);
  }
}

function renderCrisisHelp(help) {
  const box = $('apCrisisHelp');
  if (!help || !help.length) { box.classList.add('hidden'); box.innerHTML = ''; return; }
  box.innerHTML = '<b>⚠ Кризисная помощь — позвоните прямо сейчас:</b>' +
    help.map((h) => {
      const digits = String(h.phone).replace(/\D/g, '');
      const tel = (digits.length === 11 && digits[0] === '8') ? '+7' + digits.slice(1) : digits;
      return `<div class="crisis-call"><div class="crisis-txt"><div class="crisis-t">${esc(h.title)}</div><div class="crisis-d">${esc(h.description || '')}</div></div>` +
        `<a class="crisis-tel" href="tel:${tel}" aria-label="Позвонить: ${esc(h.title)}">📞 ${esc(h.phone)}</a></div>`;
    }).join('');
  box.classList.remove('hidden');
}

async function createAppeal() {
  hideErr('apErr');
  const desc = $('apDesc').value.trim();
  if (desc.length < 10) {
    showErr('apErr', new Error(t(
      `Описание слишком короткое: нужно хотя бы 10 символов, а сейчас ${desc.length}. Расскажи чуть подробнее, что происходит.`,
      `Описание слишком короткое: нужно хотя бы 10 символов, а сейчас ${desc.length}. Расскажите чуть подробнее, что происходит.`)));
    return;
  }
  const body = {
    applicant_type: $('apType').value,
    free_text: $('apFree').checked,
    description: desc,
    crisis_contact: $('apCrisis').value.trim(),
    answers: [...document.querySelectorAll('#apQuestions select')]
      .map((s) => ({ question: s.dataset.q, answer: s.value }))
      .filter((a) => a.answer)
  };
  if (!body.free_text) {
    if (!$('apCat').value) {
      showErr('apErr', new Error(t('Выбери категорию — так мы скорее найдём подходящего специалиста', 'Выберите категорию — так мы скорее найдём подходящего специалиста')));
      return;
    }
    body.category_id = $('apCat').value;
  }
  try {
    const r = await api('POST', '/api/appeals', body);
    rememberTrack(r.track_number);
    clearDraft(); // черновик больше не нужен — обращение создано
    $('apTrack').textContent = r.track_number;
    $('apResult').classList.remove('hidden');
    $('apResult').scrollIntoView({ behavior: 'smooth', block: 'center' });
    $('apCrisisNote').textContent = r.crisis_detected
      ? t('Похоже, сейчас тебе непросто. Если поддержка нужна срочно — обратись в службу из списка ниже: они работают прямо сейчас.',
          'Похоже, ситуация серьёзная. Если поддержка нужна срочно — обратитесь в службу из списка ниже: они работают прямо сейчас.')
      : '';
    // ТЗ «Кризисные обращения», п.2: полная кризисная помощь сразу, не дожидаясь оператора.
    renderCrisisHelp(r.crisis_help);
    saveTrack(r.track_number);
  } catch (e) {
    showErr('apErr', e);
  }
}

// Открыть только что созданное обращение: активируем сессию по трек-номеру
// и переходим на отдельный эндпоинт /appeal.
async function openMyAppeal() {
  const tn = lastTrackNumber() || $('apTrack').textContent.trim();
  if (!tn) { toast('Сначала отправьте обращение'); return; }
  try {
    await api('POST', '/api/appeals/track', { track_number: tn });
    location.href = '/appeal';
  } catch (e) {
    toast(e.message);
  }
}

export async function intakeInit() {
  $('apFree').addEventListener('change', syncFree);
  $('apType').addEventListener('change', () => applyTone($('apType').value));
  applyTone($('apType').value);
  restoreDraft(); // до загрузки списков: текст обращения важнее всего
  await loadCategories();
  await renderQuestions();
  applyDraftLate(); // категория/анкета из черновика — когда select'ы заполнены
  syncFree();
  // Автосохранение черновика: любое изменение полей формы.
  $('apDesc').addEventListener('input', saveDraft);
  $('apCrisis').addEventListener('input', saveDraft);
  ['apType', 'apCat', 'apFree'].forEach((id) => $(id).addEventListener('change', saveDraft));
  $('apQuestions').addEventListener('change', saveDraft);
}

registerActions({
  'create-appeal': createAppeal,
  'open-my-appeal': openMyAppeal,
  'discard-draft': discardDraft,
});
