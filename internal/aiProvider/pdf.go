package aiProvider

import (
	"bytes"
	"context"
	"fmt"
	"image/jpeg"
	"runtime"
	"sync"
	"time"

	fitz "github.com/gen2brain/go-fitz"
)

// jpegQuality controls the JPEG encoder used for rasterized PDF pages.
// 80 is a strong sweet spot for text/diagram pages: small payloads with
// no perceptible loss for vision-model OCR.
const jpegQuality = 80

// PDFOptions configures the PDF→image pipeline.
type PDFOptions struct {
	MaxConcurrency int           // MaxConcurrency caps simultaneous page conversions (0 = NumCPU)
	PerPageTimeout time.Duration // PerPageTimeout aborts a single page that hangs (0 = 30s)
	MaxPages       int           // MaxPages refuses inputs beyond this count (0 = no cap)
}

// PDFToImages rasterizes each page of pdfBytes to PNG and returns them in page order.
// Bounded concurrency and per-page timeout cap resource usage.
func PDFToImages(ctx context.Context, pdfBytes []byte, opts PDFOptions) ([]ImagePart, error) {
	opts = applyPDFDefaults(opts)
	doc, err := fitz.NewFromMemory(pdfBytes)
	if err != nil {
		return nil, fmt.Errorf("open pdf:\n%w", err)
	}
	defer doc.Close()

	n := doc.NumPage()
	if opts.MaxPages > 0 && n > opts.MaxPages {
		return nil, fmt.Errorf("pdf has %d pages, max %d", n, opts.MaxPages)
	}
	return renderPages(ctx, doc, n, opts)
}

// applyPDFDefaults fills in zero-valued opts.
func applyPDFDefaults(opts PDFOptions) PDFOptions {
	if opts.MaxConcurrency <= 0 {
		opts.MaxConcurrency = runtime.NumCPU()
	}
	if opts.PerPageTimeout <= 0 {
		opts.PerPageTimeout = 30 * time.Second
	}
	return opts
}

// renderPages fans pages out to a worker pool; returns images in source page order.
func renderPages(ctx context.Context, doc *fitz.Document, n int, opts PDFOptions) ([]ImagePart, error) {
	imgs := make([]ImagePart, n)
	errs := make([]error, n)
	sem := make(chan struct{}, opts.MaxConcurrency)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for i := 0; i < n; i++ {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go renderOne(ctx, doc, i, opts.PerPageTimeout, imgs, errs, sem, &wg, &mu)
	}
	wg.Wait()
	return combineResults(imgs, errs)
}

// renderOne rasterizes page idx into imgs[idx] / errs[idx].
func renderOne(
	ctx context.Context, doc *fitz.Document, idx int, timeout time.Duration,
	imgs []ImagePart, errs []error, sem chan struct{}, wg *sync.WaitGroup, mu *sync.Mutex,
) {
	defer wg.Done()
	defer func() { <-sem }()
	pageCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	done := make(chan struct{})
	var part ImagePart
	var err error
	go func() {
		part, err = renderPageJPEG(doc, idx, mu)
		close(done)
	}()
	select {
	case <-done:
		imgs[idx] = part
		errs[idx] = err
	case <-pageCtx.Done():
		errs[idx] = fmt.Errorf("page %d: %w", idx, pageCtx.Err())
	}
}

// renderPageJPEG renders one page to JPEG. doc access is serialized via mu
// because go-fitz documents are not goroutine-safe.
func renderPageJPEG(doc *fitz.Document, idx int, mu *sync.Mutex) (ImagePart, error) {
	mu.Lock()
	defer mu.Unlock()
	img, err := doc.Image(idx)
	if err != nil {
		return ImagePart{}, fmt.Errorf("render page %d:\n%w", idx, err)
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return ImagePart{}, fmt.Errorf("encode page %d:\n%w", idx, err)
	}
	return ImagePart{MediaType: "image/jpeg", Data: buf.Bytes()}, nil
}

// combineResults returns imgs when all errs are nil; else the first error.
func combineResults(imgs []ImagePart, errs []error) ([]ImagePart, error) {
	for _, e := range errs {
		if e != nil {
			return nil, e
		}
	}
	return imgs, nil
}

// PDFPageCount returns the number of pages in pdfBytes without rasterizing.
// Useful for upload-time validation (page caps, quota estimation).
func PDFPageCount(pdfBytes []byte) (int, error) {
	if len(pdfBytes) == 0 {
		return 0, fmt.Errorf("empty pdf bytes")
	}

	doc, err := fitz.NewFromMemory(pdfBytes)
	if err != nil {
		return 0, fmt.Errorf("open pdf:\n%w", err)
	}

	defer doc.Close()

	return doc.NumPage(), nil
}

// PDFToText extracts the per-page text content of pdfBytes.
// Returns one string per page in source order. The text content is
// what go-fitz's text-extraction layer produces — useful for the
// text-mode flashcard generation path when image-mode is not viable.
func PDFToText(ctx context.Context, pdfBytes []byte) ([]string, error) {
	if len(pdfBytes) == 0 {
		return nil, fmt.Errorf("empty pdf bytes")
	}
	doc, err := fitz.NewFromMemory(pdfBytes)
	if err != nil {
		return nil, fmt.Errorf("open pdf:\n%w", err)
	}
	defer doc.Close()

	n := doc.NumPage()
	pages := make([]string, n)
	for i := 0; i < n; i++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		text, err := doc.Text(i)
		if err != nil {
			return nil, fmt.Errorf("extract text page %d:\n%w", i, err)
		}
		pages[i] = text
	}
	return pages, nil
}
