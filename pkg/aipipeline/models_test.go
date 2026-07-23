package aipipeline_test

import (
	"testing"

	"studdle/backend/pkg/aipipeline"
)

func TestModelMapFor_OverrideAndFallback(t *testing.T) {
	m := aipipeline.ModelMap{
		Default: "claude-sonnet-4-6",
		PerFeature: map[aipipeline.FeatureKey]string{
			aipipeline.FeatureGenerateFromPDF: "gpt-5.4-mini",
			aipipeline.FeatureCheckFlashcard:  "",
		},
	}

	if got := m.For(aipipeline.FeatureGenerateFromPDF); got != "gpt-5.4-mini" {
		t.Errorf("For(generate_pdf) = %q, want gpt-5.4-mini", got)
	}
	if got := m.For(aipipeline.FeatureCheckFlashcard); got != "claude-sonnet-4-6" {
		t.Errorf("For(check_flashcard) with empty override = %q, want default", got)
	}
	if got := m.For(aipipeline.FeatureExtractKeywords); got != "claude-sonnet-4-6" {
		t.Errorf("For(extract_keywords) with no override = %q, want default", got)
	}
}
