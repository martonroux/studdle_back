package handler

import (
	"io"
	"net/http"

	"studdle/backend/internal/authctx"
	"studdle/backend/internal/httpx"
	"studdle/backend/internal/myErrors"
	"studdle/backend/pkg/image"
)

// ImageHandler handles upload / serve / delete.
type ImageHandler struct {
	svc *image.Service // svc is the image domain service
}

// NewImageHandler constructs the handler.
func NewImageHandler(svc *image.Service) *ImageHandler {
	return &ImageHandler{svc: svc}
}

// Upload handles POST /upload-image (multipart).
// Optional `purpose` form field switches the validation profile (e.g. "exam_annales" allows PDFs).
// Body cap is 6 MiB by default; exam_annales widens it to 7 MiB to leave headroom over the 5 MB PDF cap.
func (h *ImageHandler) Upload(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	purpose := image.Purpose(r.URL.Query().Get("purpose"))
	r.Body = http.MaxBytesReader(w, r.Body, uploadBodyCap(purpose))
	if err := r.ParseMultipartForm(parseFormCap(purpose)); err != nil {
		httpx.WriteError(w, myErrors.ErrInvalidInput)
		return
	}
	if purpose == "" {
		purpose = image.Purpose(r.FormValue("purpose"))
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		httpx.WriteError(w, myErrors.ErrInvalidInput)
		return
	}
	defer file.Close()
	img, err := h.svc.Upload(r.Context(), uid, file, hdr.Filename, purpose)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"id": img.ID, "url": h.svc.URL(img.ID)})
}

// uploadBodyCap returns the hard request-body cap for the given purpose.
// Annales accept PDFs up to 5 MiB, so the body cap is widened to 7 MiB to allow form-encoding overhead.
func uploadBodyCap(p image.Purpose) int64 {
	if p == image.PurposeExamAnnales {
		return 7 << 20
	}
	return 6 << 20
}

// parseFormCap returns the in-memory limit for ParseMultipartForm based on purpose.
func parseFormCap(p image.Purpose) int64 {
	if p == image.PurposeExamAnnales {
		return 6 << 20
	}
	return 5 << 20
}

// Serve handles GET /images/{id}.
func (h *ImageHandler) Serve(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rc, mime, err := h.svc.Open(r.Context(), id)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = io.Copy(w, rc)
}

// Delete handles POST /delete-image?id=...
func (h *ImageHandler) Delete(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	id := r.URL.Query().Get("id")
	if id == "" {
		httpx.WriteError(w, myErrors.ErrInvalidInput)
		return
	}
	if err := h.svc.Delete(r.Context(), uid, id); err != nil {
		httpx.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
