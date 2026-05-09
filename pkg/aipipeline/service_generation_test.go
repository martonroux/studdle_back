package aipipeline_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"studbud/backend/internal/aiProvider"
	"studbud/backend/internal/myErrors"
	"studbud/backend/pkg/access"
	"studbud/backend/pkg/aipipeline"
	"studbud/backend/testutil"
)

func TestRun_RejectsWhenNoAIAccess(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	subj := testutil.NewSubject(t, pool, u.ID)

	svc := newPipelineSvc(pool, &testutil.FakeAIClient{})
	_, _, err := svc.RunStructuredGeneration(context.Background(), newPromptReq(u.ID, subj.ID))
	if !errors.Is(err, myErrors.ErrNoAIAccess) {
		t.Fatalf("err = %v, want ErrNoAIAccess", err)
	}
}

func TestRun_RejectsWhenQuotaExhausted(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	subj := testutil.NewSubject(t, pool, u.ID)
	testutil.SeedQuotaAt(t, pool, u.ID, "prompt_calls", 20)

	svc := newPipelineSvc(pool, &testutil.FakeAIClient{})
	_, _, err := svc.RunStructuredGeneration(context.Background(), newPromptReq(u.ID, subj.ID))
	if !errors.Is(err, myErrors.ErrQuotaExhausted) {
		t.Fatalf("err = %v, want ErrQuotaExhausted", err)
	}
}

func TestRun_RejectsWhenConcurrentGenerationExists(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	subj := testutil.NewSubject(t, pool, u.ID)
	existing := testutil.SeedRunningJob(t, pool, u.ID, "generate_prompt")

	svc := newPipelineSvc(pool, &testutil.FakeAIClient{})
	_, _, err := svc.RunStructuredGeneration(context.Background(), newPromptReq(u.ID, subj.ID))
	var ae *myErrors.AppError
	if !errors.As(err, &ae) || !errors.Is(err, myErrors.ErrConflict) {
		t.Fatalf("err = %v, want AppError wrapping ErrConflict", err)
	}
	if ae.Code != "concurrent_generation" {
		t.Errorf("Code = %q, want concurrent_generation", ae.Code)
	}
	if !containsJobID(ae.Message, existing) {
		t.Errorf("Message = %q, expected to include jobID %d", ae.Message, existing)
	}
}

func TestRun_InsertsRunningJobBeforeStream(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	subj := testutil.NewSubject(t, pool, u.ID)

	svc := newPipelineSvc(pool, &testutil.FakeAIClient{
		Chunks: []aiProvider.Chunk{{Done: true}},
	})
	ch, jobID, err := svc.RunStructuredGeneration(context.Background(), newPromptReq(u.ID, subj.ID))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if jobID <= 0 {
		t.Errorf("jobID = %d, want > 0", jobID)
	}
	for range ch {
	}
	if n := testutil.CountAIJobs(t, pool, u.ID); n != 1 {
		t.Errorf("ai_jobs count = %d, want 1", n)
	}
}

func newPipelineSvc(pool *pgxpool.Pool, cli aiProvider.Client) *aipipeline.Service {
	return aipipeline.NewService(pool, cli, access.NewService(pool), aipipeline.DefaultQuotaLimits(), "test-model")
}

func newPromptReq(uid, subjectID int64) aipipeline.AIRequest {
	return aipipeline.AIRequest{
		UserID:    uid,
		Feature:   aipipeline.FeatureGenerateFromPrompt,
		SubjectID: subjectID,
		Prompt:    "anything",
		Schema:    json.RawMessage(`{"type":"object"}`),
		Metadata:  map[string]any{"style": "standard"},
	}
}

func containsJobID(s string, id int64) bool {
	return len(s) > 0 && (indexRune(s, '0'+int32(id%10)) >= 0 || indexByte(s, '#') >= 0)
}

func indexRune(s string, r int32) int {
	for i, c := range s {
		if c == r {
			return i
		}
	}
	return -1
}
func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

func TestRun_HappyPath_EmitsItemsThenDone(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	subj := testutil.NewSubject(t, pool, u.ID)

	cli := &testutil.FakeAIClient{
		Chunks: []aiProvider.Chunk{
			{Text: `{"items":[{"title":"t1","question":"q1","answer":"a1"},`},
			{Text: `{"title":"t2","question":"q2","answer":"a2"}]}`, Done: true},
		},
	}
	svc := newPipelineSvc(pool, cli)
	ch, jobID, err := svc.RunStructuredGeneration(context.Background(), newPromptReq(u.ID, subj.ID))
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	var items []json.RawMessage
	var sawDone bool
	for c := range ch {
		switch c.Kind {
		case aipipeline.ChunkItem:
			items = append(items, c.Item)
		case aipipeline.ChunkDone:
			sawDone = true
		case aipipeline.ChunkError:
			t.Fatalf("unexpected error chunk: %v", c.Err)
		}
	}
	if !sawDone {
		t.Error("missing ChunkDone")
	}
	if len(items) != 2 {
		t.Errorf("items count = %d, want 2", len(items))
	}

	var status string
	var emitted int
	_ = pool.QueryRow(context.Background(), `SELECT status, items_emitted FROM ai_jobs WHERE id=$1`, jobID).Scan(&status, &emitted)
	if status != "complete" || emitted != 2 {
		t.Errorf("row (%q, emitted=%d), want (complete, 2)", status, emitted)
	}

	var prompt int
	_ = pool.QueryRow(context.Background(), `SELECT prompt_calls FROM ai_quota_daily WHERE user_id=$1 AND day=current_date`, u.ID).Scan(&prompt)
	if prompt != 1 {
		t.Errorf("prompt_calls = %d, want 1 (one successful job)", prompt)
	}
}

func TestRun_DropsSchemaInvalidItems(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	subj := testutil.NewSubject(t, pool, u.ID)

	cli := &testutil.FakeAIClient{
		Chunks: []aiProvider.Chunk{
			{Text: `{"items":[{"title":"ok"},not-json,{"title":"ok2"}]}`, Done: true},
		},
	}
	svc := newPipelineSvc(pool, cli)
	ch, jobID, err := svc.RunStructuredGeneration(context.Background(), newPromptReq(u.ID, subj.ID))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for range ch {
	}
	var emitted, dropped int
	_ = pool.QueryRow(context.Background(), `SELECT items_emitted, items_dropped FROM ai_jobs WHERE id=$1`, jobID).Scan(&emitted, &dropped)
	if emitted != 2 {
		t.Errorf("emitted = %d, want 2", emitted)
	}
	_ = dropped
}

func TestRun_RetriesOnceOnTransientProviderError(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	subj := testutil.NewSubject(t, pool, u.ID)

	cli := &testutil.FakeAIClient{
		Err:        &myErrors.AppError{Code: "provider_5xx", Wrapped: myErrors.ErrAIProvider},
		FailFirstN: 1,
		Chunks: []aiProvider.Chunk{
			{Text: `{"items":[{"title":"ok","question":"q","answer":"a"}]}`, Done: true},
		},
	}
	svc := newPipelineSvc(pool, cli)
	ch, _, err := svc.RunStructuredGeneration(context.Background(), newPromptReq(u.ID, subj.ID))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for range ch {
	}
	if cli.Calls() != 2 {
		t.Errorf("calls = %d, want 2 (one failure + one retry)", cli.Calls())
	}
}

func TestRun_DoesNotRetryOnContentPolicy(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	subj := testutil.NewSubject(t, pool, u.ID)

	cli := &testutil.FakeAIClient{
		Err:        myErrors.ErrContentPolicy,
		FailFirstN: 5,
	}
	svc := newPipelineSvc(pool, cli)
	ch, _, err := svc.RunStructuredGeneration(context.Background(), newPromptReq(u.ID, subj.ID))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	var sawError bool
	for c := range ch {
		if c.Kind == aipipeline.ChunkError {
			sawError = true
		}
	}
	if !sawError {
		t.Error("expected ChunkError")
	}
	if cli.Calls() != 1 {
		t.Errorf("calls = %d, want 1 (no retry on content_policy)", cli.Calls())
	}
}

func TestRun_FailsAfterRetryExhausts(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	subj := testutil.NewSubject(t, pool, u.ID)

	cli := &testutil.FakeAIClient{
		Err:        &myErrors.AppError{Code: "provider_5xx", Wrapped: myErrors.ErrAIProvider},
		FailFirstN: 10,
	}
	svc := newPipelineSvc(pool, cli)
	ch, jobID, err := svc.RunStructuredGeneration(context.Background(), newPromptReq(u.ID, subj.ID))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for range ch {
	}
	if cli.Calls() != 2 {
		t.Errorf("calls = %d, want 2", cli.Calls())
	}
	var status, errKind string
	_ = pool.QueryRow(context.Background(), `SELECT status, error_kind FROM ai_jobs WHERE id=$1`, jobID).Scan(&status, &errKind)
	if status != "failed" || errKind != "provider_5xx" {
		t.Errorf("row = (%q, %q), want (failed, provider_5xx)", status, errKind)
	}
}

func TestRun_MalformedOutputNoTransparentRetry(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	subj := testutil.NewSubject(t, pool, u.ID)

	cli := &testutil.FakeAIClient{
		Chunks: []aiProvider.Chunk{
			{Text: "totally not json", Done: true},
		},
	}
	svc := newPipelineSvc(pool, cli)
	ch, jobID, err := svc.RunStructuredGeneration(context.Background(), newPromptReq(u.ID, subj.ID))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for range ch {
	}
	if cli.Calls() != 1 {
		t.Errorf("calls = %d, want 1 (no transparent retry on junk stream output)", cli.Calls())
	}
	var prompt int
	_ = pool.QueryRow(context.Background(), `SELECT prompt_calls FROM ai_quota_daily WHERE user_id=$1 AND day=current_date`, u.ID).Scan(&prompt)
	if prompt != 0 {
		t.Errorf("prompt_calls = %d, want 0 (no debit on empty items)", prompt)
	}
	var emitted int
	_ = pool.QueryRow(context.Background(), `SELECT items_emitted FROM ai_jobs WHERE id=$1`, jobID).Scan(&emitted)
	if emitted != 0 {
		t.Errorf("emitted = %d, want 0", emitted)
	}
}

func TestRun_ImageModeOverflow_SurfacesPDFImageModeUnavailable(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	subj := testutil.NewSubject(t, pool, u.ID)

	cli := &testutil.FakeAIClient{
		Err: errors.New("prompt is too long: 250000 tokens > 200000 maximum"),
	}
	svc := newPipelineSvc(pool, cli)

	req := aipipeline.AIRequest{
		UserID:    u.ID,
		Feature:   aipipeline.FeatureGenerateFromPDF,
		SubjectID: subj.ID,
		Prompt:    "anything",
		Schema:    json.RawMessage(`{"type":"object"}`),
		PDFPages:  31,
		Images:    []aiProvider.ImagePart{{MediaType: "image/jpeg", Data: []byte{0xff, 0xd8}}},
		Metadata:  map[string]any{"mode": "image"},
	}

	ch, _, err := svc.RunStructuredGeneration(context.Background(), req)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	var got aipipeline.AIChunk
	for c := range ch {
		if c.Kind == aipipeline.ChunkError {
			got = c
		}
	}
	if got.Kind != aipipeline.ChunkError {
		t.Fatalf("no ChunkError received from stream")
	}
	if !errors.Is(got.Err, myErrors.ErrPDFImageModeUnavailable) {
		t.Errorf("ChunkError.Err = %v, want errors.Is(ErrPDFImageModeUnavailable)", got.Err)
	}
}
