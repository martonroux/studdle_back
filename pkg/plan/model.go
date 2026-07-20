package plan

import "time"

// Candidate is one cross-subject flashcard returned by the shortlist query.
type Candidate struct {
	ID           int64    `json:"id"`           // ID is the flashcard primary key
	Title        string   `json:"title"`        // Title is the flashcard title
	SubjectID    int64    `json:"subjectId"`    // SubjectID is the owning subject
	SubjectName  string   `json:"subjectName"`  // SubjectName is the human-readable subject label
	Keywords     []string `json:"keywords"`     // Keywords lists the FC's tagged keywords
	OverlapScore int      `json:"overlapScore"` // OverlapScore is the count of keywords shared with the exam subject
	WeightSum    float64  `json:"weightSum"`    // WeightSum is the cumulative tf-idf-style weight of overlapping keywords
}

// PrimaryCard is one flashcard in the exam's primary subject, supplied as plan input.
type PrimaryCard struct {
	ID       int64    `json:"id"`       // ID is the flashcard primary key
	Title    string   `json:"title"`    // Title is the flashcard title
	Keywords []string `json:"keywords"` // Keywords lists the FC's tagged keywords
}

// Day is one bucket in a revision plan.
// Card IDs reference flashcards.id; ordering is intentional (AI-assigned study order).
type Day struct {
	Date                string  `json:"date"`                // Date is the YYYY-MM-DD string for this bucket
	PrimarySubjectCards []int64 `json:"primarySubjectCards"` // PrimarySubjectCards is the same-subject FC list
	CrossSubjectCards   []int64 `json:"crossSubjectCards"`   // CrossSubjectCards is the cross-subject FC list (may be empty)
	DeeperDives         []int64 `json:"deeperDives"`         // DeeperDives is the bonus-tier list, unlocked when daily goal is met
}

// Plan is the persisted output of a generation run.
type Plan struct {
	ID           int64     `json:"id"`                     // ID is the BIGSERIAL primary key
	ExamID       int64     `json:"examId"`                 // ExamID is the owning exam
	Days         []Day     `json:"days"`                   // Days is the per-day schedule from today → exam date
	Model        string    `json:"model"`                  // Model is the AI model identifier used to generate the plan
	PromptHash   string    `json:"promptHash"`             // PromptHash captures the prompt revision for debugging plan drift
	GeneratedAt  time.Time `json:"generatedAt"`            // GeneratedAt is the persistence timestamp
	GenerationID *int64    `json:"generationId,omitempty"` // GenerationID is the ai_jobs row id that produced this plan (nil for legacy rows)
}

// TodayBucket is the projection of a single day's plan plus per-card completion flags.
type TodayBucket struct {
	Date                string  `json:"date"`                // Date is the YYYY-MM-DD string
	PrimarySubjectCards []int64 `json:"primarySubjectCards"` // PrimarySubjectCards mirrors Day
	CrossSubjectCards   []int64 `json:"crossSubjectCards"`   // CrossSubjectCards mirrors Day
	DeeperDives         []int64 `json:"deeperDives"`         // DeeperDives mirrors Day
	Done                []int64 `json:"done"`                // Done is the subset of FCs marked done today
	DailyGoalMet        bool    `json:"dailyGoalMet"`        // DailyGoalMet is true when len(Done) >= primary+cross count
}

// Drift is the regen-suggestion projection on top of the stored plan.
type Drift struct {
	DaysBehind         int  `json:"daysBehind"`         // DaysBehind counts past days under the 50% completion threshold
	ShouldSuggestRegen bool `json:"shouldSuggestRegen"` // ShouldSuggestRegen is true when DaysBehind >= 2
}

// PlanView is the GET /exams/:id/plan response shape.
type PlanView struct {
	Plan  Plan        `json:"plan"`  // Plan is the stored plan body
	Today TodayBucket `json:"today"` // Today is the convenience bucket the UI renders by default
	Drift Drift       `json:"drift"` // Drift is the regen-suggestion projection
}
