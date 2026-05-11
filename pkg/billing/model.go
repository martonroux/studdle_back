package billing

import "time"

// Status is the local subscription status. Mirrors the schema CHECK set.
type Status string

const (
	StatusTrialing          Status = "trialing"
	StatusActive            Status = "active"
	StatusPastDue           Status = "past_due"
	StatusPaused            Status = "paused"
	StatusCanceled          Status = "canceled"
	StatusIncomplete        Status = "incomplete"
	StatusIncompleteExpired Status = "incomplete_expired"
	StatusComped            Status = "comped"
)

// Plan identifies which Stripe price (or 'comp') the row is anchored to.
type Plan string

const (
	PlanProMonthly Plan = "pro_monthly"
	PlanProAnnual  Plan = "pro_annual"
	PlanComp       Plan = "comp"
)

// Subscription is the read-side projection returned by GetSubscription.
type Subscription struct {
	Status            Status     // Status is the current local status
	Plan              Plan       // Plan is the row's plan column
	CurrentPeriodEnd  *time.Time // CurrentPeriodEnd is the renewal/expiry boundary
	TrialEnd          *time.Time // TrialEnd is the trial expiry (nil after conversion)
	CancelAtPeriodEnd bool       // CancelAtPeriodEnd is true after a user cancels mid-period
	StripeCustomerID  string     // StripeCustomerID is the Stripe Customer (empty for comped)
	StripeSubID       string     // StripeSubID is the Stripe Subscription id (empty for comped)
}

// IsActive returns true when the subscription grants AI access.
// Mirrors user_has_ai_access() exactly.
func (s Subscription) IsActive(now time.Time) bool {
	switch s.Status {
	case StatusActive, StatusTrialing, StatusComped:
	default:
		return false
	}
	if s.CurrentPeriodEnd == nil {
		return true
	}
	return s.CurrentPeriodEnd.After(now)
}

// StateUpdate is the payload applyStripeState writes.
// Source-agnostic — populated by webhook, refresh, and cron alike.
type StateUpdate struct {
	UserID            int64      // UserID identifies the target row
	StripeCustomerID  string     // StripeCustomerID is the Stripe Customer id
	StripeSubID       string     // StripeSubID is the Stripe Subscription id
	Status            Status     // Status is the new local status
	Plan              Plan       // Plan is the resolved plan from price ID
	CurrentPeriodEnd  *time.Time // CurrentPeriodEnd from Stripe
	TrialEnd          *time.Time // TrialEnd from Stripe (nil after conversion)
	CancelAtPeriodEnd bool       // CancelAtPeriodEnd from Stripe
	PausedAt          *time.Time // PausedAt is set when Status=paused, NULL otherwise
}
