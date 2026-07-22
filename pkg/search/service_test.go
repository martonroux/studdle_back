package search_test

import (
	"context"
	"testing"

	"studdle/backend/pkg/search"
	"studdle/backend/testutil"
)

func TestSearchOwnedSubjects(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	testutil.Reset(t, db)
	svc := search.NewService(db)

	alice := testutil.NewVerifiedUser(t, db)
	bob := testutil.NewVerifiedUser(t, db)

	_ = testutil.NewSubjectNamed(t, db, alice.ID, "Chemistry", "private")
	_ = testutil.NewSubjectNamed(t, db, bob.ID, "Chemistry Public", "public")
	_ = testutil.NewSubjectNamed(t, db, bob.ID, "Chemistry Secret", "private")

	hits, err := svc.OwnedSubjects(ctx, alice.ID, "chemistry", false, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	// alice only sees her own subject, regardless of bob's visibility
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit, got %d: %+v", len(hits), hits)
	}
	if hits[0].OwnerID != alice.ID {
		t.Fatalf("expected alice's subject, got owner_id=%d", hits[0].OwnerID)
	}
}

func TestSearchOwnedSubjects_ArchivedToggle(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	testutil.Reset(t, db)
	svc := search.NewService(db)

	alice := testutil.NewVerifiedUser(t, db)
	live := testutil.NewSubjectNamed(t, db, alice.ID, "Chemistry Live", "private")
	arch := testutil.NewSubjectNamed(t, db, alice.ID, "Chemistry Archived", "private")
	if _, err := db.Exec(ctx, `UPDATE subjects SET archived = true WHERE id = $1`, arch.ID); err != nil {
		t.Fatal(err)
	}

	defaultHits, err := svc.OwnedSubjects(ctx, alice.ID, "chemistry", false, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(defaultHits) != 1 || defaultHits[0].ID != live.ID {
		t.Fatalf("default should hide archived; got %+v", defaultHits)
	}

	allHits, err := svc.OwnedSubjects(ctx, alice.ID, "chemistry", true, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(allHits) != 2 {
		t.Fatalf("include_archived=true should return both, got %d: %+v", len(allHits), allHits)
	}
}

func TestSearchPublicSubjects(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	testutil.Reset(t, db)
	svc := search.NewService(db)

	alice := testutil.NewVerifiedUser(t, db)
	bob := testutil.NewVerifiedUser(t, db)

	_ = testutil.NewSubjectNamed(t, db, alice.ID, "Chemistry", "public")
	_ = testutil.NewSubjectNamed(t, db, bob.ID, "Chemistry Public", "public")
	_ = testutil.NewSubjectNamed(t, db, bob.ID, "Chemistry Secret", "private")

	hits, err := svc.PublicSubjects(ctx, alice.ID, "chemistry", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	// alice sees bob's public but not her own (excluded) and not bob's private
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit, got %d: %+v", len(hits), hits)
	}
	if hits[0].OwnerID != bob.ID {
		t.Fatalf("expected bob's subject, got owner_id=%d", hits[0].OwnerID)
	}
}

func TestSearchOwnedSubjects_PartialSubstring(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	testutil.Reset(t, db)
	svc := search.NewService(db)

	alice := testutil.NewVerifiedUser(t, db)
	_ = testutil.NewSubjectNamed(t, db, alice.ID, "Chemistry", "private")

	// pg_trgm ILIKE: inside-word substring matches
	hits, err := svc.OwnedSubjects(ctx, alice.ID, "istry", false, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("'istry' expected 1 hit, got %d: %+v", len(hits), hits)
	}
}

func TestSearchPublicSubjects_PrefixMatch(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	testutil.Reset(t, db)
	svc := search.NewService(db)

	alice := testutil.NewVerifiedUser(t, db)
	bob := testutil.NewVerifiedUser(t, db)
	_ = testutil.NewSubjectNamed(t, db, bob.ID, "Chemistry", "public")

	// tsquery with :*: word-start prefix must match
	prefix, err := svc.PublicSubjects(ctx, alice.ID, "chem", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(prefix) != 1 {
		t.Fatalf("'chem' expected 1 hit, got %d: %+v", len(prefix), prefix)
	}

	// Inside-word substring must NOT match (tsquery is word-start only)
	inside, err := svc.PublicSubjects(ctx, alice.ID, "istry", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(inside) != 0 {
		t.Fatalf("'istry' expected 0 hits, got %d: %+v", len(inside), inside)
	}
}

func TestSearchPublicSubjects_Pagination(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	testutil.Reset(t, db)
	svc := search.NewService(db)

	alice := testutil.NewVerifiedUser(t, db)
	bob := testutil.NewVerifiedUser(t, db)

	for i := 0; i < 3; i++ {
		_ = testutil.NewSubjectNamed(t, db, bob.ID, "Chemistry", "public")
	}

	page1, err := svc.PublicSubjects(ctx, alice.ID, "chemistry", 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page1) != 2 {
		t.Fatalf("page1 expected 2, got %d", len(page1))
	}
	page2, err := svc.PublicSubjects(ctx, alice.ID, "chemistry", 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page2) != 1 {
		t.Fatalf("page2 expected 1, got %d", len(page2))
	}
	if page1[0].ID == page2[0].ID || page1[1].ID == page2[0].ID {
		t.Fatalf("page2 overlapped page1: %+v vs %+v", page2, page1)
	}
}

func TestSearchFlashcards_ScopedToLibraryOnly(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	testutil.Reset(t, db)
	svc := search.NewService(db)

	alice := testutil.NewVerifiedUser(t, db)
	bob := testutil.NewVerifiedUser(t, db)

	aliceSubj := testutil.NewSubjectNamed(t, db, alice.ID, "A", "private")
	bobPublic := testutil.NewSubjectNamed(t, db, bob.ID, "BP", "public")
	bobPrivate := testutil.NewSubjectNamed(t, db, bob.ID, "BS", "private")
	testutil.NewFlashcard(t, db, aliceSubj.ID, 0, "What is mitochondria?", "powerhouse")
	testutil.NewFlashcard(t, db, bobPublic.ID, 0, "Define mitochondria", "the organelle")
	testutil.NewFlashcard(t, db, bobPrivate.ID, 0, "mitochondria secret", "hidden")

	// Random public content must NOT be searchable — only owner scope by default.
	hits, err := svc.Flashcards(ctx, alice.ID, "mitochondria", false, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit (own only), got %d: %+v", len(hits), hits)
	}
	if hits[0].SubjectID != aliceSubj.ID {
		t.Fatalf("expected alice's subject, got subject_id=%d", hits[0].SubjectID)
	}

	// Partial substring still works for owned scope.
	partial, err := svc.Flashcards(ctx, alice.ID, "toch", false, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(partial) != 1 {
		t.Fatalf("partial 'toch' expected 1 hit, got %d: %+v", len(partial), partial)
	}
}

func TestSearchFlashcards_IncludesSubscribedPublic(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	testutil.Reset(t, db)
	svc := search.NewService(db)

	alice := testutil.NewVerifiedUser(t, db)
	bob := testutil.NewVerifiedUser(t, db)
	bobPublic := testutil.NewSubjectNamed(t, db, bob.ID, "BP", "public")
	testutil.NewFlashcard(t, db, bobPublic.ID, 0, "Define mitochondria", "the organelle")

	// alice subscribes to bob's public subject — now its cards are searchable.
	_, err := db.Exec(ctx,
		`INSERT INTO subject_subscriptions (user_id, subject_id) VALUES ($1, $2)`,
		alice.ID, bobPublic.ID)
	if err != nil {
		t.Fatal(err)
	}

	hits, err := svc.Flashcards(ctx, alice.ID, "mitochondria", false, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("subscribed public: expected 1 hit, got %d: %+v", len(hits), hits)
	}
}

func TestSearchFlashcards_ArchivedToggle(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	testutil.Reset(t, db)
	svc := search.NewService(db)

	alice := testutil.NewVerifiedUser(t, db)
	bob := testutil.NewVerifiedUser(t, db)

	live := testutil.NewSubjectNamed(t, db, alice.ID, "Live", "private")
	arch := testutil.NewSubjectNamed(t, db, alice.ID, "Archived", "private")
	bobPublic := testutil.NewSubjectNamed(t, db, bob.ID, "BP", "public")
	if _, err := db.Exec(ctx, `UPDATE subjects SET archived = true WHERE id = $1`, arch.ID); err != nil {
		t.Fatal(err)
	}
	// Bob's archived subscribed subject must stay hidden even with the flag on.
	if _, err := db.Exec(ctx, `UPDATE subjects SET archived = true WHERE id = $1`, bobPublic.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx,
		`INSERT INTO subject_subscriptions (user_id, subject_id) VALUES ($1, $2)`,
		alice.ID, bobPublic.ID); err != nil {
		t.Fatal(err)
	}
	testutil.NewFlashcard(t, db, live.ID, 0, "mitochondria live", "ans")
	testutil.NewFlashcard(t, db, arch.ID, 0, "mitochondria archived", "ans")
	testutil.NewFlashcard(t, db, bobPublic.ID, 0, "mitochondria foreign", "ans")

	def, err := svc.Flashcards(ctx, alice.ID, "mitochondria", false, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(def) != 1 || def[0].SubjectID != live.ID {
		t.Fatalf("default should only see live owned card; got %+v", def)
	}

	withArchived, err := svc.Flashcards(ctx, alice.ID, "mitochondria", true, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(withArchived) != 2 {
		t.Fatalf("include_archived=true: expected 2 (own live + own archived), got %d: %+v", len(withArchived), withArchived)
	}
	for _, h := range withArchived {
		if h.SubjectID == bobPublic.ID {
			t.Fatalf("include_archived must NOT unhide non-owned archived: %+v", h)
		}
	}
}

func TestSearchUsers(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	testutil.Reset(t, db)
	svc := search.NewService(db)

	_ = testutil.NewVerifiedUserNamed(t, db, "alice_smith")
	_ = testutil.NewVerifiedUserNamed(t, db, "bob_jones")

	hits, err := svc.Users(ctx, "alice", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Username != "alice_smith" {
		t.Fatalf("unexpected hits: %+v", hits)
	}
}
