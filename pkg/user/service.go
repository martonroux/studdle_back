package user

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	jwtsigner "studdle/backend/internal/jwt"
	"studdle/backend/internal/myErrors"
	"studdle/backend/pkg/gamification"
)

// Service owns user register, login, profile-picture, and stats.
type Service struct {
	db     *pgxpool.Pool     // db is the shared PostgreSQL connection pool
	signer *jwtsigner.Signer // signer issues and verifies JWTs
}

// NewService constructs the user service.
func NewService(db *pgxpool.Pool, signer *jwtsigner.Signer) *Service {
	return &Service{db: db, signer: signer}
}

// Register creates a new user and returns a signed JWT.
func (s *Service) Register(ctx context.Context, in RegisterInput) (string, int64, error) {
	if err := validateRegister(in); err != nil {
		return "", 0, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	if err != nil {
		return "", 0, fmt.Errorf("bcrypt hash:\n%w", err)
	}
	var id int64
	err = s.db.QueryRow(ctx, qInsertUser, in.Username, strings.ToLower(in.Email), string(hash)).
		Scan(&id, new(any))
	if err != nil {
		if sqlstate(err) == "23505" {
			return "", 0, fmt.Errorf("username or email taken:\n%w", myErrors.ErrConflict)
		}
		return "", 0, fmt.Errorf("insert user:\n%w", err)
	}
	tok, err := s.signer.Sign(jwtsigner.Claims{UID: id, EmailVerified: false, IsAdmin: false})
	if err != nil {
		return "", 0, err
	}
	return tok, id, nil
}

// Login authenticates and returns a signed JWT.
func (s *Service) Login(ctx context.Context, in LoginInput) (string, error) {
	row := s.db.QueryRow(ctx, qFindByIdentifier, in.Identifier)
	var (
		id       int64
		username string
		email    string
		hash     string
		verified bool
		admin    bool
		created  any
	)
	err := row.Scan(&id, &username, &email, &hash, &verified, &admin, &created)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("no such user:\n%w", myErrors.ErrNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("find user:\n%w", err)
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.Password)) != nil {
		return "", fmt.Errorf("bad password:\n%w", myErrors.ErrUnauthenticated)
	}
	return s.signer.Sign(jwtsigner.Claims{UID: id, EmailVerified: verified, IsAdmin: admin})
}

// ByID returns the user row.
func (s *Service) ByID(ctx context.Context, uid int64) (*User, error) {
	u := &User{}
	err := s.db.QueryRow(ctx, qFindByID, uid).
		Scan(&u.ID, &u.Username, &u.Email, &u.EmailVerified, &u.IsAdmin, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("user %d:\n%w", uid, myErrors.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("load user:\n%w", err)
	}
	return u, nil
}

// SetProfilePicture sets the user's profile_picture_image_id (image must be owned by user).
func (s *Service) SetProfilePicture(ctx context.Context, uid int64, imageID string) error {
	var ownerID int64
	err := s.db.QueryRow(ctx, `SELECT owner_id FROM images WHERE id = $1`, imageID).Scan(&ownerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("image %s:\n%w", imageID, myErrors.ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("load image:\n%w", err)
	}
	if ownerID != uid {
		return fmt.Errorf("image not owned by user:\n%w", myErrors.ErrForbidden)
	}
	if _, err := s.db.Exec(ctx, qSetProfilePicture, uid, imageID); err != nil {
		return fmt.Errorf("set profile picture:\n%w", err)
	}
	return nil
}

// Stats computes aggregate mastery for the user's owned subjects.
func (s *Service) Stats(ctx context.Context, uid int64) (*UserStatsResponse, error) {
	out := &UserStatsResponse{}
	err := s.db.QueryRow(ctx, qStats, uid).
		Scan(&out.TotalCards, &out.GoodCount, &out.OkCount, &out.BadCount, &out.NewCount)
	if err != nil {
		return nil, fmt.Errorf("stats query:\n%w", err)
	}
	out.CardsStudied = out.TotalCards - out.NewCount
	if out.TotalCards > 0 {
		out.MasteryPercent = (float64(out.GoodCount) + float64(out.OkCount)*0.5) / float64(out.TotalCards)
	}
	if err := s.db.QueryRow(ctx, qAchievementProgress, uid).
		Scan(&out.BadgesUnlocked); err != nil {
		return nil, fmt.Errorf("achievement progress:\n%w", err)
	}
	out.BadgesTotal = len(gamification.AllAchievements())
	return out, nil
}

func validateRegister(in RegisterInput) error {
	if in.Username == "" || in.Email == "" || in.Password == "" {
		return fmt.Errorf("username, email, and password are required:\n%w", myErrors.ErrValidation)
	}
	if !strings.Contains(in.Email, "@") {
		return fmt.Errorf("invalid email:\n%w", myErrors.ErrValidation)
	}
	if len(in.Password) < 8 {
		return fmt.Errorf("password must be at least 8 chars:\n%w", myErrors.ErrValidation)
	}
	return nil
}

// sqlstate returns the Postgres SQLSTATE on err, or "" if err is not a *pgconn.PgError.
func sqlstate(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}
