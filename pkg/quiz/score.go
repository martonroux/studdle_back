package quiz

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"studbud/backend/internal/myErrors"
)

// scoreAnswer returns true if userAns matches the correct payload.
// MCQ: {"index": N}; T/F: {"value": bool}; fill_blank: {"value": "text"} compared against accepted[].
func scoreAnswer(t QuestionType, correct, user json.RawMessage) (bool, error) {
	switch t {
	case QTypeMultiChoice:
		var c struct {
			Index int `json:"index"`
		}
		var u struct {
			Index int `json:"index"`
		}
		if err := json.Unmarshal(correct, &c); err != nil {
			return false, fmt.Errorf("bad correct payload:\n%w", err)
		}
		if err := json.Unmarshal(user, &u); err != nil {
			return false, fmt.Errorf("%w: %s", myErrors.ErrInvalidInput, err)
		}
		return c.Index == u.Index, nil
	case QTypeTrueFalse:
		var c struct {
			Value bool `json:"value"`
		}
		var u struct {
			Value bool `json:"value"`
		}
		if err := json.Unmarshal(correct, &c); err != nil {
			return false, err
		}
		if err := json.Unmarshal(user, &u); err != nil {
			return false, fmt.Errorf("%w: %s", myErrors.ErrInvalidInput, err)
		}
		return c.Value == u.Value, nil
	case QTypeFillBlank:
		var c struct {
			Accepted []string `json:"accepted"`
		}
		var u struct {
			Value string `json:"value"`
		}
		if err := json.Unmarshal(correct, &c); err != nil {
			return false, err
		}
		if err := json.Unmarshal(user, &u); err != nil {
			return false, fmt.Errorf("%w: %s", myErrors.ErrInvalidInput, err)
		}
		uNorm := normalizeFillBlank(u.Value)
		for _, a := range c.Accepted {
			if normalizeFillBlank(a) == uNorm {
				return true, nil
			}
		}
		return false, nil
	}
	return false, fmt.Errorf("%w: unknown question type %q", myErrors.ErrInvalidInput, t)
}

// publicCorrectAnswer converts the server-only correct payload into the shape
// clients display. MCQ/T-F already match the wire shape clients expect and
// pass through unchanged. fill_blank is stored as {"accepted":[...]} (every
// fuzzy-matched variant, for scoring) but clients render a single answer, so
// it collapses to {"value": accepted[0]} — the same {value:string} shape
// used for submitted fill_blank answers.
func publicCorrectAnswer(t QuestionType, correct json.RawMessage) json.RawMessage {
	if t != QTypeFillBlank {
		return correct
	}
	var c struct {
		Accepted []string `json:"accepted"`
	}
	if err := json.Unmarshal(correct, &c); err != nil || len(c.Accepted) == 0 {
		return correct
	}
	out, err := json.Marshal(struct {
		Value string `json:"value"`
	}{Value: c.Accepted[0]})
	if err != nil {
		return correct
	}
	return out
}

// normalizeFillBlank lower-cases, trims outer whitespace, and strips Unicode punctuation.
// Inner whitespace is preserved (so "the cell" still differs from "thecell").
func normalizeFillBlank(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	var b strings.Builder
	for _, r := range s {
		if unicode.IsPunct(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
