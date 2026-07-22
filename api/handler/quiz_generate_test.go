package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"studbud/backend/api/handler"
	"studbud/backend/internal/aiProvider"
	"studbud/backend/internal/authctx"
	"studbud/backend/pkg/access"
	"studbud/backend/pkg/aipipeline"
	"studbud/backend/pkg/quiz"
	"studbud/backend/testutil"
)

func TestPostQuizzesGenerate_HappyPath(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	sub := testutil.NewSubjectNamed(t, pool, u.ID, "Bio", "private")
	c1 := testutil.NewChapter(t, pool, sub.ID, "C1")
	fc1 := testutil.NewFlashcard(t, pool, sub.ID, c1, "What?", "Mitochondrion")

	// AI fake returns exactly 5 MCQ items (matches Size:5 white-list).
	items := ""
	for i := 0; i < 5; i++ {
		if i > 0 {
			items += ","
		}
		items += fmt.Sprintf(`{"questionType":"multi_choice","stem":"Q%d","options":["A","B","C","D"],"correctIndex":2,"referencedFcIds":[%d]}`, i, fc1)
	}
	fake := &testutil.FakeAIClient{
		Chunks: []aiProvider.Chunk{
			{Text: `{"items":[` + items + `]}`},
			{Done: true},
		},
	}
	ai := aipipeline.NewService(pool, fake, access.NewService(pool),
		aipipeline.QuotaLimits{QuizCalls: 5}, "claude-test")
	qsvc := quiz.NewService(pool, ai)
	h := handler.NewQuizHandler(qsvc, access.NewService(pool))

	body, _ := json.Marshal(map[string]any{
		"subjectId":  sub.ID,
		"kind":       "specific",
		"size":       5,
		"types":      []string{"multi_choice"},
		"cardFilter": "all",
	})
	req := httptest.NewRequest("POST", "/quizzes/generate", bytes.NewReader(body))
	req = req.WithContext(authctx.WithIdentity(context.Background(), u.ID, true, false))
	w := httptest.NewRecorder()
	h.Generate(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var out struct {
		QuizID        int64 `json:"quizId"`
		QuestionCount int   `json:"questionCount"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out.QuizID == 0 || out.QuestionCount != 5 {
		t.Fatalf("response = %+v", out)
	}
}

func TestPostQuizzesGenerateStream_HappyPath(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	sub := testutil.NewSubjectNamed(t, pool, u.ID, "Bio", "private")
	c1 := testutil.NewChapter(t, pool, sub.ID, "C1")
	fc1 := testutil.NewFlashcard(t, pool, sub.ID, c1, "What?", "Mitochondrion")

	items := ""
	for i := 0; i < 5; i++ {
		if i > 0 {
			items += ","
		}
		items += fmt.Sprintf(`{"questionType":"multi_choice","stem":"Q%d","options":["A","B","C","D"],"correctIndex":2,"referencedFcIds":[%d]}`, i, fc1)
	}
	fake := &testutil.FakeAIClient{
		Chunks: []aiProvider.Chunk{
			{Text: `{"items":[` + items + `]}`},
			{Done: true},
		},
	}
	ai := aipipeline.NewService(pool, fake, access.NewService(pool),
		aipipeline.QuotaLimits{QuizCalls: 5}, "claude-test")
	qsvc := quiz.NewService(pool, ai)
	h := handler.NewQuizHandler(qsvc, access.NewService(pool))

	body, _ := json.Marshal(map[string]any{
		"subjectId":  sub.ID,
		"kind":       "specific",
		"size":       5,
		"types":      []string{"multi_choice"},
		"cardFilter": "all",
	})
	req := httptest.NewRequest("POST", "/quizzes/generate/stream", bytes.NewReader(body))
	req = req.WithContext(authctx.WithIdentity(context.Background(), u.ID, true, false))
	w := httptest.NewRecorder()
	h.GenerateStream(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	stream := w.Body.String()
	if got := strings.Count(stream, "event: progress"); got != 5 {
		t.Errorf("progress events = %d, want 5:\n%s", got, stream)
	}
	if !strings.Contains(stream, `data: {"index":5,"size":5}`) {
		t.Errorf("missing final progress index=5/5 in stream:\n%s", stream)
	}
	if !strings.Contains(stream, "event: done") {
		t.Errorf("missing done event in stream:\n%s", stream)
	}
	if strings.Contains(stream, "event: error") {
		t.Errorf("unexpected error event in stream:\n%s", stream)
	}
}

func TestPostQuizzesGenerateStream_EmptyPool_EmitsErrorEvent(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	sub := testutil.NewSubjectNamed(t, pool, u.ID, "Bio", "private")
	// No flashcards seeded — pool resolves to zero cards.

	qsvc := quiz.NewService(pool, nil) // AI must never be reached
	h := handler.NewQuizHandler(qsvc, access.NewService(pool))

	body, _ := json.Marshal(map[string]any{
		"subjectId":  sub.ID,
		"kind":       "specific",
		"size":       5,
		"types":      []string{"multi_choice"},
		"cardFilter": "all",
	})
	req := httptest.NewRequest("POST", "/quizzes/generate/stream", bytes.NewReader(body))
	req = req.WithContext(authctx.WithIdentity(context.Background(), u.ID, true, false))
	w := httptest.NewRecorder()
	h.GenerateStream(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	stream := w.Body.String()
	if !strings.Contains(stream, "event: error") {
		t.Errorf("missing error event in stream:\n%s", stream)
	}
	if strings.Contains(stream, "event: progress") || strings.Contains(stream, "event: done") {
		t.Errorf("unexpected progress/done event in stream:\n%s", stream)
	}
}

func TestPostQuizzesGenerate_NoAIAccess_402(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	sub := testutil.NewSubjectNamed(t, pool, u.ID, "Bio", "private")

	qsvc := quiz.NewService(pool, nil) // never reached
	h := handler.NewQuizHandler(qsvc, access.NewService(pool))

	body, _ := json.Marshal(map[string]any{
		"subjectId": sub.ID, "kind": "global", "size": 5, "types": []string{"multi_choice"},
	})
	req := httptest.NewRequest("POST", "/quizzes/generate", bytes.NewReader(body))
	req = req.WithContext(authctx.WithIdentity(context.Background(), u.ID, true, false))
	w := httptest.NewRecorder()
	h.Generate(w, req)
	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402; body=%s", w.Code, w.Body.String())
	}
}
