// Выбор с главной («школьник» / «родитель-педагог») подставляем в поле
// «Кто обращается»: intakeInit применит соответствующий тон «ты»/«вы».
// Школьнику поле больше не показываем — роль уже выбрана на главной.
// Взрослому оставляем: на главной не уточнялось, родитель это или педагог.
(function () {
  var role;
  try { role = localStorage.getItem('otklik_role'); } catch (e) { /* приватный режим — останется выбор по умолчанию */ }
  if (role === 'schoolchild' || role === 'adult') {
    var sel = document.getElementById('apType');
    if (sel) {
      sel.value = role === 'schoolchild' ? 'schoolchild' : 'parent';
      if (role === 'schoolchild') {
        var box = sel.closest('div'); // ячейка с подписью «Кто обращается»
        if (box) box.style.display = 'none';
      }
    }
  }
})();
