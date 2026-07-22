package plan

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"studdle/backend/internal/aiProvider"
	"studdle/backend/internal/myErrors"
	"studdle/backend/pkg/aipipeline"
	"studdle/backend/pkg/exam"
)

// crossSubjectShortlistLimit caps how many candidates the SQL shortlist returns.
const crossSubjectShortlistLimit = 30

// crossSubjectKeepLimit caps how many ranked cross-subject candidates the
// orchestrator forwards to the plan-generation prompt.
const crossSubjectKeepLimit = 15

// Phase is one observable stage of plan generation, surfaced to clients via SSE.
type Phase string

const (
	// PhaseShortlist is the SQL keyword-overlap shortlist.
	PhaseShortlist Phase = "shortlist"
	// PhaseRanking is the AI cross-subject relevance rank.
	PhaseRanking Phase = "ranking"
	// PhasePlanning is the AI plan generator.
	PhasePlanning Phase = "planning"
	// PhaseDone marks successful completion; payload carries the plan summary.
	PhaseDone Phase = "done"
	// PhaseError marks terminal failure; payload carries the error message.
	PhaseError Phase = "error"
)

// Event is one entry on the GenerateForExam output channel.
type Event struct {
	Phase  Phase  `json:"phase"`            // Phase identifies the orchestrator stage
	Detail string `json:"detail,omitempty"` // Detail is a free-form short status string
	Plan   *Plan  `json:"plan,omitempty"`   // Plan is set on PhaseDone with the persisted plan
	Error  string `json:"error,omitempty"`  // Error is set on PhaseError
}

// GenerateForExam orchestrates a full revision-plan generation for examID.
// Returns a streaming Event channel; the channel closes after PhaseDone or PhaseError.
func (s *Service) GenerateForExam(ctx context.Context, userID, examID int64) (<-chan Event, error) {
	exm, err := s.exam.Get(ctx, userID, examID)
	if err != nil {
		return nil, err
	}
	if err := assertExamFutureForGenerate(exm); err != nil {
		return nil, err
	}
	out := make(chan Event, 8)
	go s.runGeneration(ctx, userID, exm, out)
	return out, nil
}

// runGeneration is the async pipeline that emits phase events on `out`.
func (s *Service) runGeneration(ctx context.Context, userID int64, exm *exam.Exam, out chan<- Event) {
	defer close(out)
	primary, candidates, err := s.preparePools(ctx, userID, exm, out)
	if err != nil {
		emitError(out, err)
		return
	}
	if err := s.assertSubjectSizeOK(primary); err != nil {
		emitError(out, err)
		return
	}

	images, err := s.loadAnnalesImages(ctx, exm)
	if err != nil {
		emitError(out, err)
		return
	}
	plan, err := s.runPlanningPhase(ctx, userID, exm, primary, candidates, images, out)
	if err != nil {
		emitError(out, err)
		return
	}
	emit(out, Event{Phase: PhaseDone, Plan: plan})
}

// preparePools loads the primary cards, runs the keyword shortlist, and
// (when the shortlist is non-empty) runs the AI cross-subject re-ranker.
// Emits PhaseShortlist + PhaseRanking events.
func (s *Service) preparePools(ctx context.Context, userID int64, exm *exam.Exam, out chan<- Event) ([]PrimaryCard, []Candidate, error) {
	primary, err := s.loadPrimaryCards(ctx, exm.SubjectID)
	if err != nil {
		return nil, nil, err
	}
	emit(out, Event{Phase: PhaseShortlist})
	rawCandidates, err := Shortlist(ctx, s.db, userID, exm.SubjectID, crossSubjectShortlistLimit)
	if err != nil {
		return nil, nil, err
	}
	if len(rawCandidates) == 0 {
		return primary, nil, nil
	}
	emit(out, Event{Phase: PhaseRanking, Detail: fmt.Sprintf("%d candidates", len(rawCandidates))})
	ranked, err := s.rankCrossSubjects(ctx, userID, exm, rawCandidates)
	if err != nil {
		return nil, nil, err
	}
	return primary, ranked, nil
}

// runPlanningPhase renders + invokes the plan-generation prompt and persists.
// Emits PhasePlanning. Returns the persisted plan on success.
func (s *Service) runPlanningPhase(
	ctx context.Context, userID int64, exm *exam.Exam,
	primary []PrimaryCard, candidates []Candidate, images []aiProvider.ImagePart,
	out chan<- Event,
) (*Plan, error) {
	emit(out, Event{Phase: PhasePlanning})
	stats, err := s.loadStateCounts(ctx, exm.SubjectID)
	if err != nil {
		return nil, err
	}
	prompt, err := s.renderPlanPrompt(ctx, userID, exm, primary, candidates, stats, len(images) > 0)
	if err != nil {
		return nil, err
	}
	days, jobID, err := s.streamPlanGeneration(ctx, userID, exm, prompt, images)
	if err != nil {
		return nil, err
	}
	cleaned := normalizePlan(days, primary, candidates, time.Now(), exm.ExamDate)
	return s.persist(ctx, exm.ID, cleaned, s.model, hashPrompt(prompt), &jobID)
}

// streamPlanGeneration calls aipipeline.GenerateRevisionPlan, drains its chunk
// stream, and reassembles the AI's per-day items into a []Day. The returned
// jobID identifies the ai_jobs row for audit-correlation.
// Quota debit + ai_jobs accounting are owned by RunStructuredGeneration.
func (s *Service) streamPlanGeneration(ctx context.Context, userID int64, exm *exam.Exam, prompt string, images []aiProvider.ImagePart) ([]Day, int64, error) {
	out, err := s.ai.GenerateRevisionPlan(ctx, aipipeline.PlanGenerateInput{
		UserID:        userID,
		ExamID:        exm.ID,
		SubjectID:     exm.SubjectID,
		Prompt:        prompt,
		AnnalesImages: images,
	})
	if err != nil {
		return nil, 0, err
	}
	days, err := collectDayItems(ctx, out.Chunks)
	if err != nil {
		return nil, out.JobID, err
	}
	return days, out.JobID, nil
}

// collectDayItems consumes the streamed AIChunk channel and decodes each item
// into a Day. Bubbles ChunkError as a typed error.
func collectDayItems(ctx context.Context, chunks <-chan aipipeline.AIChunk) ([]Day, error) {
	var days []Day
	for c := range chunks {
		if c.Kind == aipipeline.ChunkError {
			return nil, c.Err
		}
		if c.Kind != aipipeline.ChunkItem {
			continue
		}
		var d Day
		if err := json.Unmarshal(c.Item, &d); err != nil {
			return nil, &myErrors.AppError{Code: "ai_schema_invalid", Message: "plan day did not match shape", Wrapped: err}
		}
		days = append(days, d)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	return days, nil
}

// renderPlanPrompt stitches together the AI inputs for FeatureGenerateRevisionPlan.
func (s *Service) renderPlanPrompt(
	ctx context.Context, uid int64, exm *exam.Exam,
	primary []PrimaryCard, candidates []Candidate, stats stateCounts, hasAnnales bool,
) (string, error) {
	subjectName, err := s.resolveSubjectName(ctx, uid, exm.SubjectID)
	if err != nil {
		return "", err
	}
	return aipipeline.RenderRevisionPlan(aipipeline.RevisionPlanValues{
		ExamDate:          exm.ExamDate.Format(dateLayout),
		DaysRemaining:     daysBetween(time.Now(), exm.ExamDate),
		ExamTitle:         exm.Title,
		ExamNotes:         exm.Notes,
		SubjectName:       subjectName,
		HasAnnales:        hasAnnales,
		PrimaryCards:      primaryCardsForPrompt(primary),
		CrossSubjectCards: candidatesForPrompt(candidates),
		UserStats: aipipeline.PlanUserStats{
			New: stats.New, Bad: stats.Bad, Ok: stats.OK, Good: stats.Good,
		},
	})
}

// rankCrossSubjects asks the AI to keep at most crossSubjectKeepLimit relevant candidates.
// On any AI error or empty selection the function falls back to returning the
// shortlist truncated to crossSubjectKeepLimit — degraded but functional.
func (s *Service) rankCrossSubjects(ctx context.Context, uid int64, exm *exam.Exam, candidates []Candidate) ([]Candidate, error) {
	subjectName, err := s.resolveSubjectName(ctx, uid, exm.SubjectID)
	if err != nil {
		return nil, err
	}
	out, err := s.ai.RankCrossSubjects(ctx, aipipeline.RankInput{
		ExamSubject: subjectName,
		ExamTitle:   exm.Title,
		Candidates:  candidatesForRank(candidates),
		TopK:        crossSubjectKeepLimit,
	})
	if err != nil || len(out.SelectedIDs) == 0 {
		return truncateCandidates(candidates, crossSubjectKeepLimit), nil
	}
	return filterCandidatesByID(candidates, out.SelectedIDs), nil
}

// loadAnnalesImages rasterizes the annales PDF (if any) into AI-ready images.
// No-op when no annales is attached.
func (s *Service) loadAnnalesImages(ctx context.Context, exm *exam.Exam) ([]aiProvider.ImagePart, error) {
	if exm.AnnalesImageID == nil || *exm.AnnalesImageID == "" {
		return nil, nil
	}
	pdfBytes, err := s.loadAnnalesBytes(ctx, *exm.AnnalesImageID)
	if err != nil {
		return nil, err
	}
	imgs, err := aiProvider.PDFToImages(ctx, pdfBytes, aiProvider.PDFOptions{PerPageTimeout: 30 * time.Second})
	if err != nil {
		return nil, &myErrors.AppError{Code: "pdf_unreadable", Message: err.Error(), Wrapped: myErrors.ErrValidation, Field: "annalesImageId"}
	}
	return imgs, nil
}

// assertSubjectSizeOK refuses generation when the subject has too few cards to
// produce a useful plan. Spec: subject_too_sparse / subject_empty.
func (s *Service) assertSubjectSizeOK(primary []PrimaryCard) error {
	if len(primary) == 0 {
		return &myErrors.AppError{Code: "subject_empty", Message: "subject has no flashcards", Wrapped: myErrors.ErrValidation}
	}
	if len(primary) < minPrimaryCardsForGeneration {
		return &myErrors.AppError{
			Code:    "subject_too_sparse",
			Message: fmt.Sprintf("subject has %d flashcards; %d required", len(primary), minPrimaryCardsForGeneration),
			Wrapped: myErrors.ErrValidation,
		}
	}
	return nil
}

// assertExamFutureForGenerate refuses plan generation for past exams.
// Edits/views remain allowed elsewhere — only generation is gated.
func assertExamFutureForGenerate(e *exam.Exam) error {
	now := time.Now().UTC()
	cutoff := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	if e.ExamDate.Before(cutoff) {
		return &myErrors.AppError{Code: "exam_date_past", Message: "cannot generate a plan for a past exam", Wrapped: myErrors.ErrValidation, Field: "examDate"}
	}
	return nil
}

// loadAnnalesBytes reads the annales PDF bytes via the image service.
func (s *Service) loadAnnalesBytes(ctx context.Context, imageID string) ([]byte, error) {
	rc, _, err := s.image.Open(ctx, imageID)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return readAllSimple(rc)
}

// emit sends an Event without blocking when the receiver is gone.
func emit(out chan<- Event, ev Event) {
	select {
	case out <- ev:
	default:
	}
}

// emitError emits a terminal error event with the wrapped string.
func emitError(out chan<- Event, err error) {
	emit(out, Event{Phase: PhaseError, Error: err.Error()})
}

// normalizePlan is a thin wrapper over NormalizeDays that passes the right ID sets.
func normalizePlan(days []Day, primary []PrimaryCard, candidates []Candidate, today, examDate time.Time) []Day {
	primaryIDs := make([]int64, len(primary))
	for i, p := range primary {
		primaryIDs[i] = p.ID
	}
	crossIDs := make([]int64, len(candidates))
	for i, c := range candidates {
		crossIDs[i] = c.ID
	}
	return NormalizeDays(PostProcessInput{
		Days: days, PrimaryIDs: primaryIDs, CrossIDs: crossIDs, DeeperIDs: primaryIDs,
	}, today, examDate)
}

// readAllSimple slurps the reader into a byte slice with a generous cap.
// 32 MB is well above the annales ceiling (5 MB) and prevents pathological reads.
func readAllSimple(r io.Reader) ([]byte, error) {
	const cap32 = 32 << 20
	b, err := io.ReadAll(io.LimitReader(r, cap32))
	if err != nil {
		return nil, fmt.Errorf("read annales:\n%w", err)
	}
	return b, nil
}
