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
