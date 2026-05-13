package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"studbud/backend/api/handler"
	"studbud/backend/internal/authctx"
	"studbud/backend/pkg/access"
	"studbud/backend/pkg/quiz"
	"studbud/backend/testutil"
)

func TestPostQuizzesStart_CreatesAttemptAndReturnsFirstQuestion(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	qid := testutil.NewQuiz(t, pool, u.ID, 2)

	qsvc := quiz.NewService(pool, nil)
	h := handler.NewQuizHandler(qsvc, access.NewService(pool))

	req := httptest.NewRequest("POST", "/quizzes/{id}/start", nil)
	req.SetPathValue("id", strconv.FormatInt(qid, 10))
	req = req.WithContext(authctx.WithIdentity(context.Background(), u.ID, true, false))
	w := httptest.NewRecorder()
	h.Start(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		AttemptID    int64 `json:"attemptId"`
		NextQuestion struct {
			ID      int64  `json:"id"`
			Ordinal int    `json:"ordinal"`
			Type    string `json:"type"`
		} `json:"nextQuestion"`
		Progress struct {
			Answered int `json:"answered"`
			Total    int `json:"total"`
		} `json:"progress"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v\nbody=%s", err, w.Body.String())
	}
	if resp.NextQuestion.Ordinal != 1 {
		t.Fatalf("nextQuestion.Ordinal = %d, want 1", resp.NextQuestion.Ordinal)
	}
	if resp.Progress.Total != 2 {
		t.Fatalf("Total = %d, want 2", resp.Progress.Total)
	}
}

func TestGetResume_ReturnsCurrentPosition(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	qid := testutil.NewQuiz(t, pool, u.ID, 2)

	qsvc := quiz.NewService(pool, nil)
	att, _, _, err := qsvc.Start(context.Background(), u.ID, qid)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	h := handler.NewQuizHandler(qsvc, access.NewService(pool))
	req := httptest.NewRequest("GET", "/quizzes/{id}/attempts/{aid}/resume", nil)
	req.SetPathValue("id", strconv.FormatInt(qid, 10))
	req.SetPathValue("aid", strconv.FormatInt(att.ID, 10))
	req = req.WithContext(authctx.WithIdentity(context.Background(), u.ID, true, false))
	w := httptest.NewRecorder()
	h.Resume(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		AttemptID int64  `json:"attemptId"`
		State     string `json:"state"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.AttemptID != att.ID {
		t.Fatalf("attemptId = %d, want %d", resp.AttemptID, att.ID)
	}
	if resp.State != "in_progress" {
		t.Fatalf("state = %q, want in_progress", resp.State)
	}
}

func TestPostAnswer_CorrectMCQ(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	qid := testutil.NewQuiz(t, pool, u.ID, 2) // 2 MCQ, correct index = 2

	qsvc := quiz.NewService(pool, nil)
	att, q1, _, _ := qsvc.Start(context.Background(), u.ID, qid)

	h := handler.NewQuizHandler(qsvc, access.NewService(pool))
	body, _ := json.Marshal(map[string]any{
		"questionId": q1.ID,
		"answer":     map[string]int{"index": 2},
	})
	req := httptest.NewRequest("POST", "/quizzes/{id}/attempts/{aid}/answer", bytes.NewReader(body))
	req.SetPathValue("id", strconv.FormatInt(qid, 10))
	req.SetPathValue("aid", strconv.FormatInt(att.ID, 10))
	req = req.WithContext(authctx.WithIdentity(context.Background(), u.ID, true, false))
	w := httptest.NewRecorder()
	h.Answer(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Correct bool `json:"correct"`
		Next    *struct {
			Ordinal int `json:"ordinal"`
		} `json:"nextQuestion"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if !resp.Correct {
		t.Fatalf("not marked correct")
	}
}

func TestPostAbandon_204(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	qid := testutil.NewQuiz(t, pool, u.ID, 2)

	qsvc := quiz.NewService(pool, nil)
	att, _, _, _ := qsvc.Start(context.Background(), u.ID, qid)

	h := handler.NewQuizHandler(qsvc, access.NewService(pool))
	req := httptest.NewRequest("POST", "/quizzes/{id}/attempts/{aid}/abandon", nil)
	req.SetPathValue("id", strconv.FormatInt(qid, 10))
	req.SetPathValue("aid", strconv.FormatInt(att.ID, 10))
	req = req.WithContext(authctx.WithIdentity(context.Background(), u.ID, true, false))
	w := httptest.NewRecorder()
	h.Abandon(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status %d", w.Code)
	}
}

func TestPostRetake_409IfInProgress(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	qid := testutil.NewQuiz(t, pool, u.ID, 2)

	qsvc := quiz.NewService(pool, nil)
	_, _, _, _ = qsvc.Start(context.Background(), u.ID, qid)

	h := handler.NewQuizHandler(qsvc, access.NewService(pool))
	req := httptest.NewRequest("POST", "/quizzes/{id}/retake", nil)
	req.SetPathValue("id", strconv.FormatInt(qid, 10))
	req = req.WithContext(authctx.WithIdentity(context.Background(), u.ID, true, false))
	w := httptest.NewRecorder()
	h.Retake(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("status %d", w.Code)
	}
}

func TestGetAttempt_ReturnsReviewPayload(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	qid := testutil.NewQuiz(t, pool, u.ID, 1) // single MCQ, correct_index=2

	qsvc := quiz.NewService(pool, nil)
	att, q1, _, _ := qsvc.Start(context.Background(), u.ID, qid)
	_, err := qsvc.Answer(context.Background(), u.ID, att.ID, q1.ID,
		json.RawMessage(`{"index":2}`))
	if err != nil {
		t.Fatalf("answer: %v", err)
	}

	h := handler.NewQuizHandler(qsvc, access.NewService(pool))
	req := httptest.NewRequest("GET", "/quizzes/{id}/attempts/{aid}", nil)
	req.SetPathValue("id", strconv.FormatInt(qid, 10))
	req.SetPathValue("aid", strconv.FormatInt(att.ID, 10))
	req = req.WithContext(authctx.WithIdentity(context.Background(), u.ID, true, false))
	w := httptest.NewRecorder()
	h.GetAttempt(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	var view struct {
		Attempt struct {
			State string `json:"state"`
		} `json:"attempt"`
		Questions []struct {
			Stem string `json:"stem"`
		} `json:"questions"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &view)
	if view.Attempt.State != "completed" {
		t.Fatalf("state = %q, want completed", view.Attempt.State)
	}
	if len(view.Questions) != 1 {
		t.Fatalf("got %d question reviews, want 1", len(view.Questions))
	}
}

func TestGetHistory_ReturnsAttempts(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	qid := testutil.NewQuiz(t, pool, u.ID, 1)

	qsvc := quiz.NewService(pool, nil)
	// First attempt complete.
	att1, q1, _, _ := qsvc.Start(context.Background(), u.ID, qid)
	_, _ = qsvc.Answer(context.Background(), u.ID, att1.ID, q1.ID,
		json.RawMessage(`{"index":2}`))
	// Second attempt via Retake.
	_, _ = qsvc.Retake(context.Background(), u.ID, qid)

	h := handler.NewQuizHandler(qsvc, access.NewService(pool))
	req := httptest.NewRequest("GET", "/quizzes/{id}/history", nil)
	req.SetPathValue("id", strconv.FormatInt(qid, 10))
	req = req.WithContext(authctx.WithIdentity(context.Background(), u.ID, true, false))
	w := httptest.NewRecorder()
	h.History(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var resp struct {
		Attempts []struct {
			ID int64 `json:"ID"` // pkg/quiz.Attempt has no json tags; fields serialise capitalised
		} `json:"attempts"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Attempts) != 2 {
		t.Fatalf("got %d, want 2; body=%s", len(resp.Attempts), w.Body.String())
	}
}
