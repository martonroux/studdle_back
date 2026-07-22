package keywordWorker

import (
	"strings"

	"studdle/backend/pkg/aipipeline"
)

// postprocess normalizes a model-emitted keyword list.
// Rules: lowercase, trim, whitespace-collapse, dedup-keep-max-weight, drop >64 chars,
// drop empties. Returns the cleaned slice (may be empty).
func postprocess(in []aipipeline.ExtractedKeyword) []aipipeline.ExtractedKeyword {
	byKey := make(map[string]float64, len(in))

	for _, k := range in {
		clean := normalize(k.Keyword)

		if clean == "" || len(clean) > 64 {
			continue
		}

		if existing, ok := byKey[clean]; !ok || k.Weight > existing {
			byKey[clean] = k.Weight
		}
	}

	out := make([]aipipeline.ExtractedKeyword, 0, len(byKey))

	for k, w := range byKey {
		out = append(out, aipipeline.ExtractedKeyword{Keyword: k, Weight: w})
	}

	return out
}

// normalize lowercases, trims, and collapses internal whitespace runs.
func normalize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))

	if s == "" {
		return s
	}

	return strings.Join(strings.Fields(s), " ")
}
