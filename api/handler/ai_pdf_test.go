package handler_test

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"studdle/backend/internal/aiProvider"
	"studdle/backend/testutil"
)

func newPDFFormReader(t *testing.T, subjectID int64, mode string, pdfBytes []byte) (*bytes.Buffer, string) {
	t.Helper()
	form := new(bytes.Buffer)
	w := multipart.NewWriter(form)
	_ = w.WriteField("subject_id", strconv.FormatInt(subjectID, 10))
	if mode != "" {
		_ = w.WriteField("mode", mode)
	}
	fw, err := w.CreateFormFile("file", "test.pdf")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := io.Copy(fw, bytes.NewReader(pdfBytes)); err != nil {
		t.Fatalf("copy pdf: %v", err)
	}
	_ = w.Close()
	return form, w.FormDataContentType()
}

// largePDFFixture returns bytes of a PDF with strictly more than 30 pages.
// Looks for api/handler/testdata/large.pdf; skips the test if missing.
func largePDFFixture(t *testing.T) []byte {
	t.Helper()
	path := filepath.Join("testdata", "large.pdf")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("missing api/handler/testdata/large.pdf (>30 pages required): %v", err)
	}
	return b
}

func TestGenerateFromPDF_ImageModeRejectsOver30Pages(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	subj := testutil.NewSubject(t, pool, u.ID)

	bigPDF := largePDFFixture(t)
	srv := newAIPDFServer(t, pool, &testutil.FakeAIClient{})
	tok := mintToken(t, u.ID, true, false)

	form, ct := newPDFFormReader(t, subj.ID, "image", bigPDF)
	req := httptest.NewRequest("POST", "/ai/flashcards/pdf", form)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body = %s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("pdf_image_mode_unavailable")) {
		t.Errorf("body missing pdf_image_mode_unavailable: %s", w.Body.String())
	}
}

func TestGenerateFromPDF_TextModeAcceptsLargePDF(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	subj := testutil.NewSubject(t, pool, u.ID)

	cli := &testutil.FakeAIClient{
		Chunks: []aiProvider.Chunk{
			{Text: `{"items":[{"title":"t1","question":"q1","answer":"a1"}]}`, Done: true},
		},
	}
	srv := newAIPDFServer(t, pool, cli)
	tok := mintToken(t, u.ID, true, false)

	bigPDF := largePDFFixture(t) // same >30-page fixture; text mode tolerates it
	form, ct := newPDFFormReader(t, subj.ID, "text", bigPDF)
	req := httptest.NewRequest("POST", "/ai/flashcards/pdf", form)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("event: card")) {
		t.Errorf("expected card event in stream:\n%s", w.Body.String())
	}
	// Verify the provider was called with no images (text mode).
	if len(cli.LastRequest().Images) != 0 {
		t.Errorf("text mode passed images to provider: got %d", len(cli.LastRequest().Images))
	}
}

// TestGenerateFromPDF_MissingSubjectIDReturns400 is a regression test for
// AI-6: a missing/zero subject_id previously defaulted to 0, which then
// 404'd on the subject lookup instead of failing input validation first.
func TestGenerateFromPDF_MissingSubjectIDReturns400(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)

	srv := newAIPDFServer(t, pool, &testutil.FakeAIClient{})
	tok := mintToken(t, u.ID, true, false)

	form, ct := newPDFFormReader(t, 0, "image", []byte("%PDF-1.4 fake"))
	req := httptest.NewRequest("POST", "/ai/flashcards/pdf", form)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", w.Code, w.Body.String())
	}
}

func TestGenerateFromPDF_TextModeRejectsOver200Pages(t *testing.T) {
	t.Skip("requires >200-page fixture; add api/handler/testdata/very_large.pdf to enable")
}

func TestGenerateFromPDF_TextModeRejectsOver400kChars(t *testing.T) {
	t.Skip("requires text-heavy fixture > 400k chars; add api/handler/testdata/text_heavy.pdf to enable")
}
