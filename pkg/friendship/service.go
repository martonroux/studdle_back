package friendship

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"studdle/backend/internal/myErrors"
)

// Service owns friendship requests and their lifecycle.
type Service struct {
	db *pgxpool.Pool // db is the shared pgx pool
}

// NewService constructs a Service.
func NewService(db *pgxpool.Pool) *Service {
	return &Service{db: db}
}

// Request creates a pending friend request from senderID to receiverID.
// It rejects self-friend requests and requests between users who are already
// friends (in either direction). If a prior request from senderID to
// receiverID was declined, that row is reopened to pending instead of
// attempting a fresh INSERT that would collide with the unique index.
func (s *Service) Request(ctx context.Context, senderID, receiverID int64) (*Friendship, error) {
	if senderID == receiverID {
		return nil, myErrors.ErrInvalidInput
	}
	if already, err := s.alreadyFriends(ctx, senderID, receiverID); err != nil {
		return nil, err
	} else if already {
		return nil, myErrors.ErrConflict
	}
	if reopened, err := s.reopenDeclined(ctx, senderID, receiverID); err != nil || reopened != nil {
		return reopened, err
	}
	return s.insertRequest(ctx, senderID, receiverID)
}

// alreadyFriends reports whether a and b have an accepted friendship, in either direction.
func (s *Service) alreadyFriends(ctx context.Context, a, b int64) (bool, error) {
	var n int
	err := s.db.QueryRow(ctx, `
		SELECT count(*) FROM friendships
		WHERE status='accepted' AND (
		  (sender_id=$1 AND receiver_id=$2) OR (sender_id=$2 AND receiver_id=$1)
		)
	`, a, b).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("check existing friendship:\n%w", err)
	}
	return n > 0, nil
}

// reopenDeclined resets a previously declined request from senderID to receiverID
// back to pending. Returns (nil, nil) when no declined row exists to reopen.
func (s *Service) reopenDeclined(ctx context.Context, senderID, receiverID int64) (*Friendship, error) {
	var f Friendship
	err := s.db.QueryRow(ctx, `
		UPDATE friendships SET status='pending', created_at=now(), updated_at=now()
		WHERE sender_id=$1 AND receiver_id=$2 AND status='declined'
		RETURNING id, sender_id, receiver_id, status, created_at, updated_at
	`, senderID, receiverID).Scan(
		&f.ID, &f.SenderID, &f.ReceiverID, &f.Status, &f.CreatedAt, &f.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reopen declined friendship:\n%w", err)
	}
	return &f, nil
}

// insertRequest performs the fresh INSERT for a brand-new friend request.
func (s *Service) insertRequest(ctx context.Context, senderID, receiverID int64) (*Friendship, error) {
	var f Friendship
	err := s.db.QueryRow(ctx, `
		INSERT INTO friendships (sender_id, receiver_id, status)
		VALUES ($1, $2, 'pending')
		RETURNING id, sender_id, receiver_id, status, created_at, updated_at
	`, senderID, receiverID).Scan(
		&f.ID, &f.SenderID, &f.ReceiverID, &f.Status, &f.CreatedAt, &f.UpdatedAt,
	)
	if err != nil {
		if sqlstate(err) == "23505" {
			return nil, myErrors.ErrConflict
		}
		return nil, fmt.Errorf("insert friendship:\n%w", err)
	}
	return &f, nil
}

// Accept transitions a pending friendship to accepted; only the receiver may accept.
func (s *Service) Accept(ctx context.Context, uid, id int64) (*Friendship, error) {
	return s.transition(ctx, uid, id, "accepted")
}

// Decline transitions a pending friendship to declined; only the receiver may decline.
func (s *Service) Decline(ctx context.Context, uid, id int64) (*Friendship, error) {
	return s.transition(ctx, uid, id, "declined")
}

// Unfriend deletes an accepted friendship row; either party may trigger it.
func (s *Service) Unfriend(ctx context.Context, uid, id int64) error {
	f, err := s.load(ctx, id)
	if err != nil {
		return err
	}
	if uid != f.SenderID && uid != f.ReceiverID {
		return myErrors.ErrForbidden
	}
	if f.Status != "accepted" {
		return myErrors.ErrInvalidInput
	}
	if _, err := s.db.Exec(ctx, `DELETE FROM friendships WHERE id=$1`, id); err != nil {
		return fmt.Errorf("delete friendship:\n%w", err)
	}
	return nil
}

// ListFriends returns every accepted friendship involving uid.
func (s *Service) ListFriends(ctx context.Context, uid int64) ([]Friendship, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, sender_id, receiver_id, status, created_at, updated_at
		FROM friendships
		WHERE status='accepted' AND (sender_id=$1 OR receiver_id=$1)
		ORDER BY updated_at DESC
	`, uid)
	if err != nil {
		return nil, fmt.Errorf("list friends:\n%w", err)
	}
	defer rows.Close()
	return scanAll(rows)
}

// ListPendingIncoming returns every pending request whose receiver is uid.
func (s *Service) ListPendingIncoming(ctx context.Context, uid int64) ([]Friendship, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, sender_id, receiver_id, status, created_at, updated_at
		FROM friendships
		WHERE status='pending' AND receiver_id=$1
		ORDER BY created_at DESC
	`, uid)
	if err != nil {
		return nil, fmt.Errorf("list pending:\n%w", err)
	}
	defer rows.Close()
	return scanAll(rows)
}

// transition moves a pending friendship into the target status for receiver uid.
func (s *Service) transition(ctx context.Context, uid, id int64, status string) (*Friendship, error) {
	f, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}
	if f.ReceiverID != uid {
		return nil, myErrors.ErrForbidden
	}
	if f.Status != "pending" {
		return nil, myErrors.ErrInvalidInput
	}
	var out Friendship
	err = s.db.QueryRow(ctx, `
		UPDATE friendships SET status=$1, updated_at=now()
		WHERE id=$2
		RETURNING id, sender_id, receiver_id, status, created_at, updated_at
	`, status, id).Scan(
		&out.ID, &out.SenderID, &out.ReceiverID, &out.Status, &out.CreatedAt, &out.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("update friendship:\n%w", err)
	}
	return &out, nil
}

// load fetches a friendship row by id, returning ErrNotFound when absent.
func (s *Service) load(ctx context.Context, id int64) (*Friendship, error) {
	var f Friendship
	err := s.db.QueryRow(ctx, `
		SELECT id, sender_id, receiver_id, status, created_at, updated_at
		FROM friendships WHERE id=$1
	`, id).Scan(&f.ID, &f.SenderID, &f.ReceiverID, &f.Status, &f.CreatedAt, &f.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, myErrors.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load friendship:\n%w", err)
	}
	return &f, nil
}

// scanAll materialises every row of a Friendship query into a slice.
func scanAll(rows pgx.Rows) ([]Friendship, error) {
	var out []Friendship
	for rows.Next() {
		var f Friendship
		if err := rows.Scan(&f.ID, &f.SenderID, &f.ReceiverID, &f.Status, &f.CreatedAt, &f.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan friendship:\n%w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// sqlstate returns the Postgres SQLSTATE on err, or "" if err is not a *pgconn.PgError.
func sqlstate(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}
