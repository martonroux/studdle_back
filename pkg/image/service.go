package image

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"studdle/backend/internal/aiProvider"
	"studdle/backend/internal/myErrors"
	"studdle/backend/internal/storage"
)

// Purpose enumerates upload contexts that gate which MIME types and limits apply.
type Purpose string

const (
	// PurposeImage is the default: profile pictures and inline images. Image MIMEs only.
	PurposeImage Purpose = ""

	// PurposeExamAnnales is a past-paper PDF attached to an exam. PDF only, capped at 5 MB and 10 pages.
	PurposeExamAnnales Purpose = "exam_annales"
)

// annalesMaxBytes is the hard size cap for an exam_annales PDF upload (5 MB).
const annalesMaxBytes = 5 << 20

// annalesMaxPages is the hard page-count cap for an exam_annales PDF upload.
const annalesMaxPages = 10

// Image represents an uploaded image row.
type Image struct {
	ID       string // ID is the image primary key (ULID-like identifier)
	OwnerID  int64  // OwnerID is the user ID that uploaded the image
	Filename string // Filename is the name under which the file is stored on disk
	MimeType string // MimeType is the detected MIME type of the image
	Bytes    int64  // Bytes is the size of the stored file in bytes
}

// Service owns upload, fetch, and delete for images.
type Service struct {
	db         *pgxpool.Pool      // db is the Postgres connection pool
	store      *storage.FileStore // store is the filesystem image store
	backendURL string             // backendURL is the public base URL used to build image links
}

// NewService constructs the image service.
func NewService(db *pgxpool.Pool, store *storage.FileStore, backendURL string) *Service {
	return &Service{db: db, store: store, backendURL: backendURL}
}

// Upload reads src, validates it against the rules of purpose, writes to storage, and records the DB row.
// Pass PurposeImage (or "") for legacy image uploads; PurposeExamAnnales for an exam annales PDF.
func (s *Service) Upload(ctx context.Context, uid int64, src io.Reader, filename string, purpose Purpose) (*Image, error) {
	body, mime, err := readAndValidate(src, purpose)
	if err != nil {
		return nil, err
	}
	return s.persist(ctx, uid, body, mime)
}

// readAndValidate sniffs the MIME, recombines src with the sniffed prefix, and applies purpose-specific rules.
// Returns a reader positioned at the start of the file along with the detected MIME type.
func readAndValidate(src io.Reader, purpose Purpose) (io.Reader, string, error) {
	sniff := make([]byte, 512)
	n, _ := io.ReadFull(src, sniff)
	mime := http.DetectContentType(sniff[:n])
	full := io.MultiReader(io.NewSectionReader(newBufReaderAt(sniff[:n]), 0, int64(n)), src)

	switch purpose {
	case PurposeExamAnnales:
		return validateAnnalesPDF(full, mime)
	case PurposeImage:
		return validateImage(full, mime)
	default:
		return nil, "", fmt.Errorf("unknown purpose %q:\n%w", purpose, myErrors.ErrValidation)
	}
}

// validateImage enforces the legacy rule: only image/* types are accepted.
func validateImage(body io.Reader, mime string) (io.Reader, string, error) {
	if !isAllowedImage(mime) {
		return nil, "", fmt.Errorf("unsupported mime type %q:\n%w", mime, myErrors.ErrValidation)
	}
	return body, mime, nil
}

// validateAnnalesPDF buffers the PDF, enforces the 5 MB / 10-page caps, and returns a re-readable body.
func validateAnnalesPDF(body io.Reader, mime string) (io.Reader, string, error) {
	if mime != "application/pdf" {
		return nil, "", &myErrors.AppError{
			Code: "validation", Message: fmt.Sprintf("annales must be PDF, got %q", mime),
			Wrapped: myErrors.ErrValidation, Field: "file",
		}
	}
	pdfBytes, err := io.ReadAll(io.LimitReader(body, annalesMaxBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("read pdf:\n%w", err)
	}
	if int64(len(pdfBytes)) > annalesMaxBytes {
		return nil, "", &myErrors.AppError{
			Code: "annales_too_large", Message: "annales pdf exceeds 5 MB",
			Wrapped: myErrors.ErrValidation, Field: "file",
		}
	}
	if err := checkAnnalesPageCount(pdfBytes); err != nil {
		return nil, "", err
	}
	return bytes.NewReader(pdfBytes), mime, nil
}

// checkAnnalesPageCount returns an AppError if the PDF is unreadable or exceeds annalesMaxPages.
func checkAnnalesPageCount(pdfBytes []byte) error {
	pages, err := aiProvider.PDFPageCount(pdfBytes)
	if err != nil {
		return &myErrors.AppError{
			Code: "pdf_unreadable", Message: err.Error(),
			Wrapped: myErrors.ErrValidation, Field: "file",
		}
	}
	if pages > annalesMaxPages {
		return &myErrors.AppError{
			Code:    "annales_too_many_pages",
			Message: fmt.Sprintf("annales has %d pages, max %d", pages, annalesMaxPages),
			Wrapped: myErrors.ErrValidation, Field: "file",
		}
	}
	return nil
}

// persist writes body to disk, inserts the images row, and rolls back the file on DB failure.
func (s *Service) persist(ctx context.Context, uid int64, body io.Reader, mime string) (*Image, error) {
	id := storage.NewImageID()
	diskName := id + extensionFor(mime)
	path, err := s.store.Write(diskName, body)
	if err != nil {
		return nil, err
	}
	size, err := fileSize(path)
	if err != nil {
		return nil, err
	}
	_, err = s.db.Exec(ctx, `
        INSERT INTO images (id, owner_id, filename, mime_type, bytes)
        VALUES ($1, $2, $3, $4, $5)
    `, id, uid, diskName, mime, size)
	if err != nil {
		_ = s.store.Remove(diskName)
		return nil, fmt.Errorf("insert image:\n%w", err)
	}
	return &Image{ID: id, OwnerID: uid, Filename: diskName, MimeType: mime, Bytes: size}, nil
}

// Open returns an io.ReadCloser for the image and its mime type.
func (s *Service) Open(ctx context.Context, id string) (io.ReadCloser, string, error) {
	img, err := s.byID(ctx, id)
	if err != nil {
		return nil, "", err
	}
	f, err := s.store.Open(img.Filename)
	if err != nil {
		return nil, "", err
	}
	return f, img.MimeType, nil
}

// Delete removes the image row and file if owned by uid.
func (s *Service) Delete(ctx context.Context, uid int64, id string) error {
	img, err := s.byID(ctx, id)
	if err != nil {
		return err
	}
	if img.OwnerID != uid {
		return fmt.Errorf("not owner:\n%w", myErrors.ErrForbidden)
	}
	if _, err := s.db.Exec(ctx, `DELETE FROM images WHERE id = $1`, id); err != nil {
		return fmt.Errorf("delete image row:\n%w", err)
	}
	return s.store.Remove(img.Filename)
}

// URL returns the public fetch URL for an image ID.
func (s *Service) URL(id string) string {
	return s.backendURL + "/images/" + id
}

func (s *Service) byID(ctx context.Context, id string) (*Image, error) {
	img := &Image{}
	err := s.db.QueryRow(ctx,
		`SELECT id, owner_id, filename, mime_type, bytes FROM images WHERE id = $1`, id).
		Scan(&img.ID, &img.OwnerID, &img.Filename, &img.MimeType, &img.Bytes)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("image %s:\n%w", id, myErrors.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("load image:\n%w", err)
	}
	return img, nil
}

func isAllowedImage(mime string) bool {
	switch mime {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return true
	}
	return false
}

func extensionFor(mime string) string {
	switch mime {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "application/pdf":
		return ".pdf"
	}
	return ""
}
