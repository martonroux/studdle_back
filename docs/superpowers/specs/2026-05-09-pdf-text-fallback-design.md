# PDF Generation — Text-Mode Fallback

**Date:** 2026-05-09
**Status:** Draft

## Problem

`POST /ai/flashcards/pdf` rasterizes every page of an uploaded PDF to JPEG and sends the images to Claude as vision content. The flow has no page-count cap and is bounded only by a 20 MB upload limit. Large PDFs (lecture decks, scanned textbooks) blow past the model's context window or exceed Anthropic's per-request limits, surfacing as opaque provider errors with no recovery path for the user.

We need a complementary text-only path that the frontend can fall back to when image mode is not viable, while keeping image mode (the higher-fidelity option) the default.

## Goals

- Reject PDFs that would fail in image mode **up front** (before rasterizing or debiting quota) when we can predict it.
- Map late provider failures (context overflow, payload too large) to the same recoverable error code so the frontend has one branch to handle.
- Offer a text-mode retry path on the same endpoint, governed by its own caps.
- Refund quota when the user is asked to retry due to an image-mode failure.

## Non-goals

- No automatic server-side fallback. The user explicitly opts into text mode after the frontend prompts them.
- No raising of the 20 MB upload cap. A file > 20 MB stays a non-recoverable rejection.
- No new quota counter. Text mode debits the existing `PDFPages` counter, 1 unit per PDF page.
- No frontend-side text extraction. All extraction happens in Go via `go-fitz`.

## API

### Endpoint

`POST /ai/flashcards/pdf` — unchanged route. New form field:

| Field | Values | Default | Meaning |
|---|---|---|---|
| `mode` | `image` \| `text` | `image` | `image` rasterizes pages and sends as vision content. `text` extracts text only. |

All other form fields (`subject_id`, `chapter_id`, `coverage`, `style`, `focus`, `auto_chapters`, `file`) are unchanged.

### Error codes

| Code | HTTP | Recoverable via text mode? | Cause |
|---|---|---|---|
| `pdf_too_large` | 413 | **No** — file is hard-capped at 20 MB | Upload bytes > 20 MB |
| `pdf_image_mode_unavailable` | 422 | **Yes** — frontend offers "convert to text" | Image-mode pre-flight (pages > 30) **or** provider context/size error during the call |
| `pdf_too_many_pages` | 413 | No — already in text mode | Text mode, pages > 200 |
| `pdf_text_too_long` | 413 | No — already in text mode | Text mode, extracted chars > 400 000 |
| `pdf_unreadable` | 400 | No | `go-fitz` cannot parse the file |
| `validation` | 400 | No | Bad/missing form fields |

The frontend treats `pdf_image_mode_unavailable` uniformly: show the "convert to text" affordance, and on user confirmation re-POST with `mode=text` and the same file.

## Flow

### Image mode (`mode=image`, default)

```
parsePDFForm                       (20 MB cap; pdf_too_large if exceeded)
  → PDFPageCount(bytes)
       pages > 30 → pdf_image_mode_unavailable, 422, no quota debit
  → rasterizePDF
  → CheckQuota(FeatureGenerateFromPDF, pages)
  → renderPDFPrompt + AIRequest{Images: ...}
  → RunStructuredGeneration
       provider context/size error → pdf_image_mode_unavailable on SSE
                                     error chunk; quota refunded
```

### Text mode (`mode=text`)

```
parsePDFForm                       (20 MB cap; pdf_too_large if exceeded)
  → PDFPageCount(bytes)
       pages > 200 → pdf_too_many_pages, 413
  → PDFToText(bytes)
       sum(len(page)) > 400_000 → pdf_text_too_long, 413
  → CheckQuota(FeatureGenerateFromPDF, pages)
  → renderPDFPrompt
  → AIRequest{
        Images:   nil,
        Prompt:   rendered + "\n\n--- Document text ---\n" + joined,
        PDFPages: pages,
        Metadata: {..., "mode": "text"},
    }
  → RunStructuredGeneration
```

`joined` is the per-page text concatenated with `\n\n--- Page N ---\n\n` markers between pages. This preserves page boundaries inside a single text content block so the model can still reason per-page.

## Constants

Declared at the top of `api/handler/ai_pdf.go`:

```go
const (
    pdfImageModeMaxPages = 30
    pdfTextModeMaxPages  = 200
    pdfTextModeMaxChars  = 400_000
)
```

## Component changes

### `internal/aiProvider/pdf.go`

Add `PDFToText(ctx, pdfBytes) ([]string, error)`. Opens the doc with `fitz.NewFromMemory`, calls `doc.Text(i)` for each page, returns one string per page in source order. Sequential — text extraction is fast and `go-fitz` is not goroutine-safe. Mirrors the shape of `PDFToImages`.

### `internal/myErrors/errors.go`

Three new sentinels:

- `ErrPDFImageModeUnavailable` — "image mode unavailable; retry with mode=text"
- `ErrPDFTooManyPages` — "pdf has too many pages for text mode"
- `ErrPDFTextTooLong` — "extracted pdf text exceeds character cap"

### `internal/httpx/errors.go`

Register the three new sentinels in both maps:

- `ErrPDFImageModeUnavailable` → 422 / `pdf_image_mode_unavailable`
- `ErrPDFTooManyPages` → 413 / `pdf_too_many_pages`
- `ErrPDFTextTooLong` → 413 / `pdf_text_too_long`

### `pkg/aipipeline/errors.go`

New helper `classifyProviderError(err, hadImages bool) error`. Pattern-matches Anthropic error responses (HTTP 413, `invalid_request_error` with substrings like `"prompt is too long"`, `"max_tokens"`, `"context_length"`). Returns `ErrPDFImageModeUnavailable` when `hadImages` is `true` **and** a match hits. Otherwise returns `err` unchanged.

### `pkg/aipipeline/service_generation.go`

Two touches:

1. Wrap the provider error path with `classifyProviderError(err, len(req.Images) > 0)` before forwarding to the SSE error chunk.
2. After classification: if the result is `ErrPDFImageModeUnavailable` **and** quota was already debited, issue a refund. Implementation depends on whether the existing `sqlDebitPDFPages` accepts negative deltas:
   - If yes: `DebitQuota(ctx, uid, FeatureGenerateFromPDF, -1, -req.PDFPages)`.
   - If no (e.g. `GREATEST(0, ...)`): add a new `RefundQuota` method running the inverse update.

To be confirmed at implementation time by reading `quota.go`.

### `pkg/aipipeline/quota.go`

Verify that the debit SQL accepts negative deltas. If not, add a `RefundQuota(ctx, uid, feat, calls, pages)` method. No other changes; no new public types.

### `api/handler/ai_pdf.go`

- Add `Mode string` to `pdfGenInput`.
- `parsePDFForm` reads/validates the `mode` form value.
- `GenerateFromPDF` branches after `parsePDFForm` into:
  - `runImageMode` — pre-flight `PDFPageCount > 30` → `ErrPDFImageModeUnavailable`; then existing rasterize/quota/run path.
  - `runTextMode` — pre-flight `PDFPageCount > 200` → `ErrPDFTooManyPages`; `PDFToText`; `sum(chars) > 400_000` → `ErrPDFTextTooLong`; build text-mode `AIRequest`; run.
- New helper `appendDocumentText(rendered, pages)` joins pages with `--- Page N ---` markers and concatenates onto the rendered prompt.
- Each helper stays at one responsibility per `CLAUDE.md`'s 15–25-line function rule.

### `pkg/aipipeline/model.go`

No changes. `AIRequest.Images` is already `nil`-friendly. Mode is carried through `Metadata["mode"]`.

### `api/handler/docs_openapi.yaml`

- Document the new `mode` form parameter.
- Document the four PDF-related error codes (`pdf_too_large`, `pdf_image_mode_unavailable`, `pdf_too_many_pages`, `pdf_text_too_long`) in the endpoint's response schema.

## Tests

- `internal/aiProvider/pdf_test.go` — `PDFToText` happy path on `testdata/sample.pdf`; empty-pdf error path.
- `api/handler/ai_pdf_test.go` (new) — image mode rejects > 30-page PDF with `pdf_image_mode_unavailable`; text mode rejects > 200 pages with `pdf_too_many_pages`; text mode rejects > 400 000 chars with `pdf_text_too_long`; text mode reaches the generator with `Images: nil`.
- `pkg/aipipeline/quota_test.go` — debit then refund leaves the counter at its starting value.
- `pkg/aipipeline/service_generation_test.go` — provider returns a context-overflow error in image mode → SSE error chunk has code `pdf_image_mode_unavailable` and PDF page quota is refunded.

## Open items deferred to implementation

- Whether `sqlDebitPDFPages` accepts negative deltas (determines refund implementation shape).
- Exact substring set used by `classifyProviderError` — finalize against a fixture of actual Anthropic error bodies.
