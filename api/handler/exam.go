package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"studdle/backend/internal/authctx"
	"studdle/backend/internal/httpx"
	"studdle/backend/internal/myErrors"
	"studdle/backend/pkg/exam"
)

// ExamHandler exposes exam CRUD endpoints.
type ExamHandler struct {
	svc *exam.Service // svc is the exam domain service
}

// NewExamHandler constructs the handler.
func NewExamHandler(svc *exam.Service) *ExamHandler {
	return &ExamHandler{svc: svc}
}

// examBody is the JSON shape for create + update.
// Dates are accepted as YYYY-MM-DD strings to keep the wire form unambiguous.
type examBody struct {
	SubjectID      int64   `json:"subjectId"`                // SubjectID is required on create; immutable on update
	Title          string  `json:"title"`                    // Title is the human-readable label
	Notes          string  `json:"notes"`                    // Notes is an optional free-form description
	ExamDate       string  `json:"examDate"`                 // ExamDate is the YYYY-MM-DD scheduled day
	AnnalesImageID *string `json:"annalesImageId,omitempty"` // AnnalesImageID is an optional annales PDF reference
}

// Create handles POST /exams.
func (h *ExamHandler) Create(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	body, err := decodeExamBody(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	in, err := body.toInput()
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	created, err := h.svc.Create(r.Context(), uid, in)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, created)
}

// List handles GET /exams.
func (h *ExamHandler) List(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	exams, err := h.svc.List(r.Context(), uid)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	if exams == nil {
		exams = []exam.Exam{}
	}
	httpx.WriteJSON(w, http.StatusOK, exams)
}

// Get handles GET /exams/{id}.
func (h *ExamHandler) Get(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	id, err := pathInt64(r, "id")
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	e, err := h.svc.Get(r.Context(), uid, id)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, e)
}

// Update handles PUT /exams/{id}.
func (h *ExamHandler) Update(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	id, err := pathInt64(r, "id")
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	body, err := decodeExamBody(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	in, err := body.toInput()
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	updated, err := h.svc.Update(r.Context(), uid, id, in)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, updated)
}

// Delete handles DELETE /exams/{id}.
func (h *ExamHandler) Delete(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	id, err := pathInt64(r, "id")
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	if err := h.svc.Delete(r.Context(), uid, id); err != nil {
		httpx.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// decodeExamBody parses the request body into examBody.
func decodeExamBody(r *http.Request) (examBody, error) {
	var b examBody
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		return b, &myErrors.AppError{Code: "invalid_input", Message: "malformed JSON", Wrapped: myErrors.ErrInvalidInput}
	}
	return b, nil
}

// toInput converts the wire shape into the domain Input. Parses examDate.
func (b examBody) toInput() (exam.Input, error) {
	d, err := time.Parse("2006-01-02", b.ExamDate)
	if err != nil {
		return exam.Input{}, &myErrors.AppError{
			Code: "validation", Message: "examDate must be YYYY-MM-DD",
			Wrapped: myErrors.ErrValidation, Field: "examDate",
		}
	}
	return exam.Input{
		SubjectID:      b.SubjectID,
		Title:          b.Title,
		Notes:          b.Notes,
		ExamDate:       d,
		AnnalesImageID: b.AnnalesImageID,
	}, nil
}

// pathInt64 reads a {name} path parameter as int64, returning a clear validation error.
func pathInt64(r *http.Request, name string) (int64, error) {
	raw := r.PathValue(name)
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, &myErrors.AppError{
			Code: "validation", Message: fmt.Sprintf("%s must be a positive integer", name),
			Wrapped: myErrors.ErrValidation, Field: name,
		}
	}
	return id, nil
}
