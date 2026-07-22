package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"studdle/backend/internal/myErrors"
)

// CompGrant captures the admin form values for POST /admin/comp-subscription.
type CompGrant struct {
	UserID      int64      // UserID is the recipient
	ExpiresAt   *time.Time // ExpiresAt is the comp expiry (nil = indefinite)
	Reason      string     // Reason is the operator-supplied note
	ActorUserID int64      // ActorUserID is the admin who granted the comp
}

// CompRevoke captures the admin form values for DELETE /admin/comp-subscription.
type CompRevoke struct {
	UserID      int64  // UserID is the target user
	Reason      string // Reason is the operator-supplied note
	ActorUserID int64  // ActorUserID is the admin who revoked
}

// GrantCompWithExpiry upserts a comp row with an optional expiry and writes
// an admin_comp_granted audit row. This is the structured Spec C admin path.
// Returns an error wrapping myErrors.ErrNotFound when g.UserID does not
// reference an existing user.
func (s *Service) GrantCompWithExpiry(ctx context.Context, g CompGrant) error {
	if err := s.upsertCompRow(ctx, g.UserID, g.ExpiresAt); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{
		"reason":    g.Reason,
		"actor":     g.ActorUserID,
		"expiresAt": g.ExpiresAt,
	})
	if _, err := s.db.Exec(ctx, sqlInsertEvent,
		"", g.UserID, "admin_comp_granted", false, payload,
	); err != nil {
		return fmt.Errorf("insert audit:\n%w", err)
	}
	return nil
}

// upsertCompRow inserts or updates the user_subscriptions comp row for uid.
// Translates a foreign-key violation (uid has no matching users row) into a
// clean myErrors.ErrNotFound instead of leaking the raw driver error.
func (s *Service) upsertCompRow(ctx context.Context, uid int64, expiresAt *time.Time) error {
	const upsert = `
        INSERT INTO user_subscriptions (user_id, plan, status, current_period_end, created_at, updated_at)
        VALUES ($1, 'comp', 'comped', $2, now(), now())
        ON CONFLICT (user_id) DO UPDATE SET
            plan = 'comp',
            status = 'comped',
            current_period_end = EXCLUDED.current_period_end,
            updated_at = now()
    `
	_, err := s.db.Exec(ctx, upsert, uid, expiresAt)
	if err == nil {
		return nil
	}
	if isForeignKeyViolation(err) {
		return &myErrors.AppError{Code: "user_not_found", Message: "user not found", Field: "user_id", Wrapped: myErrors.ErrNotFound}
	}
	return fmt.Errorf("upsert comp:\n%w", err)
}

// isForeignKeyViolation reports whether err is a Postgres foreign-key
// constraint violation (SQLSTATE 23503), e.g. an upsert targeting a
// user_id with no matching row in users.
func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

// RevokeComp sets the user's row to status='canceled' and writes an
// admin_comp_revoked audit row.
func (s *Service) RevokeComp(ctx context.Context, r CompRevoke) error {
	const upd = `
        UPDATE user_subscriptions
        SET status = 'canceled', updated_at = now()
        WHERE user_id = $1 AND plan = 'comp'
    `
	if _, err := s.db.Exec(ctx, upd, r.UserID); err != nil {
		return fmt.Errorf("revoke comp:\n%w", err)
	}
	payload, _ := json.Marshal(map[string]any{
		"reason": r.Reason,
		"actor":  r.ActorUserID,
	})
	if _, err := s.db.Exec(ctx, sqlInsertEvent,
		"", r.UserID, "admin_comp_revoked", false, payload,
	); err != nil {
		return fmt.Errorf("insert audit:\n%w", err)
	}
	return nil
}
