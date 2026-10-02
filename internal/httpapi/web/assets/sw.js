// Service worker заявителя (PWA). Файл физически лежит в assets, но отдаётся
// с корня (маршрут /sw.js в server.go) — scope SW определяется путём, и из
// /assets/ он перехватывал бы только статику, а не страницы /, /new, /track,
// /appeal. Заголовок Service-Worker-Allowed: / закрепляет scope за корнем.
//
// CACHE_VERSION подставляется сервером при отдаче (buildinfo.Version из
// -ldflags при сборке): каждый деплой автоматически даёт новое имя кэша,
// вручную ничего поднимать не нужно. В activate удаляются кэши старых версий.
// Ассеты на сервере с ETag/no-cache, HTML — no-store, поэтому конфликтов
// «протухшей» версии страницы не возникает.
const CACHE_VERSION = 'otklik-__OTKLIK_SW_CACHE__';

// Минимальный набор для офлайн-запуска: страницы заявителя + оформление.
// Остальные модули (api.js, poller.js и т.п.) догружаются и кэшируются
// по факту первого визита — стратегия stale-while-revalidate ниже.
const PRECACHE = [
  '/',
  '/new',
  '/track',
  '/appeal',
  '/assets/css/base.css',
  '/assets/css/components.css',
  '/assets/css/layout.css',
  '/assets/css/responsive.css',
  '/assets/js/boot.js',
  '/assets/js/main.js',
  '/assets/js/core/actions.js',
  '/assets/icons/icon-192.png',
  '/assets/icons/icon-512.png',
];

self.addEventListener('install', (event) => {
  event.waitUntil(
    caches.open(CACHE_VERSION)
      .then((cache) => cache.addAll(PRECACHE))
      .then(() => self.skipWaiting())
  );
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches.keys()
      .then((keys) => Promise.all(keys.filter((k) => k !== CACHE_VERSION).map((k) => caches.delete(k))))
      .then(() => self.clients.claim())
  );
});

// --- Web Push (RFC 8030/8291) ---
// Сервер присылает зашифрованный JSON {title, body, url}. Показываем
// системное уведомление; клик по нему открывает/фокусирует страницу обращения.
// userVisibleOnly: каждое пришедшее push-сообщение обязано показывать
// уведомление — иначе браузер отзовёт подписку.
self.addEventListener('push', (event) => {
  let data = {};
  try { data = event.data ? event.data.json() : {}; } catch (e) { /* пустой/битый payload — дефолт */ }
  event.waitUntil(self.registration.showNotification(data.title || 'Отклик', {
    body: data.body || 'Новое событие по вашему обращению',
    icon: '/assets/icons/icon-192.png',
    badge: '/assets/icons/icon-192.png',
    tag: data.tag || 'otklik-push', // один тег: уведомления заменяют друг друга, а не копятся
    renotify: true,                 // ...но каждое новое снова привлекает внимание
    data: { url: data.url || '/appeal' },
  }));
});

self.addEventListener('notificationclick', (event) => {
  event.notification.close();
  const url = (event.notification.data && event.notification.data.url) || '/appeal';
  event.waitUntil(
    self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then((cs) => {
      for (const c of cs) {
        // Уже открытая страница обращения — просто фокусируем.
        if (c.url.includes(url) && 'focus' in c) return c.focus();
      }
      for (const c of cs) {
        if ('focus' in c && 'navigate' in c) return c.focus().then(() => c.navigate(url));
      }
      return self.clients.openWindow(url);
    })
  );
});

self.addEventListener('fetch', (event) => {
  const req = event.request;
  if (req.method !== 'GET') return;

  const url = new URL(req.url);
  if (url.origin !== self.location.origin) return; // чужие ресурсы не трогаем
  // API всегда мимо кэша: чат, статусы и очередь обязаны быть живыми,
  // а ответы /api/* содержат приватные данные заявителя.
  if (url.pathname.startsWith('/api/')) return;

  // Навигация (HTML): сеть в приоритете (сервер отдаёт no-store), офлайн —
  // последняя закэшированная версия страницы, крайний случай — главная.
  if (req.mode === 'navigate') {
    event.respondWith(
      fetch(req)
        .then((res) => {
          const copy = res.clone();
          caches.open(CACHE_VERSION).then((c) => c.put(req, copy));
          return res;
        })
        .catch(() =>
          caches.match(req).then((cached) => cached || caches.match('/'))
        )
    );
    return;
  }

  // Статика: stale-while-revalidate — мгновенно из кэша, свежую версию
  // качаем фоном (ETag на сервере гарантирует 304, если ничего не менялось).
  event.respondWith(
    caches.match(req).then((cached) => {
      const fresh = fetch(req)
        .then((res) => {
          if (res && res.ok) {
            const copy = res.clone();
            caches.open(CACHE_VERSION).then((c) => c.put(req, copy));
          }
          return res;
        })
        .catch(() => cached);
      return cached || fresh;
    })
  );
});
