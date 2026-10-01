// Тесты словаря ошибок: node --test (из корня: npm test).
import test from 'node:test';
import assert from 'node:assert/strict';
import { humanError } from './errors.js';

test('humanError переводит известные серверные сообщения', () => {
  assert.equal(humanError('invalid credentials', 401), 'Неверный логин или пароль');
  assert.equal(humanError('not found', 404), 'Не найдено');
  assert.equal(humanError('rate limited', 429), 'Слишком много запросов — попробуйте позже');
});

test('humanError показывает уже русские сообщения как есть', () => {
  assert.equal(humanError('достигнут лимит возвратов: обращение можно завершить', 409),
    'достигнут лимит возвратов: обращение можно завершить');
});

test('humanError: неизвестное сообщение уходит в фолбэк по статусу', () => {
  assert.equal(humanError('something unexpected', 404), 'Не найдено');
  assert.match(humanError('boom', 500), /Внутренняя ошибка/);
  assert.match(humanError('boom', 418), /HTTP 418/);
});

test('humanError: пустое сообщение без статуса не ломает вызов', () => {
  assert.equal(typeof humanError('', 0), 'string');
});
