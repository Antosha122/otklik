// Единая точка перевода ошибок на русский язык.
// Сервер отвечает машинными английскими сообщениями (их же проверяют
// интеграционные тесты), а пользователь во фронте должен видеть русский текст.
// humanError(raw, status) всегда возвращает строку на русском.

const SERVER_ERRORS_RU = {
  'invalid export filter: check status, priority, group, crisis, from/to':
    'Проверьте фильтры выгрузки: статус, приоритет, специальность, кризисные, период',
  'login and password are required': 'Введите логин и пароль',
  'invalid credentials': 'Неверный логин или пароль',
  'user is deactivated': 'Учётная запись деактивирована',
  'staff authentication required': 'Требуется вход для персонала',
  'too many attempts, try later': 'Слишком много попыток — попробуйте позже',
  'current_password and new_password are required': 'Укажите текущий и новый пароль',
  'password must be at least 8 characters': 'Пароль должен быть не короче 8 символов',
  'new password must differ from current': 'Новый пароль должен отличаться от текущего',
  'password change required': 'Требуется сменить пароль по умолчанию',

  'totp code required': 'Введите код двухфакторной аутентификации',
  'invalid totp code': 'Неверный код двухфакторной аутентификации',
  'totp is already enabled: disable it first': 'Двухфакторная аутентификация уже включена — сначала отключите её',
  '2FA is disabled on this server (set TOTP_ENABLED=1)': 'Двухфакторная аутентификация отключена на этом сервере',
  'run totp setup first': 'Сначала запустите настройку 2FA и добавьте секрет в приложение',
  'invalid password': 'Неверный пароль',

  'applicant_type must be schoolchild, parent or teacher': 'Тип заявителя указан неверно',
  'category_id is required unless free_text is true': 'Выберите категорию или отправьте обращение свободным текстом',
  'category_id is invalid': 'Категория выбрана неверно',
  'description is too short: minimum 10 characters': 'Описание слишком короткое: минимум 10 символов',
  'description is too long (max 8000 characters)': 'Описание слишком длинное (максимум 8000 символов)',
  'crisis_contact is too long': 'Контакт для кризисной связи слишком длинный',
  'too many appeals from this address': 'Слишком много обращений с этого адреса — попробуйте позже',
  'track_number is required': 'Укажите трек-номер обращения',
  'track number verification required': 'Требуется подтвердить трек-номер обращения',
  'appeal not found': 'Обращение не найдено',
  'appeal id is invalid': 'Обращение указано неверно',
  'appeal is closed': 'Обращение закрыто',
  'appeal is already completed/rejected/closed_no_response — it is archived and its status cannot be changed':
    'Обращение уже завершено (выполнено, отклонено или закрыто без ответа) — оно в архиве, и изменить его статус нельзя',
  'addition is too short: minimum 10 characters': 'Дополнение слишком короткое: минимум 10 символов',
  'addition is too long (max 4000 characters)': 'Дополнение слишком длинное (максимум 4000 символов)',
  'message text length must be 1..4000': 'Сообщение должно быть от 1 до 4000 символов',
  'rating must be between 1 and 5': 'Оценка должна быть от 1 до 5',
  'complaint text is too short': 'Текст жалобы слишком короткий',
  'intake answer is too long': 'Ответ на уточняющий вопрос слишком длинный',
  'idempotency_key is too long': 'Ключ идемпотентности слишком длинный',

  'file field is required': 'Не выбран файл',
  'cannot read file': 'Не удалось прочитать файл',
  'file size must be between 1 byte and 10 MB': 'Размер файла должен быть от 1 байта до 10 МБ',
  'content type is not allowed': 'Этот тип файла не разрешён',
  'invalid multipart form': 'Некорректная форма загрузки файла',
  'attachment id is invalid': 'Файл указан неверно',
  'attachment limit reached': 'Достигнут лимит вложений',

  'insufficient permissions': 'Недостаточно прав для этого действия',
  'not found': 'Не найдено',
  'internal error': 'Внутренняя ошибка сервера — попробуйте позже',
  'invalid JSON body': 'Некорректный формат запроса',
  'validation error': 'Проверьте правильность заполнения полей',
  'state conflict': 'Действие несовместимо с текущим состоянием обращения',
  'rate limited': 'Слишком много запросов — попробуйте позже',
  'status is invalid': 'Указан недопустимый статус',

  'assign is available to operator and admin only': 'Назначение специалиста доступно только оператору и администратору',
  'reject is available to operator only': 'Отклонение доступно только оператору',
  'complete is available to operator only': 'Завершение доступно только оператору',
  'rejection reason is required (min 5 characters)': 'Укажите причину отклонения (минимум 5 символов)',
  'recommendation is required (min 10 characters)': 'Укажите рекомендацию (минимум 10 символов)',
  'priority change is available to operator and admin only': 'Смена приоритета доступна только оператору и администратору',
  'priority must be low, normal or urgent': 'Приоритет должен быть: низкий, обычный или срочный',
  'return for rework is available to operator only': 'Возврат на доработку доступен только оператору',
  'return reason is required (min 5 characters)': 'Укажите причину возврата (минимум 5 символов)',
  'transfer reason is required (min 5 characters)': 'Укажите причину передачи (минимум 5 символов)',
  'category change is available to operator and admin only': 'Смена категории доступна только оператору и администратору',
  'category id is invalid': 'Категория указана неверно',
  'category not found': 'Категория не найдена',
  'category not found or inactive': 'Категория не найдена или неактивна',
  'status override is available to admin only': 'Ручная смена статуса доступна только администратору',
  'terminal status is not allowed: completed, rejected and closed_no_response are set by the process, not by admin':
    'Терминальные статусы (выполнено, отклонено, закрыто без ответа) выставляются процессом, а не вручную',
  'events are available to operator and admin only': 'Журнал событий доступен только оператору и администратору',

  'chat is available to expert only': 'Чат доступен только специалисту',
  'notes are available to expert only': 'Заметки доступны только специалисту',
  'closing is available to operator only': 'Закрытие обращения доступно только оператору',
  'can only close from needs_clarification or answer_ready': 'Закрыть обращение можно только после уточнений или готового ответа',
  'only the responsible expert can do this': 'Это действие доступно только ответственному специалисту',
  'this action is available to expert only': 'Это действие доступно только специалисту',
  'expert_id is invalid': 'Специалист указан неверно',
  'expert not found, inactive or not an expert': 'Специалист не найден, деактивирован или не является специалистом',
  'cannot add yourself as a contributor': 'Нельзя добавить самого себя соисполнителем',
  'personal stats are available to operator and expert only': 'Личная статистика доступна только оператору и специалисту',
  'comment is too long': 'Комментарий слишком длинный',
  'note text length must be 1..4000': 'Заметка должна быть от 1 до 4000 символов',

  'login length must be 3..50': 'Логин должен быть от 3 до 50 символов',
  'role must be operator, expert or admin': 'Роль указана неверно',
  'specialist_group must be psychologists, conflictologists, lawyers or social_pedagogues': 'Группа специалистов указана неверно',
  'user id is invalid': 'Пользователь указан неверно',
  'category name length must be 3..100': 'Название категории должно быть от 3 до 100 символов',
  'cross-origin request rejected': 'Запрос отклонён: это действие доступно только с сайта Отклик',
  'page and per_page must be positive integers (per_page max 100)': 'Неверные параметры постраничной выдачи (per_page не больше 100)',
  'crisis flag is available to operator only': 'Кризисную пометку меняет только оператор',
  'detected must be true or false': 'Укажите новое значение кризисной пометки',
  'reason is required (min 5 characters)': 'Укажите причину (минимум 5 символов)',

  'kind must be substring or word': 'Выберите тип маркера: подстрока или по границе слова',
  'text length must be 2..100 characters': 'Маркер должен быть от 2 до 100 символов',
  'marker id is invalid': 'Маркер указан неверно',
  'invalid push subscription': 'Не удалось подписаться на уведомления — обновите страницу и попробуйте снова',
  'endpoint is required': 'Не удалось отписаться от уведомлений',
};

// Фолбэк по HTTP-коду: когда тело без текста ошибки либо сообщение не из словаря.
const STATUS_RU = {
  400: 'Некорректный запрос — проверьте введённые данные',
  401: 'Требуется вход в систему',
  403: 'Недостаточно прав для этого действия',
  404: 'Не найдено',
  405: 'Действие недоступно',
  409: 'Действие несовместимо с текущим состоянием — обновите страницу',
  413: 'Отправлено слишком много данных',
  415: 'Этот тип файла не разрешён',
  422: 'Проверьте правильность заполнения полей',
  429: 'Слишком много запросов — попробуйте позже',
  500: 'Внутренняя ошибка сервера — попробуйте позже',
  502: 'Сервер временно недоступен — попробуйте позже',
  503: 'Сервер временно недоступен — попробуйте позже',
};

const hasCyrillic = (s) => /[а-яА-ЯёЁ]/.test(s);

export function humanError(raw, status) {
  if (raw && SERVER_ERRORS_RU[raw]) return SERVER_ERRORS_RU[raw];
  // Сервер иногда отвечает уже по-русски (например, лимиты настроек) — показываем как есть.
  if (raw && hasCyrillic(raw)) return raw;
  if (status >= 500) return STATUS_RU[500] + ' (HTTP ' + status + ')';
  return STATUS_RU[status] || ('Ошибка запроса (HTTP ' + status + ')');
}

