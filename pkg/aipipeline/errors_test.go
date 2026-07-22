package aipipeline

import (
	"errors"
	"fmt"
	"testing"

	"studdle/backend/internal/myErrors"
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
