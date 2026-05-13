package quiz

import (
	"encoding/json"
	"fmt"

	"studbud/backend/internal/myErrors"
	"studbud/backend/pkg/aipipeline"
)

// drainQuestions consumes the AI chunk channel into RawQuestion rows.
// Validates each item; rejects the run if the total count != wantSize.
func drainQuestions(ch <-chan aipipeline.AIChunk, wantSize int) ([]RawQuestion, error) {
	var out []RawQuestion
	for chunk := range ch {
		switch chunk.Kind {
		case aipipeline.ChunkItem:
			rq, err := decodeItem(chunk.Item)
			if err != nil {
				return nil, err
			}
			out = append(out, rq)
		case aipipeline.ChunkError:
			return nil, chunk.Err
		}
	}
	if len(out) != wantSize {
		return nil, fmt.Errorf("%w: AI returned %d items, want %d",
			myErrors.ErrAIProvider, len(out), wantSize)
	}
	return out, nil
}

// rawItem matches the JSON shape emitted by the AI per question (see prompts/generate_quiz.tmpl).
type rawItem struct {
	QuestionType    string          `json:"questionType"`
	Stem            string          `json:"stem"`
	Options         json.RawMessage `json:"options,omitempty"`
	CorrectIndex    *int            `json:"correctIndex,omitempty"`
	CorrectValue    *bool           `json:"correctValue,omitempty"`
	Accepted        []string        `json:"accepted,omitempty"`
	Explanation     string          `json:"explanation,omitempty"`
	ReferencedFcIDs []int64         `json:"referencedFcIds"`
}

// decodeItem turns one raw AI emission into a typed RawQuestion, validating
// that the correctness payload matches the declared question type.
func decodeItem(raw json.RawMessage) (RawQuestion, error) {
	var item rawItem
	if err := json.Unmarshal(raw, &item); err != nil {
		return RawQuestion{}, fmt.Errorf("%w: %s", myErrors.ErrAIProvider, err)
	}
	rq := RawQuestion{
		Type:            QuestionType(item.QuestionType),
		Stem:            item.Stem,
		Explanation:     item.Explanation,
		ReferencedFcIDs: item.ReferencedFcIDs,
	}
	switch rq.Type {
	case QTypeMultiChoice:
		if item.CorrectIndex == nil || item.Options == nil {
			return RawQuestion{}, fmt.Errorf("%w: MCQ missing options/correctIndex", myErrors.ErrAIProvider)
		}
		rq.Options = item.Options
		correct, _ := json.Marshal(map[string]any{"index": *item.CorrectIndex})
		rq.Correct = correct
	case QTypeTrueFalse:
		if item.CorrectValue == nil {
			return RawQuestion{}, fmt.Errorf("%w: T/F missing correctValue", myErrors.ErrAIProvider)
		}
		correct, _ := json.Marshal(map[string]any{"value": *item.CorrectValue})
		rq.Correct = correct
	case QTypeFillBlank:
		if len(item.Accepted) == 0 {
			return RawQuestion{}, fmt.Errorf("%w: fill_blank missing accepted[]", myErrors.ErrAIProvider)
		}
		correct, _ := json.Marshal(map[string]any{"accepted": item.Accepted})
		rq.Correct = correct
	default:
		return RawQuestion{}, fmt.Errorf("%w: unknown questionType %q", myErrors.ErrAIProvider, item.QuestionType)
	}
	return rq, nil
}
