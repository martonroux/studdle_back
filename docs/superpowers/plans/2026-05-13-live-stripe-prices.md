# Live Stripe Prices Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace hardcoded pricing tiles in `GET /billing/plans` with live Stripe prices, cached for 5 minutes, with auto-computed discount.

**Architecture:** New `PriceProvider` interface in `internal/billing` with a TTL-cached implementation that wraps a new `Client.GetPrice` method. The `BillingHandler` is rewritten to inject the provider, compute the discount from real amounts, and return 502 on Stripe failure.

**Tech Stack:** Go 1.22, `stripe-go/v76`, existing `httpx`/`myErrors` helpers.

**Spec:** `docs/superpowers/specs/2026-05-13-live-stripe-prices-design.md`

---

## File Structure

| File | Status | Responsibility |
|---|---|---|
| `internal/billing/price_provider.go` | New | `PriceData`, `Prices`, `PriceProvider` types + `cachedPriceProvider` |
| `internal/billing/price_provider_test.go` | New | Unit tests for `cachedPriceProvider` |
| `internal/billing/client.go` | Modify | Add `GetPrice` to `Client` interface and `NoopClient` |
| `internal/billing/stripe_client.go` | Modify | Implement `StripeClient.GetPrice` |
| `testutil/stripe.go` | Modify | Add `GetPrice` (with configurable canned data) to `FakeBilling` |
| `api/handler/billing.go` | Modify | `BillingHandler` gains `prices` field; rewrite `GetPlans` |
| `api/handler/billing_plans_test.go` | Modify | Test cases for live prices, errors, discount, currency |
| `cmd/app/deps.go` | Modify | Build `cachedPriceProvider`, expose on `deps` |
| `cmd/app/routes.go` | Modify | Pass `prices` into both `NewBillingHandler` call sites |
| `cmd/app/e2e_test.go`, `cmd/app/e2e_ai_test.go` | Modify | No changes expected — e2e tests don't hit `/billing/plans`. Verify they still pass. |

---

## Task 1: Extend `Client` interface with `GetPrice`

**Files:**
- Modify: `internal/billing/client.go`
- Modify: `testutil/stripe.go`

This task adds the new method to the interface and to both stub implementations (`NoopClient`, `FakeBilling`). `StripeClient.GetPrice` lands in a later task. After this, the project must still build.

- [ ] **Step 1: Edit `internal/billing/client.go` — define `PriceData`**

Insert after the `WebhookEvent` block (around line 45), before the `Client` interface:

```go
// PriceData is a Stripe Price flattened to the fields the pricing UI needs.
type PriceData struct {
	Amount   int64  // Amount is the price in the smallest currency unit (e.g. cents).
	Currency string // Currency is the ISO 4217 lowercase code (e.g. "eur").
	Interval string // Interval is "month" or "year" for recurring prices, "" otherwise.
}
```

- [ ] **Step 2: Edit `internal/billing/client.go` — add method to `Client` interface**

Add this line inside the `Client` interface (after `ConstructWebhookEvent`):

```go
	GetPrice(ctx context.Context, priceID string) (PriceData, error)
```

- [ ] **Step 3: Edit `internal/billing/client.go` — add `NoopClient.GetPrice`**

Append after `NoopClient.ConstructWebhookEvent`:

```go
// GetPrice returns ErrNotImplemented.
func (NoopClient) GetPrice(ctx context.Context, priceID string) (PriceData, error) {
	return PriceData{}, myErrors.ErrNotImplemented
}
```

- [ ] **Step 4: Edit `testutil/stripe.go` — add canned price field + method**

Add a `Price` field to the `FakeBilling` struct:

```go
	Price billing.PriceData
```

And append this method:

```go
// GetPrice returns the configured PriceData regardless of priceID.
func (f *FakeBilling) GetPrice(ctx context.Context, priceID string) (billing.PriceData, error) {
	return f.Price, nil
}
```

- [ ] **Step 5: Verify the project still builds**

Run: `cd /Users/martonroux/Documents/WEB/studbud_3/backend && go build ./...`
Expected: no errors. (Any other `Client` implementers in the codebase will surface here; if so, add a `GetPrice` stub returning zero value + `nil` to keep them compiling.)

- [ ] **Step 6: Commit**

```bash
git add internal/billing/client.go testutil/stripe.go
git commit -m "$(cat <<'EOF'
Spec C: add GetPrice to billing.Client interface

Adds PriceData type and GetPrice method to Client, plus stubs on
NoopClient and FakeBilling. No call sites yet — that comes with the
cached price provider.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: Implement `StripeClient.GetPrice`

**Files:**
- Modify: `internal/billing/stripe_client.go`

This wraps the Stripe SDK call. No unit test (would require a Stripe HTTP stub); correctness is exercised indirectly by the e2e build and any manual smoke test against a real Stripe test-mode account.

- [ ] **Step 1: Edit `internal/billing/stripe_client.go` — add import**

Add this import to the existing import block:

```go
	"github.com/stripe/stripe-go/v76/price"
```

- [ ] **Step 2: Edit `internal/billing/stripe_client.go` — append `GetPrice` method**

Append at the end of the file:

```go
// GetPrice fetches a Stripe Price by ID and maps it to PriceData.
// Non-recurring prices return an empty Interval.
func (c *StripeClient) GetPrice(ctx context.Context, priceID string) (PriceData, error) {
	p, err := price.Get(priceID, nil)
	if err != nil {
		return PriceData{}, fmt.Errorf("stripe get price %s:\n%w", priceID, err)
	}
	out := PriceData{
		Amount:   p.UnitAmount,
		Currency: string(p.Currency),
	}
	if p.Recurring != nil {
		out.Interval = string(p.Recurring.Interval)
	}
	return out, nil
}
```

- [ ] **Step 3: Verify the project builds**

Run: `cd /Users/martonroux/Documents/WEB/studbud_3/backend && go build ./...`
Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add internal/billing/stripe_client.go
git commit -m "$(cat <<'EOF'
Spec C: implement StripeClient.GetPrice

Wraps stripe-go price.Get and maps UnitAmount, Currency, and
Recurring.Interval into PriceData. Errors wrap the Stripe error
with the price id for traceability.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: TDD `cachedPriceProvider`

**Files:**
- Create: `internal/billing/price_provider.go`
- Create: `internal/billing/price_provider_test.go`

TDD: tests first, then implementation. The provider exposes `GetPrices(ctx) (Prices, error)`, caches the pair, and refetches when TTL expires.

- [ ] **Step 1: Create `internal/billing/price_provider_test.go` with cache hit + miss tests**

Write this file:

```go
package billing_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"studbud/backend/internal/billing"
)

// fakeClient counts GetPrice calls and returns canned PriceData per ID.
type fakeClient struct {
	billing.NoopClient
	prices map[string]billing.PriceData
	err    error
	calls  int64
}

func (f *fakeClient) GetPrice(ctx context.Context, priceID string) (billing.PriceData, error) {
	atomic.AddInt64(&f.calls, 1)
	if f.err != nil {
		return billing.PriceData{}, f.err
	}
	p, ok := f.prices[priceID]
	if !ok {
		return billing.PriceData{}, errors.New("unknown price id")
	}
	return p, nil
}

func newFake() *fakeClient {
	return &fakeClient{
		prices: map[string]billing.PriceData{
			"price_monthly": {Amount: 699, Currency: "eur", Interval: "month"},
			"price_annual":  {Amount: 5999, Currency: "eur", Interval: "year"},
		},
	}
}

func TestCachedPriceProvider_FirstCallFetches(t *testing.T) {
	fc := newFake()
	p := billing.NewCachedPriceProvider(fc, "price_monthly", "price_annual", time.Minute)

	out, err := p.GetPrices(context.Background())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if out.Monthly.Amount != 699 || out.Annual.Amount != 5999 {
		t.Fatalf("got %+v", out)
	}
	if got := atomic.LoadInt64(&fc.calls); got != 2 {
		t.Fatalf("calls = %d, want 2 (one per price)", got)
	}
}

func TestCachedPriceProvider_SecondCallHitsCache(t *testing.T) {
	fc := newFake()
	p := billing.NewCachedPriceProvider(fc, "price_monthly", "price_annual", time.Minute)

	if _, err := p.GetPrices(context.Background()); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := p.GetPrices(context.Background()); err != nil {
		t.Fatalf("second: %v", err)
	}
	if got := atomic.LoadInt64(&fc.calls); got != 2 {
		t.Fatalf("calls = %d, want 2 total (cache hit on second call)", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/martonroux/Documents/WEB/studbud_3/backend && go test ./internal/billing/ -run TestCachedPriceProvider -v`
Expected: build failure — `undefined: billing.NewCachedPriceProvider`, etc.

- [ ] **Step 3: Create `internal/billing/price_provider.go` with types + cached impl**

Write this file:

```go
package billing

import (
	"context"
	"sync"
	"time"
)

// Prices is the pair of plan prices the pricing UI displays.
type Prices struct {
	Monthly PriceData
	Annual  PriceData
}

// PriceProvider returns the current pair of plan prices.
type PriceProvider interface {
	GetPrices(ctx context.Context) (Prices, error)
}

// NewCachedPriceProvider builds a PriceProvider that fetches both prices
// from c on first use and caches the result for ttl. Concurrent cold-cache
// callers may double-fetch; the window is small and singleflight is YAGNI.
func NewCachedPriceProvider(c Client, monthlyID, annualID string, ttl time.Duration) PriceProvider {
	return &cachedPriceProvider{
		client:    c,
		monthlyID: monthlyID,
		annualID:  annualID,
		ttl:       ttl,
	}
}

type cachedPriceProvider struct {
	client              Client
	monthlyID, annualID string
	ttl                 time.Duration

	mu      sync.Mutex
	cache   Prices
	fetched time.Time
	valid   bool
}

func (p *cachedPriceProvider) GetPrices(ctx context.Context) (Prices, error) {
	p.mu.Lock()
	if p.valid && time.Since(p.fetched) < p.ttl {
		out := p.cache
		p.mu.Unlock()
		return out, nil
	}
	p.mu.Unlock()

	monthly, err := p.client.GetPrice(ctx, p.monthlyID)
	if err != nil {
		return Prices{}, err
	}
	annual, err := p.client.GetPrice(ctx, p.annualID)
	if err != nil {
		return Prices{}, err
	}

	out := Prices{Monthly: monthly, Annual: annual}
	p.mu.Lock()
	p.cache = out
	p.fetched = time.Now()
	p.valid = true
	p.mu.Unlock()
	return out, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/billing/ -run TestCachedPriceProvider -v`
Expected: both tests PASS.

- [ ] **Step 5: Append TTL-expiry and error tests to `price_provider_test.go`**

Append at the bottom:

```go
func TestCachedPriceProvider_RefetchesAfterTTL(t *testing.T) {
	fc := newFake()
	p := billing.NewCachedPriceProvider(fc, "price_monthly", "price_annual", time.Nanosecond)

	if _, err := p.GetPrices(context.Background()); err != nil {
		t.Fatalf("first: %v", err)
	}
	time.Sleep(time.Millisecond) // ensure TTL elapsed
	if _, err := p.GetPrices(context.Background()); err != nil {
		t.Fatalf("second: %v", err)
	}
	if got := atomic.LoadInt64(&fc.calls); got != 4 {
		t.Fatalf("calls = %d, want 4 (two per fetch)", got)
	}
}

func TestCachedPriceProvider_ErrorDoesNotPoisonCache(t *testing.T) {
	fc := newFake()
	fc.err = errors.New("boom")
	p := billing.NewCachedPriceProvider(fc, "price_monthly", "price_annual", time.Minute)

	if _, err := p.GetPrices(context.Background()); err == nil {
		t.Fatal("expected error")
	}
	fc.err = nil
	out, err := p.GetPrices(context.Background())
	if err != nil {
		t.Fatalf("recovery: %v", err)
	}
	if out.Monthly.Amount != 699 {
		t.Fatalf("got %+v", out)
	}
}
```

- [ ] **Step 6: Run tests; verify all four pass**

Run: `go test ./internal/billing/ -run TestCachedPriceProvider -v`
Expected: all four PASS.

- [ ] **Step 7: Append the concurrent-safety test**

Append at the bottom:

```go
func TestCachedPriceProvider_ConcurrentSafe(t *testing.T) {
	fc := newFake()
	p := billing.NewCachedPriceProvider(fc, "price_monthly", "price_annual", time.Minute)

	const n = 50
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			out, err := p.GetPrices(context.Background())
			if err != nil {
				t.Errorf("goroutine err: %v", err)
				return
			}
			if out.Monthly.Amount != 699 || out.Annual.Amount != 5999 {
				t.Errorf("goroutine got %+v", out)
			}
		}()
	}
	wg.Wait()
}
```

- [ ] **Step 8: Run all tests with race detector**

Run: `go test ./internal/billing/ -run TestCachedPriceProvider -race -v`
Expected: all five PASS, no race detected.

- [ ] **Step 9: Commit**

```bash
git add internal/billing/price_provider.go internal/billing/price_provider_test.go
git commit -m "$(cat <<'EOF'
Spec C: add cached PriceProvider

Wraps billing.Client with a 5-minute TTL cache for the monthly/annual
price pair. Concurrent cold-cache callers may double-fetch (accepted;
singleflight is YAGNI for a public pricing endpoint). Unit tests cover
cache hit, TTL refetch, error recovery, and concurrent access.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: Rewrite `BillingHandler.GetPlans` to use `PriceProvider` (TDD)

**Files:**
- Modify: `api/handler/billing.go`
- Modify: `api/handler/billing_plans_test.go`

The handler constructor gains a `PriceProvider` parameter; `GetPlans` becomes Stripe-driven. The existing `billing_plans_test.go` happy-path test gets rewritten to inject `testutil.FakeBilling` and is extended with error/discount/currency cases.

- [ ] **Step 1: Rewrite `api/handler/billing_plans_test.go` with the new test surface**

Replace the entire file contents with:

```go
package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"studbud/backend/api/handler"
	internalbilling "studbud/backend/internal/billing"
	jwtsigner "studbud/backend/internal/jwt"
	pkgbilling "studbud/backend/pkg/billing"
	pkguser "studbud/backend/pkg/user"
	"studbud/backend/testutil"
)

// stubProvider implements internalbilling.PriceProvider with canned data.
type stubProvider struct {
	prices internalbilling.Prices
	err    error
}

func (s *stubProvider) GetPrices(ctx context.Context) (internalbilling.Prices, error) {
	return s.prices, s.err
}

func newHandler(t *testing.T, prov internalbilling.PriceProvider) *handler.BillingHandler {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)

	signer := jwtsigner.NewSigner("a-minimum-32-byte-secret-xxxxxxxxxx", "studbud-test", time.Hour)
	billSvc := pkgbilling.NewService(pool, internalbilling.NoopClient{}, pkgbilling.PriceMap{})
	userSvc := pkguser.NewService(pool, signer)
	return handler.NewBillingHandler(billSvc, userSvc, prov, "https://app/billing", "https://app/pricing")
}

func okPrices() internalbilling.Prices {
	return internalbilling.Prices{
		Monthly: internalbilling.PriceData{Amount: 699, Currency: "eur", Interval: "month"},
		Annual:  internalbilling.PriceData{Amount: 5999, Currency: "eur", Interval: "year"},
	}
}

func decodeTiles(t *testing.T, w *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	var tiles []map[string]any
	if err := json.NewDecoder(w.Body).Decode(&tiles); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return tiles
}

func TestGetPlans_HappyPath(t *testing.T) {
	h := newHandler(t, &stubProvider{prices: okPrices()})
	w := httptest.NewRecorder()
	h.GetPlans(w, httptest.NewRequest("GET", "/billing/plans", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	tiles := decodeTiles(t, w)
	if len(tiles) != 2 {
		t.Fatalf("len = %d, want 2", len(tiles))
	}
	by := map[string]map[string]any{}
	for _, tile := range tiles {
		by[tile["plan"].(string)] = tile
	}
	if by["pro_monthly"]["priceEur"] != 6.99 {
		t.Fatalf("monthly priceEur = %v", by["pro_monthly"]["priceEur"])
	}
	if by["pro_annual"]["priceEur"] != 59.99 {
		t.Fatalf("annual priceEur = %v", by["pro_annual"]["priceEur"])
	}
	if by["pro_annual"]["discountPct"] != float64(29) {
		t.Fatalf("discountPct = %v, want 29", by["pro_annual"]["discountPct"])
	}
}

func TestGetPlans_NoDiscountWhenAnnualEqualsMonthlyTimes12(t *testing.T) {
	h := newHandler(t, &stubProvider{prices: internalbilling.Prices{
		Monthly: internalbilling.PriceData{Amount: 1000, Currency: "eur", Interval: "month"},
		Annual:  internalbilling.PriceData{Amount: 12000, Currency: "eur", Interval: "year"},
	}})
	w := httptest.NewRecorder()
	h.GetPlans(w, httptest.NewRequest("GET", "/billing/plans", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	tiles := decodeTiles(t, w)
	for _, tile := range tiles {
		if tile["plan"] == "pro_annual" {
			if _, present := tile["discountPct"]; present {
				t.Fatalf("discountPct should be omitted when there is no discount, got %v", tile["discountPct"])
			}
		}
	}
}

func TestGetPlans_NoDiscountWhenAnnualExceedsMonthlyTimes12(t *testing.T) {
	h := newHandler(t, &stubProvider{prices: internalbilling.Prices{
		Monthly: internalbilling.PriceData{Amount: 1000, Currency: "eur", Interval: "month"},
		Annual:  internalbilling.PriceData{Amount: 13000, Currency: "eur", Interval: "year"},
	}})
	w := httptest.NewRecorder()
	h.GetPlans(w, httptest.NewRequest("GET", "/billing/plans", nil))

	tiles := decodeTiles(t, w)
	for _, tile := range tiles {
		if tile["plan"] == "pro_annual" {
			if _, present := tile["discountPct"]; present {
				t.Fatalf("discountPct should be omitted when annual > monthly*12")
			}
		}
	}
}

func TestGetPlans_ProviderErrorReturns502(t *testing.T) {
	h := newHandler(t, &stubProvider{err: errors.New("stripe down")})
	w := httptest.NewRecorder()
	h.GetPlans(w, httptest.NewRequest("GET", "/billing/plans", nil))

	if w.Code != http.StatusBadGateway {
		t.Fatalf("status %d, want 502; body %s", w.Code, w.Body.String())
	}
}

func TestGetPlans_NonEURReturns502(t *testing.T) {
	h := newHandler(t, &stubProvider{prices: internalbilling.Prices{
		Monthly: internalbilling.PriceData{Amount: 699, Currency: "usd", Interval: "month"},
		Annual:  internalbilling.PriceData{Amount: 5999, Currency: "usd", Interval: "year"},
	}})
	w := httptest.NewRecorder()
	h.GetPlans(w, httptest.NewRequest("GET", "/billing/plans", nil))

	if w.Code != http.StatusBadGateway {
		t.Fatalf("status %d, want 502", w.Code)
	}
}

func TestGetPlans_ZeroMonthlyOmitsDiscount(t *testing.T) {
	h := newHandler(t, &stubProvider{prices: internalbilling.Prices{
		Monthly: internalbilling.PriceData{Amount: 0, Currency: "eur", Interval: "month"},
		Annual:  internalbilling.PriceData{Amount: 5999, Currency: "eur", Interval: "year"},
	}})
	w := httptest.NewRecorder()
	h.GetPlans(w, httptest.NewRequest("GET", "/billing/plans", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	tiles := decodeTiles(t, w)
	for _, tile := range tiles {
		if tile["plan"] == "pro_annual" {
			if _, present := tile["discountPct"]; present {
				t.Fatalf("discountPct should be omitted when monthly is 0")
			}
		}
	}
}

```

- [ ] **Step 2: Run the tests; verify they fail to compile**

Run: `cd /Users/martonroux/Documents/WEB/studbud_3/backend && go test ./api/handler/ -run TestGetPlans -v`
Expected: build failure — `NewBillingHandler` signature mismatch, `BillingHandler` has no `prices` field.

- [ ] **Step 3: Edit `api/handler/billing.go` — imports**

In the existing import block, add:

```go
	"log"
	"math"

	billingadapter "studbud/backend/internal/billing"
```

(`billingadapter` is the alias used elsewhere in the codebase — see `cmd/app/deps.go:10`.)

- [ ] **Step 4: Edit `api/handler/billing.go` — `BillingHandler` struct**

Replace the existing struct:

```go
type BillingHandler struct {
	svc            *billing.Service
	users          *user.Service
	prices         billingadapter.PriceProvider
	billingPageURL string
	pricingPageURL string
	expectLive     bool
	limMu          sync.Mutex
	lim            map[int64]*rate.Limiter
}
```

- [ ] **Step 5: Edit `api/handler/billing.go` — `NewBillingHandler`**

Replace the constructor:

```go
func NewBillingHandler(svc *billing.Service, users *user.Service, prices billingadapter.PriceProvider, billingPageURL, pricingPageURL string) *BillingHandler {
	return &BillingHandler{
		svc:            svc,
		users:          users,
		prices:         prices,
		billingPageURL: billingPageURL,
		pricingPageURL: pricingPageURL,
		lim:            map[int64]*rate.Limiter{},
	}
}
```

- [ ] **Step 6: Edit `api/handler/billing.go` — rewrite `GetPlans`**

Replace the body of `GetPlans` (currently lines 119–126):

```go
func (h *BillingHandler) GetPlans(w http.ResponseWriter, r *http.Request) {
	p, err := h.prices.GetPrices(r.Context())
	if err != nil {
		httpx.WriteError(w, &myErrors.AppError{
			Code:    "prices_unavailable",
			Message: "pricing temporarily unavailable",
			Wrapped: myErrors.ErrStripe,
		})
		return
	}
	if p.Monthly.Currency != "eur" || p.Annual.Currency != "eur" {
		log.Printf("billing.GetPlans: non-EUR currency monthly=%q annual=%q", p.Monthly.Currency, p.Annual.Currency)
		httpx.WriteError(w, &myErrors.AppError{
			Code:    "prices_unavailable",
			Message: "pricing temporarily unavailable",
			Wrapped: myErrors.ErrStripe,
		})
		return
	}

	monthly := planTile{
		Plan:     "pro_monthly",
		PriceEur: float64(p.Monthly.Amount) / 100.0,
		Interval: "month",
	}
	annual := planTile{
		Plan:     "pro_annual",
		PriceEur: float64(p.Annual.Amount) / 100.0,
		Interval: "year",
	}
	if p.Monthly.Amount > 0 {
		raw := 1.0 - float64(p.Annual.Amount)/float64(p.Monthly.Amount*12)
		d := int(math.Round(raw * 100))
		if d > 0 {
			annual.DiscountPct = &d
		}
	}
	httpx.WriteJSON(w, http.StatusOK, []planTile{monthly, annual})
}
```

- [ ] **Step 7: Run the handler tests**

Run: `go test ./api/handler/ -run TestGetPlans -v`
Expected: all six PASS.

- [ ] **Step 8: Run the full handler test suite to confirm no regressions**

Run: `go test ./api/handler/ -v`
Expected: every test PASSES. If other billing handler tests break because the constructor signature changed, fix them by passing `&testutil.FakeBilling{}` is the wrong fix — those tests construct the handler via different setup paths. Check `billing_checkout_test.go`, `billing_portal_test.go`, etc., and add a `PriceProvider` argument (a small inline stub returning `internalbilling.Prices{}` is fine — they don't exercise `GetPlans`).

- [ ] **Step 9: Commit**

```bash
git add api/handler/billing.go api/handler/billing_plans_test.go api/handler/billing_checkout_test.go api/handler/billing_portal_test.go api/handler/billing_refresh_test.go api/handler/billing_subscription_test.go api/handler/billing_webhook_test.go
git commit -m "$(cat <<'EOF'
Spec C: GetPlans now reads live prices from Stripe

BillingHandler gains a PriceProvider dependency. GetPlans fetches the
monthly/annual pair, validates currency, computes the discount
percentage from the actual amounts, and returns 502 (via ErrStripe)
when Stripe is unreachable or returns a non-EUR currency.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: Wire the cached provider into `cmd/app`

**Files:**
- Modify: `cmd/app/deps.go`
- Modify: `cmd/app/routes.go`

The runtime needs to build a `cachedPriceProvider` and pass it to both `NewBillingHandler` call sites.

- [ ] **Step 1: Edit `cmd/app/deps.go` — add `prices` to `infra` (or `deps`)**

Find the `deps` struct (around line 60). Add this field next to `billing`:

```go
	prices billingadapter.PriceProvider // prices feeds GET /billing/plans
```

Find the `infra` struct (around line 74). Confirm `billing billingadapter.Client` is already there — it is. No change.

- [ ] **Step 2: Edit `cmd/app/deps.go` — build the provider in `buildDeps` (or equivalent)**

Locate the function that constructs `deps` (around the lines that set `billing: stubs.billing` at line 260). Just above the `return &deps{...}` block, add:

```go
	pricesProvider := billingadapter.NewCachedPriceProvider(
		inf.billing,
		cfg.StripePriceProMonth,
		cfg.StripePriceProAnnual,
		5*time.Minute,
	)
```

Then add `prices: pricesProvider,` to the returned struct literal (next to `billing: stubs.billing,`).

Verify the `time` package is already imported in `deps.go` (it typically is — if not, add `"time"`).

- [ ] **Step 3: Edit `cmd/app/routes.go` — both handler construction sites**

Line 48 (currently):
```go
	billH := handler.NewBillingHandler(d.billing, d.user, d.cfg.AppURL+"/billing", d.cfg.AppURL+"/pricing")
```
Replace with:
```go
	billH := handler.NewBillingHandler(d.billing, d.user, d.prices, d.cfg.AppURL+"/billing", d.cfg.AppURL+"/pricing")
```

Line 97 (currently):
```go
	billH := handler.NewBillingHandler(d.billing, d.user, d.cfg.AppURL+"/billing", d.cfg.AppURL+"/pricing")
```
Replace with:
```go
	billH := handler.NewBillingHandler(d.billing, d.user, d.prices, d.cfg.AppURL+"/billing", d.cfg.AppURL+"/pricing")
```

- [ ] **Step 4: Build the binary**

Run: `cd /Users/martonroux/Documents/WEB/studbud_3/backend && go build ./...`
Expected: no errors. Any remaining call sites needing the new arg surface here.

- [ ] **Step 5: Run the full test suite**

Run: `go test ./...`
Expected: all PASS. The e2e tests use `NoopClient` (returns `ErrNotImplemented` for `GetPrice`), so any e2e test that hits `/billing/plans` would now fail with 502 — confirm none do by checking the output. If one does, swap to a `testutil.FakeBilling{Price: billing.PriceData{Amount: 699, Currency: "eur", Interval: "month"}}` in that test's setup.

- [ ] **Step 6: Commit**

```bash
git add cmd/app/deps.go cmd/app/routes.go
git commit -m "$(cat <<'EOF'
Spec C: wire cached PriceProvider into the app deps

Builds a 5-minute TTL cached provider over the configured Stripe price
IDs at startup and threads it through to both BillingHandler call
sites.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: Manual smoke test

**Files:** None.

End-to-end check against real Stripe test mode, since `StripeClient.GetPrice` has no unit test.

- [ ] **Step 1: Confirm `.env` is configured**

Verify `STRIPE_SECRET_KEY`, `STRIPE_PRICE_PRO_MONTHLY`, `STRIPE_PRICE_PRO_ANNUAL` are set with real test-mode values.

- [ ] **Step 2: Start the backend**

Run: `./launch_app.sh`
Expected: server boots without "STRIPE_..." validation errors.

- [ ] **Step 3: Hit the endpoint**

Run: `curl -s http://localhost:8080/billing/plans | jq .`
Expected: two tiles with `priceEur` matching the amounts in your Stripe dashboard (divided by 100), and a `discountPct` on the annual tile derived from the two amounts.

- [ ] **Step 4: Tamper test — verify caching**

In Stripe dashboard, edit one price (e.g., set monthly from 699 → 799). Re-curl within 5 minutes: expect the OLD value (cache). Wait >5 minutes, re-curl: expect the NEW value. Roll back the dashboard change.

- [ ] **Step 5: Tamper test — verify 502 on bad config**

Stop the server, set `STRIPE_PRICE_PRO_MONTHLY=price_does_not_exist`, restart. `curl -sv http://localhost:8080/billing/plans` should return HTTP 502 with `prices_unavailable` in the body. Restore the env value.

- [ ] **Step 6: No commit**

This task only validates; nothing changed in tree.

---

## Self-Review Notes

Quickly verified against the spec:

- **Caching (TTL 5m):** Task 3 + Task 5 (`time.Minute*5`).
- **Cold-cache Stripe error → 502:** Task 4 step 6 wraps `ErrStripe`, which already maps to `StatusBadGateway` (`internal/httpx/errors.go:42`). Tested in Task 4 step 1.
- **Discount computed from prices:** Task 4 step 6 + tests for happy path, no-discount, negative-discount, zero monthly.
- **`PriceProvider` in `internal/billing`:** Task 3.
- **Currency must be EUR; else 502:** Task 4 step 6 + test.
- **Both `NewBillingHandler` call sites updated:** Task 5 step 3.
- **Test fakes updated:** Task 1 step 4 (`FakeBilling`), Task 4 step 1 (handler test stub).
- **e2e tests:** Task 5 step 5 verifies. If any e2e exercises `/billing/plans`, the engineer is instructed to swap in `testutil.FakeBilling` with canned `Price` data.

One known soft spot: Task 5 Step 2 assumes the engineer can locate the right place inside `buildDeps`/`buildStubServices` to construct the provider. The path is shown via line numbers but the actual function name varies — they may need to scan `deps.go` briefly to pick the right insertion point. Acceptable: the file is ~300 lines and the surrounding context (`billing: stubs.billing`) is unique.
