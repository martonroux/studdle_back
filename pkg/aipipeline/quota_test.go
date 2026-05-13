package aipipeline_test

import (
	"context"
	"errors"
	"testing"

	"studbud/backend/internal/myErrors"
	"studbud/backend/pkg/access"
	"studbud/backend/pkg/aipipeline"
	"studbud/backend/testutil"
)

func TestQuotaCheck_FirstCallAllowed(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)

	svc := aipipeline.NewService(pool, nil, nil, aipipeline.DefaultQuotaLimits(), "test-model")
	if err := svc.CheckQuota(context.Background(), u.ID, aipipeline.FeatureGenerateFromPrompt, 0); err != nil {
		t.Fatalf("first-call CheckQuota: %v", err)
	}
}

func TestQuotaCheck_ExhaustedReturnsQuotaError(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.SeedQuotaAt(t, pool, u.ID, "prompt_calls", 20)

	svc := aipipeline.NewService(pool, nil, nil, aipipeline.DefaultQuotaLimits(), "test-model")
	err := svc.CheckQuota(context.Background(), u.ID, aipipeline.FeatureGenerateFromPrompt, 0)
	if !errors.Is(err, myErrors.ErrQuotaExhausted) {
		t.Fatalf("err = %v, want ErrQuotaExhausted", err)
	}
}

func TestQuotaCheck_PDFPagesSeparateFromCalls(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	// 95 pages already used; remaining budget is 5 pages.
	testutil.SeedQuotaAt(t, pool, u.ID, "pdf_pages", 95)

	svc := aipipeline.NewService(pool, nil, nil, aipipeline.DefaultQuotaLimits(), "test-model")
	ctx := context.Background()

	if err := svc.CheckQuota(ctx, u.ID, aipipeline.FeatureGenerateFromPDF, 5); err != nil {
		t.Fatalf("5-page CheckQuota: %v", err)
	}
	err := svc.CheckQuota(ctx, u.ID, aipipeline.FeatureGenerateFromPDF, 6)
	if !errors.Is(err, myErrors.ErrQuotaExhausted) {
		t.Fatalf("6-page CheckQuota err = %v, want ErrQuotaExhausted", err)
	}
}

func TestQuotaDebit_IncrementsCounter(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)

	svc := aipipeline.NewService(pool, nil, nil, aipipeline.DefaultQuotaLimits(), "test-model")
	ctx := context.Background()

	if err := svc.DebitQuota(ctx, u.ID, aipipeline.FeatureGenerateFromPrompt, 1, 0); err != nil {
		t.Fatalf("first debit: %v", err)
	}
	if err := svc.DebitQuota(ctx, u.ID, aipipeline.FeatureGenerateFromPDF, 1, 7); err != nil {
		t.Fatalf("pdf debit: %v", err)
	}

	var prompt, pdfCalls, pages int
	_ = pool.QueryRow(ctx, `SELECT prompt_calls, pdf_calls, pdf_pages FROM ai_quota_daily WHERE user_id=$1 AND day=current_date`, u.ID).Scan(&prompt, &pdfCalls, &pages)
	if prompt != 1 || pdfCalls != 1 || pages != 7 {
		t.Errorf("counters = (%d,%d,%d), want (1,1,7)", prompt, pdfCalls, pages)
	}
}

func TestQuotaSnapshot_ReflectsDebits(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	testutil.SeedQuotaAt(t, pool, u.ID, "check_calls", 12)

	acc := access.NewService(pool)
	svc := aipipeline.NewService(pool, nil, acc, aipipeline.DefaultQuotaLimits(), "test-model")
	snap, err := svc.QuotaSnapshot(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("QuotaSnapshot: %v", err)
	}
	if snap.Check.Used != 12 || snap.Check.Limit != 50 {
		t.Errorf("check = (%d/%d), want (12/50)", snap.Check.Used, snap.Check.Limit)
	}
	if !snap.AIAccess {
		t.Error("AIAccess = false, want true")
	}
}

func TestCheckAgainstLimits_PlanFeature(t *testing.T) {
	limits := aipipeline.QuotaLimits{PlanCalls: 1}
	used := map[string]int{"plan_calls": 1, "cross_subject_rank_calls": 0}
	if err := aipipeline.CheckAgainstLimitsForTest(aipipeline.FeatureGenerateRevisionPlan, used, limits, 0); err == nil {
		t.Error("want quota exhausted at limit, got nil")
	}
}

func TestCheckAgainstLimits_CrossSubjectRankNeverDebits(t *testing.T) {
	limits := aipipeline.QuotaLimits{PlanCalls: 1}
	used := map[string]int{"plan_calls": 0, "cross_subject_rank_calls": 999}
	if err := aipipeline.CheckAgainstLimitsForTest(aipipeline.FeatureCrossSubjectRank, used, limits, 0); err != nil {
		t.Errorf("cross-subject rank should always pass quota check, got %v", err)
	}
}

func TestCheckQuota_QuizCallsAllowsUnderLimit(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)

	svc := aipipeline.NewService(pool, nil, nil, aipipeline.QuotaLimits{QuizCalls: 3}, "test-model")
	if err := svc.CheckQuota(context.Background(), u.ID, aipipeline.FeatureGenerateQuiz, 0); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestCheckQuota_QuizCallsRejectsAtLimit(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.SeedQuotaAt(t, pool, u.ID, "quiz_calls", 3)

	svc := aipipeline.NewService(pool, nil, nil, aipipeline.QuotaLimits{QuizCalls: 3}, "test-model")
	err := svc.CheckQuota(context.Background(), u.ID, aipipeline.FeatureGenerateQuiz, 0)
	if !errors.Is(err, myErrors.ErrQuotaExhausted) {
		t.Fatalf("expected ErrQuotaExhausted, got %v", err)
	}
}

func TestDebitQuota_QuizCallsIncrementsRow(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)

	svc := aipipeline.NewService(pool, nil, nil, aipipeline.QuotaLimits{QuizCalls: 5}, "test-model")
	if err := svc.DebitQuota(context.Background(), u.ID, aipipeline.FeatureGenerateQuiz, 1, 0); err != nil {
		t.Fatalf("debit: %v", err)
	}

	var n int
	err := pool.QueryRow(context.Background(),
		`SELECT quiz_calls FROM ai_quota_daily WHERE user_id = $1 AND day = CURRENT_DATE`, u.ID,
	).Scan(&n)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if n != 1 {
		t.Fatalf("quiz_calls = %d, want 1", n)
	}
}
