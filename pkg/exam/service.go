package exam

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"studdle/backend/internal/myErrors"
	"studdle/backend/pkg/access"
)

// maxActiveExamsPerUser caps how many future exams a user can have at once.
const maxActiveExamsPerUser = 10

// Service owns exam CRUD with ownership + access checks.
type Service struct {
	db     *pgxpool.Pool   // db is the shared pool
	access *access.Service // access resolves subject permissions
}

// NewService constructs the exam service.
func NewService(db *pgxpool.Pool, acc *access.Service) *Service {
	return &Service{db: db, access: acc}
}

// Create inserts a new exam after enforcing future-date, max-active, and viewer-or-higher access.
func (s *Service) Create(ctx context.Context, uid int64, in Input) (*Exam, error) {
	if err := validateInputForCreate(in); err != nil {
		return nil, err
	}
	if err := s.assertSubjectAccess(ctx, uid, in.SubjectID); err != nil {
		return nil, err
	}
	if err := s.assertActiveCap(ctx, uid); err != nil {
		return nil, err
	}
	if err := s.assertImageOwnership(ctx, uid, in.AnnalesImageID); err != nil {
		return nil, err
	}
	return s.insert(ctx, uid, in)
}

// Get returns an exam by id, scoped to the calling user.
func (s *Service) Get(ctx context.Context, uid, id int64) (*Exam, error) {
	e, err := s.byID(ctx, id)
	if err != nil {
		return nil, err
	}
	if e.UserID != uid {
		return nil, myErrors.ErrForbidden
	}
	return e, nil
}

// List returns the user's exams ordered by ascending exam date.
// Past exams are included so the UI can show recent history.
func (s *Service) List(ctx context.Context, uid int64) ([]Exam, error) {
	rows, err := s.db.Query(ctx, `
        SELECT id, user_id, subject_id, title, COALESCE(notes, '') AS notes, date, annales_image_id, created_at, updated_at
        FROM exams WHERE user_id = $1 ORDER BY date ASC, id ASC
    `, uid)
	if err != nil {
		return nil, fmt.Errorf("list exams:\n%w", err)
	}
	defer rows.Close()
	var out []Exam
	for rows.Next() {
		e, err := scanExam(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *e)
	}
	return out, rows.Err()
}

// Update overwrites the editable fields on an existing exam owned by uid.
// SubjectID may not change post-create — pass the existing value.
func (s *Service) Update(ctx context.Context, uid, id int64, in Input) (*Exam, error) {
	current, err := s.Get(ctx, uid, id)
	if err != nil {
		return nil, err
	}
	if in.SubjectID != current.SubjectID {
		return nil, &myErrors.AppError{
			Code: "validation", Message: "subject_id cannot be changed after creation",
			Wrapped: myErrors.ErrValidation, Field: "subject_id",
		}
	}
	if err := validateInputForUpdate(in); err != nil {
		return nil, err
	}
	if err := s.assertImageOwnership(ctx, uid, in.AnnalesImageID); err != nil {
		return nil, err
	}
	return s.update(ctx, uid, id, in)
}

// Delete removes the exam (cascading any plan + progress).
func (s *Service) Delete(ctx context.Context, uid, id int64) error {
	if _, err := s.Get(ctx, uid, id); err != nil {
		return err
	}
	if _, err := s.db.Exec(ctx, `DELETE FROM exams WHERE id = $1`, id); err != nil {
		return fmt.Errorf("delete exam:\n%w", err)
	}
	return nil
}

// validateInputForCreate enforces the rules that are only relevant at insert time.
func validateInputForCreate(in Input) error {
	if err := validateInputForUpdate(in); err != nil {
		return err
	}
	if !in.ExamDate.IsZero() && in.ExamDate.Before(today()) {
		return &myErrors.AppError{
			Code: "exam_date_past", Message: "exam date must be today or later",
			Wrapped: myErrors.ErrValidation, Field: "examDate",
		}
	}
	return nil
}

// validateInputForUpdate enforces the rules that always apply to a request body.
func validateInputForUpdate(in Input) error {
	if in.SubjectID <= 0 {
		return &myErrors.AppError{
			Code: "validation", Message: "subject_id is required",
			Wrapped: myErrors.ErrValidation, Field: "subject_id",
		}
	}
	if in.Title == "" {
		return &myErrors.AppError{
			Code: "validation", Message: "title is required",
			Wrapped: myErrors.ErrValidation, Field: "title",
		}
	}
	if in.ExamDate.IsZero() {
		return &myErrors.AppError{
			Code: "validation", Message: "examDate is required",
			Wrapped: myErrors.ErrValidation, Field: "examDate",
		}
	}
	return nil
}

// assertSubjectAccess fails when the user can't even read the target subject.
func (s *Service) assertSubjectAccess(ctx context.Context, uid, subjectID int64) error {
	lvl, err := s.access.SubjectLevel(ctx, uid, subjectID)
	if err != nil {
		return err
	}
	if !lvl.CanRead() {
		return myErrors.ErrForbidden
	}
	return nil
}

// assertActiveCap rejects new exams once the user is at the active-exam ceiling.
func (s *Service) assertActiveCap(ctx context.Context, uid int64) error {
	var n int
	err := s.db.QueryRow(ctx,
		`SELECT count(*) FROM exams WHERE user_id = $1 AND date >= current_date`, uid).Scan(&n)
	if err != nil {
		return fmt.Errorf("count active exams:\n%w", err)
	}
	if n >= maxActiveExamsPerUser {
		return &myErrors.AppError{
			Code:    "active_exams_cap",
			Message: fmt.Sprintf("max %d active exams per user", maxActiveExamsPerUser),
			Wrapped: myErrors.ErrConflict,
		}
	}
	return nil
}

// assertImageOwnership rejects an annales reference that doesn't belong to uid.
// Nil pointer or empty value means "no annales attached" — accepted.
func (s *Service) assertImageOwnership(ctx context.Context, uid int64, imageID *string) error {
	if imageID == nil || *imageID == "" {
		return nil
	}
	var ownerID int64
	err := s.db.QueryRow(ctx, `SELECT owner_id FROM images WHERE id = $1`, *imageID).Scan(&ownerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return &myErrors.AppError{
			Code: "validation", Message: "annales image not found",
			Wrapped: myErrors.ErrNotFound, Field: "annales_image_id",
		}
	}
	if err != nil {
		return fmt.Errorf("load annales owner:\n%w", err)
	}
	if ownerID != uid {
		return myErrors.ErrForbidden
	}
	return nil
}

// insert performs the actual exam INSERT.
func (s *Service) insert(ctx context.Context, uid int64, in Input) (*Exam, error) {
	row := s.db.QueryRow(ctx, `
        INSERT INTO exams (user_id, subject_id, title, notes, date, annales_image_id)
        VALUES ($1, $2, $3, $4, $5, $6)
        RETURNING id, user_id, subject_id, title, notes, date, annales_image_id, created_at, updated_at
    `, uid, in.SubjectID, in.Title, in.Notes, in.ExamDate, in.AnnalesImageID)
	return scanExam(row)
}

// update performs the exam UPDATE for editable fields.
func (s *Service) update(ctx context.Context, uid, id int64, in Input) (*Exam, error) {
	row := s.db.QueryRow(ctx, `
        UPDATE exams
        SET title = $3, notes = $4, date = $5, annales_image_id = $6, updated_at = now()
        WHERE id = $1 AND user_id = $2
        RETURNING id, user_id, subject_id, title, notes, date, annales_image_id, created_at, updated_at
    `, id, uid, in.Title, in.Notes, in.ExamDate, in.AnnalesImageID)
	return scanExam(row)
}

// byID loads an exam by id without an ownership filter; callers must verify.
func (s *Service) byID(ctx context.Context, id int64) (*Exam, error) {
	row := s.db.QueryRow(ctx, `
        SELECT id, user_id, subject_id, title, COALESCE(notes, '') AS notes, date, annales_image_id, created_at, updated_at
        FROM exams WHERE id = $1
    `, id)
	e, err := scanExam(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, myErrors.ErrNotFound
		}
		return nil, err
	}
	return e, nil
}

// rowScanner abstracts pgx.Row and pgx.Rows so scanExam can serve both.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanExam reads a single exam row from a Scan-able row source.
func scanExam(r rowScanner) (*Exam, error) {
	var e Exam
	err := r.Scan(&e.ID, &e.UserID, &e.SubjectID, &e.Title, &e.Notes, &e.ExamDate, &e.AnnalesImageID, &e.CreatedAt, &e.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("scan exam:\n%w", err)
	}
	return &e, nil
}

// today returns midnight UTC of the current day so date comparisons ignore wall-clock minutes.
func today() time.Time {
	now := time.Now().UTC()
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
}
