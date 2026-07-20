package myErrors

import (
	"errors"
	"fmt"
)

// ErrNotFound indicates a requested resource does not exist.
var ErrNotFound = errors.New("not found")

// ErrUnauthenticated indicates a missing or invalid JWT.
var ErrUnauthenticated = errors.New("unauthenticated")

// ErrNotVerified indicates the caller's email is not verified.
var ErrNotVerified = errors.New("email not verified")

// ErrForbidden indicates the caller lacks permission on a resource.
var ErrForbidden = errors.New("forbidden")

// ErrAdminRequired indicates an admin-only route was hit by a non-admin user.
var ErrAdminRequired = errors.New("admin required")

// ErrInvalidInput indicates malformed request input (JSON, types).
var ErrInvalidInput = errors.New("invalid input")

// ErrValidation indicates a request passed parsing but failed semantic checks.
var ErrValidation = errors.New("validation failed")

// ErrConflict indicates a uniqueness or state conflict.
var ErrConflict = errors.New("conflict")

// ErrAlreadyVerified indicates email verification was attempted on an already-verified user.
var ErrAlreadyVerified = errors.New("already verified")

// ErrNoAIAccess indicates the caller lacks an active AI subscription.
var ErrNoAIAccess = errors.New("no AI access")

// ErrQuotaExhausted indicates the caller has hit their daily AI quota.
var ErrQuotaExhausted = errors.New("quota exhausted")

// ErrPdfTooLarge indicates a PDF would exceed the remaining page quota.
var ErrPdfTooLarge = errors.New("pdf too large for remaining quota")

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

// ErrAIProvider indicates an upstream AI provider failure.
var ErrAIProvider = errors.New("ai provider error")

// ErrStripe indicates an upstream Stripe failure.
var ErrStripe = errors.New("stripe error")

// ErrContentPolicy indicates the provider refused to answer on content-policy grounds.
var ErrContentPolicy = errors.New("ai content policy refusal")

// ErrNotImplemented indicates a route exists but its feature is not yet implemented.
var ErrNotImplemented = errors.New("not implemented")

// ErrRateLimited indicates the caller exceeded a per-user request rate limit.
var ErrRateLimited = errors.New("rate limited")

// AppError carries contextual error information alongside a sentinel.
// Use when the caller needs structured details (e.g., which field failed).
type AppError struct {
	Code    string // Code is a stable identifier used by API responses
	Message string // Message is a user-safe explanation
	Field   string // Field optionally names the offending input field
	Status  int    // Status overrides HTTP status mapping; zero means "use sentinel default"
	Wrapped error  // Wrapped is the underlying sentinel or cause
}

// Error implements the error interface.
func (e *AppError) Error() string {
	if e.Wrapped != nil {
		return fmt.Sprintf("%s: %s (%v)", e.Code, e.Message, e.Wrapped)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Unwrap returns the underlying wrapped error for use with errors.Is / errors.As.
func (e *AppError) Unwrap() error {
	return e.Wrapped
}
