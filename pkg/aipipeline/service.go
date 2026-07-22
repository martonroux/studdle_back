package aipipeline

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"studdle/backend/internal/aiProvider"
	"studdle/backend/internal/myErrors"
	"studdle/backend/pkg/access"
)

// Service is the AI pipeline facade.
type Service struct {
	db       *pgxpool.Pool     // db is the shared pool
	provider aiProvider.Client // provider is the Anthropic (or noop) client
	access   *access.Service   // access answers entitlement questions
	limits   QuotaLimits       // limits bounds per-feature daily calls
	model    string            // model is the provider model identifier
}

// NewService constructs a Service. Methods are filled in across later tasks.
func NewService(db *pgxpool.Pool, provider aiProvider.Client, access *access.Service, limits QuotaLimits, model string) *Service {
	return &Service{db: db, provider: provider, access: access, limits: limits, model: model}
}

// NewServiceForTest constructs a minimal Service for tests that exercise the
// extraction or check primitives without the entitlement / quota plumbing.
// Production must use NewService.
func NewServiceForTest(db *pgxpool.Pool, provider aiProvider.Client, model string) *Service {
	return &Service{db: db, provider: provider, model: model}
}

// isNoRows returns true when err is pgx's "no rows" sentinel.
func isNoRows(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}

// SubjectMeta is a minimal subject projection for prompt templating.
type SubjectMeta struct {
	ID   int64  // ID is the subject id
	Name string // Name is the subject name used in prompt templates
}

// LookupSubject fetches the name of the subject for prompt templating.
// Requires uid to have at least read access to the subject; strangers get
// ErrForbidden.
func (s *Service) LookupSubject(ctx context.Context, uid, id int64) (*SubjectMeta, error) {
	var m SubjectMeta
	err := s.db.QueryRow(ctx, `SELECT id, name FROM subjects WHERE id = $1`, id).Scan(&m.ID, &m.Name)
	if err != nil {
		if isNoRows(err) {
			return nil, myErrors.ErrNotFound
		}
		return nil, fmt.Errorf("lookup subject:\n%w", err)
	}
	lvl, err := s.access.SubjectLevel(ctx, uid, id)
	if err != nil {
		return nil, err
	}
	if !lvl.CanRead() {
		return nil, myErrors.ErrForbidden
	}
	return &m, nil
}
