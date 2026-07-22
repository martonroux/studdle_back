package subjectsub

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

// Subscription represents a user's subscription to a subject.
type Subscription struct {
	UserID    int64     `json:"user_id"`    // UserID is the subscriber's user id
	SubjectID int64     `json:"subject_id"` // SubjectID is the subscribed subject id
	CreatedAt time.Time `json:"created_at"` // CreatedAt is when the subscription was created
}

// Service owns subject-subscription persistence and enforces visibility.
type Service struct {
	db     *pgxpool.Pool   // db is the shared pgx pool
	access *access.Service // access resolves per-subject permission levels
}

// NewService constructs a Service with the given pool and access service.
func NewService(db *pgxpool.Pool, acc *access.Service) *Service {
	return &Service{db: db, access: acc}
}

// Subscribe records a subscription from uid to subjectID if the caller can read it.
// Returns ErrForbidden when the subject is not visible to the caller.
// Uses INSERT ... ON CONFLICT DO NOTHING, so duplicate calls are idempotent.
func (s *Service) Subscribe(ctx context.Context, uid, subjectID int64) error {
	level, err := s.access.SubjectLevel(ctx, uid, subjectID)
	if err != nil {
		return err
	}
	if level == access.LevelNone {
		return myErrors.ErrForbidden
	}
	_, err = s.db.Exec(ctx, `
		INSERT INTO subject_subscriptions (user_id, subject_id)
		VALUES ($1, $2)
		ON CONFLICT (user_id, subject_id) DO NOTHING
	`, uid, subjectID)
	if err != nil {
		return fmt.Errorf("insert subject subscription:\n%w", err)
	}
	return nil
}

// Unsubscribe removes uid's subscription to subjectID.
// Missing rows are ignored silently (idempotent).
func (s *Service) Unsubscribe(ctx context.Context, uid, subjectID int64) error {
	_, err := s.db.Exec(ctx,
		`DELETE FROM subject_subscriptions WHERE user_id = $1 AND subject_id = $2`,
		uid, subjectID)
	if err != nil {
		return fmt.Errorf("delete subject subscription:\n%w", err)
	}
	return nil
}

// ListSubscribed returns the subject ids the user is currently subscribed to.
func (s *Service) ListSubscribed(ctx context.Context, uid int64) ([]int64, error) {
	rows, err := s.db.Query(ctx,
		`SELECT subject_id FROM subject_subscriptions WHERE user_id = $1 ORDER BY created_at DESC`,
		uid)
	if err != nil {
		return nil, fmt.Errorf("list subject subscriptions:\n%w", err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan subject subscription:\n%w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// IsSubscribed reports whether uid is subscribed to subjectID.
func (s *Service) IsSubscribed(ctx context.Context, uid, subjectID int64) (bool, error) {
	var one int
	err := s.db.QueryRow(ctx,
		`SELECT 1 FROM subject_subscriptions WHERE user_id = $1 AND subject_id = $2`,
		uid, subjectID).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check subject subscription:\n%w", err)
	}
	return true, nil
}
