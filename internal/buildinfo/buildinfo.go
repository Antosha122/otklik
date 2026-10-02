// Package buildinfo хранит версию сборки. Значение подставляется при сборке:
//
//	go build -ldflags "-X otklik/internal/buildinfo.Version=$(git rev-parse --short HEAD)"
//
// Без флага — "dev" (локальная разработка и go test). Используется в
// /api/health (проверка выката) и как версия кэша service worker: после
// деплоя SW получает новое имя кэша и чистит старое автоматически.
package buildinfo

var Version = "dev"
