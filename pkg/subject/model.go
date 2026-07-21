package subject

import "time"

// Subject represents a study subject owned by a user.
type Subject struct {
	ID          int64      `json:"id"`          // ID is the subject's primary key
	OwnerID     int64      `json:"owner_id"`    // OwnerID is the user who created the subject
	Name        string     `json:"name"`        // Name is the subject's display name
	Color       string     `json:"color"`       // Color is a hex code used by the UI
	Icon        string     `json:"icon"`        // Icon is an emoji or icon identifier
	Tags        string     `json:"tags"`        // Tags is a space-separated tag list
	Visibility  string     `json:"visibility"`  // Visibility is one of private|friends|public
	Archived    bool       `json:"archived"`    // Archived hides the subject from active lists
	Description string     `json:"description"` // Description is a short free-text summary
	LastUsed    *time.Time `json:"last_used"`   // LastUsed stores the last training timestamp
	CreatedAt   time.Time  `json:"created_at"`  // CreatedAt stores creation time
	UpdatedAt   time.Time  `json:"updated_at"`  // UpdatedAt stores last update time
}

// CreateInput is the payload to create a subject.
type CreateInput struct {
	Name        string `json:"name"`        // Name is the subject's display name
	Color       string `json:"color"`       // Color is an optional hex code
	Icon        string `json:"icon"`        // Icon is an optional emoji
	Tags        string `json:"tags"`        // Tags is an optional tag list
	Visibility  string `json:"visibility"`  // Visibility is private|friends|public (default private)
	Description string `json:"description"` // Description is optional
}

// StatsResponse is returned from GET /subject-stats.
type StatsResponse struct {
	TotalCards     int     `json:"totalCards"`     // TotalCards is the total number of cards in the subject
	GoodCount      int     `json:"goodCount"`      // GoodCount is cards whose last review was good (2)
	OkCount        int     `json:"okCount"`        // OkCount is cards whose last review was ok (1)
	BadCount       int     `json:"badCount"`       // BadCount is cards whose last review was bad (0)
	NewCount       int     `json:"newCount"`       // NewCount is cards not yet reviewed (-1)
	CardsStudied   int     `json:"cardsStudied"`   // CardsStudied is TotalCards - NewCount
	MasteryPercent float64 `json:"masteryPercent"` // MasteryPercent weights good=1, ok=0.5 against TotalCards
}

// HistoryResponse is returned from GET /subject-stats-history.
type HistoryResponse struct {
	Sessions []SessionEntry `json:"sessions"` // Sessions is the most recent sessions, newest first
	Heatmap  []DayIntensity `json:"heatmap"`  // Heatmap is the last 8 full weeks, oldest first
	Chapters []ChapterEntry `json:"chapters"` // Chapters is per-chapter aggregation, ordered by chapter_id
}

// SessionEntry is one row of session history.
type SessionEntry struct {
	CompletedAt time.Time `json:"completedAt"`
	ChapterID   *int64    `json:"chapterId"`   // ChapterID is nil for sessions recorded before chapter attribution shipped
	ChapterName *string   `json:"chapterName"` // ChapterName is nil when ChapterID is nil
	Cards       int       `json:"cards"`
	DurationMs  int       `json:"durationMs"`
	Accuracy    float64   `json:"accuracy"` // Accuracy is score / (2 * cards); 0 when cards == 0
}

// DayIntensity is one calendar day's training volume for the activity heatmap.
type DayIntensity struct {
	Day   string `json:"day"`   // Day is "2026-07-21"
	Cards int    `json:"cards"` // Cards is the sum of total_cards across sessions that day
}

// ChapterEntry is one chapter's aggregated training + mastery data.
type ChapterEntry struct {
	ChapterID      int64   `json:"chapterId"`
	ChapterName    string  `json:"chapterName"`
	Cards          int     `json:"cards"`          // Cards is the sum of total_cards from training_sessions
	MinutesTrained int     `json:"minutesTrained"` // MinutesTrained is the sum of duration_ms / 60000
	MasteryPercent float64 `json:"masteryPercent"` // MasteryPercent is live, from flashcards.last_result
}

// MasteryTrendResponse is returned from GET /subject-stats-mastery-trend.
type MasteryTrendResponse struct {
	Period string    `json:"period"`
	Series []float64 `json:"series"` // Series is one point per day in range, oldest first
	Delta  float64   `json:"delta"`  // Delta is series[last] - series[0]
}

// UpdateInput is the payload to update a subject. Nil fields are unchanged.
type UpdateInput struct {
	Name        *string `json:"name"`        // Name updates the display name when non-nil
	Color       *string `json:"color"`       // Color updates the color hex when non-nil
	Icon        *string `json:"icon"`        // Icon updates the icon when non-nil
	Tags        *string `json:"tags"`        // Tags updates the tag list when non-nil
	Visibility  *string `json:"visibility"`  // Visibility updates visibility when non-nil
	Description *string `json:"description"` // Description updates description when non-nil
	Archived    *bool   `json:"archived"`    // Archived updates archive flag when non-nil
}
