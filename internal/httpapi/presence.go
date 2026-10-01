package httpapi

import (
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"

	"otklik/internal/domain"
)

// Лёгкое in-memory присутствие: heartbeat + TTL; достаточно для одиночного инстанса.

type presenceEntry struct {
	login    string
	role     domain.Role
	lastSeen time.Time
	typing   bool
}

type presenceKey struct {
	appeal uuid.UUID
	user   uuid.UUID
}

type presenceHub struct {
	mu      sync.Mutex
	entries map[presenceKey]presenceEntry
}

const presenceTTL = 15 * time.Second

func newPresenceHub() *presenceHub {
	return &presenceHub{entries: map[presenceKey]presenceEntry{}}
}

func (h *presenceHub) gcLocked() {
	for k, e := range h.entries {
		if time.Since(e.lastSeen) > presenceTTL {
			delete(h.entries, k)
		}
	}
}

func (h *presenceHub) touch(appealID, userID uuid.UUID, login string, role domain.Role, typing bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.entries[presenceKey{appeal: appealID, user: userID}] = presenceEntry{
		login: login, role: role, lastSeen: time.Now(), typing: typing,
	}
	h.gcLocked()
}

func (h *presenceHub) list(appealID, exceptUser uuid.UUID) []map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.gcLocked()
	var out []map[string]any
	for k, e := range h.entries {
		if k.appeal != appealID || k.user == exceptUser {
			continue
		}
		out = append(out, map[string]any{
			"login": e.login, "role": string(e.role), "typing": e.typing,
		})
	}
	return out
}

type presenceReq struct {
	Typing bool `json:"typing"`
}

func (s *Server) handlePresencePost(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	id, ok := s.appealID(w, r)
	if !ok {
		return
	}
	if _, err := s.loadAppealWithAccess(r, id, p); err != nil {
		writeErr(w, r, err)
		return
	}
	var req presenceReq
	if !decodeJSON(w, r, &req) {
		return
	}
	s.presence.touch(id, p.UserID, p.Login, p.Role, req.Typing)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handlePresenceGet(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	id, ok := s.appealID(w, r)
	if !ok {
		return
	}
	if _, err := s.loadAppealWithAccess(r, id, p); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"present": s.presence.list(id, p.UserID)})
}
