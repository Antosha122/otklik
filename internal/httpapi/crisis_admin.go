package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"otklik/internal/domain"
	"otklik/internal/store"
)

// crisisCacheTTL — сколько живёт кэш словаря кризисных маркеров. Админ меняет
// словарь редко, а детектор гоняется на каждый текст заявителя; минутная
// задержка применения изменений несущественна.
const crisisCacheTTL = time.Minute

// detectCrisis — детектор на актуальном словаре из БД (кэш на минуту).
// Фолбэк — встроенные списки: детектор обязан работать всегда, даже если БД
// на миграции словаря ответила ошибкой.
func (s *Server) detectCrisis(ctx context.Context, texts ...string) bool {
	subs, words := s.crisisLists(ctx)
	return domain.DetectCrisisWith(subs, words, texts...)
}

func (s *Server) crisisLists(ctx context.Context) (substrings, words []string) {
	s.crisisMu.Lock()
	defer s.crisisMu.Unlock()
	if s.crisisAt.After(time.Now()) && s.crisisSubs != nil {
		return s.crisisSubs, s.crisisWords
	}
	substrings, words = domain.CrisisMarkers, domain.CrisisWordMarkers
	if s.st != nil {
		subs, wrds, err := s.st.CrisisLists(ctx)
		if err != nil {
			slog.Error("crisis: чтение словаря, используется встроенный", "error", err.Error())
		} else if len(subs)+len(wrds) > 0 {
			substrings, words = subs, wrds
		}
		s.crisisSubs, s.crisisWords = substrings, words
	}
	s.crisisAt = time.Now().Add(crisisCacheTTL)
	return substrings, words
}

// --- Админские CRUD-эндпоинты словаря (панель администратора) ---

func (s *Server) handleAdminListCrisisMarkers(w http.ResponseWriter, r *http.Request) {
	markers, err := s.st.ListCrisisMarkers(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if markers == nil {
		markers = []store.CrisisMarker{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"markers": markers})
}

type addCrisisMarkerReq struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

func (s *Server) handleAdminAddCrisisMarker(w http.ResponseWriter, r *http.Request) {
	var req addCrisisMarkerReq
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Text = strings.TrimSpace(req.Text)
	if req.Kind != "substring" && req.Kind != "word" {
		writeJSON(w, http.StatusBadRequest, errorResp{"kind must be substring or word"})
		return
	}
	// Нормализация маркера — как в детекторе: малый регистр, ё→е.
	req.Text = strings.ToLower(strings.ReplaceAll(req.Text, "ё", "е"))
	if n := len([]rune(req.Text)); n < 2 || n > 100 {
		writeJSON(w, http.StatusBadRequest, errorResp{"text length must be 2..100 characters"})
		return
	}
	m, err := s.st.AddCrisisMarker(r.Context(), req.Kind, req.Text)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	s.invalidateCrisisCache()
	writeJSON(w, http.StatusCreated, m)
}

func (s *Server) handleAdminDeleteCrisisMarker(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(chi.URLParam(r, "markerID"))
	if !ok {
		writeJSON(w, http.StatusBadRequest, errorResp{"marker id is invalid"})
		return
	}
	if err := s.st.DeleteCrisisMarker(r.Context(), id); err != nil {
		writeErr(w, r, err)
		return
	}
	s.invalidateCrisisCache()
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) invalidateCrisisCache() {
	s.crisisMu.Lock()
	s.crisisAt = time.Time{}
	s.crisisMu.Unlock()
}
