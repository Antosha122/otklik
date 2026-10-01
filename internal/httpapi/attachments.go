package httpapi

import (
	"bytes"
	"database/sql"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"otklik/internal/domain"
)

var allowedContentTypes = map[string]bool{
	"image/jpeg": true, "image/png": true, "image/gif": true,
	"image/webp": true, "application/pdf": true, "text/plain": true,
}

func (s *Server) appealIDFrom(r *http.Request, p domain.Principal) (uuid.UUID, bool) {
	if p.Role == domain.RoleApplicant {
		return p.AppealID, true
	}
	if raw := chi.URLParam(r, "appealID"); raw != "" {
		if id, ok := parseUUID(raw); ok {
			return id, true
		}
	}
	return uuid.Nil, false
}

func (s *Server) handleUploadAttachment(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	appealID, ok := s.appealIDFrom(r, p)
	if !ok {
		writeJSON(w, http.StatusBadRequest, errorResp{"appeal id is invalid"})
		return
	}
	a, err := s.loadAppealWithAccess(r, appealID, p)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if a.Status.Terminal() {
		writeJSON(w, http.StatusConflict, errorResp{"appeal is closed"})
		return
	}
	if n, err := s.st.CountAttachments(r.Context(), appealID); err == nil && n >= domain.MaxAttachmentsPerAppeal {
		writeJSON(w, http.StatusConflict, errorResp{"attachment limit reached"})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, domain.MaxAttachmentSizeBytes+(1<<20))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{"invalid multipart form"})
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{"file field is required"})
		return
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{"cannot read file"})
		return
	}
	if len(data) == 0 || len(data) > domain.MaxAttachmentSizeBytes {
		writeJSON(w, http.StatusBadRequest, errorResp{"file size must be between 1 byte and 10 MB"})
		return
	}
	detected := strings.SplitN(http.DetectContentType(data), ";", 2)[0]
	if !allowedContentTypes[detected] {
		writeJSON(w, http.StatusUnsupportedMediaType, errorResp{"content type is not allowed"})
		return
	}
	data = stripImageMetadata(data, detected)

	storageName := uuid.NewString() + ".bin"
	dir := filepath.Join(s.cfg.AttachmentsDir, appealID.String())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeErr(w, r, err)
		return
	}
	if err := os.WriteFile(filepath.Join(dir, storageName), data, 0o644); err != nil {
		writeErr(w, r, err)
		return
	}
	att, err := s.st.CreateAttachment(r.Context(), appealID, storageName, detected, len(data))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, att)
}

func (s *Server) handleDownloadAttachment(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	appealID, ok := s.appealIDFrom(r, p)
	if !ok {
		writeJSON(w, http.StatusBadRequest, errorResp{"appeal id is invalid"})
		return
	}
	attID, ok := parseUUID(chi.URLParam(r, "attachmentID"))
	if !ok {
		writeJSON(w, http.StatusBadRequest, errorResp{"attachment id is invalid"})
		return
	}
	if _, err := s.loadAppealWithAccess(r, appealID, p); err != nil {
		writeErr(w, r, err)
		return
	}
	att, err := s.st.GetAttachment(r.Context(), appealID, attID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResp{"not found"})
			return
		}
		writeErr(w, r, err)
		return
	}
	path := filepath.Join(s.cfg.AttachmentsDir, appealID.String(), att.StorageName)
	data, err := os.ReadFile(path)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	w.Header().Set("Content-Type", att.ContentType)
	w.Header().Set("Content-Disposition", `inline; filename="attachment.bin"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(data)
}

// Перекодировка из пиксельного буфера: EXIF (включая GPS) не попадает в хранилище.
func stripImageMetadata(data []byte, contentType string) []byte {
	if contentType != "image/jpeg" && contentType != "image/png" {
		return data // gif/webp/pdf/txt без EXIF-геолокации не перекодируем
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return data // не декодируется — сохраняем как есть
	}
	var buf bytes.Buffer
	if contentType == "image/jpeg" {
		err = jpeg.Encode(&buf, img, nil)
	} else {
		err = png.Encode(&buf, img)
	}
	if err != nil || buf.Len() > domain.MaxAttachmentSizeBytes {
		return data
	}
	return buf.Bytes()
}
