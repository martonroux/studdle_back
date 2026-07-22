package chapter

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"studdle/backend/internal/myErrors"
	"studdle/backend/pkg/access"
)

// Service owns chapter CRUD.
type Service struct {
	db     *pgxpool.Pool   // db is the shared pool
	access *access.Service // access enforces subject-scoped permissions
}

// NewService constructs a Service.
func NewService(db *pgxpool.Pool, acc *access.Service) *Service {
	return &Service{db: db, access: acc}
}

// Create inserts a chapter; caller must have edit rights on the subject.
func (s *Service) Create(ctx context.Context, uid int64, in CreateInput) (*Chapter, error) {
	if in.Title == "" {
		return nil, myErrors.ErrInvalidInput
	}
	level, err := s.access.SubjectLevel(ctx, uid, in.SubjectID)
	if err != nil {
		return nil, err
	}
	if !level.CanEdit() {
		return nil, myErrors.ErrForbidden
	}
	var ch Chapter
	err = s.db.QueryRow(ctx, `
		INSERT INTO chapters (subject_id, title, position)
		VALUES ($1,$2, COALESCE((SELECT MAX(position)+1 FROM chapters WHERE subject_id=$1), 0))
		RETURNING id, subject_id, title, position, created_at, updated_at
	`, in.SubjectID, in.Title).Scan(
		&ch.ID, &ch.SubjectID, &ch.Title, &ch.Position, &ch.CreatedAt, &ch.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert chapter:\n%w", err)
	}
	return &ch, nil
}

// List returns chapters for a subject if caller can read it.
func (s *Service) List(ctx context.Context, uid, subjectID int64) ([]Chapter, error) {
	level, err := s.access.SubjectLevel(ctx, uid, subjectID)
	if err != nil {
		return nil, err
	}
	if level == access.LevelNone {
		return nil, myErrors.ErrForbidden
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, subject_id, title, position, created_at, updated_at
		FROM chapters WHERE subject_id=$1 ORDER BY position ASC, id ASC
	`, subjectID)
	if err != nil {
		return nil, fmt.Errorf("list chapters:\n%w", err)
	}
	defer rows.Close()
	var out []Chapter
	for rows.Next() {
		var ch Chapter
		if err := rows.Scan(&ch.ID, &ch.SubjectID, &ch.Title, &ch.Position, &ch.CreatedAt, &ch.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan chapter:\n%w", err)
		}
		out = append(out, ch)
	}
	return out, rows.Err()
}

// Update patches a chapter; caller must have edit rights on its subject.
func (s *Service) Update(ctx context.Context, uid, id int64, in UpdateInput) (*Chapter, error) {
	ch, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.ensureEdit(ctx, uid, ch.SubjectID); err != nil {
		return nil, err
	}
	title, pos, err := applyChapterPatch(ch, in)
	if err != nil {
		return nil, err
	}
	var out Chapter
	err = s.db.QueryRow(ctx, `
		UPDATE chapters SET title=$1, position=$2, updated_at=now()
		WHERE id=$3
		RETURNING id, subject_id, title, position, created_at, updated_at
	`, title, pos, id).Scan(
		&out.ID, &out.SubjectID, &out.Title, &out.Position, &out.CreatedAt, &out.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("update chapter:\n%w", err)
	}
	return &out, nil
}

// applyChapterPatch merges UpdateInput fields onto the existing Chapter values.
// Returns patched title and position, or ErrInvalidInput for an empty title.
func applyChapterPatch(ch *Chapter, in UpdateInput) (title string, pos int, err error) {
	title, pos = ch.Title, ch.Position
	if in.Title != nil {
		if *in.Title == "" {
			return "", 0, myErrors.ErrInvalidInput
		}
		title = *in.Title
	}
	if in.Position != nil {
		pos = *in.Position
	}
	return title, pos, nil
}

// Delete removes a chapter; caller must have edit rights on its subject.
func (s *Service) Delete(ctx context.Context, uid, id int64) error {
	ch, err := s.load(ctx, id)
	if err != nil {
		return err
	}
	if err := s.ensureEdit(ctx, uid, ch.SubjectID); err != nil {
		return err
	}
	if _, err := s.db.Exec(ctx, `DELETE FROM chapters WHERE id=$1`, id); err != nil {
		return fmt.Errorf("delete chapter:\n%w", err)
	}
	return nil
}

// Stats returns the per-difficulty card distribution for a chapter the caller can read.
func (s *Service) Stats(ctx context.Context, uid, chapterID int64) (*StatsResponse, error) {
	ch, err := s.load(ctx, chapterID)
	if err != nil {
		return nil, err
	}
	level, err := s.access.SubjectLevel(ctx, uid, ch.SubjectID)
	if err != nil {
		return nil, err
	}
	if level == access.LevelNone {
		return nil, myErrors.ErrForbidden
	}
	out := &StatsResponse{}
	err = s.db.QueryRow(ctx, `
		SELECT
			COUNT(*)                                   AS total,
			COUNT(*) FILTER (WHERE last_result = 2)    AS good,
			COUNT(*) FILTER (WHERE last_result = 1)    AS ok,
			COUNT(*) FILTER (WHERE last_result = 0)    AS bad,
			COUNT(*) FILTER (WHERE last_result = -1)   AS new_count
		FROM flashcards
		WHERE chapter_id = $1
	`, chapterID).Scan(&out.TotalCards, &out.GoodCount, &out.OkCount, &out.BadCount, &out.NewCount)
	if err != nil {
		return nil, fmt.Errorf("chapter stats:\n%w", err)
	}
	out.CardsStudied = out.TotalCards - out.NewCount
	if out.TotalCards > 0 {
		out.MasteryPercent = (float64(out.GoodCount) + float64(out.OkCount)*0.5) / float64(out.TotalCards)
	}
	return out, nil
}

// load fetches a chapter row by id, returning ErrNotFound when absent.
func (s *Service) load(ctx context.Context, id int64) (*Chapter, error) {
	var ch Chapter
	err := s.db.QueryRow(ctx, `
		SELECT id, subject_id, title, position, created_at, updated_at
		FROM chapters WHERE id=$1
	`, id).Scan(&ch.ID, &ch.SubjectID, &ch.Title, &ch.Position, &ch.CreatedAt, &ch.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, myErrors.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load chapter:\n%w", err)
	}
	return &ch, nil
}

// ensureEdit returns ErrForbidden unless the caller has edit rights on the subject.
func (s *Service) ensureEdit(ctx context.Context, uid, subjectID int64) error {
	level, err := s.access.SubjectLevel(ctx, uid, subjectID)
	if err != nil {
		return err
	}
	if !level.CanEdit() {
		return myErrors.ErrForbidden
	}
	return nil
}
