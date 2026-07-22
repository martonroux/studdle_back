package aipipeline

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"studdle/backend/internal/myErrors"
)

// CommitChapter is one chapter the client proposes to create.
type CommitChapter struct {
	ClientID string // ClientID is a frontend-generated string joining cards to this chapter
	Title    string // Title is the chapter name
}

// CommitCard is one flashcard the client proposes to create.
type CommitCard struct {
	ChapterClientID string // ChapterClientID references a Chapters[].ClientID, or "" for loose cards
	Title           string // Title is the optional card heading
	Question        string // Question is the flashcard prompt
	Answer          string // Answer is the flashcard answer
}

// CommitInput is the CommitGeneration request body, server-side.
type CommitInput struct {
	UserID    int64           // UserID is the caller; checked against subject editor rights
	SubjectID int64           // SubjectID is the target subject
	Chapters  []CommitChapter // Chapters may be empty when all cards are loose
	Cards     []CommitCard    // Cards must be non-empty for a meaningful commit
}

// CommitOutput is the CommitGeneration response.
type CommitOutput struct {
	SubjectID  int64            // SubjectID echoes the input
	ChapterIDs map[string]int64 // ChapterIDs maps ClientID → DB id for created chapters
	CardIDs    []int64          // CardIDs is the ordered list of created flashcard ids
}

// CommitGeneration inserts the accepted chapters + cards in a single transaction.
// All cards get source='ai'. On any error the transaction rolls back.
func (s *Service) CommitGeneration(ctx context.Context, in CommitInput) (*CommitOutput, error) {
	if len(in.Cards) == 0 {
		return nil, fmt.Errorf("cards must be non-empty")
	}
	if err := s.assertSubjectEditAccess(ctx, in.UserID, in.SubjectID); err != nil {
		return nil, err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx:\n%w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	chapterIDs, err := insertChapters(ctx, tx, in.SubjectID, in.Chapters)
	if err != nil {
		return nil, err
	}
	cardIDs, err := insertCards(ctx, tx, in.SubjectID, chapterIDs, in.Cards)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit tx:\n%w", err)
	}
	return &CommitOutput{SubjectID: in.SubjectID, ChapterIDs: chapterIDs, CardIDs: cardIDs}, nil
}

// assertSubjectEditAccess rejects the commit unless uid can edit subjectID
// (owner or editor collaborator). Prevents cross-user writes via AI commit.
func (s *Service) assertSubjectEditAccess(ctx context.Context, uid, subjectID int64) error {
	lvl, err := s.access.SubjectLevel(ctx, uid, subjectID)
	if err != nil {
		return err
	}
	if !lvl.CanEdit() {
		return myErrors.ErrForbidden
	}
	return nil
}

// insertChapters inserts all proposed chapters and returns the ClientID→id map.
func insertChapters(ctx context.Context, tx pgx.Tx, subjectID int64, chapters []CommitChapter) (map[string]int64, error) {
	out := make(map[string]int64, len(chapters))
	for i, c := range chapters {
		var id int64
		err := tx.QueryRow(ctx,
			`INSERT INTO chapters (subject_id, title, position) VALUES ($1, $2, $3) RETURNING id`,
			subjectID, c.Title, i,
		).Scan(&id)
		if err != nil {
			return nil, fmt.Errorf("insert chapter %q:\n%w", c.Title, err)
		}
		out[c.ClientID] = id
	}
	return out, nil
}

// insertCards inserts the cards, resolving ChapterClientID via the provided map.
// Unknown ChapterClientID values abort the transaction.
func insertCards(ctx context.Context, tx pgx.Tx, subjectID int64, chapterIDs map[string]int64, cards []CommitCard) ([]int64, error) {
	ids := make([]int64, 0, len(cards))
	for _, c := range cards {
		chapterFK, err := resolveChapterFK(chapterIDs, c.ChapterClientID)
		if err != nil {
			return nil, err
		}
		var id int64
		err = tx.QueryRow(ctx, `
            INSERT INTO flashcards (subject_id, chapter_id, title, question, answer, source)
            VALUES ($1, $2, $3, $4, $5, 'ai')
            RETURNING id
        `, subjectID, chapterFK, c.Title, c.Question, c.Answer).Scan(&id)
		if err != nil {
			return nil, fmt.Errorf("insert flashcard:\n%w", err)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// resolveChapterFK looks up the DB id for a ChapterClientID; empty string means "loose card".
// An unknown ChapterClientID is a client input mistake, not a server error, so
// it is reported as ErrValidation (→ 400) rather than an opaque 500.
func resolveChapterFK(chapterIDs map[string]int64, clientID string) (*int64, error) {
	if clientID == "" {
		return nil, nil
	}
	id, ok := chapterIDs[clientID]
	if !ok {
		return nil, &myErrors.AppError{
			Code:    "validation",
			Message: fmt.Sprintf("unknown chapterClientId %q", clientID),
			Field:   "chapterClientId",
			Wrapped: myErrors.ErrValidation,
		}
	}
	return &id, nil
}
