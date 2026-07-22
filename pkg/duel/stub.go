package duel

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"studdle/backend/internal/duelHub"
	"studdle/backend/internal/myErrors"
)

// Service is the duel facade. Spec E will replace this.
type Service struct {
	db  *pgxpool.Pool // db is the shared pool (unused in the stub)
	hub *duelHub.Hub  // hub is the (future) live-duel WebSocket hub
}

// NewService constructs a stub Service.
func NewService(db *pgxpool.Pool, hub *duelHub.Hub) *Service {
	return &Service{db: db, hub: hub}
}

// Invite is a placeholder for inviting a friend to a duel.
func (s *Service) Invite(ctx context.Context, challenger, opponent, subjectID int64) (any, error) {
	return nil, myErrors.ErrNotImplemented
}

// Accept is a placeholder for invitee acceptance + quiz generation.
func (s *Service) Accept(ctx context.Context, uid, duelID int64) (any, error) {
	return nil, myErrors.ErrNotImplemented
}

// Play is a placeholder for the turn-taking duel flow (WebSocket-driven).
func (s *Service) Play(ctx context.Context, uid, duelID int64) (any, error) {
	return nil, myErrors.ErrNotImplemented
}
