package handler

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"studdle/backend/internal/aiProvider"
	"studdle/backend/internal/authctx"
	"studdle/backend/internal/httpx"
	"studdle/backend/internal/myErrors"
	"studdle/backend/pkg/aipipeline"
)

const (
	// pdfImageModeMaxPages caps the page count we send to the vision model.
	// Above this, the handler returns ErrPDFImageModeUnavailable so the
	// frontend can offer text-mode retry.
	pdfImageModeMaxPages = 30

	// pdfTextModeMaxPages caps the page count for text-mode generation.
	pdfTextModeMaxPages = 200

	// pdfTextModeMaxChars caps the total extracted character count for
	// text-mode generation. ~400k chars is a comfortable buffer below
	// Claude's 200k-token context window.
	pdfTextModeMaxChars = 400_000
)

// pdfGenInput captures the form fields for POST /ai/flashcards/pdf.
type pdfGenInput struct {
	SubjectID    int64  // SubjectID is the target subject
	ChapterID    int64  // ChapterID is optional; when set, suppresses auto-chapters
	Coverage     string // Coverage is "Core" | "Balanced" | "Comprehensive"
	Style        string // Style is "short" | "standard" | "detailed"
	Focus        string // Focus is an optional narrowing phrase
	AutoChapters bool   // AutoChapters requests proposed chapter splits
	Mode         string // Mode is "image" (default) or "text"
	PDFBytes     []byte // PDFBytes is the uploaded file
}

// GenerateFromPDF is the SSE endpoint for PDF-based flashcard generation.
// Branches on the `mode` form field: image (default) rasterizes pages,
// text extracts per-page text. The text path is used as a fallback when
// image mode is rejected for size reasons.
func (h *AIHandler) GenerateFromPDF(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	in, err := parsePDFForm(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	switch in.Mode {
	case "image":
		h.runImageMode(r.Context(), w, uid, in)
	case "text":
		h.runTextMode(r.Context(), w, uid, in)
	default:
		httpx.WriteError(w, &myErrors.AppError{
			Code: "validation", Message: "mode must be image or text",
			Wrapped: myErrors.ErrValidation, Field: "mode",
		})
	}
}

// runImageMode is the legacy path: rasterize each page and send images to Claude.
// Rejects >30-page PDFs with pdf_image_mode_unavailable so the frontend can
// offer text-mode retry.
func (h *AIHandler) runImageMode(ctx context.Context, w http.ResponseWriter, uid int64, in pdfGenInput) {
	if err := checkImageModePageCount(in.PDFBytes); err != nil {
		httpx.WriteError(w, err)
		return
	}
	images, err := rasterizePDF(ctx, in.PDFBytes)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	rendered, err := h.prepareGeneration(ctx, uid, in)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	h.runPDFGeneration(ctx, w, uid, in, rendered, images)
}

// checkImageModePageCount verifies the PDF has at most pdfImageModeMaxPages.
// Returns pdf_unreadable on count failure, pdf_image_mode_unavailable when
// over the cap, or nil when within the limit.
func checkImageModePageCount(pdfBytes []byte) error {
	pages, err := aiProvider.PDFPageCount(pdfBytes)
	if err != nil {
		return &myErrors.AppError{Code: "pdf_unreadable", Message: err.Error(), Wrapped: myErrors.ErrValidation}
	}
	if pages > pdfImageModeMaxPages {
		return &myErrors.AppError{
			Code:    "pdf_image_mode_unavailable",
			Message: fmt.Sprintf("pdf has %d pages, image mode supports up to %d", pages, pdfImageModeMaxPages),
			Wrapped: myErrors.ErrPDFImageModeUnavailable,
		}
	}
	return nil
}

// runTextMode extracts text per page and sends it as a single text content
// block. Used when image mode is not viable (>30 pages) or the user has
// explicitly opted into text. Has its own page + character caps.
func (h *AIHandler) runTextMode(ctx context.Context, w http.ResponseWriter, uid int64, in pdfGenInput) {
	pageTexts, err := extractPDFText(ctx, in.PDFBytes)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	rendered, err := h.prepareGeneration(ctx, uid, in)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	h.runTextGeneration(ctx, w, uid, in, rendered, pageTexts)
}

// prepareGeneration validates the subject + coverage, then renders the prompt.
// Used by both image and text modes.
func (h *AIHandler) prepareGeneration(ctx context.Context, uid int64, in pdfGenInput) (string, error) {
	subject, err := h.svc.LookupSubject(ctx, uid, in.SubjectID)
	if err != nil {
		return "", err
	}
	if !isValidCoverage(in.Coverage) {
		return "", &myErrors.AppError{Code: "validation", Message: "coverage must be Core | Balanced | Comprehensive", Wrapped: myErrors.ErrValidation, Field: "coverage"}
	}
	return renderPDFPrompt(in, subject.Name)
}

// extractPDFText returns one string per page, after enforcing the text-mode
// page and character caps. The returned slice can be passed to
// appendDocumentText to compose the prompt body.
func extractPDFText(ctx context.Context, pdfBytes []byte) ([]string, error) {
	pages, err := aiProvider.PDFPageCount(pdfBytes)
	if err != nil {
		return nil, &myErrors.AppError{Code: "pdf_unreadable", Message: err.Error(), Wrapped: myErrors.ErrValidation}
	}
	if pages > pdfTextModeMaxPages {
		return nil, &myErrors.AppError{
			Code:    "pdf_too_many_pages",
			Message: fmt.Sprintf("pdf has %d pages, text mode supports up to %d", pages, pdfTextModeMaxPages),
			Wrapped: myErrors.ErrPDFTooManyPages,
		}
	}
	pageTexts, err := aiProvider.PDFToText(ctx, pdfBytes)
	if err != nil {
		return nil, &myErrors.AppError{Code: "pdf_unreadable", Message: err.Error(), Wrapped: myErrors.ErrValidation}
	}
	if total := totalChars(pageTexts); total > pdfTextModeMaxChars {
		return nil, &myErrors.AppError{
			Code:    "pdf_text_too_long",
			Message: fmt.Sprintf("extracted text is %d chars, text mode supports up to %d", total, pdfTextModeMaxChars),
			Wrapped: myErrors.ErrPDFTextTooLong,
		}
	}
	return pageTexts, nil
}

// totalChars returns the summed character count of all page strings.
func totalChars(pages []string) int {
	n := 0
	for _, p := range pages {
		n += len(p)
	}
	return n
}

// runTextGeneration assembles a text-mode AIRequest (no images, document
// text appended to the rendered prompt) and pushes it through the pipeline.
func (h *AIHandler) runTextGeneration(
	ctx context.Context, w http.ResponseWriter,
	uid int64, in pdfGenInput, rendered string, pageTexts []string,
) {
	autoChapters := in.AutoChapters && in.ChapterID == 0
	body := appendDocumentText(rendered, pageTexts)
	req := aipipeline.AIRequest{
		UserID:       uid,
		Feature:      aipipeline.FeatureGenerateFromPDF,
		SubjectID:    in.SubjectID,
		Prompt:       body,
		PDFBytes:     in.PDFBytes,
		PDFPages:     len(pageTexts),
		Images:       nil,
		DropChapters: !autoChapters,
		Metadata: map[string]any{
			"coverage": in.Coverage, "style": in.Style, "focus": in.Focus,
			"auto_chapters": in.AutoChapters, "chapter_id": in.ChapterID,
			"page_count": len(pageTexts),
			"mode":       "text",
		},
	}
	h.runGenerationWithReq(ctx, w, req)
}

// appendDocumentText concatenates per-page text under the rendered prompt
// with --- Page N --- separators between pages.
func appendDocumentText(rendered string, pages []string) string {
	var b strings.Builder
	b.WriteString(rendered)
	b.WriteString("\n\n--- Document text ---\n")
	for i, p := range pages {
		fmt.Fprintf(&b, "\n--- Page %d ---\n", i+1)
		b.WriteString(p)
	}
	return b.String()
}

// renderPDFPrompt renders the PDF-mode prompt template from form inputs.
func renderPDFPrompt(in pdfGenInput, subjectName string) (string, error) {
	return aipipeline.RenderPDFGen(aipipeline.PDFGenValues{
		SubjectName:  subjectName,
		Style:        in.Style,
		Coverage:     in.Coverage,
		Focus:        in.Focus,
		AutoChapters: in.AutoChapters && in.ChapterID == 0,
	})
}

// runPDFGeneration pushes the assembled image-mode request through the pipeline.
func (h *AIHandler) runPDFGeneration(
	ctx context.Context, w http.ResponseWriter,
	uid int64, in pdfGenInput, rendered string, images []aiProvider.ImagePart,
) {
	autoChapters := in.AutoChapters && in.ChapterID == 0
	req := aipipeline.AIRequest{
		UserID:       uid,
		Feature:      aipipeline.FeatureGenerateFromPDF,
		SubjectID:    in.SubjectID,
		Prompt:       rendered,
		PDFBytes:     in.PDFBytes,
		PDFPages:     len(images),
		Images:       images,
		DropChapters: !autoChapters,
		Metadata: map[string]any{
			"coverage": in.Coverage, "style": in.Style, "focus": in.Focus,
			"auto_chapters": in.AutoChapters, "chapter_id": in.ChapterID,
			"page_count": len(images),
			"mode":       "image",
		},
	}
	h.runGenerationWithReq(ctx, w, req)
}

// runGenerationWithReq pushes a fully-assembled AIRequest through the pipeline
// and forwards the streamed result as SSE to the client. Used by both
// image and text PDF modes.
func (h *AIHandler) runGenerationWithReq(ctx context.Context, w http.ResponseWriter, req aipipeline.AIRequest) {
	ch, jobID, err := h.svc.RunStructuredGeneration(ctx, req)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	setSSEHeaders(w)
	flusher, _ := w.(http.Flusher)
	writeSSE(w, flusher, "job", map[string]any{"jobId": jobID})
	for c := range ch {
		forwardChunkToSSE(w, flusher, c)
	}
}

// parsePDFForm reads the multipart form and returns a validated pdfGenInput.
func parsePDFForm(r *http.Request) (pdfGenInput, error) {
	if err := r.ParseMultipartForm(20 << 20); err != nil {
		return pdfGenInput{}, &myErrors.AppError{Code: "pdf_too_large", Message: "pdf exceeds 20 MB", Wrapped: myErrors.ErrPdfTooLarge}
	}
	f, _, err := r.FormFile("file")
	if err != nil {
		return pdfGenInput{}, &myErrors.AppError{Code: "validation", Message: "file field required", Wrapped: myErrors.ErrValidation, Field: "file"}
	}
	defer f.Close()
	bytesBuf, err := readAllCapped(f, 20<<20)
	if err != nil {
		return pdfGenInput{}, err
	}
	in := pdfGenInput{
		SubjectID:    parseInt64Form(r, "subject_id"),
		ChapterID:    parseInt64Form(r, "chapter_id"),
		Coverage:     orDefaultStr(r.FormValue("coverage"), "Balanced"),
		Style:        orDefaultStr(r.FormValue("style"), "standard"),
		Focus:        r.FormValue("focus"),
		AutoChapters: r.FormValue("auto_chapters") == "true",
		Mode:         orDefaultStr(r.FormValue("mode"), "image"),
		PDFBytes:     bytesBuf,
	}
	if in.SubjectID <= 0 {
		return pdfGenInput{}, &myErrors.AppError{
			Code: "validation", Message: "subject_id is required",
			Wrapped: myErrors.ErrValidation, Field: "subject_id",
		}
	}
	return in, nil
}

// readAllCapped slurps at most limit bytes; returns pdf_too_large past that.
func readAllCapped(r io.Reader, limit int64) ([]byte, error) {
	buf, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read file:\n%w", err)
	}
	if int64(len(buf)) > limit {
		return nil, &myErrors.AppError{Code: "pdf_too_large", Message: "pdf exceeds 20 MB", Wrapped: myErrors.ErrPdfTooLarge}
	}
	return buf, nil
}

// rasterizePDF turns a PDF byte slice into a per-page []ImagePart.
// Page count is implicitly bounded by the 20 MB upload cap.
func rasterizePDF(ctx context.Context, pdfBytes []byte) ([]aiProvider.ImagePart, error) {
	imgs, err := aiProvider.PDFToImages(ctx, pdfBytes, aiProvider.PDFOptions{PerPageTimeout: 30 * time.Second})
	if err != nil {
		return nil, &myErrors.AppError{Code: "pdf_unreadable", Message: err.Error(), Wrapped: myErrors.ErrValidation}
	}
	return imgs, nil
}

// parseInt64Form parses a multipart form field into int64; 0 on absence/parse-error.
func parseInt64Form(r *http.Request, field string) int64 {
	var v int64
	_, _ = fmt.Sscanf(r.FormValue(field), "%d", &v)
	return v
}

// orDefaultStr returns s unless empty, in which case fallback.
func orDefaultStr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
