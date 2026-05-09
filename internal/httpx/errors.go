package httpx

import (
	"encoding/json"
	"errors"
	"net/http"

	"studbud/backend/internal/myErrors"
)

type errorBody struct {
	Error errorDetails `json:"error"`
}

type errorDetails struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Field   string `json:"field,omitempty"`
}

// sentinelStatus maps each sentinel error to its HTTP status code.
var sentinelStatus = []struct {
	err    error // err is the sentinel to match via errors.Is
	status int   // status is the HTTP status code to return
}{
	{myErrors.ErrUnauthenticated, http.StatusUnauthorized},
	{myErrors.ErrNotVerified, http.StatusForbidden},
	{myErrors.ErrForbidden, http.StatusForbidden},
	{myErrors.ErrAdminRequired, http.StatusForbidden},
	{myErrors.ErrNotFound, http.StatusNotFound},
	{myErrors.ErrConflict, http.StatusConflict},
	{myErrors.ErrAlreadyVerified, http.StatusConflict},
	{myErrors.ErrInvalidInput, http.StatusBadRequest},
	{myErrors.ErrValidation, http.StatusBadRequest},
	{myErrors.ErrNoAIAccess, http.StatusPaymentRequired},
	{myErrors.ErrQuotaExhausted, http.StatusTooManyRequests},
	{myErrors.ErrPdfTooLarge, http.StatusTooManyRequests},
	{myErrors.ErrPDFImageModeUnavailable, http.StatusUnprocessableEntity},
	{myErrors.ErrPDFTooManyPages, http.StatusRequestEntityTooLarge},
	{myErrors.ErrPDFTextTooLong, http.StatusRequestEntityTooLarge},
	{myErrors.ErrAIProvider, http.StatusBadGateway},
	{myErrors.ErrStripe, http.StatusBadGateway},
	{myErrors.ErrContentPolicy, http.StatusUnprocessableEntity},
	{myErrors.ErrNotImplemented, http.StatusNotImplemented},
}

// sentinelCodes maps each sentinel error to its stable string code.
var sentinelCodes = []struct {
	err  error  // err is the sentinel to match via errors.Is
	code string // code is the stable API error code string
}{
	{myErrors.ErrUnauthenticated, "unauthenticated"},
	{myErrors.ErrNotVerified, "not_verified"},
	{myErrors.ErrForbidden, "forbidden"},
	{myErrors.ErrAdminRequired, "admin_required"},
	{myErrors.ErrNotFound, "not_found"},
	{myErrors.ErrConflict, "conflict"},
	{myErrors.ErrAlreadyVerified, "already_verified"},
	{myErrors.ErrInvalidInput, "invalid_input"},
	{myErrors.ErrValidation, "validation"},
	{myErrors.ErrNoAIAccess, "no_ai_access"},
	{myErrors.ErrQuotaExhausted, "quota_exhausted"},
	{myErrors.ErrPdfTooLarge, "pdf_too_large"},
	{myErrors.ErrPDFImageModeUnavailable, "pdf_image_mode_unavailable"},
	{myErrors.ErrPDFTooManyPages, "pdf_too_many_pages"},
	{myErrors.ErrPDFTextTooLong, "pdf_text_too_long"},
	{myErrors.ErrAIProvider, "ai_provider_error"},
	{myErrors.ErrStripe, "stripe_error"},
	{myErrors.ErrContentPolicy, "content_policy"},
	{myErrors.ErrNotImplemented, "not_implemented"},
}

// WriteError writes a JSON error envelope with HTTP status mapped from the sentinel.
// If the error is an *myErrors.AppError with Status != 0, that overrides the mapping.
func WriteError(w http.ResponseWriter, err error) {
	status := mapStatus(err)
	code, message, field := describe(err)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorBody{Error: errorDetails{Code: code, Message: message, Field: field}})
}

// mapStatus returns the HTTP status code for err by consulting sentinelStatus.
func mapStatus(err error) int {
	var ae *myErrors.AppError
	if errors.As(err, &ae) && ae.Status != 0 {
		return ae.Status
	}
	for _, entry := range sentinelStatus {
		if errors.Is(err, entry.err) {
			return entry.status
		}
	}
	return http.StatusInternalServerError
}

// sentinelCode returns the stable string code for err by consulting sentinelCodes.
func sentinelCode(err error) string {
	for _, entry := range sentinelCodes {
		if errors.Is(err, entry.err) {
			return entry.code
		}
	}
	return "internal_error"
}

func describe(err error) (code, message, field string) {
	var ae *myErrors.AppError
	if errors.As(err, &ae) {
		return orDefault(ae.Code, sentinelCode(ae.Wrapped)), ae.Message, ae.Field
	}
	return sentinelCode(err), err.Error(), ""
}

func orDefault(s, fallback string) string {
	if s != "" {
		return s
	}
	return fallback
}
