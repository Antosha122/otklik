// Поллер, экономящий батарею в фоне: пока вкладка видна — обычный интервал,
// в фоне — в 6 раз реже (30 с вместо 5 с). Полностью не останавливаем:
// фоновый опрос нужен, чтобы показать уведомление о новом сообщении
// специалиста, пока заявитель не смотрит на вкладку.
export function createPoller(task, intervalMs = 5000) {
  let timer = null;
  let busy = false;
  let wanted = false;

  async function tick() {
    if (busy) return;
    busy = true;
    try { await task(); } finally { busy = false; }
  }

  function arm(ms) {
    if (timer) clearInterval(timer);
    timer = setInterval(tick, ms);
  }

  document.addEventListener('visibilitychange', () => {
    if (!wanted || !timer) return;
    if (document.hidden) {
      arm(intervalMs * 6);
    } else {
      tick(); // сразу свежие данные после возврата на вкладку
      arm(intervalMs);
    }
  });

  return {
    start() { wanted = true; if (!timer) arm(intervalMs); },
    stop() { wanted = false; if (timer) { clearInterval(timer); timer = null; } },
    get running() { return timer !== null; },
  };
}
