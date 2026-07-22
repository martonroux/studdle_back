package keywordWorker

import (
	"context"
	"testing"

	"studdle/backend/testutil"
)

func TestMaterialChange_Identity(t *testing.T) {
	if MaterialChange("q", "a", "q", "a") {
		t.Error("identity should not be material")
	}
}

func TestMaterialChange_TrailingWhitespace(t *testing.T) {
	if MaterialChange("hello", "world", "hello ", "world") {
		t.Error("trailing whitespace should not be material")
	}
}

func TestMaterialChange_TwentyCharAddition(t *testing.T) {
	old := "Quelle est la phase ?"
	newQ := "Quelle est la phase de la mitose dans le cycle cellulaire ?"
	if !MaterialChange(old, "answer", newQ, "answer") {
		t.Error("20+ char addition should be material")
	}
}

func TestMaterialChange_FullRewrite(t *testing.T) {
	if !MaterialChange("foo", "bar", "completely different content here please", "ok") {
		t.Error("full rewrite should be material")
	}
}

func TestMaterialChange_EmptyToEmpty(t *testing.T) {
	if MaterialChange("", "", "", "") {
		t.Error("empty to empty should not be material")
	}
}

func TestMaterialChange_TypoFix(t *testing.T) {
	if MaterialChange("Quelle est la phase ?", "Phase A", "Quelle est la phase ?", "Phase A.") {
		t.Error("trailing period should not be material")
	}
}

func TestEnqueueForFlashcard_DedupesAndKeepsMaxPriority(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)

	uid := testutil.NewVerifiedUser(t, pool).ID
	subj := testutil.NewSubject(t, pool, uid)
	fcID := testutil.NewFlashcard(t, pool, subj.ID, 0, "Q", "A")

	enq := NewEnqueuer(pool)
	ctx := context.Background()

	if err := enq.EnqueueForFlashcard(ctx, fcID, PriorityUser); err != nil {
		t.Fatalf("first enqueue: %v", err)
	}

	if err := enq.EnqueueForFlashcard(ctx, fcID, PriorityRetry); err != nil {
		t.Fatalf("second enqueue: %v", err)
	}

	var n int

	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ai_extraction_jobs WHERE fc_id=$1`, fcID).Scan(&n); err != nil {
		t.Fatal(err)
	}

	if n != 1 {
		t.Fatalf("want 1 row (deduped), got %d", n)
	}

	var prio int16

	if err := pool.QueryRow(ctx, `SELECT priority FROM ai_extraction_jobs WHERE fc_id=$1`, fcID).Scan(&prio); err != nil {
		t.Fatal(err)
	}

	if prio != int16(PriorityUser) {
		t.Fatalf("want priority kept at PriorityUser=%d, got %d", PriorityUser, prio)
	}
}
