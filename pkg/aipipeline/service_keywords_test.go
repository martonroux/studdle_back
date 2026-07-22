package aipipeline_test

import (
	"context"
	"strings"
	"testing"

	"studdle/backend/internal/aiProvider"
	"studdle/backend/pkg/aipipeline"
	"studdle/backend/testutil"
)

// newKeywordSvc returns a Service wired only with a fake provider and model.
// db and access are nil because ExtractKeywords never touches them.
func newKeywordSvc(cli aiProvider.Client) *aipipeline.Service {
	return aipipeline.NewService(nil, cli, nil, aipipeline.DefaultQuotaLimits(), "claude-test")
}

func TestExtractKeywords_HappyPath(t *testing.T) {
	body := `{"keywords":[{"keyword":"mitose","weight":1.0},{"keyword":"chromosome","weight":0.7}]}`
	svc := newKeywordSvc(&testutil.FakeAIClient{
		Chunks: []aiProvider.Chunk{{Text: body, Done: true}},
	})
	out, err := svc.ExtractKeywords(context.Background(), aipipeline.ExtractInput{
		Title:    "Mitose",
		Question: "Q",
		Answer:   "A",
	})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(out.Keywords) != 2 {
		t.Fatalf("want 2 keywords, got %d", len(out.Keywords))
	}
	if out.Keywords[0].Keyword != "mitose" || out.Keywords[0].Weight != 1.0 {
		t.Errorf("first kw mismatch: %+v", out.Keywords[0])
	}
}

func TestExtractKeywords_BadJSON(t *testing.T) {
	svc := newKeywordSvc(&testutil.FakeAIClient{
		Chunks: []aiProvider.Chunk{{Text: "not json", Done: true}},
	})
	_, err := svc.ExtractKeywords(context.Background(), aipipeline.ExtractInput{Question: "Q", Answer: "A"})
	if err == nil || !strings.Contains(err.Error(), "parse keywords") {
		t.Fatalf("want parse error, got %v", err)
	}
}
