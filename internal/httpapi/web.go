package httpapi

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed web
var webFS embed.FS

func assetsHandler() http.Handler {
	sub, err := fs.Sub(webFS, "web/assets")
	if err != nil {
		panic(err)
	}
	fileServer := http.StripPrefix("/assets/", http.FileServer(http.FS(sub)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// У файлов из embed.FS нулевой modtime, поэтому http.FileServer не выдаёт
		// Last-Modified/ETag и браузер держит устаревшие JS-модули по эвристике.
		// ETag по содержимому: пока файл не изменился в новом бинарнике — 304,
		// изменился — браузер обязан забрать свежую версию.
		p := strings.TrimPrefix(r.URL.Path, "/assets/")
		if p != "" && !strings.HasSuffix(p, "/") {
			// .webmanifest неизвестен mime-таблицам ОС — тип ставим сами,
			// иначе браузер может получить application/octet-stream и забраковать манифест.
			if strings.HasSuffix(p, ".webmanifest") {
				w.Header().Set("Content-Type", "application/manifest+json")
			}
			if data, err := fs.ReadFile(sub, path.Clean(p)); err == nil {
				sum := sha256.Sum256(data)
				etag := `"` + hex.EncodeToString(sum[:8]) + `"`
				w.Header().Set("Cache-Control", "no-cache")
				w.Header().Set("ETag", etag)
				if r.Header.Get("If-None-Match") == etag {
					w.WriteHeader(http.StatusNotModified)
					return
				}
			}
		}
		fileServer.ServeHTTP(w, r)
	})
}

// swHandler отдаёт service worker заявителя (PWA). Файл лежит в assets, но
// маршрутизируется с корня (/sw.js): scope SW определяется путём файла, а не
// манифестом, и заголовок Service-Worker-Allowed: "/" разрешает перехват
// всех страниц заявителя. no-cache — браузер обязан сверяться с сервером,
// иначе застряла бы старая версия воркера после деплоя.
func swHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		data, err := webFS.ReadFile("web/assets/sw.js")
		if err != nil {
			http.Error(w, "sw not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.Header().Set("Service-Worker-Allowed", "/")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(data)
	}
}

func (s *Server) pageFor(mode, file string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		data, err := webFS.ReadFile("web/" + file)
		if err != nil {
			http.Error(w, "ui not found", http.StatusNotFound)
			return
		}
		page := strings.ReplaceAll(string(data), "__OTKLIK_UI_MODE__", mode)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store") // HTML всегда свежий: он разный для 8080/8081
		_, _ = w.Write([]byte(page))
	}
}
