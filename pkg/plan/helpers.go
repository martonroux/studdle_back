package plan

import (
	"context"
	"errors"
	"time"

	"studdle/backend/internal/myErrors"
	"studdle/backend/pkg/aipipeline"
)

// daysBetween returns the inclusive day count from `from` to `to` (date granularity).
// Returns 0 for same-day, negative when `to` is before `from`.
func daysBetween(from, to time.Time) int {
	a := startOfDay(from)
	b := startOfDay(to)
	return int(b.Sub(a).Hours() / 24)
}

// primaryCardsForPrompt projects domain primary cards to the aipipeline shape.
func primaryCardsForPrompt(cards []PrimaryCard) []aipipeline.PlanCardInfo {
	out := make([]aipipeline.PlanCardInfo, len(cards))
	for i, c := range cards {
		out[i] = aipipeline.PlanCardInfo{ID: c.ID, Title: c.Title, Keywords: c.Keywords}
	}
	return out
}

// candidatesForPrompt projects shortlisted candidates to the plan-prompt shape.
// SubjectName is included so the plan prompt can label cross-subject cards.
func candidatesForPrompt(cs []Candidate) []aipipeline.PlanCardInfo {
	out := make([]aipipeline.PlanCardInfo, len(cs))
	for i, c := range cs {
		out[i] = aipipeline.PlanCardInfo{
			ID: c.ID, Title: c.Title, Keywords: c.Keywords, SubjectName: c.SubjectName,
		}
	}
	return out
}

// candidatesForRank projects candidates to the cross-subject ranker input shape.
func candidatesForRank(cs []Candidate) []aipipeline.CrossSubjectCandidate {
	out := make([]aipipeline.CrossSubjectCandidate, len(cs))
	for i, c := range cs {
		out[i] = aipipeline.CrossSubjectCandidate{
			ID: c.ID, Title: c.Title, SubjectName: c.SubjectName,
			Keywords: c.Keywords, OverlapScore: c.OverlapScore,
		}
	}
	return out
}

// truncateCandidates returns at most `n` candidates, preserving order.
func truncateCandidates(cs []Candidate, n int) []Candidate {
	if len(cs) <= n {
		return cs
	}
	return cs[:n]
}

// filterCandidatesByID returns candidates whose ID is in keepIDs, preserving
// the order of keepIDs (so AI ranking order is honored).
func filterCandidatesByID(cs []Candidate, keepIDs []int64) []Candidate {
	if len(keepIDs) == 0 {
		return nil
	}
	byID := make(map[int64]Candidate, len(cs))
	for _, c := range cs {
		byID[c.ID] = c
	}
	out := make([]Candidate, 0, len(keepIDs))
	for _, id := range keepIDs {
		if c, ok := byID[id]; ok {
			out = append(out, c)
		}
	}
	return out
}

// resolveSubjectName looks up the subject name for the given ID, returning a
// validation error if the subject was deleted mid-flight. uid must already
// have read access to subjectID (verified by LookupSubject itself).
func (s *Service) resolveSubjectName(ctx context.Context, uid, subjectID int64) (string, error) {
	meta, err := s.ai.LookupSubject(ctx, uid, subjectID)
	if err != nil {
		if errors.Is(err, myErrors.ErrNotFound) {
			return "", &myErrors.AppError{Code: "subject_missing", Message: "exam subject was removed", Wrapped: myErrors.ErrNotFound}
		}
		return "", err
	}
	return meta.Name, nil
}
