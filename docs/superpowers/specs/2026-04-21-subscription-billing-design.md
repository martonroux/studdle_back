# Spec C — Subscription Billing

**Status:** Design approved, ready for implementation planning.
**Date:** 2026-04-21
**Revised:** 2026-05-11 — trial extended 7 → 30 days (Q4); dunning switched from immediate pause to Stripe Smart Retries with no grace (Q6); Pro-tier daily quotas now explicit (new Q16); §5.3 / §6.3 updated accordingly; §4.4 expanded to cover scaffold-drift cleanup carried over from Spec A's foundation work.
**Scope:** Replace Spec A's `ai_subscription_active` admin-flip stub with real payment rails on the web (Stripe Checkout + Customer Portal). Single paid tier ("Pro"), monthly and annual billing, **30-day card-required free trial**, EUR-only at launch. Web only — mobile IAP (Apple + Google) is a later spec (Spec C.1). Supersedes Spec A's `/admin/set-ai-subscription` endpoint.

Not in scope: mobile in-app purchases, multi-currency pricing, purchasing-power-parity adjustments, tiered plans (Pro / Pro+), quota top-ups / credit packs, student discounts, family plans, referral rewards, public refund policy, email campaigns / abandoned-cart recovery.

---

## 1. Purpose

StudBud gates AI features behind an entitlement flag (`ai_subscription_active`). Spec A shipped this as an admin-flipped boolean so the AI pipeline could be built in parallel with billing. This spec wires that flag to real Stripe subscriptions so users can self-serve their way to Pro, and introduces the local state, audit trail, and recovery tooling required to operate billing safely in production.

Outcomes:
- A free user can click "Start 30-day trial," pay via Stripe Checkout, and have AI access within seconds.
- Cancellations, payment failures, and admin comps all flow through the same `user_subscriptions` table.
- Webhook loss, out-of-order delivery, and duplicate events cannot corrupt entitlement state.
- Spec A's admin endpoint is removed; its functional replacement is a comp-grant endpoint writing to the same table.

## 2. Product Decisions (Locked)

| # | Decision | Choice | Rationale |
|---|----------|--------|-----------|
| Q1 | Platform scope at launch | Web only (Stripe). Mobile IAP deferred to Spec C.1. | Fastest proven billing pipeline. Pre-launch, App Store paywall friction is acceptable. |
| Q2 | Tier shape | Single paid tier ("Pro"). | No usage data yet to justify tier splits. Adding Pro+ later is additive, not migratory. |
| Q3 | Billing cadence | Monthly + Annual (annual ~29% off). Two Stripe price IDs under one product. | Monthly = try-before-commit. Annual = retention + revenue smoothing. Standard SaaS dual offer. |
| Q4 | Free trial | **30-day trial, card required upfront. One per user.** | Card-required trials convert meaningfully better; 30 days gives enough study sessions to feel Pro's impact. Stripe owns the lifecycle; no custom expiry logic. One-trial-per-Customer enforcement deduplicates abuse. |
| Q5 | Cancellation behavior | Downgrade at period end. No refund on cancel. | Stripe default; removes refund-abuse vectors; matches user expectations. |
| Q6 | Payment-failure dunning | **Stripe Smart Retries on, no grace.** Status moves to `past_due` on first failure → access revoked immediately. Successful retry returns status to `active` → access restored. If retries exhaust, Stripe cancels the subscription. | Recovers involuntary churn (forgotten cards) without exposing free access during the retry window. We never call `pause_collection` ourselves — Stripe handles the entire retry schedule. |
| Q7 | Checkout infrastructure | Stripe Checkout (hosted) + Stripe Customer Portal (hosted). | ~90% less code than Elements. SCA, tax, Apple/Google Pay all handled. Brand equity not yet worth optimizing. |
| Q8 | Pricing & currency | Single currency (EUR). €6.99/mo, €59.99/yr. Tax-inclusive. | Pre-launch, per-market anchoring is premature. Non-EUR users pay EUR at their card's FX. |
| Q9 | Entitlement source of truth | Local `user_subscriptions` row mirrors Stripe state. `user_has_ai_access(uid)` SQL helper is the entitlement check. Append-only `billing_events` audit log. | Cheap reads, zero Stripe dependency on every AI call, full self-serve debugging. |
| Q10 | Tax | Stripe Tax enabled. Prices displayed tax-inclusive. | Near-zero-effort EU VAT compliance via Stripe's tax partners. |
| Q11 | Refunds & chargebacks | No public refund policy. Manual discretionary refunds via Stripe dashboard. Chargebacks <€50 eaten, >€50 fought with usage evidence. | Industry-standard for indie SaaS. Public policies defer until support bandwidth justifies. |
| Q12 | Webhook reliability | Signature-verify + idempotency by `event.id` (unique constraint) + nightly reconciliation cron + user-facing refresh endpoint, all sharing one Stripe-retrieve path. | Defense in depth without code duplication. Refresh button flips many support tickets into self-service. |
| Q13 | Paywall placement | Inline paywall (reuses Spec A's `PaywallCard.vue`) + public `/pricing` route. Both call the same `/billing/checkout`. | Inline captures impulse conversions; `/pricing` handles considered ones and doubles as marketing. |
| Q14 | Admin backdoor | Keep admin endpoint, now writes `user_subscriptions` rows with `status='comped'`. Gated by `RequireAdmin` middleware (`users.is_admin=TRUE`), same gate Spec A's `/admin/grant-ai-access` uses. | One source of truth. Comp audit trail identical to paid-customer audit trail. The earlier `ADMIN_API_ENABLED` env flag was retired during Spec A implementation in favor of a persisted admin attribute. |
| Q15 | Env config & test-mode isolation | `STRIPE_MODE=test\|live` + key-prefix assertion at boot + per-webhook `livemode` field check. | Three independent safeties. The livemode check specifically blocks cross-environment webhook misrouting. |
| Q16 | Pro-tier quotas (new) | **Pro tier = the daily caps already defined in `aipipeline.DefaultQuotaLimits()`**: `prompt_calls=20`, `pdf_calls=5`, `pdf_pages=100`, `check_calls=50`, `plan_calls=5`. **Free tier = zero AI calls of any kind** (no AI access, period). | Single source of truth for "what does Pro get." The numbers were tuned during Spec A/B implementation; locking them into Spec C means future tier additions inherit a documented baseline rather than discovering it via code archaeology. Free = zero matches the existing entitlement check (`user_has_ai_access` returns false → pipeline returns 402). |

## 3. Architecture Overview

### 3.1 Module map

**Backend (`study_buddy_backend/`):**
- `pkg/billing/` — Stripe client wrapper, webhook signature verification, mode-isolation checks (key prefix + livemode).
- `api/service/billingService.go` — `CreateCheckoutSession`, `CreatePortalSession`, `HandleWebhookEvent`, `RefreshFromStripe`, `GrantComp`, `RevokeComp`. All writes to `user_subscriptions` + `billing_events` live here.
- `api/handler/billingHandler.go` — `POST /billing/checkout`, `POST /billing/portal`, `POST /billing/webhook`, `POST /billing/refresh`, `GET /billing/subscription`, `GET /billing/plans`.
- `api/handler/adminHandler.go` — `POST /admin/comp-subscription`, `DELETE /admin/comp-subscription`. Removes the old `POST /admin/set-ai-subscription`.
- `api/cron/billingReconcile.go` — nightly job.
- `api/migrations/` — creates `user_subscriptions`, `billing_events`, `user_has_ai_access()`; backfills; drops `users.ai_subscription_active`.

**Frontend (`studbud/src/`):**
- `api/billing.ts` — client for checkout / portal / subscription / refresh.
- `stores/billing.ts` — Pinia store for current subscription state.
- `pages/PricingPage.vue` — public `/pricing`.
- `pages/BillingPage.vue` — authed `/billing`.
- `components/ai/PaywallCard.vue` — **existing stub from Spec A**, updated to call `/billing/checkout`.
- Navigation additions to Profile + QuotaBadge.

### 3.2 Hard boundaries

- Only `billingService` writes to `user_subscriptions` and `billing_events`.
- Webhook handler and reconciliation cron and refresh endpoint all funnel through `billingService.applyStripeState(user, stripeSub)` — one write path, three entry points.
- `users.ai_subscription_active` column is **removed** after migration. The only way to ask "does this user have AI access?" is `user_has_ai_access(uid)` (or the equivalent Go helper that calls it).
- Frontend never talks to Stripe's API directly (only via redirects to Stripe-hosted pages).
- Price IDs live in env config keyed by plan name. No hard-coded IDs in source.

## 4. Data Model

### 4.1 `user_subscriptions`

```sql
CREATE TABLE user_subscriptions (
    user_id              BIGINT       PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    stripe_customer_id   TEXT         UNIQUE,
    stripe_sub_id        TEXT         UNIQUE,
    status               TEXT         NOT NULL CHECK (status IN (
                                         'trialing','active','past_due','paused',
                                         'canceled','incomplete','incomplete_expired',
                                         'comped'
                                       )),
    plan                 TEXT         NOT NULL CHECK (plan IN ('pro_monthly','pro_annual','comp')),
    current_period_end   TIMESTAMPTZ,
    trial_end            TIMESTAMPTZ,
    cancel_at_period_end BOOLEAN      NOT NULL DEFAULT FALSE,
    paused_at            TIMESTAMPTZ,
    created_at           TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_user_subs_status ON user_subscriptions (status);
CREATE INDEX idx_user_subs_period_end ON user_subscriptions (current_period_end)
    WHERE status IN ('active','trialing','past_due');
```

- `stripe_customer_id` and `stripe_sub_id` are NULL for comped rows.
- One row per user (PK on `user_id`); upsert semantics.
- `status='paused'` and `'past_due'` explicitly **do not** grant access.

### 4.2 Entitlement helper

```sql
CREATE FUNCTION user_has_ai_access(uid BIGINT) RETURNS BOOLEAN
LANGUAGE SQL STABLE AS $$
    SELECT EXISTS (
        SELECT 1 FROM user_subscriptions
        WHERE user_id = uid
          AND status IN ('active','trialing','comped')
          AND (current_period_end IS NULL OR current_period_end > NOW())
    );
$$;
```

Go helper mirrors this — either a direct SQL call or an equivalent predicate against the cached subscription row.

### 4.3 `billing_events`

```sql
CREATE TABLE billing_events (
    id                BIGSERIAL PRIMARY KEY,
    stripe_event_id   TEXT      UNIQUE,
    user_id           BIGINT    REFERENCES users(id) ON DELETE SET NULL,
    event_type        TEXT      NOT NULL,
    livemode          BOOLEAN   NOT NULL,
    payload           JSONB     NOT NULL,
    received_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_billing_events_user ON billing_events (user_id, received_at DESC);
```

- `stripe_event_id` UNIQUE is the idempotency guard. Duplicate webhook delivery conflicts on insert → handler short-circuits with 200 OK.
- `stripe_event_id` is NULL for non-Stripe-originated events (admin actions, cron reconciliations). Those use synthetic `event_type` values: `admin_comp_granted`, `admin_comp_revoked`, `cron_reconciled`, `user_refresh_triggered`.
- `livemode` recorded from the Stripe event (or set to the configured mode for admin/cron events).

### 4.4 Migration from current scaffold

Spec A's implementation already landed a minimal scaffold (`user_subscriptions`, `billing_events`, `user_has_ai_access()`) so the AI pipeline could read entitlement from day one. The scaffold diverges from this spec in a few specific ways that the Spec C implementation plan must fix in a single migration step before any Stripe wiring.

**Scaffold deltas to reconcile:**

| Object | Current scaffold | Spec target | Migration action |
|---|---|---|---|
| `user_subscriptions` PK | `id BIGSERIAL PRIMARY KEY`, `user_id` FK (non-unique) | `user_id BIGINT PRIMARY KEY` (one row per user) | Collapse to one row per user (keep most recent), drop `id`, set PK to `user_id`. |
| `user_subscriptions.stripe_customer_id` | absent | required (UNIQUE) | Add column. |
| `user_subscriptions.stripe_sub_id` | named `stripe_subscription_id` | named `stripe_sub_id` (UNIQUE) | Rename. |
| `user_subscriptions.trial_end` | absent | required | Add column. |
| `user_subscriptions.paused_at` | absent | required | Add column. |
| `user_subscriptions.status` CHECK | `('active','past_due','canceled','trialing','comp')` | spec set with `comped` (not `comp`) plus `paused`, `incomplete`, `incomplete_expired` | Drop + recreate CHECK; rewrite any existing `'comp'` rows to `'comped'`. |
| `user_subscriptions.plan` CHECK | `('pro_monthly','pro_annual','comp')` | unchanged | Keep. |
| `billing_events.livemode` | absent | required NOT NULL | Add column (default `(STRIPE_MODE='live')` for existing rows). |
| `billing_events.stripe_event_id` | `NOT NULL UNIQUE` | `NULL` allowed UNIQUE (admin/cron entries omit it) | Drop NOT NULL. |

There is no `ai_subscription_active` column to drop — the original migration step from this spec's first draft was already executed during Spec A. The pipeline already reads via `user_has_ai_access(uid)`. Existing scaffold rows (if any in dev) survive the reconciliation as long as `status='comp'` is rewritten to `status='comped'`.

The `user_has_ai_access()` function itself is correct as-is; no changes required.

## 5. Backend Endpoints

### 5.1 `POST /billing/checkout`

**Auth:** required.
**Body:** `{ plan: "pro_monthly" | "pro_annual" }`
**Returns:** `{ url: string }`

Flow:
1. Look up `user_subscriptions.stripe_customer_id` for the user. If absent, create a Stripe Customer (`email=user.email`, `metadata={userId}`) and persist.
2. Refuse if user already has `status IN ('trialing','active')` — return 409 with `{ kind: "already_subscribed" }`. Frontend redirects to `/billing` instead.
3. Map `plan` → price ID via env config `STRIPE_PRICE_PRO_MONTHLY` / `STRIPE_PRICE_PRO_ANNUAL`.
4. Create Stripe Checkout Session:
   - `mode='subscription'`
   - `customer=<stripe_customer_id>`
   - `line_items=[{ price, quantity: 1 }]`
   - `subscription_data={ trial_period_days: 7, metadata: { userId } }`
   - `payment_method_collection='always'`
   - `automatic_tax={ enabled: true }`
   - `tax_id_collection={ enabled: true }`
   - `success_url=<APP_URL>/billing?status=success&session_id={CHECKOUT_SESSION_ID}`
   - `cancel_url=<APP_URL>/pricing?status=cancelled`
   - `client_reference_id=<userId>`
   - `metadata={ userId }`
5. Return the session's `url`.

Trial eligibility: Stripe enforces one-trial-per-customer automatically via the Customer's trial history when both paths go through the same `stripe_customer_id`.

### 5.2 `POST /billing/portal`

**Auth:** required.
**Returns:** `{ url: string }`

Requires a `stripe_customer_id` on the user — 404 if the user never checked out. Calls `stripe.billingPortal.Session.Create({ customer, return_url: <APP_URL>/billing })`.

### 5.3 `POST /billing/webhook`

**Auth:** public. Signature verified via `Stripe-Signature` header against `STRIPE_WEBHOOK_SECRET`.

Preamble (runs before event dispatch):
1. Read raw body.
2. Verify signature.
3. Check `event.livemode` matches configured `STRIPE_MODE`. Mismatch → 400 + log.
4. INSERT into `billing_events` (stripe_event_id, event_type, livemode, payload). On duplicate `stripe_event_id` conflict → return 200 OK immediately (idempotent re-delivery).

Dispatched events:

| Stripe event | Handler behavior |
|-------------|------------------|
| `checkout.session.completed` | Extract `subscription`, `customer` from session. Retrieve subscription from Stripe (authoritative). Call `applyStripeState(userId, sub)`. |
| `customer.subscription.created` | Retrieve sub (may be same as above; idempotent). `applyStripeState`. |
| `customer.subscription.updated` | Retrieve sub. `applyStripeState` — picks up status transitions, cancellation flag, period rollover, plan swap. |
| `customer.subscription.deleted` | Set local `status='canceled'`. |
| `customer.subscription.paused` | Set `status='paused'`, `paused_at=NOW()`. Reserved for explicit Stripe pause (e.g. operator action). Smart Retries does **not** pause — it moves the subscription to `past_due`. |
| `customer.subscription.resumed` | Set `status='active'`, `paused_at=NULL`. |
| `invoice.payment_failed` | **Log only.** Stripe Smart Retries owns the retry schedule. The companion `customer.subscription.updated` event will carry `status='past_due'` and is what mutates local state. We never call `pause_collection` ourselves. |
| `invoice.payment_succeeded` | Log only. (Subscription.updated fires separately with refreshed `status='active'` and new `current_period_end`.) |
| `charge.refunded` | Log only. No entitlement change. |
| any other | Log only (already written by preamble). |

`applyStripeState(userId, sub)` — one upsert resolving status, plan (from price ID), `current_period_end`, `trial_end`, `cancel_at_period_end` from the Stripe subscription object. Plan lookup: price ID → plan name via reverse map of the same env config used by checkout.

### 5.4 `POST /billing/refresh`

**Auth:** required.
**Returns:** same shape as `GET /billing/subscription`.

Calls `stripe.subscriptions.list({ customer: user.stripe_customer_id, status: 'all', limit: 1 })`. If a subscription exists, `applyStripeState`. If not, no-op. Logs `user_refresh_triggered` to `billing_events`. Rate-limited to 10/min per user (guard against abuse).

### 5.5 `GET /billing/subscription`

**Auth:** required.
**Returns:**
```json
{
  "status": "trialing|active|past_due|paused|canceled|comped|none",
  "plan": "pro_monthly|pro_annual|comp|null",
  "currentPeriodEnd": "2026-05-21T00:00:00Z|null",
  "trialEnd": "2026-04-28T00:00:00Z|null",
  "cancelAtPeriodEnd": false,
  "isActive": true
}
```

`status: "none"` for users with no `user_subscriptions` row.
`isActive` = the same boolean `user_has_ai_access()` would return.

### 5.6 `GET /billing/plans`

**Auth:** public.
**Returns:** `[{ plan: "pro_monthly", priceEur: 6.99, interval: "month" }, { plan: "pro_annual", priceEur: 59.99, interval: "year", discountPct: 29 }]`

Config-driven; lets us tune display prices without frontend redeploys.

### 5.7 Admin endpoints

- `POST /admin/comp-subscription` — body `{ userId, expiresAt: ISO-date|null, reason: string }`. Upserts row with `status='comped'`, `plan='comp'`, `current_period_end=expiresAt`, `stripe_customer_id=null`. Logs `admin_comp_granted` with `{reason, actor, expiresAt}` in payload.
- `DELETE /admin/comp-subscription` — body `{ userId, reason: string }`. Sets `status='canceled'`. Logs `admin_comp_revoked`.

Both endpoints sit behind the same `RequireAdmin` middleware as Spec A's `POST /admin/grant-ai-access` — i.e. the authenticated user must have `users.is_admin = TRUE`. No separate env flag.

Spec A's `POST /admin/grant-ai-access` continues to exist as the dev/QA path to flip entitlement without dropping into the subscription model; this spec adds `comp-subscription` for cases where you want a structured, expiring comp recorded as a `user_subscriptions` row. Both paths funnel writes through `billingService` so the audit trail is uniform.

## 6. Control Flow (Lifecycle Scenarios)

### 6.1 New trial signup (happy path)

```
User clicks "Start 30-day trial" in PaywallCard
  → POST /billing/checkout {plan: "pro_monthly"}
  → Backend: get-or-create stripe_customer_id
  → Backend: create Checkout Session (trial_period_days=30, automatic_tax=on)
  → return {url}
User → Stripe Checkout (card form) → submits → returns to /billing?status=success
Stripe → POST /billing/webhook [checkout.session.completed]
  → Backend: verify sig + livemode → insert billing_events
  → retrieve sub → applyStripeState(user, sub):
       status='trialing', plan='pro_monthly', trial_end=now+30d, current_period_end=now+30d
/billing fetches subscription → isActive=true → UI shows "Trial active, 30 days remaining"
AI pipeline: user_has_ai_access(userId) → true → unlocks
```

### 6.2 Trial → paid conversion (automatic)

```
Day 30: Stripe charges
  → webhook invoice.payment_succeeded (logged)
  → webhook customer.subscription.updated (status='active', current_period_end=now+30d)
  → applyStripeState upserts
User sees no difference except /billing now reads "Renews <date>"
```

### 6.3 Trial → paid conversion fails (card declined) — Smart Retries flow

```
Day 30: charge fails
  → webhook invoice.payment_failed (logged only)
  → webhook customer.subscription.updated (status='past_due')
  → applyStripeState → local status='past_due'
  → user_has_ai_access → false (past_due is not a granting status)
/billing shows red "Payment failed — update your card" banner with portal CTA
Stripe Smart Retries: re-attempts charge on its own schedule (typically 3 retries over ~2 weeks).
  Scenario A — user updates card via portal before retry:
    → webhook customer.subscription.updated (status='active') → access restored
  Scenario B — retry succeeds on its own:
    → webhook invoice.payment_succeeded (logged)
    → webhook customer.subscription.updated (status='active', current_period_end advanced)
    → access restored
  Scenario C — all retries exhaust:
    → webhook customer.subscription.deleted → status='canceled'
    → user_has_ai_access stays false; PaywallCard returns
```

The backend never calls `subscriptions.update` for failed payments. Stripe Smart Retries owns the entire recovery lifecycle; we just mirror the resulting state.

### 6.4 Mid-period cancellation

```
User → /billing → portal → "Cancel plan"
Stripe: cancel_at_period_end=true (sub stays active until period end)
  → webhook customer.subscription.updated → cancel_at_period_end=true
/billing shows orange "Ends <date>. Resubscribe anytime." banner
user_has_ai_access stays true until period end
Period end:
  → webhook customer.subscription.deleted → status='canceled'
  → user_has_ai_access → false; PaywallCard returns
```

### 6.5 Missed webhook recovery

```
Server outage / firewall / delivery lag: webhook for user U not applied
User: "I paid but can't generate" → clicks Refresh on /billing
  → POST /billing/refresh
  → stripe.subscriptions.list({customer: U.stripe_customer_id, limit: 1})
  → applyStripeState → local state now correct
  → return fresh /billing/subscription response → UI re-renders
```

Cron backstop (01:00 UTC, `billingReconcile.go`):
```
SELECT user_id, stripe_sub_id FROM user_subscriptions WHERE stripe_sub_id IS NOT NULL
For each: stripe.subscriptions.retrieve(stripe_sub_id)
  If state differs from local: applyStripeState + log cron_reconciled
Rate: 100 req/min to Stripe
Emit metric: reconciliations_performed, drifts_corrected
```

### 6.6 Admin comp (support / beta)

```
Admin → POST /admin/comp-subscription {userId: 42, expiresAt: "2026-12-31", reason: "Beta tester"}
  → upsert user_subscriptions {status:'comped', plan:'comp', current_period_end: 2026-12-31, stripe_customer_id:null}
  → billing_events(event_type='admin_comp_granted', payload={reason, actor, expiresAt})
user_has_ai_access(42) → true through 2026-12-31
```

### 6.7 Immediate access revocation (rare support case)

For the unusual case where a refund should also revoke access:
```
Admin via Stripe dashboard: "Cancel subscription immediately"
  → webhook customer.subscription.deleted → status='canceled'
  → access gone on next pipeline call
```

Default refund (issued without cancellation) does **not** revoke access — the user keeps Pro through `current_period_end`.

## 7. Error Handling

| Failure | Handling |
|--------|----------|
| Webhook signature invalid | 400 + structured log. Do not process. |
| Webhook livemode mismatch | 400 + structured log + alert. Do not process. |
| Webhook duplicate (`event.id` conflict) | 200 OK, short-circuit before dispatch. |
| Webhook handler panics mid-dispatch | 500. Stripe retries with exponential backoff (up to ~3 days). `billing_events` row already written → on retry, idempotency short-circuits only if insert succeeded. If panic was before insert, retry re-inserts normally. |
| Stripe API call fails (checkout create, portal create, subscription.list, subscription.retrieve) | Surface 502 with `{kind: "upstream_stripe", message}`. Do not mutate local state on failure. |
| `CreateCheckoutSession` for user already subscribed | 409 `{kind: "already_subscribed"}`. |
| `CreatePortalSession` for user without `stripe_customer_id` | 404 `{kind: "no_customer"}`. Frontend re-routes to `/pricing`. |
| `user_has_ai_access` called during outage of `user_subscriptions` reads | Bubble up error to pipeline; pipeline returns a distinguished `entitlement_unknown` which the handler maps to 503. Never default to true. |
| Cron job: Stripe rate-limit | Back off; resume next cycle. Metric `reconciliations_rate_limited` incremented. |
| Migration backfill: `users.ai_subscription_active` missing | Migration runs on a schema without Spec A's flag (clean install) → backfill no-op, continues. |

## 8. Frontend UX

### 8.1 Routes

- `/pricing` — public. Feature list, plan toggle (monthly / annual), per-plan price tile, "Start 30-day trial" CTA. Reachable from landing page, profile, QuotaBadge when free, AiCheckModal/AiGenerationControls paywall links.
- `/billing` — authed. Current plan status, renewal / trial-end date, "Manage subscription" portal link, "Refresh status" button, conditional banners.

### 8.2 Paywall entry points

1. **Inline** — existing `components/ai/PaywallCard.vue`. Now contains a two-tile toggle (monthly / annual) + CTA "Start 30-day trial." CTA → `POST /billing/checkout` → `window.location.href = url`.
2. **Pricing page** — long-form feature-by-feature layout with FAQ. Same checkout call.
3. **Landing page** — unauthenticated `/` renders existing hero + new "See pricing" link to `/pricing`.

### 8.3 `/billing` banners

Priority (top-to-bottom, one at most shown):
- Past-due (payment failed, retrying): red. Copy: "Payment failed. Stripe is retrying — update your card to restore AI access sooner." CTA: Manage subscription. Triggered by `status='past_due'`.
- Paused (operator-initiated): red. Copy: "Subscription paused. Contact support to resume." CTA: Manage subscription. Triggered by `status='paused'`. Rare in v1 — Smart Retries flows through `past_due`, not `paused`.
- Cancel at period end: orange. Copy: "Your Pro access ends on <date>. Resubscribe anytime." CTA: Manage subscription.
- Comped: neutral. Copy: "Complimentary access" + "expires <date>" or "no expiry."
- Trialing: blue. Copy: "Free trial — <N> days remaining. Converts to <plan> on <date>." CTA: Manage subscription.
- Active: green. Copy: "Pro — renews on <date>." CTA: Manage subscription.
- No subscription: gray. Copy: "You're on the free plan." CTA: See pricing → `/pricing`.

### 8.4 Post-checkout return

- `/billing?status=success&session_id=...` → toast "Welcome to Pro!" → `stores/billing.refresh()` once (picks up webhook-lagged state) → render banner.
- `/pricing?status=cancelled` → silent return; pricing re-displayed.

### 8.5 Pinia store (`stores/billing.ts`)

State:
```ts
{
  subscription: {
    status: 'none'|'trialing'|'active'|'past_due'|'paused'|'canceled'|'comped',
    plan: 'pro_monthly'|'pro_annual'|'comp'|null,
    currentPeriodEnd: string|null,
    trialEnd: string|null,
    cancelAtPeriodEnd: boolean,
    isActive: boolean,
  } | null,
  plans: Plan[] | null,
  loading: boolean,
  error: string | null,
}
```

Actions:
- `fetch()` — `GET /billing/subscription`; cached.
- `refresh()` — `POST /billing/refresh` then re-fetch.
- `checkout(plan)` — `POST /billing/checkout`, redirect.
- `portal()` — `POST /billing/portal`, redirect.
- `fetchPlans()` — `GET /billing/plans`.

Invalidation: re-fetch on login, on app resume (Capacitor `appStateChange`), after `refresh()`, after returning from Checkout with `?status=success`.

### 8.6 Navigation integration

- Profile → "Billing" row (authed users).
- Profile → "Upgrade to Pro" row (when not active).
- `components/ai/QuotaBadge.vue` — tapping opens `/billing` if active, `/pricing` otherwise.

## 9. Environment & Test-Mode Isolation

Required env at boot:
- `STRIPE_MODE` — `test` or `live`.
- `STRIPE_SECRET_KEY` — must start with `sk_test_` when `STRIPE_MODE=test`, `sk_live_` when `STRIPE_MODE=live`. Mismatch → refuse to boot.
- `STRIPE_WEBHOOK_SECRET` — separate secret per mode.
- `STRIPE_PRICE_PRO_MONTHLY`, `STRIPE_PRICE_PRO_ANNUAL` — must start with `price_`.
- `APP_URL` — used for `success_url` / `cancel_url` / portal `return_url`.
- Admin endpoints (`/admin/comp-subscription` POST/DELETE) reuse Spec A's admin gating: the `RequireAdmin` HTTP middleware checks `users.is_admin = TRUE` for the authenticated user. No env flag — admin status is a persisted user attribute. (Spec A's earlier `ADMIN_API_ENABLED` env flag was retired during implementation.)

Every webhook event checks `event.livemode === (STRIPE_MODE === 'live')`. Mismatch → 400 + structured alert log.

Reconciliation cron checks `STRIPE_MODE` before calling Stripe — never runs in test mode against a non-test key.

## 10. Testing

### Unit (backend)
- `applyStripeState`: all status transitions: `trialing → active`, `active → past_due` (Smart Retries first failure), `past_due → active` (retry success or card updated), `past_due → canceled` (retries exhausted), `active → cancel_at_period_end=true`, `cancel_at_period_end → canceled`, `active → paused` (operator-initiated), `paused → active`.
- Plan resolution: price ID → plan name; unknown price ID → `failed_to_map` logged, upsert skipped.
- Webhook idempotency: same `event.id` twice → one row in `billing_events`, one state change.
- Livemode mismatch: signature-valid event with wrong `livemode` → 400, no state change.
- Key-prefix assertion: boot with `STRIPE_MODE=live` and `sk_test_xxx` → boot fails with specific error.
- `user_has_ai_access`: returns true for `trialing/active/comped` within period, false for `paused/past_due/canceled/incomplete/incomplete_expired`, false for expired comp.
- Dunning: `invoice.payment_failed` handler is log-only (no `subscriptions.update` call is made).

### Integration (backend with DB + Stripe test mode)
- Full checkout → webhook → subscription row created flow (signed webhook delivered to test endpoint).
- Payment failure path: simulate `invoice.payment_failed` + companion `customer.subscription.updated{status=past_due}` → verify local status goes `past_due`, `user_has_ai_access=false`, no `subscriptions.update` API call made.
- Smart Retries recovery path: simulate retry success → `customer.subscription.updated{status=active}` → verify status returns to `active`, `user_has_ai_access=true`.
- Smart Retries exhaustion path: simulate retry final failure → `customer.subscription.deleted` → verify status goes `canceled`.
- Cancellation path: cancel via Stripe API → webhook chain → status transitions correctly.
- Reconciliation cron: manually desync local state → run cron → state corrected.
- Refresh endpoint: manually desync → call `/billing/refresh` → state corrected.
- Admin comp: POST → row with `status='comped'`, `stripe_customer_id=null`, `user_has_ai_access=true`. DELETE → `status='canceled'`, `user_has_ai_access=false`.
- Scaffold reconciliation migration (§4.4): pre-migration DB with `id BIGSERIAL` PK, two rows for one user, one row with `status='comp'` → run migration → one row per user, PK is `user_id`, status rewritten to `'comped'`, `livemode` populated, pipeline still reads entitlement correctly.

### Frontend (component)
- `PaywallCard.vue`: renders plan toggle; clicking CTA calls `stores/billing.checkout(plan)`.
- `PricingPage.vue`: renders both plans from `/billing/plans`; renders "See current plan" link when authed + active.
- `BillingPage.vue`: renders correct banner per status; Refresh button calls `refresh()`.
- Post-checkout return: `/billing?status=success` triggers `refresh()` once.

### Manual QA (end-to-end, Stripe test mode)
- New signup → 30-day trial → fast-forward via Stripe CLI `trigger` → auto-conversion → verify Pro active.
- New signup → trial → cancel mid-trial → verify keeps access until trial end → period end → verify access revoked.
- Subscribe → trigger `invoice.payment_failed` → verify `past_due` banner ("Payment failed — update your card") → verify AI access blocked → update card via portal → verify access restored.
- Subscribe → trigger 4× `invoice.payment_failed` (Smart Retries exhaustion) → verify subscription canceled and PaywallCard returns.
- Admin comp → user gains Pro without payment.
- Cross-mode guard: deploy test webhook to live endpoint → live endpoint rejects with livemode mismatch.

## 11. Observability

- **Metrics:**
  - `billing_webhook_received_total{event_type, outcome}` — counter (outcome ∈ `applied`, `duplicate`, `mismatch`, `error`).
  - `billing_checkout_session_created_total{plan}` — counter.
  - `billing_portal_session_created_total` — counter.
  - `billing_refresh_triggered_total` — counter.
  - `billing_reconciliations_performed_total` — counter.
  - `billing_reconciliation_drifts_corrected_total` — counter.
  - `billing_livemode_mismatch_total` — counter, alert on >0.
- **Structured logs:** every state transition logs `{user_id, stripe_sub_id, from_status, to_status, from_plan, to_plan, source}` where `source ∈ webhook|cron|refresh|admin|migration`.
- **SQL probes (runbook):**
  - Current paying users: `SELECT COUNT(*) FROM user_subscriptions WHERE status IN ('active','trialing')`.
  - Revenue at risk (dunning in flight): `SELECT COUNT(*) FROM user_subscriptions WHERE status = 'past_due'`.
  - Recent drifts: `SELECT * FROM billing_events WHERE event_type='cron_reconciled' ORDER BY received_at DESC LIMIT 50`.

## 12. Out of Scope (Deferred)

- Mobile in-app purchases (Apple + Google) — **Spec C.1**.
- Multi-currency pricing / PPP adjustments — post-launch based on paying-user geography.
- Tiered plans (Pro / Pro+) — after usage data justifies.
- Quota top-ups / credit packs — requires consumption-based billing redesign.
- Student / educator discounts — requires verification partner.
- Family / group plans — requires access-sharing model.
- Public refund policy — requires support bandwidth.
- Referral rewards / promo codes (Stripe coupons usable via dashboard only for now).
- Abandoned-cart email campaigns.
- Dunning retry recovery — we intentionally don't retry (Q6 = C).
- Revenue analytics dashboards (Stripe's built-in dashboard covers v1 needs).

## 13. Open Questions (Non-Blocking)

- Launch prices (€6.99/€59.99) are starting guesses; tune before announcing based on comparable indie SaaS.
- Whether to add a "Claim beta comp" self-service page for approved beta testers (vs. admin endpoint only) — can be added without schema change.
- Webhook endpoint path: `/billing/webhook` vs. Stripe's convention `/webhooks/stripe` — leave as `/billing/webhook` for symmetry with the rest of the namespace unless ops has a preference.
