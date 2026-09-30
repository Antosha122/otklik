-- Кризисные обращения автоматически получают срочный приоритет.
-- Backfill для существующих данных + страховка на случай ручных правок
-- (миграции выполняются при каждом старте, UPDATE идемпотентен).
UPDATE appeals SET priority = 'urgent' WHERE crisis_detected AND priority <> 'urgent';
