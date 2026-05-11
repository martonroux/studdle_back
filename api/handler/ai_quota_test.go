package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"studbud/backend/api/handler"
	"studbud/backend/internal/aiProvider"
	"studbud/backend/internal/billing"
	"studbud/backend/internal/http/middleware"
	jwtsigner "studbud/backend/internal/jwt"
	"studbud/backend/pkg/access"
	"studbud/backend/pkg/aipipeline"
	pkgbilling "studbud/backend/pkg/billing"
	"studbud/backend/testutil"
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
