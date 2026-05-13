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
