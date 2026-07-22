package preferences

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"studdle/backend/internal/myErrors"
)

// Prefs is the per-user preferences blob.
type Prefs struct {
	UserID            int64 `json:"user_id"`             // UserID is the owner
	AIPlanningEnabled bool  `json:"ai_planning_enabled"` // AIPlanningEnabled toggles plan generation on
	DailyGoalTarget   int   `json:"daily_goal_target"`   // DailyGoalTarget is the per-day card target
}

// UpdateInput patches preferences.
type UpdateInput struct {
	AIPlanningEnabled *bool `json:"ai_planning_enabled"` // AIPlanningEnabled when non-nil updates the flag
	DailyGoalTarget   *int  `json:"daily_goal_target"`   // DailyGoalTarget when non-nil updates the goal
}

// Service owns the preferences blob.
type Service struct {
	db *pgxpool.Pool // db is the shared pool
}

// NewService constructs a Service.
func NewService(db *pgxpool.Pool) *Service {
	return &Service{db: db}
}

// Get returns the user's preferences, creating a default row if missing.
func (s *Service) Get(ctx context.Context, uid int64) (*Prefs, error) {
	p, err := s.load(ctx, uid)
	if errors.Is(err, myErrors.ErrNotFound) {
		return s.ensureDefault(ctx, uid)
	}
	return p, err
}

// Update patches preferences. Creates the row if missing.
func (s *Service) Update(ctx context.Context, uid int64, in UpdateInput) (*Prefs, error) {
	if _, err := s.ensureDefault(ctx, uid); err != nil {
		return nil, err
	}
	current, err := s.load(ctx, uid)
	if err != nil {
		return nil, err
	}
	ai, goal, err := patchFields(current, in)
	if err != nil {
		return nil, err
	}
	return s.writeUpdate(ctx, uid, ai, goal)
}

// patchFields applies the non-nil fields of in onto current and validates goal range.
func patchFields(current *Prefs, in UpdateInput) (bool, int, error) {
	ai := current.AIPlanningEnabled
	goal := current.DailyGoalTarget
	if in.AIPlanningEnabled != nil {
		ai = *in.AIPlanningEnabled
	}
	if in.DailyGoalTarget != nil {
		if *in.DailyGoalTarget < 0 || *in.DailyGoalTarget > 1000 {
			return false, 0, myErrors.ErrInvalidInput
		}
		goal = *in.DailyGoalTarget
	}
	return ai, goal, nil
}

// writeUpdate persists the given (ai, goal) pair for uid and returns the refreshed row.
func (s *Service) writeUpdate(ctx context.Context, uid int64, ai bool, goal int) (*Prefs, error) {
	var out Prefs
	err := s.db.QueryRow(ctx, `
		UPDATE preferences SET ai_planning_enabled=$1, daily_goal_target=$2, updated_at=now()
		WHERE user_id=$3
		RETURNING user_id, ai_planning_enabled, daily_goal_target
	`, ai, goal, uid).Scan(&out.UserID, &out.AIPlanningEnabled, &out.DailyGoalTarget)
	if err != nil {
		return nil, fmt.Errorf("update preferences:\n%w", err)
	}
	return &out, nil
}

// ensureDefault inserts a default preferences row for uid if missing and returns the row.
// It uses INSERT ... ON CONFLICT DO UPDATE SET user_id=EXCLUDED.user_id so RETURNING always fires.
func (s *Service) ensureDefault(ctx context.Context, uid int64) (*Prefs, error) {
	var p Prefs
	err := s.db.QueryRow(ctx, `
		INSERT INTO preferences (user_id) VALUES ($1)
		ON CONFLICT (user_id) DO UPDATE SET user_id=EXCLUDED.user_id
		RETURNING user_id, ai_planning_enabled, daily_goal_target
	`, uid).Scan(&p.UserID, &p.AIPlanningEnabled, &p.DailyGoalTarget)
	if err != nil {
		return nil, fmt.Errorf("ensure default preferences:\n%w", err)
	}
	return &p, nil
}

// load reads the existing preferences row for uid, returning ErrNotFound when absent.
func (s *Service) load(ctx context.Context, uid int64) (*Prefs, error) {
	var p Prefs
	err := s.db.QueryRow(ctx, `
		SELECT user_id, ai_planning_enabled, daily_goal_target
		FROM preferences WHERE user_id=$1
	`, uid).Scan(&p.UserID, &p.AIPlanningEnabled, &p.DailyGoalTarget)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, myErrors.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load preferences:\n%w", err)
	}
	return &p, nil
}
