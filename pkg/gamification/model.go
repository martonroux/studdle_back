package gamification

import "time"

// Streak tracks a user's current + best streaks.
type Streak struct {
	UserID        int64      `json:"user_id"`        // UserID is the streak owner
	CurrentStreak int        `json:"current_streak"` // CurrentStreak is the active consecutive day count
	LongestStreak int        `json:"longest_streak"` // LongestStreak is the historical best
	LastDay       *time.Time `json:"last_day"`       // LastDay is the last day a session was recorded
	UpdatedAt     time.Time  `json:"updated_at"`     // UpdatedAt is the last mutation time
}

// DailyGoal tracks one day's progress toward the daily target.
type DailyGoal struct {
	UserID    int64     `json:"user_id"`    // UserID is the goal owner
	Day       time.Time `json:"day"`        // Day is the calendar day (UTC)
	DoneToday int       `json:"done_today"` // DoneToday is cards reviewed today
	Target    int       `json:"target"`     // Target is the target copied from preferences
}

// TrainingSession captures a completed training run.
type TrainingSession struct {
	ID         int64     `json:"id"`          // ID is the session primary key
	UserID     int64     `json:"user_id"`     // UserID is the learner
	SubjectID  int64     `json:"subject_id"`  // SubjectID is the subject trained
	ChapterID  *int64    `json:"chapter_id"`  // ChapterID is the chapter trained, nil if cards spanned no single chapter
	CardCount  int       `json:"card_count"`  // CardCount is the number of cards reviewed
	DurationMs int       `json:"duration_ms"` // DurationMs is the total session duration
	Score      int       `json:"score"`       // Score is an aggregate score
	CreatedAt  time.Time `json:"created_at"`  // CreatedAt is session end time
}

// RecordSessionInput is the payload to record a finished session.
type RecordSessionInput struct {
	SubjectID  int64  `json:"subject_id"`  // SubjectID is the subject being trained
	ChapterID  *int64 `json:"chapter_id"`  // ChapterID is the chapter being trained, nil if cards spanned no single chapter
	CardCount  int    `json:"card_count"`  // CardCount is cards answered
	DurationMs int    `json:"duration_ms"` // DurationMs is the wall time
	Score      int    `json:"score"`       // Score is the aggregate score
}

// RecordSessionResult bundles the mutated state returned to the caller.
type RecordSessionResult struct {
	Session      TrainingSession `json:"session"`       // Session is the row just inserted
	Streak       Streak          `json:"streak"`        // Streak is the updated streak snapshot
	DailyGoal    DailyGoal       `json:"daily_goal"`    // DailyGoal is the updated daily goal
	NewlyAwarded []Achievement   `json:"newly_awarded"` // NewlyAwarded are achievements unlocked by this session
}

// Achievement represents an achievement (definition + unlock row combined).
type Achievement struct {
	Code        string     `json:"code"`        // Code is the achievement identifier
	Title       string     `json:"title"`       // Title is the display title
	Description string     `json:"description"` // Description is human text
	UnlockedAt  *time.Time `json:"unlocked_at"` // UnlockedAt is when the user earned it (nil = locked)
}

// UserStats aggregates high-level stats for the profile screen.
type UserStats struct {
	TotalCards    int `json:"total_cards"`    // TotalCards is the total flashcards owned
	TotalSessions int `json:"total_sessions"` // TotalSessions is the lifetime training count
	CurrentStreak int `json:"current_streak"` // CurrentStreak mirrors the streak row
	LongestStreak int `json:"longest_streak"` // LongestStreak mirrors the streak row
}
