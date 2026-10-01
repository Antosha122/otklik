package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"otklik/internal/store"
)

var errBadPagination = errors.New("page and per_page must be positive integers (per_page max 100)")

const (
	defaultPerPage = 20
	maxPerPage     = 100
)

// pageQuery — разобранные параметры ?page=&per_page= (page с 1).
type pageQuery struct {
	Page    int
	PerPage int
	Offset  int
}

func parsePageQuery(r *http.Request) (pageQuery, store.Page, bool, error) {
	pq := pageQuery{Page: 1, PerPage: defaultPerPage}
	q := r.URL.Query()
	bad := func() (pageQuery, store.Page, bool, error) {
		return pq, store.Page{}, false, errBadPagination
	}
	if v := q.Get("page"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return bad()
		}
		pq.Page = n
	}
	if v := q.Get("per_page"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxPerPage {
			return bad()
		}
		pq.PerPage = n
	}
	pq.Offset = (pq.Page - 1) * pq.PerPage
	return pq, store.Page{Limit: pq.PerPage, Offset: pq.Offset}, true, nil
}

// pagedBody собирает ответ спискового эндпоинта: данные + метаданные страниц.
func pagedBody(key string, items any, pq pageQuery, total int) map[string]any {
	pages := 0
	if pq.PerPage > 0 {
		pages = (total + pq.PerPage - 1) / pq.PerPage
	}
	body := map[string]any{
		key:           items,
		"page":        pq.Page,
		"per_page":    pq.PerPage,
		"total":       total,
		"total_pages": pages,
	}
	return body
}
