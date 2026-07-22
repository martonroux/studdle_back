package image

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"studdle/backend/internal/myErrors"
	"studdle/backend/internal/storage"
	"studdle/backend/testutil"
)

// 1x1 PNG (red pixel).
var pngBytes = []byte{
	0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D,
	0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53, 0xDE, 0x00, 0x00, 0x00,
	0x0C, 0x49, 0x44, 0x41, 0x54, 0x08, 0xD7, 0x63, 0xF8, 0xCF, 0xC0, 0x00,
	0x00, 0x00, 0x03, 0x00, 0x01, 0x5B, 0x2F, 0xC0, 0x0F, 0x00, 0x00, 0x00,
	0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE, 0x42, 0x60, 0x82,
}

func TestUploadOpenDelete(t *testing.T) {
	svc, u, cleanup := newTestService(t)
	defer cleanup()

	img, err := svc.Upload(context.Background(), u.ID, bytes.NewReader(pngBytes), "pic.png", PurposeImage)
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if img.MimeType != "image/png" {
		t.Fatalf("mime = %q, want image/png", img.MimeType)
	}

	rc, mime, err := svc.Open(context.Background(), img.ID)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	if mime != "image/png" || len(b) == 0 {
		t.Fatalf("Open returned bad data")
	}

	if err := svc.Delete(context.Background(), u.ID, img.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, _, err := svc.Open(context.Background(), img.ID); err == nil {
		t.Fatal("expected error after Delete")
	}
}

func TestUpload_ExamAnnales_AcceptsPDF(t *testing.T) {
	svc, u, cleanup := newTestService(t)
	defer cleanup()

	pdf := loadAnnalesPDF(t)
	img, err := svc.Upload(context.Background(), u.ID, bytes.NewReader(pdf), "annales.pdf", PurposeExamAnnales)
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if img.MimeType != "application/pdf" {
		t.Fatalf("mime = %q, want application/pdf", img.MimeType)
	}
	if !bytes.HasSuffix([]byte(img.Filename), []byte(".pdf")) {
		t.Fatalf("filename %q does not have .pdf extension", img.Filename)
	}
}

func TestUpload_ExamAnnales_RejectsImage(t *testing.T) {
	svc, u, cleanup := newTestService(t)
	defer cleanup()

	_, err := svc.Upload(context.Background(), u.ID, bytes.NewReader(pngBytes), "pic.png", PurposeExamAnnales)
	if err == nil {
		t.Fatal("expected error: PNG should be rejected for exam_annales")
	}
	if !errors.Is(err, myErrors.ErrValidation) {
		t.Fatalf("err = %v, want ErrValidation", err)
	}
}

func TestUpload_DefaultPurpose_RejectsPDF(t *testing.T) {
	svc, u, cleanup := newTestService(t)
	defer cleanup()

	pdf := loadAnnalesPDF(t)
	_, err := svc.Upload(context.Background(), u.ID, bytes.NewReader(pdf), "annales.pdf", PurposeImage)
	if err == nil {
		t.Fatal("expected error: PDF should be rejected for default image purpose")
	}
	if !errors.Is(err, myErrors.ErrValidation) {
		t.Fatalf("err = %v, want ErrValidation", err)
	}
}

func TestUpload_UnknownPurpose_Rejected(t *testing.T) {
	svc, u, cleanup := newTestService(t)
	defer cleanup()

	_, err := svc.Upload(context.Background(), u.ID, bytes.NewReader(pngBytes), "pic.png", Purpose("nonsense"))
	if err == nil {
		t.Fatal("expected error on unknown purpose")
	}
	if !errors.Is(err, myErrors.ErrValidation) {
		t.Fatalf("err = %v, want ErrValidation", err)
	}
}

// newTestService builds a Service backed by a tmp filesystem and the shared test DB.
func newTestService(t *testing.T) (*Service, *testutil.UserFixture, func()) {
	t.Helper()
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	dir, err := os.MkdirTemp("", "imgtest-*")
	if err != nil {
		t.Fatal(err)
	}
	store, _ := storage.NewFileStore(dir)
	svc := NewService(pool, store, "http://localhost:8080")
	cleanup := func() { os.RemoveAll(dir) }
	return svc, u, cleanup
}

// loadAnnalesPDF reads a small (≤10-page) PDF from the aiProvider testdata directory.
// Skips the test if the fixture isn't available (e.g. cgo-disabled builds).
func loadAnnalesPDF(t *testing.T) []byte {
	t.Helper()
	path := filepath.Join("..", "..", "internal", "aiProvider", "testdata", "sample.pdf")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("no test PDF at %s: %v", path, err)
	}
	return b
}
