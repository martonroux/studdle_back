package quiz

import (
	"encoding/json"
	"testing"
)

func TestNormalizeFillBlank_StripsCaseSpaceAndPunctuation(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Mitochondria.", "mitochondria"},
		{"  the  Cell ", "the  cell"}, // intentional inner-double-space preserved
		{"Reagan,", "reagan"},
	}
	for _, c := range cases {
		if got := normalizeFillBlank(c.in); got != c.want {
			t.Fatalf("normalize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// STU-47: fill_blank stores {"accepted":[...]} for fuzzy-matching, but clients
// render a single answer under "Correct answer" and expect {"value": "..."}
// (the same shape used for submitted fill_blank answers) — without this
// collapse, correctAnswer.value is undefined client-side and the row renders
// empty.
func TestPublicCorrectAnswer_FillBlankCollapsesToSingleValue(t *testing.T) {
	got := publicCorrectAnswer(QTypeFillBlank, json.RawMessage(`{"accepted":["Paris","paris"]}`))
	var v struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(got, &v); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if v.Value != "Paris" {
		t.Fatalf("value = %q, want %q", v.Value, "Paris")
	}
}

func TestPublicCorrectAnswer_MCQAndTrueFalsePassThroughUnchanged(t *testing.T) {
	mcq := json.RawMessage(`{"index":2}`)
	if got := publicCorrectAnswer(QTypeMultiChoice, mcq); string(got) != string(mcq) {
		t.Fatalf("MCQ payload mutated: got %s, want %s", got, mcq)
	}
	tf := json.RawMessage(`{"value":true}`)
	if got := publicCorrectAnswer(QTypeTrueFalse, tf); string(got) != string(tf) {
		t.Fatalf("T/F payload mutated: got %s, want %s", got, tf)
	}
}
