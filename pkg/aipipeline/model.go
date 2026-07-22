package aipipeline

import (
	"encoding/json"
	"time"

	"studdle/backend/internal/aiProvider"
)

// FeatureKey identifies the feature that an AI call belongs to.
type FeatureKey string

const (
	// FeatureGenerateFromPrompt is the prompt-based flashcard generator.
	FeatureGenerateFromPrompt FeatureKey = "generate_prompt"
	// FeatureGenerateFromPDF is the PDF-based flashcard generator.
	FeatureGenerateFromPDF FeatureKey = "generate_pdf"
	// FeatureCheckFlashcard is the AI-check of an existing flashcard.
	FeatureCheckFlashcard FeatureKey = "check_flashcard"
	// FeatureExtractKeywords is the background keyword-extraction worker (Spec B.0).
	FeatureExtractKeywords FeatureKey = "extract_keywords"
	// FeatureGenerateRevisionPlan is the AI revision plan generator (Spec B).
	FeatureGenerateRevisionPlan FeatureKey = "revision_plan"
	// FeatureCrossSubjectRank is the cross-subject ranking helper (Spec B).
	FeatureCrossSubjectRank FeatureKey = "cross_subject_rank"
)

// ChunkKind tags a streamed AIChunk.
type ChunkKind string

const (
	// ChunkItem carries one validated flashcard JSON object.
	ChunkItem ChunkKind = "item"
	// ChunkChapter carries one validated chapter JSON object {index,title}.
	ChunkChapter ChunkKind = "chapter"
	// ChunkProgress carries an optional progress update.
	ChunkProgress ChunkKind = "progress"
	// ChunkDone marks successful stream termination.
	ChunkDone ChunkKind = "done"
	// ChunkError marks a terminal error on the stream.
	ChunkError ChunkKind = "error"
)

// ProgressInfo describes where a PDF-based generation is within its input.
type ProgressInfo struct {
	Phase string `json:"phase"`           // Phase is a short tag ("analyzing", "writing")
	Page  int    `json:"page,omitempty"`  // Page is the current page index, 1-based
	Total int    `json:"total,omitempty"` // Total is the page count when known
}

// AIChunk is one emission from RunStructuredGeneration.
type AIChunk struct {
	Kind     ChunkKind       // Kind tags the payload
	Item     json.RawMessage // Item is set when Kind == ChunkItem
	Progress *ProgressInfo   // Progress is set when Kind == ChunkProgress
	Err      error           // Err is set when Kind == ChunkError
}

// AIRequest is the pipeline invocation shape.
type AIRequest struct {
	UserID       int64                  // UserID is the authenticated user running the call
	Feature      FeatureKey             // Feature selects quota counter + concurrent-cap policy
	SubjectID    int64                  // SubjectID must be set for generation features
	FlashcardID  int64                  // FlashcardID is non-zero only for FeatureCheckFlashcard
	Prompt       string                 // Prompt is the assembled user-facing prompt body
	PDFBytes     []byte                 // PDFBytes is populated only for FeatureGenerateFromPDF
	PDFPages     int                    // PDFPages is the declared page count (pre-counted by handler)
	Images       []aiProvider.ImagePart // Images is populated only for FeatureGenerateFromPDF (pre-rasterized)
	Schema       json.RawMessage        // Schema is the tool-use JSON schema for the expected output
	Metadata     map[string]any         // Metadata is persisted into ai_jobs.metadata (style, focus, coverage...)
	DropChapters bool                   // DropChapters suppresses ChunkChapter emissions when chapters are disabled
}

// QuotaLimits holds per-feature daily caps.
type QuotaLimits struct {
	PromptCalls int // PromptCalls is the daily cap on successful prompt generations
	PDFCalls    int // PDFCalls is the daily cap on successful PDF generations
	PDFPages    int // PDFPages is the daily cap on total PDF pages consumed
	CheckCalls  int // CheckCalls is the daily cap on successful check calls
	PlanCalls   int // PlanCalls caps daily revision-plan generations (default 5)
}

// DefaultQuotaLimits returns the v1 starting caps. Tune post-launch.
func DefaultQuotaLimits() QuotaLimits {
	return QuotaLimits{
		PromptCalls: 20,
		PDFCalls:    5,
		PDFPages:    100,
		CheckCalls:  50,
		PlanCalls:   5,
	}
}

// AIJob is the ai_jobs row projection used by the service.
type AIJob struct {
	ID           int64      // ID is the BIGSERIAL primary key
	UserID       int64      // UserID owns the job
	FeatureKey   FeatureKey // FeatureKey identifies the feature
	Model        string     // Model is the provider model identifier
	SubjectID    *int64     // SubjectID is nil when the feature doesn't target a subject
	FlashcardID  *int64     // FlashcardID is set for check-flashcard jobs only
	Status       string     // Status is running | complete | failed | cancelled
	InputTokens  int        // InputTokens counts provider prompt tokens
	OutputTokens int        // OutputTokens counts provider completion tokens
	CentsSpent   int        // CentsSpent is the rounded cost estimate
	PDFPageCount int        // PDFPageCount mirrors the declared PDFPages at start
	ItemsEmitted int        // ItemsEmitted counts items that passed validation
	ItemsDropped int        // ItemsDropped counts items that failed validation
	ErrorKind    string     // ErrorKind is empty on success
	ErrorMessage string     // ErrorMessage is empty on success
	Metadata     []byte     // Metadata is the raw JSONB blob
	StartedAt    time.Time  // StartedAt is the insertion timestamp
	FinishedAt   *time.Time // FinishedAt is nil while status == running
}
