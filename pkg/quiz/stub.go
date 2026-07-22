package quiz

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"studdle/backend/internal/myErrors"
)

// Service is the quiz facade. Spec D will replace this.
type Service struct {
	db *pgxpool.Pool // db is the shared pool (unused in the stub)
}

// NewService constructs a stub Service.
func NewService(db *pgxpool.Pool) *Service {
	return &Service{db: db}
}

// Generate is a placeholder for creating a quiz from flashcards or a free-form prompt.
func (s *Service) Generate(ctx context.Context, uid int64, req any) (any, error) {
	return nil, myErrors.ErrNotImplemented
}

// Attempt is a placeholder for recording quiz answers.
func (s *Service) Attempt(ctx context.Context, uid, quizID int64, answers any) (any, error) {
	return nil, myErrors.ErrNotImplemented
}

// Share is a placeholder for quiz sharing.
func (s *Service) Share(ctx context.Context, uid, quizID int64) (string, error) {
	return "", myErrors.ErrNotImplemented
}
