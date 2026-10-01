// Лендинг заявителя (порт 8080): выбор «школьник / родитель-педагог»
// запоминается в localStorage и сразу ведёт на форму обращения —
// поле «Кто обращается» и тон «ты»/«вы» подставятся на /new.
// Служебному порту (8081) выбор не нужен: там roleChoice скрыт классом staff-mode.
(function () {
  if (window.UI_MODE === 'staff') return;
  document.querySelectorAll('#roleChoice [data-choice]').forEach(function (card) {
    card.addEventListener('click', function () {
      var role = card.getAttribute('data-choice');
      try { localStorage.setItem('otklik_role', role); } catch (e) { /* приватный режим — форма всё равно откроется */ }
      location.href = '/new';
    });
  });
})();
