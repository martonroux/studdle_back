package aipipeline

import (
	"context"
	"fmt"
	"time"

	"studbud/backend/internal/myErrors"
)

// quotaRow is the per-user per-day counter tuple we read before a call.
type quotaRow struct {
	PromptCalls           int // PromptCalls is today's prompt generation count
	PDFCalls              int // PDFCalls is today's PDF generation count
	PDFPages              int // PDFPages is today's PDF page consumption
	CheckCalls            int // CheckCalls is today's check-flashcard count
	PlanCalls             int // PlanCalls is today's revision-plan generation count
	CrossSubjectRankCalls int // CrossSubjectRankCalls is today's cross-subject rank count
	QuizCalls             int // QuizCalls is today's quiz generation count
}

// QuotaBucket is one feature's used/limit/reset tuple as surfaced to clients.
type QuotaBucket struct {
	Used    int       `json:"used"`    // Used is the count of operations performed today
	Limit   int       `json:"limit"`   // Limit is the per-feature daily cap
	ResetAt time.Time `json:"resetAt"` // ResetAt is midnight UTC of tomorrow
}

// PDFBucket extends QuotaBucket with separate page accounting.
type PDFBucket struct {
	Used       int       `json:"used"`       // Used is today's PDF generation count
	Limit      int       `json:"limit"`      // Limit is the per-day PDF generation cap
	PagesUsed  int       `json:"pagesUsed"`  // PagesUsed is today's PDF page consumption
	PagesLimit int       `json:"pagesLimit"` // PagesLimit is the per-day page cap
	ResetAt    time.Time `json:"resetAt"`    // ResetAt is midnight UTC of tomorrow
}

// QuotaSnapshot is the GET /ai/quota response shape.
type QuotaSnapshot struct {
	AIAccess bool        `json:"aiAccess"` // AIAccess reflects user_has_ai_access
	Prompt   QuotaBucket `json:"prompt"`   // Prompt is the prompt-mode bucket
	PDF      PDFBucket   `json:"pdf"`      // PDF is the PDF-mode bucket
	Check    QuotaBucket `json:"check"`    // Check is the AI-check bucket
}

// CheckQuota asserts the user has budget left for the given feature.
// pdfPages is used only for FeatureGenerateFromPDF; pass 0 otherwise.
// Returns ErrQuotaExhausted (wrapped in AppError) when over-budget.
func (s *Service) CheckQuota(ctx context.Context, uid int64, feat FeatureKey, pdfPages int) error {
	row, err := s.readOrCreateQuotaRow(ctx, uid)
	if err != nil {
		return err
	}
	return checkAgainstLimits(row, feat, pdfPages, s.limits)
}

// readOrCreateQuotaRow ensures the row exists for today and returns its counters.
func (s *Service) readOrCreateQuotaRow(ctx context.Context, uid int64) (quotaRow, error) {
	if _, err := s.db.Exec(ctx, sqlEnsureQuotaRow, uid); err != nil {
		return quotaRow{}, fmt.Errorf("ensure quota row:\n%w", err)
	}
	var row quotaRow
	err := s.db.QueryRow(ctx, sqlSelectQuotaRow, uid).Scan(&row.PromptCalls, &row.PDFCalls, &row.PDFPages, &row.CheckCalls, &row.PlanCalls, &row.CrossSubjectRankCalls, &row.QuizCalls)
	if err != nil {
		return quotaRow{}, fmt.Errorf("select quota row:\n%w", err)
	}
	return row, nil
}

// checkAgainstLimitsPure is the routing logic for quota checks, factored as a
// pure function to support unit testing without a database.
// The used map keys match the ai_quota_daily column names.
func checkAgainstLimitsPure(feat FeatureKey, used map[string]int, limits QuotaLimits, pdfPages int) error {
	switch feat {
	case FeatureGenerateFromPrompt:
		if used["prompt_calls"] >= limits.PromptCalls {
			return quotaExhausted("prompt")
		}
	case FeatureGenerateFromPDF:
		if used["pdf_calls"] >= limits.PDFCalls {
			return quotaExhausted("pdf")
		}
		if used["pdf_pages"]+pdfPages > limits.PDFPages {
			return quotaExhausted("pdf_pages")
		}
	case FeatureCheckFlashcard:
		if used["check_calls"] >= limits.CheckCalls {
			return quotaExhausted("check")
		}
	case FeatureGenerateRevisionPlan:
		if used["plan_calls"] >= limits.PlanCalls {
			return quotaExhausted("plan")
		}
	case FeatureGenerateQuiz:
		if used["quiz_calls"] >= limits.QuizCalls {
			return quotaExhausted("quiz")
		}
	case FeatureCrossSubjectRank:
		return nil // sub-step of plan generation; no quota check
	}
	return nil
}

// checkAgainstLimits returns an ErrQuotaExhausted if the feature is over cap.
func checkAgainstLimits(row quotaRow, feat FeatureKey, pdfPages int, lim QuotaLimits) error {
	used := map[string]int{
		"prompt_calls":             row.PromptCalls,
		"pdf_calls":                row.PDFCalls,
		"pdf_pages":                row.PDFPages,
		"check_calls":              row.CheckCalls,
		"plan_calls":               row.PlanCalls,
		"cross_subject_rank_calls": row.CrossSubjectRankCalls,
		"quiz_calls":               row.QuizCalls,
	}
	return checkAgainstLimitsPure(feat, used, lim, pdfPages)
}

// quotaExhausted builds an AppError wrapping ErrQuotaExhausted with feature-specific message.
func quotaExhausted(bucket string) error {
	return &myErrors.AppError{
		Code:    "quota_exceeded",
		Message: fmt.Sprintf("daily %s quota exhausted; resets at midnight UTC", bucket),
		Wrapped: myErrors.ErrQuotaExhausted,
	}
}

// DebitQuota increments the relevant counter(s) for feat.
// For FeatureGenerateFromPDF pass pages > 0 to also bump pdf_pages.
// Prompt/PDF/check "calls" are debited once per successful job (callers decide when).
func (s *Service) DebitQuota(ctx context.Context, uid int64, feat FeatureKey, calls, pages int) error {
	if _, err := s.db.Exec(ctx, sqlEnsureQuotaRow, uid); err != nil {
		return fmt.Errorf("ensure quota row:\n%w", err)
	}
	if err := s.debitCalls(ctx, uid, feat, calls); err != nil {
		return err
	}
	if feat == FeatureGenerateFromPDF && pages > 0 {
		if _, err := s.db.Exec(ctx, sqlDebitPDFPages, uid, pages); err != nil {
			return fmt.Errorf("debit pdf_pages:\n%w", err)
		}
	}
	return nil
}

// debitCalls routes to the per-feature call counter.
func (s *Service) debitCalls(ctx context.Context, uid int64, feat FeatureKey, calls int) error {
	if calls <= 0 {
		return nil
	}
	q, err := debitCallsSQL(feat)
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(ctx, q, uid, calls); err != nil {
		return fmt.Errorf("debit %s calls:\n%w", feat, err)
	}
	return nil
}

// debitCallsSQL returns the UPDATE statement for feat's calls column.
func debitCallsSQL(feat FeatureKey) (string, error) {
	switch feat {
	case FeatureGenerateFromPrompt:
		return sqlDebitPromptCalls, nil
	case FeatureGenerateFromPDF:
		return sqlDebitPDFCalls, nil
	case FeatureCheckFlashcard:
		return sqlDebitCheckCalls, nil
	case FeatureGenerateRevisionPlan:
		return sqlDebitPlanCalls, nil
	case FeatureCrossSubjectRank:
		return sqlDebitCrossSubjectRankCalls, nil
	case FeatureGenerateQuiz:
		return sqlDebitQuizCalls, nil
	}
	return "", fmt.Errorf("unknown feature %q", feat)
}

// QuotaSnapshot returns the per-feature usage counters for today plus aiAccess.
func (s *Service) QuotaSnapshot(ctx context.Context, uid int64) (*QuotaSnapshot, error) {
	row, err := s.readOrCreateQuotaRow(ctx, uid)
	if err != nil {
		return nil, err
	}
	hasAccess, err := s.access.HasAIAccess(ctx, uid)
	if err != nil {
		return nil, err
	}
	return buildSnapshot(row, s.limits, hasAccess), nil
}

// buildSnapshot assembles the JSON-facing QuotaSnapshot from raw counters.
func buildSnapshot(row quotaRow, lim QuotaLimits, aiAccess bool) *QuotaSnapshot {
	reset := nextMidnightUTC(time.Now())
	return &QuotaSnapshot{
		AIAccess: aiAccess,
		Prompt:   QuotaBucket{Used: row.PromptCalls, Limit: lim.PromptCalls, ResetAt: reset},
		PDF: PDFBucket{
			Used: row.PDFCalls, Limit: lim.PDFCalls,
			PagesUsed: row.PDFPages, PagesLimit: lim.PDFPages,
			ResetAt: reset,
		},
		Check: QuotaBucket{Used: row.CheckCalls, Limit: lim.CheckCalls, ResetAt: reset},
	}
}

// nextMidnightUTC returns the next 00:00 UTC instant strictly after t.
func nextMidnightUTC(t time.Time) time.Time {
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), u.Day()+1, 0, 0, 0, 0, time.UTC)
}

// CheckAgainstLimitsForTest is a pure-function shim over the limit-routing
// branch of checkAgainstLimits so unit tests can exercise it without a DB.
// The real path goes through Service.CheckQuota.
func CheckAgainstLimitsForTest(feat FeatureKey, used map[string]int, limits QuotaLimits, pdfPages int) error {
	return checkAgainstLimitsPure(feat, used, limits, pdfPages)
}
