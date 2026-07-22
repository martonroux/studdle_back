package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"studdle/backend/api/handler"
	"studdle/backend/internal/aiProvider"
	"studdle/backend/internal/billing"
	"studdle/backend/internal/http/middleware"
	jwtsigner "studdle/backend/internal/jwt"
	"studdle/backend/pkg/access"
	"studdle/backend/pkg/aipipeline"
	pkgbilling "studdle/backend/pkg/billing"
	"studdle/backend/testutil"
)

func TestQuota_ReturnsSnapshotForAuthenticatedUser(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	testutil.SeedQuotaAt(t, pool, u.ID, "prompt_calls", 4)

	srv := newAIQuotaServer(t, pool)
	tok := mintToken(t, u.ID, true, false)

	req := httptest.NewRequest("GET", "/ai/quota", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var body aipipeline.QuotaSnapshot
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.AIAccess {
		t.Error("AIAccess = false, want true")
	}
	if body.Prompt.Used != 4 || body.Prompt.Limit != 20 {
		t.Errorf("prompt = (%d/%d), want (4/20)", body.Prompt.Used, body.Prompt.Limit)
	}
}

// TestQuota_IncludesPlanBucket is a regression test for AI-5: plan_calls is a
// real, enforced daily quota (default 5/day) that GET /ai/quota previously
// omitted from the response entirely.
func TestQuota_IncludesPlanBucket(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	testutil.SeedQuotaAt(t, pool, u.ID, "plan_calls", 2)

	srv := newAIQuotaServer(t, pool)
	tok := mintToken(t, u.ID, true, false)

	req := httptest.NewRequest("GET", "/ai/quota", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var body aipipeline.QuotaSnapshot
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Plan.Used != 2 || body.Plan.Limit != 5 {
		t.Errorf("plan = (%d/%d), want (2/5)", body.Plan.Used, body.Plan.Limit)
	}
}

func newAIQuotaServer(t *testing.T, pool *pgxpool.Pool) http.Handler {
	t.Helper()
	signer := jwtsigner.NewSigner("a-minimum-32-byte-secret-xxxxxxxxxx", "studbud-test", time.Hour)
	acc := access.NewService(pool)
	ai := aipipeline.NewService(pool, aiProvider.NoopClient{}, acc, aipipeline.DefaultQuotaLimits(), "test-model")
	_ = pkgbilling.NewService(pool, billing.NoopClient{}, pkgbilling.PriceMap{})
	h := handler.NewAIHandler(ai)
	mux := http.NewServeMux()
	stack := middleware.Chain(middleware.Auth(signer))
	mux.Handle("GET /ai/quota", stack(http.HandlerFunc(h.Quota)))
	return mux
}
