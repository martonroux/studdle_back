package aipipeline_test

import (
	"context"
	"strings"
	"testing"

	"studdle/backend/internal/aiProvider"
	"studdle/backend/pkg/access"
	"studdle/backend/pkg/aipipeline"
	"studdle/backend/testutil"
)

func TestRankCrossSubjects_HappyPath(t *testing.T) {
	body := `{"selectedIds":[12,205,308]}`
	svc := aipipeline.NewServiceForTest(nil, &testutil.FakeAIClient{
		Chunks: []aiProvider.Chunk{{Text: body, Done: true}},
	}, "claude-test")

	out, err := svc.RankCrossSubjects(context.Background(), aipipeline.RankInput{
		ExamSubject: "Biologie",
		ExamTitle:   "Partiel",
		Candidates: []aipipeline.CrossSubjectCandidate{
			{ID: 12, Title: "Mitose", SubjectName: "Microbio", Keywords: []string{"mitose"}, OverlapScore: 2},
			{ID: 205, Title: "Cycle", SubjectName: "Biochimie", Keywords: []string{"cycle"}, OverlapScore: 3},
			{ID: 308, Title: "ADN", SubjectName: "Biochimie", Keywords: []string{"chromosome"}, OverlapScore: 1},
		},
		TopK: 15,
	})
	if err != nil {
		t.Fatalf("rank: %v", err)
	}
	if len(out.SelectedIDs) != 3 || out.SelectedIDs[0] != 12 {
		t.Errorf("unexpected ids: %+v", out.SelectedIDs)
	}
}

func TestRankCrossSubjects_EmptyCandidates(t *testing.T) {
	svc := aipipeline.NewServiceForTest(nil, &testutil.FakeAIClient{}, "claude-test")
	out, err := svc.RankCrossSubjects(context.Background(), aipipeline.RankInput{TopK: 15})
	if err != nil {
		t.Fatalf("expected silent success on empty input, got %v", err)
	}
	if len(out.SelectedIDs) != 0 {
		t.Errorf("want empty, got %+v", out.SelectedIDs)
	}
}

func TestGenerateRevisionPlan_HappyPath(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	subj := testutil.NewSubject(t, pool, u.ID)

	body := `{"items":[{"date":"2026-06-15","primarySubjectCards":[12],"crossSubjectCards":[],"deeperDives":[]}]}`
	cli := &testutil.FakeAIClient{Chunks: []aiProvider.Chunk{{Text: body, Done: true}}}
	svc := aipipeline.NewService(pool, cli, access.NewService(pool), aipipeline.DefaultQuotaLimits(), "claude-test")

	out, err := svc.GenerateRevisionPlan(context.Background(), aipipeline.PlanGenerateInput{
		UserID:        u.ID,
		ExamID:        99,
		SubjectID:     subj.ID,
		Prompt:        "render me a plan",
		AnnalesImages: nil,
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if out.JobID == 0 {
		t.Errorf("want jobId > 0")
	}

	var collected string
	for c := range out.Chunks {
		if c.Kind == aipipeline.ChunkError {
			t.Fatalf("provider error chunk: %s", c.Err)
		}
		if c.Kind == aipipeline.ChunkItem {
			collected += string(c.Item)
		}
	}
	if !strings.Contains(collected, "2026-06-15") {
		t.Errorf("did not see plan body in stream: %q", collected)
	}
}
