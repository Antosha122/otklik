package httpapi

import (
	"fmt"
	"net/http"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"otklik/internal/buildinfo"
)

// Минимальный Prometheus-эндпоинт (текстовый exposition-формат) без внешних
// зависимостей. Счётчики — пакетные переменные, поэтому общие для обоих портов
// и /metrics показывает картину целиком. Маршрут вешается только на служебный
// порт: публичный домен метрики не светит, а скрейпер ходит на staff-порт.
var (
	metricsStart = time.Now()
	reqTotalMu   sync.Mutex
	reqTotal     = map[int]*atomic.Uint64{}
	reqDurSum    atomic.Int64 // наносекунды, суммарно по всем запросам
)

func metricsCount(code int, d time.Duration) {
	reqTotalMu.Lock()
	c, ok := reqTotal[code]
	if !ok {
		c = &atomic.Uint64{}
		reqTotal[code] = c
	}
	reqTotalMu.Unlock()
	c.Add(1)
	reqDurSum.Add(int64(d))
}

func metricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		if sw.status == 0 {
			sw.status = http.StatusOK
		}
		metricsCount(sw.status, time.Since(start))
	})
}

func metricsHandler(w http.ResponseWriter, _ *http.Request) {
	var b strings.Builder
	fmt.Fprintf(&b, "# HELP otklik_build_info Версия сборки.\n# TYPE otklik_build_info gauge\n")
	fmt.Fprintf(&b, "otklik_build_info{version=%q} 1\n", buildinfo.Version)

	fmt.Fprintf(&b, "# HELP http_requests_total Обработанные запросы по коду ответа.\n# TYPE http_requests_total counter\n")
	reqTotalMu.Lock()
	codes := make([]int, 0, len(reqTotal))
	for c := range reqTotal {
		codes = append(codes, c)
	}
	sort.Ints(codes)
	for _, c := range codes {
		fmt.Fprintf(&b, "http_requests_total{status=\"%d\"} %d\n", c, reqTotal[c].Load())
	}
	reqTotalMu.Unlock()

	fmt.Fprintf(&b, "# HELP http_request_duration_seconds Суммарное время обработки запросов.\n# TYPE http_request_duration_seconds summary\n")
	fmt.Fprintf(&b, "http_request_duration_seconds_sum %.6f\n", float64(reqDurSum.Load())/1e9)

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	fmt.Fprintf(&b, "# HELP otklik_uptime_seconds Время работы процесса.\n# TYPE otklik_uptime_seconds gauge\notklik_uptime_seconds %.0f\n", time.Since(metricsStart).Seconds())
	fmt.Fprintf(&b, "# TYPE go_goroutines gauge\ngo_goroutines %d\n", runtime.NumGoroutine())
	fmt.Fprintf(&b, "# TYPE otklik_mem_alloc_bytes gauge\notklik_mem_alloc_bytes %d\n", mem.Alloc)

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = w.Write([]byte(b.String()))
}
