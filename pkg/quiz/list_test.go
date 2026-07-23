package quiz_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"studbud/backend/pkg/quiz"
	"studbud/backend/testutil"
)

// insertQuiz inserts a quizzes row with explicit kind/chapter/types and returns its id.
func insertQuiz(t *testing.T, pool *pgxpool.Pool, uid, subjectID int64, chapterID *int64, kind, types string, questionCount int) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(context.Background(), `
		INSERT INTO quizzes (user_id, subject_id, chapter_id, kind, source,
		                     card_pool_jsonb, settings_jsonb, question_count, model, prompt_hash)
		VALUES ($1, $2, $3, $4, 'user', '[]'::jsonb, $5::jsonb, $6, 'test', 'h')
		RETURNING id`,
		uid, subjectID, chapterID, kind, types, questionCount,
	).Scan(&id)
	if err != nil {
		t.Fatalf("insert quiz: %v", err)
	}
	return id
}

func TestList_NewestFirst_WithNamesAndTypes(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	sub := testutil.NewSubjectNamed(t, pool, u.ID, "Biology", "private")
	chap := testutil.NewChapter(t, pool, sub.ID, "Chapter 3")

	q1 := insertQuiz(t, pool, u.ID, sub.ID, nil, "global", `{"types":["multi_choice"]}`, 5)
	q2 := insertQuiz(t, pool, u.ID, sub.ID, &chap, "specific", `{"types":["multi_choice","true_false"]}`, 10)

	if _, err := pool.Exec(context.Background(),
		`UPDATE quizzes SET created_at = now() - interval '1 hour' WHERE id = $1`, q1); err != nil {
		t.Fatalf("backdate q1: %v", err)
	}

	svc := quiz.NewService(pool, nil)
	res, err := svc.List(context.Background(), quiz.ListRequest{UserID: u.ID})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if res.Total != 2 {
		t.Fatalf("total = %d, want 2", res.Total)
	}
	if len(res.Quizzes) != 2 {
		t.Fatalf("quizzes = %d, want 2", len(res.Quizzes))
	}
	if res.Quizzes[0].ID != q2 || res.Quizzes[1].ID != q1 {
		t.Fatalf("order = [%d,%d], want newest-first [%d,%d]", res.Quizzes[0].ID, res.Quizzes[1].ID, q2, q1)
	}

	specific := res.Quizzes[0]
	if specific.SubjectName != "Biology" {
		t.Fatalf("subjectName = %q, want Biology", specific.SubjectName)
	}
	if specific.ChapterID == nil || *specific.ChapterID != chap {
		t.Fatalf("chapterID = %v, want %d", specific.ChapterID, chap)
	}
	if specific.ChapterName == nil || *specific.ChapterName != "Chapter 3" {
		t.Fatalf("chapterName = %v, want Chapter 3", specific.ChapterName)
	}
	if len(specific.Types) != 2 || specific.Types[0] != quiz.QTypeMultiChoice || specific.Types[1] != quiz.QTypeTrueFalse {
		t.Fatalf("types = %v, want [multi_choice true_false]", specific.Types)
	}

	global := res.Quizzes[1]
	if global.ChapterID != nil || global.ChapterName != nil {
		t.Fatalf("global quiz should have nil chapter, got id=%v name=%v", global.ChapterID, global.ChapterName)
	}
}

func TestList_AttemptSummary(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	sub := testutil.NewSubjectNamed(t, pool, u.ID, "Bio", "private")
	qid := insertQuiz(t, pool, u.ID, sub.ID, nil, "global", `{"types":["multi_choice"]}`, 10)

	ctx := context.Background()
	insertAttempt := func(state string, scorePct *int, completedAt *time.Time) {
		var id int64
		err := pool.QueryRow(ctx, `
			INSERT INTO quiz_attempts (quiz_id, user_id, state, total_count, score_pct, completed_at)
			VALUES ($1, $2, $3, 10, $4, $5) RETURNING id`,
			qid, u.ID, state, scorePct, completedAt,
		).Scan(&id)
		if err != nil {
			t.Fatalf("insert attempt: %v", err)
		}
	}
	earlier := time.Now().Add(-2 * time.Hour)
	later := time.Now().Add(-1 * time.Hour)
	best, last := 90, 80
	insertAttempt("completed", &best, &earlier)
	insertAttempt("completed", &last, &later)

	svc := quiz.NewService(pool, nil)
	res, err := svc.List(ctx, quiz.ListRequest{UserID: u.ID})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(res.Quizzes) != 1 {
		t.Fatalf("quizzes = %d, want 1", len(res.Quizzes))
	}
	att := res.Quizzes[0].Attempts
	if att.CompletedCount != 2 {
		t.Fatalf("completedCount = %d, want 2", att.CompletedCount)
	}
	if att.BestScorePct == nil || *att.BestScorePct != 90 {
		t.Fatalf("bestScorePct = %v, want 90", att.BestScorePct)
	}
	if att.LastScorePct == nil || *att.LastScorePct != 80 {
		t.Fatalf("lastScorePct = %v, want 80", att.LastScorePct)
	}
	if att.LastCompletedAt == nil {
		t.Fatal("lastCompletedAt = nil, want set")
	}
	if att.InProgressAttemptID != nil {
		t.Fatalf("inProgressAttemptId = %v, want nil", att.InProgressAttemptID)
	}
}

func TestList_InProgressAttemptID(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	sub := testutil.NewSubjectNamed(t, pool, u.ID, "Bio", "private")
	qid := insertQuiz(t, pool, u.ID, sub.ID, nil, "global", `{"types":["multi_choice"]}`, 10)

	svc := quiz.NewService(pool, nil)
	att, err := svc.Retake(context.Background(), u.ID, qid)
	if err != nil {
		t.Fatalf("retake (create in-progress attempt): %v", err)
	}

	res, err := svc.List(context.Background(), quiz.ListRequest{UserID: u.ID})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(res.Quizzes) != 1 {
		t.Fatalf("quizzes = %d, want 1", len(res.Quizzes))
	}
	got := res.Quizzes[0].Attempts.InProgressAttemptID
	if got == nil || *got != att.ID {
		t.Fatalf("inProgressAttemptId = %v, want %d", got, att.ID)
	}
}

func TestList_SubjectFilter(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	subA := testutil.NewSubjectNamed(t, pool, u.ID, "A", "private")
	subB := testutil.NewSubjectNamed(t, pool, u.ID, "B", "private")
	insertQuiz(t, pool, u.ID, subA.ID, nil, "global", `{"types":["multi_choice"]}`, 5)
	insertQuiz(t, pool, u.ID, subB.ID, nil, "global", `{"types":["multi_choice"]}`, 5)

	svc := quiz.NewService(pool, nil)
	res, err := svc.List(context.Background(), quiz.ListRequest{UserID: u.ID, SubjectID: &subA.ID})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if res.Total != 1 || len(res.Quizzes) != 1 {
		t.Fatalf("total/len = %d/%d, want 1/1", res.Total, len(res.Quizzes))
	}
	if res.Quizzes[0].SubjectID != subA.ID {
		t.Fatalf("subjectId = %d, want %d", res.Quizzes[0].SubjectID, subA.ID)
	}
}

func TestList_Pagination(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	sub := testutil.NewSubjectNamed(t, pool, u.ID, "Bio", "private")
	for i := 0; i < 5; i++ {
		insertQuiz(t, pool, u.ID, sub.ID, nil, "global", `{"types":["multi_choice"]}`, 5)
	}

	svc := quiz.NewService(pool, nil)
	page1, err := svc.List(context.Background(), quiz.ListRequest{UserID: u.ID, Limit: 2, Offset: 0})
	if err != nil {
		t.Fatalf("list page1: %v", err)
	}
	if page1.Total != 5 || len(page1.Quizzes) != 2 {
		t.Fatalf("page1 total/len = %d/%d, want 5/2", page1.Total, len(page1.Quizzes))
	}
	page2, err := svc.List(context.Background(), quiz.ListRequest{UserID: u.ID, Limit: 2, Offset: 2})
	if err != nil {
		t.Fatalf("list page2: %v", err)
	}
	if len(page2.Quizzes) != 2 {
		t.Fatalf("page2 len = %d, want 2", len(page2.Quizzes))
	}
	if page1.Quizzes[0].ID == page2.Quizzes[0].ID {
		t.Fatal("page1 and page2 overlap at offset 0/2")
	}
}

func TestList_ScopedToOwner(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	owner := testutil.NewVerifiedUser(t, pool)
	other := testutil.NewVerifiedUserNamed(t, pool, "other")
	sub := testutil.NewSubjectNamed(t, pool, owner.ID, "Bio", "private")
	insertQuiz(t, pool, owner.ID, sub.ID, nil, "global", `{"types":["multi_choice"]}`, 5)

	svc := quiz.NewService(pool, nil)
	res, err := svc.List(context.Background(), quiz.ListRequest{UserID: other.ID})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if res.Total != 0 || len(res.Quizzes) != 0 {
		t.Fatalf("total/len = %d/%d, want 0/0 (owned by another user)", res.Total, len(res.Quizzes))
	}
}

func TestDelete_RemovesQuizAndCascades(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	qid := testutil.NewQuiz(t, pool, u.ID, 3)

	svc := quiz.NewService(pool, nil)
	if _, err := svc.Retake(context.Background(), u.ID, qid); err != nil {
		t.Fatalf("seed in-progress attempt: %v", err)
	}

	if err := svc.Delete(context.Background(), u.ID, qid); err != nil {
		t.Fatalf("delete: %v", err)
	}

	var count int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM quizzes WHERE id = $1`, qid).Scan(&count); err != nil {
		t.Fatalf("count quizzes: %v", err)
	}
	if count != 0 {
		t.Fatalf("quiz still exists after delete")
	}
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM quiz_questions WHERE quiz_id = $1`, qid).Scan(&count); err != nil {
		t.Fatalf("count quiz_questions: %v", err)
	}
	if count != 0 {
		t.Fatalf("quiz_questions not cascaded, count = %d", count)
	}
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM quiz_attempts WHERE quiz_id = $1`, qid).Scan(&count); err != nil {
		t.Fatalf("count quiz_attempts: %v", err)
	}
	if count != 0 {
		t.Fatalf("quiz_attempts not cascaded, count = %d", count)
	}
}

func TestDelete_MissingQuiz_NotFound(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)

	svc := quiz.NewService(pool, nil)
	err := svc.Delete(context.Background(), u.ID, 999999)
	if err == nil {
		t.Fatal("want error for missing quiz, got nil")
	}
}

func TestDelete_NotOwner_NotFound(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	owner := testutil.NewVerifiedUser(t, pool)
	intruder := testutil.NewVerifiedUserNamed(t, pool, "intruder")
	qid := testutil.NewQuiz(t, pool, owner.ID, 3)

	svc := quiz.NewService(pool, nil)
	err := svc.Delete(context.Background(), intruder.ID, qid)
	if err == nil {
		t.Fatal("want error for non-owner delete, got nil")
	}

	var count int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM quizzes WHERE id = $1`, qid).Scan(&count); err != nil {
		t.Fatalf("count quizzes: %v", err)
	}
	if count != 1 {
		t.Fatal("quiz should still exist after rejected non-owner delete")
	}
}
