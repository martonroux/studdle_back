package aipipeline

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"studdle/backend/internal/myErrors"
)

// classifyProviderStartErr wraps raw provider-client errors into sentinel AppErrors.
// Called for synchronous errors returned from aiProvider.Client.Stream.
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

// classifyErrForPersistence returns (error_kind, error_message) for the ai_jobs row.
func classifyErrForPersistence(err error) (kind, msg string) {
	if err == nil {
		return "", ""
	}
	switch {
	case errors.Is(err, context.Canceled):
		return "cancelled", "context canceled"
	case errors.Is(err, myErrors.ErrContentPolicy):
		return "content_policy", err.Error()
	case errors.Is(err, myErrors.ErrAIProvider):
		return providerKind(err), err.Error()
	}
	return "internal", err.Error()
}

// providerKind returns a narrower provider error kind based on AppError.Code.
func providerKind(err error) string {
	var ae *myErrors.AppError
	if errors.As(err, &ae) && ae.Code != "" {
		return ae.Code
	}
	return "provider_5xx"
}

// statusFor maps a terminal error to an ai_jobs.status value.
func statusFor(err error) string {
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	return "failed"
}

// retryable reports whether a synchronous provider-start error is worth one
// transparent retry. Only transport transients qualify (5xx / timeout / 429).
// Content-policy refusals, 4xx, and malformed output are terminal.
func retryable(err error) bool {
	var ae *myErrors.AppError
	if !errors.As(err, &ae) {
		return false
	}
	switch ae.Code {
	case "provider_5xx", "provider_timeout", "provider_rate_limit":
		return true
	}
	return false
}

// isWellFormedObject returns true when b parses as a JSON object.
// Used to drop garbled items without aborting the stream.
func isWellFormedObject(b []byte) bool {
	s := strings.TrimSpace(string(b))
	if !strings.HasPrefix(s, "{") {
		return false
	}
	var m map[string]json.RawMessage
	return json.Unmarshal(b, &m) == nil
}

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
