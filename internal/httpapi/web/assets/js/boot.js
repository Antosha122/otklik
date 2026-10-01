// Инициализация режима UI до загрузки модулей (main.js — module, исполняется
// после парсинга). Режим подставляет сервер в data-атрибуте <body data-ui-mode>:
// applicant — публичный порт, staff — служебный. Инлайн-скрипты не используются,
// чтобы CSP обходился без 'unsafe-inline' для script-src.
(function () {
  var mode = document.body.getAttribute('data-ui-mode') === 'staff' ? 'staff' : 'applicant';
  window.UI_MODE = mode;
  document.body.classList.add(mode + '-mode');
})();
