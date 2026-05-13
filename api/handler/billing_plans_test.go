package handler_test

import (
	"encoding/json"
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

func TestGetPlans_ReturnsBothPlans(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)

	signer := jwtsigner.NewSigner("a-minimum-32-byte-secret-xxxxxxxxxx", "studbud-test", time.Hour)
	billSvc := pkgbilling.NewService(pool, internalbilling.NoopClient{}, pkgbilling.PriceMap{})
	userSvc := pkguser.NewService(pool, signer)
	h := handler.NewBillingHandler(billSvc, userSvc, "https://app/billing", "https://app/pricing")

	req := httptest.NewRequest("GET", "/billing/plans", nil)
	w := httptest.NewRecorder()
	h.GetPlans(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var tiles []map[string]any
	if err := json.NewDecoder(w.Body).Decode(&tiles); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(tiles) != 2 {
		t.Fatalf("len = %d, want 2", len(tiles))
	}

	byPlan := map[string]map[string]any{}
	for _, tile := range tiles {
		byPlan[tile["plan"].(string)] = tile
	}

	monthly, ok := byPlan["pro_monthly"]
	if !ok {
		t.Fatal("missing pro_monthly")
	}
	if monthly["priceEur"] != 6.99 {
		t.Fatalf("pro_monthly priceEur = %v, want 6.99", monthly["priceEur"])
	}
	if monthly["interval"] != "month" {
		t.Fatalf("pro_monthly interval = %v, want month", monthly["interval"])
	}

	annual, ok := byPlan["pro_annual"]
	if !ok {
		t.Fatal("missing pro_annual")
	}
	if annual["priceEur"] != 59.99 {
		t.Fatalf("pro_annual priceEur = %v, want 59.99", annual["priceEur"])
	}
	if annual["interval"] != "year" {
		t.Fatalf("pro_annual interval = %v, want year", annual["interval"])
	}
	if annual["discountPct"] == nil {
		t.Fatal("pro_annual discountPct should not be nil")
	}
}
