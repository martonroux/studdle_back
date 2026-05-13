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
	if by["pro_annual"]["discountPct"] != float64(28) {
		t.Fatalf("discountPct = %v, want 28", by["pro_annual"]["discountPct"])
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
