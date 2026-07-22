package emailverification

import (
	"context"
	"strings"
	"testing"

	"studdle/backend/testutil"
)

func TestIssueSendsEmail(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewUser(t, pool)
	rec := testutil.NewEmailRecorder()
	svc := NewService(pool, rec, "http://localhost:5173")

	if err := svc.Issue(context.Background(), u.ID, u.Email); err != nil {
		t.Fatalf("Issue: %v", err)
	}
	sent := rec.Sent()
	if len(sent) != 1 {
		t.Fatalf("want 1 email, got %d", len(sent))
	}
	if !strings.Contains(sent[0].Body, "/verify-email?token=") {
		t.Fatalf("email missing token link: %q", sent[0].Body)
	}
}

func TestVerifyFlipsFlag(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewUser(t, pool)
	rec := testutil.NewEmailRecorder()
	svc := NewService(pool, rec, "http://localhost:5173")

	if err := svc.Issue(context.Background(), u.ID, u.Email); err != nil {
		t.Fatalf("Issue: %v", err)
	}
	body := rec.Sent()[0].Body
	tok := body[strings.Index(body, "token=")+len("token="):]
	if err := svc.Verify(context.Background(), tok); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	var verified bool
	if err := pool.QueryRow(context.Background(),
		`SELECT email_verified FROM users WHERE id = $1`, u.ID).Scan(&verified); err != nil {
		t.Fatal(err)
	}
	if !verified {
		t.Fatal("user should be marked verified")
	}
}
