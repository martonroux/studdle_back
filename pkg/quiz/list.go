package quiz

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"studbud/backend/internal/myErrors"
)

// defaultListLimit / maxListLimit bound the page size for List.
const (
	defaultListLimit = 20
	maxListLimit     = 100
)

// ListRequest scopes a quiz-list query to a user, optionally narrowed to a subject.
type ListRequest struct {
	UserID    int64  // UserID is the authenticated caller; results are always scoped to them
	SubjectID *int64 // SubjectID narrows to one subject; nil = all subjects
	Limit     int    // Limit caps the page size; <=0 falls back to defaultListLimit, capped at maxListLimit
	Offset    int    // Offset skips this many rows (newest-first order); <0 treated as 0
}

// AttemptSummary is the per-quiz attempt rollup shown on the list.
type AttemptSummary struct {
	CompletedCount      int        `json:"completedCount"`
	BestScorePct        *int       `json:"bestScorePct"`
	LastScorePct        *int       `json:"lastScorePct"`
	LastCompletedAt     *time.Time `json:"lastCompletedAt"`
	InProgressAttemptID *int64     `json:"inProgressAttemptId"`
}

// ListItem is one row of the GET /quizzes response.
type ListItem struct {
	ID            int64          `json:"id"`
	SubjectID     int64          `json:"subjectId"`
	SubjectName   string         `json:"subjectName"`
	ChapterID     *int64         `json:"chapterId"`
	ChapterName   *string        `json:"chapterName"`
	Kind          Kind           `json:"kind"`
	QuestionCount int            `json:"questionCount"`
	Types         []QuestionType `json:"types"`
	CreatedAt     time.Time      `json:"createdAt"`
	Attempts      AttemptSummary `json:"attempts"`
}

// ListResult is the output of Service.List.
type ListResult struct {
	Quizzes []ListItem
	Total   int
}

// listQuery joins subject/chapter names and the attempt rollup in a single pass
// (lateral joins avoid N+1). agg/last/inprog each return at most one row per
// quiz, so the LEFT JOIN LATERALs never duplicate the base quizzes row.
const listQuery = `
SELECT q.id, q.subject_id, s.name, q.chapter_id, c.title,
       q.kind, q.question_count, q.settings_jsonb, q.created_at,
       agg.completed_count, agg.best_score_pct,
       last.last_score_pct, last.last_completed_at,
       inprog.id
  FROM quizzes q
  JOIN subjects s ON s.id = q.subject_id
  LEFT JOIN chapters c ON c.id = q.chapter_id
  LEFT JOIN LATERAL (
      SELECT count(*) AS completed_count, max(score_pct) AS best_score_pct
        FROM quiz_attempts
       WHERE quiz_id = q.id AND state = 'completed'
  ) agg ON true
  LEFT JOIN LATERAL (
      SELECT score_pct AS last_score_pct, completed_at AS last_completed_at
        FROM quiz_attempts
       WHERE quiz_id = q.id AND state = 'completed'
       ORDER BY completed_at DESC
       LIMIT 1
  ) last ON true
  LEFT JOIN LATERAL (
      SELECT id FROM quiz_attempts
       WHERE quiz_id = q.id AND user_id = q.user_id AND state = 'in_progress'
       LIMIT 1
  ) inprog ON true
 WHERE q.user_id = $1`

// List returns the caller's quizzes, newest-first, with attempt summaries and total count.
func (s *Service) List(ctx context.Context, req ListRequest) (ListResult, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = defaultListLimit
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}
	offset := req.Offset
	if offset < 0 {
		offset = 0
	}

	total, err := s.countQuizzes(ctx, req.UserID, req.SubjectID)
	if err != nil {
		return ListResult{}, err
	}
	items, err := s.listQuizzes(ctx, req.UserID, req.SubjectID, limit, offset)
	if err != nil {
		return ListResult{}, err
	}
	return ListResult{Quizzes: items, Total: total}, nil
}

// countQuizzes returns the total row count for the (optionally subject-scoped) list query.
func (s *Service) countQuizzes(ctx context.Context, uid int64, subjectID *int64) (int, error) {
	q := `SELECT count(*) FROM quizzes WHERE user_id = $1`
	args := []any{uid}
	if subjectID != nil {
		q += ` AND subject_id = $2`
		args = append(args, *subjectID)
	}
	var total int
	if err := s.db.QueryRow(ctx, q, args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("count quizzes:\n%w", err)
	}
	return total, nil
}

// listQuizzes runs the paginated list query, appending the subject filter when set.
func (s *Service) listQuizzes(ctx context.Context, uid int64, subjectID *int64, limit, offset int) ([]ListItem, error) {
	q := listQuery
	args := []any{uid}
	if subjectID != nil {
		q += ` AND q.subject_id = $2`
		args = append(args, *subjectID)
	}
	q += fmt.Sprintf(" ORDER BY q.created_at DESC LIMIT $%d OFFSET $%d", len(args)+1, len(args)+2)
	args = append(args, limit, offset)

	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list quizzes:\n%w", err)
	}
	defer rows.Close()

	items := []ListItem{}
	for rows.Next() {
		var item ListItem
		var settings []byte
		if err := rows.Scan(
			&item.ID, &item.SubjectID, &item.SubjectName, &item.ChapterID, &item.ChapterName,
			&item.Kind, &item.QuestionCount, &settings, &item.CreatedAt,
			&item.Attempts.CompletedCount, &item.Attempts.BestScorePct,
			&item.Attempts.LastScorePct, &item.Attempts.LastCompletedAt,
			&item.Attempts.InProgressAttemptID,
		); err != nil {
			return nil, fmt.Errorf("scan quiz list row:\n%w", err)
		}
		item.Types = parseSettingsTypes(settings)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate quiz list rows:\n%w", err)
	}
	return items, nil
}

// parseSettingsTypes extracts the "types" array persisted in settings_jsonb (see Generate).
// Returns nil on malformed/absent data rather than failing the whole list.
func parseSettingsTypes(raw []byte) []QuestionType {
	var settings struct {
		Types []QuestionType `json:"types"`
	}
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		return nil
	}
	return settings.Types
}

// Delete removes a quiz owned by uid. FK cascades (ON DELETE CASCADE) handle
// quiz_questions/quiz_attempts/quiz_attempt_answers. Returns ErrNotFound if the
// quiz does not exist or is not owned by uid (unowned quizzes are not distinguished
// from missing ones, so existence isn't leaked).
func (s *Service) Delete(ctx context.Context, uid, quizID int64) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM quizzes WHERE id = $1 AND user_id = $2`, quizID, uid)
	if err != nil {
		return fmt.Errorf("delete quiz:\n%w", err)
	}
	if tag.RowsAffected() == 0 {
		return myErrors.ErrNotFound
	}
	return nil
}
