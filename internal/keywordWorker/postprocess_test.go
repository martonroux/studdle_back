package keywordWorker

import (
	"testing"

	"studdle/backend/pkg/aipipeline"
)

func TestPostprocess_LowercasesAndTrims(t *testing.T) {
	in := []aipipeline.ExtractedKeyword{
		{Keyword: "  Mitose  ", Weight: 0.9},
		{Keyword: "Chromosome", Weight: 0.7},
	}
	out := postprocess(in)
	if len(out) != 2 {
		t.Fatalf("want 2, got %d", len(out))
	}
	for _, k := range out {
		if k.Keyword == "  Mitose  " || k.Keyword == "Chromosome" {
			t.Errorf("not lowercased/trimmed: %q", k.Keyword)
		}
	}
}

func TestPostprocess_CollapsesWhitespace(t *testing.T) {
	in := []aipipeline.ExtractedKeyword{{Keyword: "cycle   cellulaire", Weight: 0.5}}
	out := postprocess(in)
	if len(out) != 1 || out[0].Keyword != "cycle cellulaire" {
		t.Errorf("whitespace not collapsed: %+v", out)
	}
}

func TestPostprocess_DedupesKeepingHighestWeight(t *testing.T) {
	in := []aipipeline.ExtractedKeyword{
		{Keyword: "Mitose", Weight: 0.4},
		{Keyword: "mitose", Weight: 0.9},
		{Keyword: " mitose ", Weight: 0.2},
	}
	out := postprocess(in)
	if len(out) != 1 {
		t.Fatalf("want 1 deduped, got %d", len(out))
	}
	if out[0].Weight != 0.9 {
		t.Errorf("want highest weight 0.9, got %v", out[0].Weight)
	}
}

func TestPostprocess_DropsOver64Chars(t *testing.T) {
	long := make([]byte, 70)
	for i := range long {
		long[i] = 'a'
	}
	in := []aipipeline.ExtractedKeyword{
		{Keyword: string(long), Weight: 0.9},
		{Keyword: "ok", Weight: 0.5},
	}
	out := postprocess(in)
	if len(out) != 1 || out[0].Keyword != "ok" {
		t.Errorf("did not drop oversized: %+v", out)
	}
}

func TestPostprocess_EmptyAfterCleanup(t *testing.T) {
	in := []aipipeline.ExtractedKeyword{{Keyword: "   ", Weight: 0.5}}
	out := postprocess(in)
	if len(out) != 0 {
		t.Errorf("want empty, got %+v", out)
	}
}
