package billing

// sqlUpsertSubscription writes the full row from a StateUpdate.
// All comp-related rows skip this path (comp.go owns those).
const sqlUpsertSubscription = `
INSERT INTO user_subscriptions (
    user_id, stripe_customer_id, stripe_sub_id, status, plan,
    current_period_end, trial_end, cancel_at_period_end, paused_at,
    created_at, updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now(), now())
ON CONFLICT (user_id) DO UPDATE SET
    stripe_customer_id   = EXCLUDED.stripe_customer_id,
    stripe_sub_id        = EXCLUDED.stripe_sub_id,
    status               = EXCLUDED.status,
    plan                 = EXCLUDED.plan,
    current_period_end   = EXCLUDED.current_period_end,
    trial_end            = EXCLUDED.trial_end,
    cancel_at_period_end = EXCLUDED.cancel_at_period_end,
    paused_at            = EXCLUDED.paused_at,
    updated_at           = now()
`

// sqlSelectSubscription returns the columns GetSubscription needs.
const sqlSelectSubscription = `
SELECT status, plan, current_period_end, trial_end, cancel_at_period_end,
       COALESCE(stripe_customer_id, ''), COALESCE(stripe_sub_id, '')
FROM user_subscriptions
WHERE user_id = $1
`

// sqlGetCustomerID returns the Stripe customer id for a user (empty when missing).
const sqlGetCustomerID = `
SELECT COALESCE(stripe_customer_id, '')
FROM user_subscriptions
WHERE user_id = $1
`

// sqlSetCustomerID upserts a row that only knows the customer (pre-checkout).
// Status is 'incomplete' until the first webhook arrives.
const sqlSetCustomerID = `
INSERT INTO user_subscriptions (user_id, stripe_customer_id, status, plan)
VALUES ($1, $2, 'incomplete', 'pro_monthly')
ON CONFLICT (user_id) DO UPDATE SET
    stripe_customer_id = COALESCE(user_subscriptions.stripe_customer_id, EXCLUDED.stripe_customer_id),
    updated_at = now()
`

// sqlInsertEvent records one entry in the audit log.
// stripe_event_id is nullable; pass empty string and we treat it as NULL.
const sqlInsertEvent = `
INSERT INTO billing_events (stripe_event_id, user_id, event_type, livemode, payload)
VALUES (NULLIF($1, ''), $2, $3, $4, $5)
`

// sqlListActiveStripeSubs returns every row with a non-NULL stripe_sub_id.
// Used by the reconciliation cron.
const sqlListActiveStripeSubs = `
SELECT user_id, stripe_sub_id
FROM user_subscriptions
WHERE stripe_sub_id IS NOT NULL
`
