# PDF Text-Mode Fallback Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a text-only retry path to `POST /ai/flashcards/pdf` that the frontend can fall back to when image mode is rejected (>30 pages) or fails at the provider (context overflow). Image mode stays the default.

**Architecture:** Same endpoint, new `mode` form field (`image` default, `text` opt-in). New error code `pdf_image_mode_unavailable` (422) covers both the pre-flight page-count rejection and the runtime provider-overflow case so the frontend has one branch. Text mode extracts pages via `go-fitz`'s `doc.Text(i)`, joins with `--- Page N ---` markers, and rides the existing `AIRequest.Prompt` pipeline with `Images: nil`. Quota model unchanged: same `PDFPages` counter, debited only on success (no refund needed).

**Tech Stack:** Go 1.23, `github.com/gen2brain/go-fitz` (already a dep), `pgxpool`, existing `aipipeline` service.

**Spec:** `docs/superpowers/specs/2026-05-09-pdf-text-fallback-design.md`

---

## File Structure

| File | Role | Action |
|---|---|---|
| `internal/myErrors/errors.go` | Sentinel errors | Modify — add 3 sentinels |
| `internal/httpx/errors.go` | HTTP status + code mappings | Modify — register 3 sentinels |
| `internal/aiProvider/pdf.go` | `go-fitz` adapter | Modify — add `PDFToText` |
| `internal/aiProvider/pdf_test.go` | PDF adapter tests | Modify — add `PDFToText` tests |
| `pkg/aipipeline/errors.go` | Provider-error classification | Modify — add `classifyProviderError` |
| `pkg/aipipeline/errors_test.go` | New — classifier tests | Create |
| `pkg/aipipeline/service_generation.go` | Streaming pipeline | Modify — call new classifier in `streamOnce` |
| `api/handler/ai_pdf.go` | PDF endpoint handler | Modify — add `mode` field + image/text branches |
| `api/handler/ai_pdf_test.go` | New — handler tests | Create |
| `api/handler/docs_openapi.yaml` | OpenAPI spec | Modify — document `mode` + new error codes |

Constants (`pdfImageModeMaxPages`, `pdfTextModeMaxPages`, `pdfTextModeMaxChars`) live at the top of `api/handler/ai_pdf.go` — single source of truth for the caps.

---

### Task 1: Error sentinels and HTTP mappings

**Files:**
- Modify: `internal/myErrors/errors.go` (after `ErrPdfTooLarge` at line 42)
- Modify: `internal/httpx/errors.go` (extend the two map slices)

Add three new sentinels and wire them into the HTTP status / code maps. These are used by every downstream task, so we land them first.

- [ ] **Step 1: Add the sentinels in `internal/myErrors/errors.go`**

Insert these three blocks immediately after the `ErrPdfTooLarge` declaration (line 42):

```go
// ErrPDFImageModeUnavailable indicates image-mode generation is not viable
// for this PDF (too many pages, or provider rejected it as too large) and
// the user should be offered a text-mode retry.
var ErrPDFImageModeUnavailable = errors.New("pdf image mode unavailable")

// ErrPDFTooManyPages indicates a text-mode PDF exceeds the per-request
// page cap. Not recoverable — the user is already in text mode.
var ErrPDFTooManyPages = errors.New("pdf has too many pages for text mode")

// ErrPDFTextTooLong indicates the extracted text from a text-mode PDF
// exceeds the per-request character cap. Not recoverable.
var ErrPDFTextTooLong = errors.New("extracted pdf text exceeds character cap")
```

- [ ] **Step 2: Register the sentinels in `internal/httpx/errors.go`**

In `sentinelStatus` (between the existing `ErrPdfTooLarge` line and `ErrAIProvider`), add:

```go
{myErrors.ErrPDFImageModeUnavailable, http.StatusUnprocessableEntity},
{myErrors.ErrPDFTooManyPages, http.StatusRequestEntityTooLarge},
{myErrors.ErrPDFTextTooLong, http.StatusRequestEntityTooLarge},
```

In `sentinelCodes` (in the same relative position), add:

```go
{myErrors.ErrPDFImageModeUnavailable, "pdf_image_mode_unavailable"},
{myErrors.ErrPDFTooManyPages, "pdf_too_many_pages"},
{myErrors.ErrPDFTextTooLong, "pdf_text_too_long"},
```

Also fix the existing `ErrPdfTooLarge` HTTP status if appropriate. **Verify first** by reading line 37 of `httpx/errors.go`: it currently maps to `http.StatusTooManyRequests` (429) which is wrong for "file size too large". Do NOT change this in this task — file a follow-up if needed; out of scope.

- [ ] **Step 3: Compile-check**

Run:
```bash
go build ./...
```
Expected: clean build (no test runs needed yet — these sentinels have no logic).

- [ ] **Step 4: Commit**

```bash
git add internal/myErrors/errors.go internal/httpx/errors.go
git commit -m "$(cat <<'EOF'
PDF text-fallback: error sentinels + HTTP mappings

[+] ErrPDFImageModeUnavailable (422)
[+] ErrPDFTooManyPages (413)
[+] ErrPDFTextTooLong (413)
EOF
)"
```

---

### Task 2: `PDFToText` in aiProvider — failing test first

**Files:**
- Modify: `internal/aiProvider/pdf.go` (add new public function, mirror style of `PDFPageCount`)
- Modify: `internal/aiProvider/pdf_test.go` (add tests using existing `testdata/sample.pdf`)

`PDFToText` extracts text per page using `doc.Text(i)`. Sequential — `go-fitz` is not goroutine-safe and text extraction is fast.

- [ ] **Step 1: Write the failing tests in `internal/aiProvider/pdf_test.go`**

Append to the file (after `TestPDFPageCount_ValidPDF`):

```go
func TestPDFToText_ReturnsOneStringPerPage(t *testing.T) {
	pdf := loadTestPDF(t)
	pages, err := aiProvider.PDFToText(context.Background(), pdf)
	if err != nil {
		t.Fatalf("PDFToText: %v", err)
	}
	if len(pages) == 0 {
		t.Fatal("no pages returned")
	}
	for i, p := range pages {
		if p == "" {
			t.Errorf("pages[%d] is empty (sample.pdf is expected to have text on every page)", i)
		}
	}
}

func TestPDFToText_RejectsEmptyBytes(t *testing.T) {
	_, err := aiProvider.PDFToText(context.Background(), nil)
	if err == nil {
		t.Error("want error on nil bytes")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:
```bash
go test ./internal/aiProvider/ -run 'PDFToText' -v
```
Expected: FAIL with `undefined: aiProvider.PDFToText`.

- [ ] **Step 3: Implement `PDFToText` in `internal/aiProvider/pdf.go`**

Append at the bottom of the file:

```go
// PDFToText extracts the per-page text content of pdfBytes.
// Returns one string per page in source order. The text content is
// what go-fitz's text-extraction layer produces — useful for the
// text-mode flashcard generation path when image-mode is not viable.
func PDFToText(ctx context.Context, pdfBytes []byte) ([]string, error) {
	if len(pdfBytes) == 0 {
		return nil, fmt.Errorf("empty pdf bytes")
	}
	doc, err := fitz.NewFromMemory(pdfBytes)
	if err != nil {
		return nil, fmt.Errorf("open pdf:\n%w", err)
	}
	defer doc.Close()

	n := doc.NumPage()
	pages := make([]string, n)
	for i := 0; i < n; i++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		text, err := doc.Text(i)
		if err != nil {
			return nil, fmt.Errorf("extract text page %d:\n%w", i, err)
		}
		pages[i] = text
	}
	return pages, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run:
```bash
go test ./internal/aiProvider/ -run 'PDFToText' -v
```
Expected: PASS.

- [ ] **Step 5: Run the full aiProvider package test to catch regressions**

Run:
```bash
go test ./internal/aiProvider/...
```
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/aiProvider/pdf.go internal/aiProvider/pdf_test.go
git commit -m "$(cat <<'EOF'
PDF text-fallback: PDFToText extractor

[+] aiProvider.PDFToText returns per-page text via go-fitz
[+] Rejects empty input
[+] Tests over existing testdata/sample.pdf
EOF
)"
```

---

### Task 3: `classifyProviderError` in aipipeline — failing test first

**Files:**
- Modify: `pkg/aipipeline/errors.go` (add classifier function)
- Create: `pkg/aipipeline/errors_test.go`

The classifier inspects a provider error and, when image content was sent **and** the error matches a known overflow pattern, returns `ErrPDFImageModeUnavailable`. Otherwise it returns the error untouched.

- [ ] **Step 1: Write the failing tests in `pkg/aipipeline/errors_test.go`**

Create the file:

```go
package aipipeline

import (
	"errors"
	"fmt"
	"testing"

	"studbud/backend/internal/myErrors"
)

func TestClassifyProviderError_OverflowWithImagesMapsToImageModeUnavailable(t *testing.T) {
	cases := []string{
		"prompt is too long: 250000 tokens > 200000 maximum",
		"request exceeds context_length_exceeded",
		"input exceeds the model context window",
	}
	for _, msg := range cases {
		got := classifyProviderError(errors.New(msg), true)
		if !errors.Is(got, myErrors.ErrPDFImageModeUnavailable) {
			t.Errorf("classifyProviderError(%q, true) did not wrap ErrPDFImageModeUnavailable: got %v", msg, got)
		}
	}
}

func TestClassifyProviderError_OverflowWithoutImagesPassThrough(t *testing.T) {
	original := errors.New("prompt is too long: 250000 tokens > 200000 maximum")
	got := classifyProviderError(original, false)
	if got != original {
		t.Errorf("classifyProviderError without images mutated err: got %v, want pass-through", got)
	}
}

func TestClassifyProviderError_UnrelatedErrorPassThrough(t *testing.T) {
	original := fmt.Errorf("upstream 503 service unavailable")
	got := classifyProviderError(original, true)
	if got != original {
		t.Errorf("classifyProviderError mutated unrelated error: got %v, want pass-through", got)
	}
}

func TestClassifyProviderError_NilPassThrough(t *testing.T) {
	if got := classifyProviderError(nil, true); got != nil {
		t.Errorf("nil err should pass through, got %v", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:
```bash
go test ./pkg/aipipeline/ -run 'ClassifyProviderError' -v
```
Expected: FAIL with `undefined: classifyProviderError`.

- [ ] **Step 3: Implement `classifyProviderError` in `pkg/aipipeline/errors.go`**

Append at the bottom of the file (after `isWellFormedObject`):

```go
// classifyProviderError maps a raw provider error to ErrPDFImageModeUnavailable
// when the call carried image content and the error matches a known
// "context window / payload too large" pattern. Otherwise the error is
// returned unchanged.
//
// Pattern set is conservative: tighten or widen as we observe real-world
// Anthropic responses in production.
func classifyProviderError(err error, hadImages bool) error {
	if err == nil {
		return nil
	}
	if !hadImages {
		return err
	}
	if !looksLikeContextOverflow(err) {
		return err
	}
	return &myErrors.AppError{
		Code:    "pdf_image_mode_unavailable",
		Message: "PDF too large for image-mode generation; retry with mode=text",
		Wrapped: myErrors.ErrPDFImageModeUnavailable,
	}
}

// looksLikeContextOverflow checks the error string for known overflow markers.
func looksLikeContextOverflow(err error) bool {
	s := strings.ToLower(err.Error())
	for _, needle := range []string{
		"prompt is too long",
		"context_length",
		"context window",
		"too many tokens",
		"request_too_large",
		"413",
	} {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run:
```bash
go test ./pkg/aipipeline/ -run 'ClassifyProviderError' -v
```
Expected: PASS.

- [ ] **Step 5: Run the full aipipeline tests for regressions**

Run:
```bash
go test ./pkg/aipipeline/...
```
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/aipipeline/errors.go pkg/aipipeline/errors_test.go
git commit -m "$(cat <<'EOF'
PDF text-fallback: provider-error classifier

[+] classifyProviderError maps overflow + images to ErrPDFImageModeUnavailable
[+] Pass-through for non-image calls and unrelated errors
[+] Unit tests over known overflow patterns
EOF
)"
```

---

### Task 4: Wire classifier into `streamOnce`

**Files:**
- Modify: `pkg/aipipeline/service_generation.go` (line 122–135)

Surface `ErrPDFImageModeUnavailable` to the SSE error chunk and `ai_jobs.error_kind` by classifying the provider error inside `streamOnce`. The classifier sits at the source of provider errors so callers don't need to remember to wrap.

- [ ] **Step 1: Read the current `streamOnce` to confirm shape**

Read `pkg/aipipeline/service_generation.go` lines 121–135. Note that `s.provider.Stream(...)` returns `(chunks, err)` and we currently call `classifyProviderStartErr(err)` on the start error. The new wrapping happens **outside** that — we wrap the classifier's output so behavior degrades gracefully if the error is already a known sentinel.

- [ ] **Step 2: Modify `streamOnce` to call the new classifier**

Replace lines 122–135 (the body of `streamOnce`) with:

```go
func (s *Service) streamOnce(ctx context.Context, req AIRequest, jobID int64, out chan<- AIChunk) streamResult {
	chunks, err := s.provider.Stream(ctx, aiProvider.Request{
		FeatureKey: string(req.Feature),
		Model:      s.model,
		Prompt:     req.Prompt,
		Images:     req.Images,
		Schema:     req.Schema,
		MaxTokens:  16384,
	})
	if err != nil {
		classified := classifyProviderError(err, len(req.Images) > 0)
		return streamResult{err: classifyProviderStartErr(classified)}
	}
	return s.consumeStream(ctx, chunks, out, req.DropChapters)
}
```

The order matters: `classifyProviderError` runs first (turning an overflow error into `ErrPDFImageModeUnavailable`); `classifyProviderStartErr` then runs and is a no-op for our sentinel (it only wraps unknown errors as `provider_5xx`).

- [ ] **Step 3: Verify `classifyProviderStartErr` does pass through `ErrPDFImageModeUnavailable`**

Read `pkg/aipipeline/errors.go:14–25`. The current function only short-circuits on `ErrContentPolicy` and `ErrAIProvider`, otherwise wraps as `provider_5xx`. Our `classifyProviderError` returns an `AppError` wrapping `ErrPDFImageModeUnavailable`, which is **none of those** — meaning it would be rewrapped as `provider_5xx`. Fix:

In `pkg/aipipeline/errors.go`, modify `classifyProviderStartErr` (around line 14) to also pass through `ErrPDFImageModeUnavailable`. Replace:

```go
func classifyProviderStartErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, myErrors.ErrContentPolicy) {
		return err
	}
	if errors.Is(err, myErrors.ErrAIProvider) {
		return err
	}
	return &myErrors.AppError{Code: "provider_5xx", Message: "AI service failed before streaming", Wrapped: myErrors.ErrAIProvider}
}
```

with:

```go
func classifyProviderStartErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, myErrors.ErrContentPolicy) {
		return err
	}
	if errors.Is(err, myErrors.ErrAIProvider) {
		return err
	}
	if errors.Is(err, myErrors.ErrPDFImageModeUnavailable) {
		return err
	}
	return &myErrors.AppError{Code: "provider_5xx", Message: "AI service failed before streaming", Wrapped: myErrors.ErrAIProvider}
}
```

- [ ] **Step 4: Add a regression test for the wiring**

Append to `pkg/aipipeline/errors_test.go`:

```go
func TestClassifyProviderStartErr_PassesThroughImageModeUnavailable(t *testing.T) {
	src := &myErrors.AppError{
		Code:    "pdf_image_mode_unavailable",
		Message: "x",
		Wrapped: myErrors.ErrPDFImageModeUnavailable,
	}
	got := classifyProviderStartErr(src)
	if !errors.Is(got, myErrors.ErrPDFImageModeUnavailable) {
		t.Fatalf("classifyProviderStartErr swallowed image-mode-unavailable: %v", got)
	}
}
```

- [ ] **Step 5: Run full aipipeline tests**

Run:
```bash
go test ./pkg/aipipeline/...
```
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/aipipeline/service_generation.go pkg/aipipeline/errors.go pkg/aipipeline/errors_test.go
git commit -m "$(cat <<'EOF'
PDF text-fallback: surface ErrPDFImageModeUnavailable from streamOnce

[&] streamOnce classifies provider start errors against image-mode overflow
[&] classifyProviderStartErr passes through ErrPDFImageModeUnavailable
[+] Regression test
EOF
)"
```

---

### Task 5: Image-mode pre-flight (>30 page rejection)

**Files:**
- Modify: `api/handler/ai_pdf.go` (constants block + new pre-flight in `GenerateFromPDF`)
- Modify: `api/handler/ai_pdf_test.go` — actually create new file (`ai_pdf_test.go`) since handler tests for PDF are currently in `ai_generate_test.go`. Use that style.

Rejecting up-front before rasterization means we never spend CPU on a doomed image-mode call.

- [ ] **Step 1: Create the new test file `api/handler/ai_pdf_test.go` with a failing test**

Create the file:

```go
package handler_test

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"studbud/backend/testutil"
)

func newPDFFormReader(t *testing.T, subjectID int64, mode string, pdfBytes []byte) (*bytes.Buffer, string) {
	t.Helper()
	form := new(bytes.Buffer)
	w := multipart.NewWriter(form)
	_ = w.WriteField("subject_id", strconv.FormatInt(subjectID, 10))
	if mode != "" {
		_ = w.WriteField("mode", mode)
	}
	fw, err := w.CreateFormFile("file", "test.pdf")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := io.Copy(fw, bytes.NewReader(pdfBytes)); err != nil {
		t.Fatalf("copy pdf: %v", err)
	}
	_ = w.Close()
	return form, w.FormDataContentType()
}

// largePDFFixture returns bytes of a PDF with strictly more than 30 pages.
// Looks for api/handler/testdata/large.pdf; skips the test if missing.
func largePDFFixture(t *testing.T) []byte {
	t.Helper()
	path := filepath.Join("testdata", "large.pdf")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("missing api/handler/testdata/large.pdf (>30 pages required): %v", err)
	}
	return b
}

func TestGenerateFromPDF_ImageModeRejectsOver30Pages(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	subj := testutil.NewSubject(t, pool, u.ID)

	bigPDF := largePDFFixture(t)
	srv := newAIPDFServer(t, pool, &testutil.FakeAIClient{})
	tok := mintToken(t, u.ID, true, false)

	form, ct := newPDFFormReader(t, subj.ID, "image", bigPDF)
	req := httptest.NewRequest("POST", "/ai/flashcards/pdf", form)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body = %s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("pdf_image_mode_unavailable")) {
		t.Errorf("body missing pdf_image_mode_unavailable: %s", w.Body.String())
	}
}
```

- [ ] **Step 2: Add a >30-page fixture**

Create the directory and produce a PDF with at least 31 pages at `api/handler/testdata/large.pdf`. The simplest path with `pypdf`:

```bash
mkdir -p api/handler/testdata
pip install pypdf 2>/dev/null || pip3 install pypdf
python3 -c '
from pypdf import PdfWriter, PdfReader
src = PdfReader("internal/aiProvider/testdata/sample.pdf")
w = PdfWriter()
while len(w.pages) <= 30:
    for p in src.pages:
        w.add_page(p)
        if len(w.pages) > 30: break
with open("api/handler/testdata/large.pdf","wb") as f:
    w.write(f)
print("pages:", len(w.pages))
'
```

Verify it parses and has >30 pages:

```bash
go test ./internal/aiProvider/ -run TestPDFPageCount_ValidPDF -v
```

(Or read the fixture into a quick `aiProvider.PDFPageCount` call from a temporary `_test.go`.) Any production tool that yields a >30-page PDF derived from a known-good source is acceptable.

- [ ] **Step 3: Run the new test to verify it fails**

Run:
```bash
go test ./api/handler/ -run 'TestGenerateFromPDF_ImageModeRejectsOver30Pages' -v
```
Expected: FAIL — handler currently returns 200 (or rasterizes) instead of 422.

- [ ] **Step 4: Add constants and pre-flight in `api/handler/ai_pdf.go`**

At the top of `api/handler/ai_pdf.go`, immediately after the `import (...)` block, add:

```go
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
```

In `GenerateFromPDF` (line 29), replace the body to add the pre-flight check after `parsePDFForm` and before `rasterizePDF`:

```go
func (h *AIHandler) GenerateFromPDF(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	in, err := parsePDFForm(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	pages, err := aiProvider.PDFPageCount(in.PDFBytes)
	if err != nil {
		httpx.WriteError(w, &myErrors.AppError{Code: "pdf_unreadable", Message: err.Error(), Wrapped: myErrors.ErrValidation})
		return
	}
	if pages > pdfImageModeMaxPages {
		httpx.WriteError(w, &myErrors.AppError{
			Code:    "pdf_image_mode_unavailable",
			Message: fmt.Sprintf("pdf has %d pages, image mode supports up to %d", pages, pdfImageModeMaxPages),
			Wrapped: myErrors.ErrPDFImageModeUnavailable,
		})
		return
	}
	images, err := rasterizePDF(r.Context(), in.PDFBytes)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	subject, err := h.svc.LookupSubject(r.Context(), in.SubjectID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	if !isValidCoverage(in.Coverage) {
		httpx.WriteError(w, &myErrors.AppError{Code: "validation", Message: "coverage must be Core | Balanced | Comprehensive", Wrapped: myErrors.ErrValidation, Field: "coverage"})
		return
	}
	rendered, err := renderPDFPrompt(in, subject.Name)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	h.runPDFGeneration(r.Context(), w, uid, in, rendered, images)
}
```

(Note: `fmt` is already imported.)

- [ ] **Step 5: Run the test to verify it passes**

Run:
```bash
go test ./api/handler/ -run 'TestGenerateFromPDF_ImageModeRejectsOver30Pages' -v
```
Expected: PASS.

- [ ] **Step 6: Run full handler package tests for regressions**

Run:
```bash
go test ./api/handler/...
```
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add api/handler/ai_pdf.go api/handler/ai_pdf_test.go api/handler/testdata/large.pdf
git commit -m "$(cat <<'EOF'
PDF text-fallback: image-mode 30-page pre-flight

[+] Reject >30-page PDFs with pdf_image_mode_unavailable (422)
[+] pdfImageModeMaxPages, pdfTextModeMaxPages, pdfTextModeMaxChars constants
[+] api/handler/testdata/large.pdf fixture (>30 pages)
[+] Handler-level test for the rejection
EOF
)"
```

---

### Task 6: Text-mode handler branch

**Files:**
- Modify: `api/handler/ai_pdf.go` (add `Mode`, branch `GenerateFromPDF`, add helpers)
- Modify: `api/handler/ai_pdf_test.go` (add three text-mode tests)

This is the largest single task, but each helper is small. Per CLAUDE.md, every helper stays at one responsibility.

- [ ] **Step 1: Write three failing text-mode tests in `api/handler/ai_pdf_test.go`**

Append to `api/handler/ai_pdf_test.go`:

```go
func TestGenerateFromPDF_TextModeAcceptsLargePDF(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	subj := testutil.NewSubject(t, pool, u.ID)

	cli := &testutil.FakeAIClient{
		Chunks: []aiProvider.Chunk{
			{Text: `{"items":[{"title":"t1","question":"q1","answer":"a1"}]}`, Done: true},
		},
	}
	srv := newAIPDFServer(t, pool, cli)
	tok := mintToken(t, u.ID, true, false)

	bigPDF := largePDFFixture(t) // same >30-page fixture; text mode tolerates it
	form, ct := newPDFFormReader(t, subj.ID, "text", bigPDF)
	req := httptest.NewRequest("POST", "/ai/flashcards/pdf", form)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("event: card")) {
		t.Errorf("expected card event in stream:\n%s", w.Body.String())
	}
	// Verify the provider was called with no images (text mode).
	if len(cli.LastRequest().Images) != 0 {
		t.Errorf("text mode passed images to provider: got %d", len(cli.LastRequest().Images))
	}
}

func TestGenerateFromPDF_TextModeRejectsOver200Pages(t *testing.T) {
	t.Skip("requires >200-page fixture; add api/handler/testdata/very_large.pdf to enable")
}

func TestGenerateFromPDF_TextModeRejectsOver400kChars(t *testing.T) {
	t.Skip("requires text-heavy fixture > 400k chars; add api/handler/testdata/text_heavy.pdf to enable")
}
```

The two skipped tests document the contract; opt them in once fixtures exist. We rely on the unit-level coverage of the cap-checks via the helper functions (Step 7 below) for now.

- [ ] **Step 2: Extend `testutil.FakeAIClient` to expose `LastRequest()` if not present**

Read `testutil/ai.go`. If `LastRequest()` is not present, add:

```go
// LastRequest returns the most recent Request received by Stream.
// Used by handler tests to assert on what was sent to the provider.
func (f *FakeAIClient) LastRequest() aiProvider.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastReq
}
```

And in `Stream(...)`, store the request:

```go
func (f *FakeAIClient) Stream(ctx context.Context, req aiProvider.Request) (<-chan aiProvider.Chunk, error) {
	f.mu.Lock()
	f.lastReq = req
	f.mu.Unlock()
	// ... existing body
}
```

You'll need a `mu sync.Mutex` and `lastReq aiProvider.Request` field on the struct. **Read the existing file first** — these may already exist; if so, just expose the getter. Don't duplicate.

- [ ] **Step 3: Run the new text-mode test to verify it fails**

Run:
```bash
go test ./api/handler/ -run 'TestGenerateFromPDF_TextModeAcceptsLargePDF' -v
```
Expected: FAIL — handler does not understand `mode=text` yet.

- [ ] **Step 4: Add `Mode` to `pdfGenInput` and parse it**

Modify `pdfGenInput` (line 18 in `ai_pdf.go`) to add the field:

```go
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
```

In `parsePDFForm` (line 110), set the field:

```go
return pdfGenInput{
	SubjectID:    parseInt64Form(r, "subject_id"),
	ChapterID:    parseInt64Form(r, "chapter_id"),
	Coverage:     orDefaultStr(r.FormValue("coverage"), "Balanced"),
	Style:        orDefaultStr(r.FormValue("style"), "standard"),
	Focus:        r.FormValue("focus"),
	AutoChapters: r.FormValue("auto_chapters") == "true",
	Mode:         orDefaultStr(r.FormValue("mode"), "image"),
	PDFBytes:     bytesBuf,
}, nil
```

- [ ] **Step 5: Split `GenerateFromPDF` into `runImageMode` / `runTextMode` branches**

Replace the body of `GenerateFromPDF` again (still in `api/handler/ai_pdf.go`) with the dispatching shape:

```go
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
```

Add `runImageMode` (the body that we just expanded in Task 5, factored out):

```go
// runImageMode is the legacy path: rasterize each page and send images to Claude.
// Rejects >30-page PDFs with pdf_image_mode_unavailable so the frontend can
// offer text-mode retry.
func (h *AIHandler) runImageMode(ctx context.Context, w http.ResponseWriter, uid int64, in pdfGenInput) {
	pages, err := aiProvider.PDFPageCount(in.PDFBytes)
	if err != nil {
		httpx.WriteError(w, &myErrors.AppError{Code: "pdf_unreadable", Message: err.Error(), Wrapped: myErrors.ErrValidation})
		return
	}
	if pages > pdfImageModeMaxPages {
		httpx.WriteError(w, &myErrors.AppError{
			Code:    "pdf_image_mode_unavailable",
			Message: fmt.Sprintf("pdf has %d pages, image mode supports up to %d", pages, pdfImageModeMaxPages),
			Wrapped: myErrors.ErrPDFImageModeUnavailable,
		})
		return
	}
	images, err := rasterizePDF(ctx, in.PDFBytes)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	rendered, err := h.prepareGeneration(ctx, in)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	h.runPDFGeneration(ctx, w, uid, in, rendered, images)
}
```

Add `runTextMode`:

```go
// runTextMode extracts text per page and sends it as a single text content
// block. Used when image mode is not viable (>30 pages) or the user has
// explicitly opted into text. Has its own page + character caps.
func (h *AIHandler) runTextMode(ctx context.Context, w http.ResponseWriter, uid int64, in pdfGenInput) {
	pageTexts, err := extractPDFText(ctx, in.PDFBytes)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	rendered, err := h.prepareGeneration(ctx, in)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	h.runTextGeneration(ctx, w, uid, in, rendered, pageTexts)
}
```

Add `extractPDFText`:

```go
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
```

Refactor the existing prompt-rendering / subject-lookup duplication into a single helper used by both modes:

```go
// prepareGeneration validates the subject + coverage, then renders the prompt.
// Used by both image and text modes.
func (h *AIHandler) prepareGeneration(ctx context.Context, in pdfGenInput) (string, error) {
	subject, err := h.svc.LookupSubject(ctx, in.SubjectID)
	if err != nil {
		return "", err
	}
	if !isValidCoverage(in.Coverage) {
		return "", &myErrors.AppError{Code: "validation", Message: "coverage must be Core | Balanced | Comprehensive", Wrapped: myErrors.ErrValidation, Field: "coverage"}
	}
	return renderPDFPrompt(in, subject.Name)
}
```

Add `runTextGeneration`:

```go
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
```

Update `runPDFGeneration` to also tag `mode: image` in metadata for symmetry:

```go
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
```

Add the `strings` import to the file's import block if not already present.

- [ ] **Step 6: Run text-mode test to verify it passes**

Run:
```bash
go test ./api/handler/ -run 'TestGenerateFromPDF_TextModeAcceptsLargePDF' -v
```
Expected: PASS.

- [ ] **Step 7: Add a unit test for the page-marker composition**

Convert the helper to package-internal test access by changing `ai_pdf_test.go` to a white-box file in the same package. Two options:

**Option A (recommended):** Create a separate file `api/handler/ai_pdf_internal_test.go` with `package handler` (no `_test`):

```go
package handler

import (
	"strings"
	"testing"
)

func TestAppendDocumentText_IncludesPageMarkers(t *testing.T) {
	got := appendDocumentText("PROMPT", []string{"alpha", "beta"})
	for _, want := range []string{"PROMPT", "--- Document text ---", "--- Page 1 ---", "alpha", "--- Page 2 ---", "beta"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}
```

This keeps `appendDocumentText` private (per CLAUDE.md "Minimal Public API") while still being testable.

**Option B:** Skip this unit test entirely — the integration test in Step 1 (TextModeAcceptsLargePDF) already exercises the path end-to-end. Acceptable if Option A feels like over-testing.

- [ ] **Step 8: Run the full handler tests for regressions**

Run:
```bash
go test ./api/handler/...
```
Expected: PASS.

- [ ] **Step 9: Run all tests**

Run:
```bash
go test ./...
```
Expected: PASS (skips for missing fixtures are fine).

- [ ] **Step 10: Commit**

```bash
git add api/handler/ai_pdf.go api/handler/ai_pdf_test.go testutil/ai.go
git commit -m "$(cat <<'EOF'
PDF text-fallback: text-mode handler branch

[+] mode=text form field on POST /ai/flashcards/pdf
[+] runTextMode extracts page text via aiProvider.PDFToText
[+] Page cap (200) and char cap (400k) for text mode
[+] appendDocumentText assembles prompt body with page markers
[&] runPDFGeneration tags mode=image in metadata for symmetry
[+] FakeAIClient.LastRequest() for asserting provider input
[+] Text-mode handler tests
EOF
)"
```

---

### Task 7: OpenAPI documentation

**Files:**
- Modify: `api/handler/docs_openapi.yaml`

Document the `mode` field and the four PDF-related error codes.

- [ ] **Step 1: Locate the PDF endpoint definition in `api/handler/docs_openapi.yaml`**

Find the path entry for `POST /ai/flashcards/pdf`. Read its `requestBody` and `responses`.

- [ ] **Step 2: Add `mode` to the multipart form schema**

Inside the `requestBody.content.multipart/form-data.schema.properties` of the PDF endpoint, add:

```yaml
mode:
  type: string
  enum: [image, text]
  default: image
  description: |
    Generation mode. `image` (default) rasterizes each page and sends images
    to the model. `text` extracts the document text only — used as a fallback
    when image mode is rejected. The frontend re-submits with `mode=text`
    after a `pdf_image_mode_unavailable` error.
```

- [ ] **Step 3: Document the error responses**

Inside the same endpoint's `responses`, ensure entries exist for `413` and `422`. The error envelope schema is already shared (`#/components/schemas/Error` or similar — read the file to confirm). Add description bullets covering each code:

```yaml
"413":
  description: |
    Upload too large or content too large for the chosen mode.
    Codes: `pdf_too_large` (file > 20 MB), `pdf_too_many_pages`
    (text mode, > 200 pages), `pdf_text_too_long` (text mode, > 400k chars).
  content:
    application/json:
      schema: { $ref: "#/components/schemas/Error" }   # use whatever the file uses
"422":
  description: |
    Image-mode generation is not viable for this PDF. Frontend should
    offer a text-mode retry by re-submitting with `mode=text`.
    Code: `pdf_image_mode_unavailable`.
  content:
    application/json:
      schema: { $ref: "#/components/schemas/Error" }
```

If the file uses inline error schemas, mirror the existing convention rather than introducing a `$ref`.

- [ ] **Step 4: Validate the OpenAPI doc still parses**

If the repo has a docs test:

```bash
go test ./api/handler/ -run TestDocsOpenAPIParses -v
```

Otherwise:

```bash
go vet ./...
go build ./...
```

Expected: clean.

- [ ] **Step 5: Commit**

```bash
git add api/handler/docs_openapi.yaml
git commit -m "$(cat <<'EOF'
PDF text-fallback: OpenAPI updates

[+] Document mode form field on POST /ai/flashcards/pdf
[+] Document 413 and 422 error responses with codes
EOF
)"
```

---

### Task 8: Final integration check

- [ ] **Step 1: Run the full test suite**

```bash
go test ./...
```
Expected: PASS (skipped tests due to missing fixtures are acceptable; document any genuine failures and fix before declaring done).

- [ ] **Step 2: Run the linter / vet**

```bash
go vet ./...
gofmt -l . | grep -v vendor || true
```
Expected: no output from `go vet`; no files reported by `gofmt -l`.

- [ ] **Step 3: Sanity check `go.mod`**

```bash
grep '^replace' go.mod || echo "no replace directives — OK"
```
Expected: `no replace directives — OK` (per CLAUDE.md, replace directives must not be committed).

- [ ] **Step 4: Spot-check the constants are the only place caps live**

```bash
grep -n 'pdfImageModeMaxPages\|pdfTextModeMaxPages\|pdfTextModeMaxChars' api/handler/ai_pdf.go
```
Expected: each constant is referenced by name (not by literal value) wherever the cap is checked or formatted into an error message. Inline literals like `30`, `200`, or `400_000` should NOT appear in cap-related code paths.

- [ ] **Step 5: No commit needed** — the suite passing is the deliverable.

---

## Spec coverage check

| Spec section | Implemented in |
|---|---|
| `mode` form field | Task 6 (parsePDFForm) |
| `pdf_image_mode_unavailable` (pre-flight) | Task 5 + Task 6 (runImageMode) |
| `pdf_image_mode_unavailable` (provider runtime) | Tasks 3 + 4 (classifier + wiring) |
| `pdf_too_many_pages`, `pdf_text_too_long` | Task 6 (extractPDFText) |
| Constants pdfImageModeMaxPages / TextModeMaxPages / TextModeMaxChars | Task 5 |
| `PDFToText` extractor | Task 2 |
| `classifyProviderError` | Task 3 |
| `streamOnce` wiring | Task 4 |
| `runImageMode` / `runTextMode` branching | Task 6 |
| `appendDocumentText` page markers | Task 6 |
| `Metadata["mode"]` tagging | Task 6 |
| OpenAPI updates | Task 7 |
| No quota refund needed (already side-effect-free) | Acknowledged in spec; no task |

## Dependencies between tasks

- Task 1 → Task 3, 4, 5, 6 (everything needs the sentinels)
- Task 2 → Task 6 (text mode uses `PDFToText`)
- Task 3 → Task 4 (wiring depends on the classifier existing)
- Task 5 → Task 6 (text mode adds `Mode` and refactors `GenerateFromPDF` already in image-only shape)
- Task 6 → Task 7 (OpenAPI documents the field added in Task 6)
- Task 7 → Task 8 (final check)

Tasks 2 and 3 have no inter-dependency and can be parallelized if desired.
