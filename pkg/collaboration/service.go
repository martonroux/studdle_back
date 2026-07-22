package collaboration

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"studdle/backend/internal/myErrors"
	"studdle/backend/pkg/access"
)

// Service owns collaborator and invite-link persistence for subjects.
type Service struct {
	db     *pgxpool.Pool   // db is the shared pgx pool
	access *access.Service // access resolves the caller's effective subject permissions
}

// NewService constructs a Service with the given pool and access service.
func NewService(db *pgxpool.Pool, acc *access.Service) *Service {
	return &Service{db: db, access: acc}
}

// AddCollaborator grants userID the given role on subjectID. Owner only.
// Existing rows are upgraded to the new role (INSERT ... ON CONFLICT DO UPDATE).
func (s *Service) AddCollaborator(ctx context.Context, ownerID, subjectID, userID int64, role string) (*Collaborator, error) {
	if role != "viewer" && role != "editor" {
		return nil, myErrors.ErrInvalidInput
	}
	if err := s.ensureOwner(ctx, ownerID, subjectID); err != nil {
		return nil, err
	}
	var c Collaborator
	err := s.db.QueryRow(ctx, `
		INSERT INTO collaborators (subject_id, user_id, role)
		VALUES ($1, $2, $3)
		ON CONFLICT (subject_id, user_id) DO UPDATE SET role = EXCLUDED.role
		RETURNING id, subject_id, user_id, role, created_at
	`, subjectID, userID, role).Scan(
		&c.ID, &c.SubjectID, &c.UserID, &c.Role, &c.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert collaborator:\n%w", err)
	}
	return &c, nil
}

// RemoveCollaborator deletes the collaborator row for userID on subjectID. Owner only.
// Missing rows are ignored silently.
func (s *Service) RemoveCollaborator(ctx context.Context, ownerID, subjectID, userID int64) error {
	if err := s.ensureOwner(ctx, ownerID, subjectID); err != nil {
		return err
	}
	_, err := s.db.Exec(ctx,
		`DELETE FROM collaborators WHERE subject_id=$1 AND user_id=$2`,
		subjectID, userID)
	if err != nil {
		return fmt.Errorf("delete collaborator:\n%w", err)
	}
	return nil
}

// ListCollaborators returns every collaborator row on subjectID, oldest first. Owner only.
func (s *Service) ListCollaborators(ctx context.Context, ownerID, subjectID int64) ([]Collaborator, error) {
	if err := s.ensureOwner(ctx, ownerID, subjectID); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, subject_id, user_id, role, created_at
		FROM collaborators WHERE subject_id=$1 ORDER BY created_at ASC
	`, subjectID)
	if err != nil {
		return nil, fmt.Errorf("list collaborators:\n%w", err)
	}
	defer rows.Close()
	var out []Collaborator
	for rows.Next() {
		var c Collaborator
		if err := rows.Scan(&c.ID, &c.SubjectID, &c.UserID, &c.Role, &c.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan collaborator:\n%w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CreateInvite mints a new invite link for the given subject. Owner only.
// TTLHours > 0 sets an expiry; zero or negative means the invite never expires.
func (s *Service) CreateInvite(ctx context.Context, ownerID int64, in CreateInviteInput) (*InviteLink, error) {
	if in.Role != "viewer" && in.Role != "editor" {
		return nil, myErrors.ErrInvalidInput
	}
	if err := s.ensureOwner(ctx, ownerID, in.SubjectID); err != nil {
		return nil, err
	}
	token, err := randomToken(24)
	if err != nil {
		return nil, err
	}
	var expiresAt *time.Time
	if in.TTLHours > 0 {
		exp := time.Now().Add(time.Duration(in.TTLHours) * time.Hour)
		expiresAt = &exp
	}
	return s.insertInvite(ctx, ownerID, in.SubjectID, in.Role, token, expiresAt)
}

// RedeemInvite consumes an invite token and creates (or upgrades) a collaborator row for uid.
// Unknown or revoked tokens return ErrNotFound; expired tokens return ErrInvalidInput.
// A redeemer who already owns the subject is rejected with ErrConflict instead
// of gaining a pointless collaborator row — owners already have full access.
func (s *Service) RedeemInvite(ctx context.Context, uid int64, token string) (*Collaborator, error) {
	subjectID, role, err := s.loadRedeemableInvite(ctx, token)
	if err != nil {
		return nil, err
	}
	isOwner, err := s.isSubjectOwner(ctx, uid, subjectID)
	if err != nil {
		return nil, err
	}
	if isOwner {
		return nil, myErrors.ErrConflict
	}
	return s.upsertCollaborator(ctx, subjectID, uid, role)
}

// RevokeInvite marks an invite link as revoked so it can no longer be
// redeemed. Only the subject owner may revoke their own invite links.
func (s *Service) RevokeInvite(ctx context.Context, ownerID int64, token string) error {
	subjectID, err := s.inviteSubject(ctx, token)
	if err != nil {
		return err
	}
	if err := s.ensureOwner(ctx, ownerID, subjectID); err != nil {
		return err
	}
	if _, err := s.db.Exec(ctx, `UPDATE invite_links SET revoked_at = now() WHERE token = $1`, token); err != nil {
		return fmt.Errorf("revoke invite:\n%w", err)
	}
	return nil
}

// loadRedeemableInvite loads an invite token's subject and role, validating
// that it is neither unknown, revoked (both ErrNotFound), nor expired (ErrInvalidInput).
func (s *Service) loadRedeemableInvite(ctx context.Context, token string) (subjectID int64, role string, err error) {
	var expiresAt, revokedAt *time.Time
	err = s.db.QueryRow(ctx, `
		SELECT subject_id, role, expires_at, revoked_at
		FROM invite_links WHERE token=$1
	`, token).Scan(&subjectID, &role, &expiresAt, &revokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, "", myErrors.ErrNotFound
	}
	if err != nil {
		return 0, "", fmt.Errorf("load invite:\n%w", err)
	}
	if revokedAt != nil {
		return 0, "", myErrors.ErrNotFound
	}
	if expiresAt != nil && time.Now().After(*expiresAt) {
		return 0, "", myErrors.ErrInvalidInput
	}
	return subjectID, role, nil
}

// inviteSubject returns the subject_id an invite token belongs to, or ErrNotFound.
func (s *Service) inviteSubject(ctx context.Context, token string) (int64, error) {
	var subjectID int64
	err := s.db.QueryRow(ctx, `SELECT subject_id FROM invite_links WHERE token=$1`, token).Scan(&subjectID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, myErrors.ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("load invite subject:\n%w", err)
	}
	return subjectID, nil
}

// isSubjectOwner reports whether uid holds owner-level access on subjectID.
func (s *Service) isSubjectOwner(ctx context.Context, uid, subjectID int64) (bool, error) {
	level, err := s.access.SubjectLevel(ctx, uid, subjectID)
	if err != nil {
		return false, err
	}
	return level.CanManage(), nil
}

// insertInvite persists a freshly minted invite link and returns the row.
func (s *Service) insertInvite(ctx context.Context, ownerID, subjectID int64, role, token string, expiresAt *time.Time) (*InviteLink, error) {
	var inv InviteLink
	err := s.db.QueryRow(ctx, `
		INSERT INTO invite_links (subject_id, token, role, created_by, expires_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING token, subject_id, role, expires_at, created_at
	`, subjectID, token, role, ownerID, expiresAt).Scan(
		&inv.Token, &inv.SubjectID, &inv.Role, &inv.ExpiresAt, &inv.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert invite:\n%w", err)
	}
	return &inv, nil
}

// upsertCollaborator creates or upgrades a collaborator row for the redeeming user.
func (s *Service) upsertCollaborator(ctx context.Context, subjectID, uid int64, role string) (*Collaborator, error) {
	var c Collaborator
	err := s.db.QueryRow(ctx, `
		INSERT INTO collaborators (subject_id, user_id, role)
		VALUES ($1, $2, $3)
		ON CONFLICT (subject_id, user_id) DO UPDATE SET role = EXCLUDED.role
		RETURNING id, subject_id, user_id, role, created_at
	`, subjectID, uid, role).Scan(
		&c.ID, &c.SubjectID, &c.UserID, &c.Role, &c.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("upsert collaborator:\n%w", err)
	}
	return &c, nil
}

// ensureOwner returns ErrForbidden unless uid owns subjectID.
func (s *Service) ensureOwner(ctx context.Context, uid, subjectID int64) error {
	level, err := s.access.SubjectLevel(ctx, uid, subjectID)
	if err != nil {
		return err
	}
	if !level.CanManage() {
		return myErrors.ErrForbidden
	}
	return nil
}

// randomToken returns a hex-encoded string backed by n bytes of crypto/rand data.
func randomToken(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("random token:\n%w", err)
	}
	return hex.EncodeToString(buf), nil
}
