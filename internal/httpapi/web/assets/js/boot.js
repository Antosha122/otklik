// Инициализация режима UI до загрузки модулей (main.js — module, исполняется
// после парсинга). Режим подставляет сервер в data-атрибуте <body data-ui-mode>:
// applicant — публичный порт, staff — служебный. Инлайн-скрипты не используются,
// чтобы CSP обходился без 'unsafe-inline' для script-src.
(function () {
  var mode = document.body.getAttribute('data-ui-mode') === 'staff' ? 'staff' : 'applicant';
  window.UI_MODE = mode;
  document.body.classList.add(mode + '-mode');

  // PWA (только заявитель): регистрируем service worker после загрузки
  // страницы, чтобы не конкурировать с основными запросами. На служебном
  // порту 8081 маршрута /sw.js нет, поэтому сотрудникам он не предлагается.
  if (mode === 'applicant' && 'serviceWorker' in navigator) {
    window.addEventListener('load', function () {
      navigator.serviceWorker.register('/sw.js').catch(function () {
        // Офлайн-режим не критичен: тихо игнорируем (нет сети и т.п.).
      });
    });
  }
})();
