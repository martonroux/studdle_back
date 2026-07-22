package aipipeline_test

import (
	"context"
	"testing"
	"time"

	"studdle/backend/pkg/aipipeline"
	"studdle/backend/testutil"
)

func TestReapOrphanedJobs_FlipsLongRunningJobs(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	fresh := testutil.SeedRunningJob(t, pool, u.ID, "generate_prompt")
	stale := testutil.SeedRunningJob(t, pool, u.ID, "generate_pdf")
	_, _ = pool.Exec(context.Background(), `UPDATE ai_jobs SET started_at = now() - interval '2 hours' WHERE id = $1`, stale)

	svc := aipipeline.NewService(pool, nil, nil, aipipeline.DefaultQuotaLimits(), "test-model")
	n, err := svc.ReapOrphanedJobs(context.Background())
	if err != nil {
		t.Fatalf("reap: %v", err)
	}
	if n != 1 {
		t.Errorf("reaped = %d, want 1", n)
	}

	var freshStatus, staleStatus, staleErrKind string
	_ = pool.QueryRow(context.Background(), `SELECT status FROM ai_jobs WHERE id=$1`, fresh).Scan(&freshStatus)
	_ = pool.QueryRow(context.Background(), `SELECT status, error_kind FROM ai_jobs WHERE id=$1`, stale).Scan(&staleStatus, &staleErrKind)
	if freshStatus != "running" {
		t.Errorf("fresh status = %q, want running", freshStatus)
	}
	if staleStatus != "failed" || staleErrKind != "orphaned" {
		t.Errorf("stale = (%q, %q), want (failed, orphaned)", staleStatus, staleErrKind)
	}
	_ = time.Second
}
