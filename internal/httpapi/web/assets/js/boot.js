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

  // Установка PWA: браузер (Chrome/Edge на Android и десктопе) присылает
  // beforeinstallprompt — прячем его и показываем свою кнопку #pwaInstallBtn
  // на главной. iOS это событие не присылает: ставят через «Поделиться →
  // На экран «Домой»», им остаётся обычная инструкция/иконка в манифесте.
  if (mode === 'applicant') {
    var installEvt = null;
    window.addEventListener('beforeinstallprompt', function (e) {
      e.preventDefault();
      installEvt = e;
      var b = document.getElementById('pwaInstallBtn');
      if (b) b.classList.remove('hidden');
    });
    window.addEventListener('appinstalled', function () {
      installEvt = null;
      var b = document.getElementById('pwaInstallBtn');
      if (b) b.classList.add('hidden');
    });
    window.addEventListener('DOMContentLoaded', function () {
      var b = document.getElementById('pwaInstallBtn');
      if (!b) return;
      // Уже установлено (запущено из иконки) — кнопку не показываем.
      if (window.matchMedia && window.matchMedia('(display-mode: standalone)').matches) return;
      if (installEvt) b.classList.remove('hidden');
      b.addEventListener('click', function () {
        if (!installEvt) return;
        installEvt.prompt();
        if (installEvt.userChoice && installEvt.userChoice.finally) {
          installEvt.userChoice.finally(function () {
            installEvt = null;
            b.classList.add('hidden');
          });
        }
      });
    });
  }
})();
