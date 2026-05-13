package billing

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ErrNoCustomer is returned when the user has no stripe_customer_id (never checked out).
var ErrNoCustomer = errors.New("user has no stripe customer")

// CreatePortalSession returns the URL of a Stripe Customer Portal session.
// Returns ErrNoCustomer if the user has never checked out or has no customer row.
func (s *Service) CreatePortalSession(ctx context.Context, uid int64, returnURL string) (string, error) {
	var cust string
	err := s.db.QueryRow(ctx, sqlGetCustomerID, uid).Scan(&cust)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNoCustomer
	}
	if err != nil {
		return "", fmt.Errorf("lookup customer:\n%w", err)
	}
	if cust == "" {
		return "", ErrNoCustomer
	}
	url, err := s.provider.CreatePortal(ctx, cust, returnURL)
	if err != nil {
		return "", fmt.Errorf("create portal:\n%w", err)
	}
	return url, nil
}
