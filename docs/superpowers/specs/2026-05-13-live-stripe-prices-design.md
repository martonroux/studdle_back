# Live Stripe Prices for `GET /billing/plans`

**Date:** 2026-05-13
**Spec:** C — Subscription Billing
**Scope:** Replace hardcoded pricing tiles in `GET /billing/plans` with live data from Stripe, cached in-memory.

## Motivation

`GET /billing/plans` currently returns hardcoded EUR amounts (`6.99`, `59.99`) and a hardcoded 29% discount badge (`api/handler/billing.go:119`). The actual Stripe prices live in the dashboard and are referenced by the env vars `STRIPE_PRICE_PRO_MONTHLY` / `STRIPE_PRICE_PRO_ANNUAL`. The two sources can drift: changing a price in Stripe does not update the API response, and vice versa, which risks displaying one amount while charging another.

The fix: have `GET /billing/plans` read amounts directly from Stripe (via the existing `STRIPE_PRICE_*` env vars), cache them in memory for 5 minutes, and compute the discount badge from the actual amounts.

## Design Decisions

| Decision | Choice | Rationale |
|---|---|---|
| Caching | In-memory TTL cache, 5 minutes | Cuts Stripe calls to ≤1 per 5 min while keeping dashboard edits visible within minutes. |
| Stripe unreachable on cold cache | Return 502 (via existing `myErrors.ErrStripe`) | Per user preference; frontend renders an error state. No stale fallback, no hardcoded values baked in. `ErrStripe` is already wired to `StatusBadGateway` in `httpx/errors.go:42`. |
| Discount % | Computed from prices | `discount = round((1 - annual / (monthly * 12)) * 100)`. Stays in sync with whatever is set in Stripe. |
| Code structure | Dedicated `PriceProvider` in `internal/billing` | Keeps the handler thin, makes the cache testable, isolates Stripe dependency. |
| Currency | EUR-only; non-EUR → 503 | API field is `priceEur`; mismatched currency is a misconfiguration and should fail loudly rather than silently displaying the wrong number. |

## Architecture

Three layers, mirroring the existing split:

1. **`internal/billing` (Stripe adapter layer)** — new `PriceProvider` interface, cached implementation, and a new `GetPrice` method on the existing `Client` interface.
2. **`api/handler` (HTTP layer)** — `BillingHandler` gains a `PriceProvider` dependency; `GetPlans` consumes it.
3. **`cmd/app` (wiring)** — constructs the provider at startup with a 5-minute TTL.

### New types — `internal/billing/price_provider.go`

```go
// PriceData is one Stripe Price flattened to the fields the pricing UI needs.
type PriceData struct {
    Amount   int64  // smallest currency unit (cents for EUR)
    Currency string // ISO 4217 lowercase, e.g. "eur"
    Interval string // "month" | "year"
}

// Prices is the pair the pricing UI displays.
type Prices struct {
    Monthly PriceData
    Annual  PriceData
}

// PriceProvider returns the current pair of plan prices.
type PriceProvider interface {
    GetPrices(ctx context.Context) (Prices, error)
}

// NewCachedPriceProvider builds a PriceProvider that fetches both prices from
// the given Client on first use and caches the result for ttl.
func NewCachedPriceProvider(c Client, monthlyID, annualID string, ttl time.Duration) PriceProvider
```

Internal struct:
```go
type cachedPriceProvider struct {
    client                  Client
    monthlyID, annualID     string
    ttl                     time.Duration

    mu      sync.Mutex
    cache   Prices
    fetched time.Time
    valid   bool
}
```

`GetPrices` logic:
1. Lock; if `valid && time.Since(fetched) < ttl`, return cached copy; unlock.
2. Otherwise unlock, call `client.GetPrice(ctx, monthlyID)` and `client.GetPrice(ctx, annualID)` sequentially.
3. On any error, return `(Prices{}, err)` without modifying cache.
4. On success: lock, write `cache`, `fetched = time.Now()`, `valid = true`, unlock, return.

Concurrent cold cache callers may double-fetch — accepted, since the window is small and the cost is two Stripe calls. `singleflight` is not introduced (YAGNI).

### `Client` interface — `internal/billing/client.go`

Add one method:
```go
GetPrice(ctx context.Context, priceID string) (PriceData, error)
```

- `StripeClient.GetPrice` (new method in `internal/billing/stripe_client.go`) calls `github.com/stripe/stripe-go/v76/price.Get(priceID, nil)` and maps:
  - `p.UnitAmount` → `Amount`
  - `string(p.Currency)` → `Currency` (already lowercase per Stripe)
  - If `p.Recurring != nil`: `string(p.Recurring.Interval)` → `Interval`; else `""`.
  - Returns a wrapped error on failure (`fmt.Errorf("stripe get price:\n%w", err)`).
- `NoopClient.GetPrice` returns `(PriceData{}, myErrors.ErrNotImplemented)`.

### Handler changes — `api/handler/billing.go`

`BillingHandler` gains:
```go
prices billingadapter.PriceProvider
```
Threaded through `NewBillingHandler` as a new parameter (call sites in `cmd/app/routes.go:48` and `:97` updated).

`GetPlans` rewritten:
1. `p, err := h.prices.GetPrices(r.Context())`
2. On error → `httpx.WriteError` with `&myErrors.AppError{Code:"prices_unavailable", Message:"pricing temporarily unavailable", Wrapped: myErrors.ErrStripe}`. The existing mapping in `internal/httpx/errors.go:42` renders this as HTTP 502.
3. Validate currencies: if `p.Monthly.Currency != "eur"` or `p.Annual.Currency != "eur"`, log a warning and return the same 502.
4. Build tiles:
   ```go
   monthly := planTile{Plan: "pro_monthly", PriceEur: float64(p.Monthly.Amount) / 100.0, Interval: "month"}
   annual  := planTile{Plan: "pro_annual",  PriceEur: float64(p.Annual.Amount)  / 100.0, Interval: "year"}
   if p.Monthly.Amount > 0 {
       raw := 1.0 - float64(p.Annual.Amount)/float64(p.Monthly.Amount*12)
       d := int(math.Round(raw * 100))
       if d > 0 {
           annual.DiscountPct = &d
       }
   }
   ```
5. Write `[]planTile{monthly, annual}` as JSON.

The `planTile` struct stays unchanged: `Plan`, `PriceEur`, `Interval`, `DiscountPct *int`. No API shape change for the frontend.

### Wiring — `cmd/app/deps.go` and `cmd/app/routes.go`

In `deps.go` (or wherever the billing handler is constructed), build the provider after the Stripe client exists:
```go
prices := billingadapter.NewCachedPriceProvider(
    stripeClient,
    cfg.StripePriceProMonth,
    cfg.StripePriceProAnnual,
    5*time.Minute,
)
```
Pass `prices` into `handler.NewBillingHandler(...)`.

For tests (`cmd/app/e2e_test.go`, `e2e_ai_test.go`), pass a small fake `PriceProvider` that returns canned `Prices{}` — the existing e2e tests don't exercise `GET /billing/plans`, so a `NoopClient`-style stub returning a deterministic pair is enough.

## Error Handling

| Scenario | Behavior |
|---|---|
| Stripe call errors (network, 401, etc.) | Provider returns error → handler 502 |
| Currency ≠ EUR for either price | Handler 502 + log warning |
| `monthly.Amount == 0` | Discount field omitted (avoid div by zero) |
| Discount ≤ 0 (annual ≥ 12× monthly) | Discount field omitted |
| Cache valid but expired during outage | Cold path runs, errors propagate as 503 (no stale fallback) |

## Testing

### Unit — `internal/billing/price_provider_test.go` (new)

Uses a small in-package fake `Client` that records `GetPrice` calls and returns canned `PriceData`.

- **CacheHitSkipsFetch:** Two `GetPrices` calls in succession trigger only two Stripe calls total (the first fetch fetches both prices, the second hits cache).
- **CacheExpiresAfterTTL:** With `ttl = 0`, every call refetches.
- **ClientErrorPropagates:** Fake client returns error on monthly fetch → `GetPrices` returns error, cache stays invalid; subsequent successful fetch populates cache.
- **ConcurrentReadsAreSafe:** 50 goroutines call `GetPrices` concurrently; no panic, all return same `Prices`. (Race detector run via `go test -race`.)

### Handler — `api/handler/billing_test.go` (extend)

Uses a fake `PriceProvider` injected via constructor.

- **GetPlans_HappyPath:** Provider returns monthly=699c, annual=5999c, both EUR → response has `priceEur: 6.99`, `priceEur: 59.99`, `discountPct: 29`.
- **GetPlans_NoDiscount:** Annual = monthly × 12 → no `discountPct` field.
- **GetPlans_NegativeDiscount:** Annual > monthly × 12 → no `discountPct` field.
- **GetPlans_ProviderError:** Provider returns error → 502.
- **GetPlans_NonEUR:** Currency = "usd" → 502.
- **GetPlans_ZeroMonthly:** `monthly.Amount == 0` → response renders, no discount, no panic.

## Files Touched

| File | Change |
|---|---|
| `internal/billing/price_provider.go` | New file — types, interface, cached impl |
| `internal/billing/client.go` | Add `GetPrice` to `Client`; add `NoopClient.GetPrice` |
| `internal/billing/stripe_client.go` | Implement `GetPrice` using `price.Get` |
| `internal/billing/price_provider_test.go` | New file — unit tests |
| `api/handler/billing.go` | `BillingHandler` gains `prices` field, constructor param, rewritten `GetPlans` |
| `api/handler/billing_test.go` | New `GetPlans_*` test cases |
| `cmd/app/deps.go` | Build `cachedPriceProvider`, pass into handler |
| `cmd/app/routes.go` | Update both `NewBillingHandler(...)` call sites |
| `cmd/app/e2e_test.go`, `cmd/app/e2e_ai_test.go` | Pass a stub `PriceProvider` |

## Out of Scope

- Surfacing the live price during checkout. Checkout already uses `STRIPE_PRICE_*` IDs directly (server-side via `Service.CreateCheckoutSession`), so Stripe charges the correct amount regardless of what the tile shows.
- Multi-currency support. Currency is fixed at EUR per the field name `priceEur`. A future API revision could add a generic `price` + `currency` pair.
- Background refresh / pre-warming the cache at startup. Cold-start latency on the first request after boot is accepted.
- Webhook-driven cache invalidation on `price.updated`. Pricing changes are rare enough that a 5-min TTL is acceptable.
