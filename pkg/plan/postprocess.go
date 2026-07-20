package plan

import (
	"sort"
	"time"
)

// dateLayout is the canonical YYYY-MM-DD format used in plan day buckets.
const dateLayout = "2006-01-02"

// PostProcessInput is the trimmed view of a raw AI response we hand to NormalizeDays.
type PostProcessInput struct {
	Days       []Day   // Days is the raw AI-emitted list, possibly malformed
	PrimaryIDs []int64 // PrimaryIDs is the allowed list of primary-subject FC IDs
	CrossIDs   []int64 // CrossIDs is the allowed list of cross-subject FC IDs
	DeeperIDs  []int64 // DeeperIDs is the allowed list of deeper-dive FC IDs (typically a subset of primary)
}

// NormalizeDays cleans an AI-generated plan:
//   - drops days outside [today, examDate]
//   - drops card IDs not in the candidate sets
//   - drops duplicate IDs across days (keeps the first occurrence)
//   - fills missing days in the [today, examDate] window with empty buckets
//
// `today` and `examDate` are interpreted at day granularity; their times are ignored.
func NormalizeDays(in PostProcessInput, today, examDate time.Time) []Day {
	primaryAllowed := toSet(in.PrimaryIDs)
	crossAllowed := toSet(in.CrossIDs)
	deeperAllowed := toSet(in.DeeperIDs)

	rangeStart := startOfDay(today)
	rangeEnd := startOfDay(examDate)
	seen := map[int64]bool{}

	byDate := map[string]Day{}
	for _, d := range in.Days {
		clean, ok := cleanDay(d, rangeStart, rangeEnd, primaryAllowed, crossAllowed, deeperAllowed, seen)
		if ok {
			byDate[clean.Date] = clean
		}
	}
	return fillRange(byDate, rangeStart, rangeEnd)
}

// cleanDay applies range + dedup + allowlist filters to a single day.
// Returns ok=false when the date itself is malformed or out of range.
func cleanDay(
	d Day, rangeStart, rangeEnd time.Time,
	primary, cross, deeper map[int64]bool, seen map[int64]bool,
) (Day, bool) {
	parsed, err := time.Parse(dateLayout, d.Date)
	if err != nil {
		return Day{}, false
	}
	parsed = startOfDay(parsed)
	if parsed.Before(rangeStart) || parsed.After(rangeEnd) {
		return Day{}, false
	}
	return Day{
		Date:                parsed.Format(dateLayout),
		PrimarySubjectCards: filterAllowed(d.PrimarySubjectCards, primary, seen),
		CrossSubjectCards:   filterAllowed(d.CrossSubjectCards, cross, seen),
		DeeperDives:         filterAllowed(d.DeeperDives, deeper, seen),
	}, true
}

// filterAllowed drops IDs not in `allowed` and IDs already taken across the plan.
// The `seen` set is mutated as we accept IDs.
func filterAllowed(ids []int64, allowed map[int64]bool, seen map[int64]bool) []int64 {
	if len(ids) == 0 {
		return nil
	}
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if !allowed[id] || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// fillRange returns one Day per date in [rangeStart, rangeEnd], inclusive,
// preferring entries from `byDate` when present and emitting empty buckets otherwise.
func fillRange(byDate map[string]Day, rangeStart, rangeEnd time.Time) []Day {
	if rangeEnd.Before(rangeStart) {
		return nil
	}
	var out []Day
	for d := rangeStart; !d.After(rangeEnd); d = d.AddDate(0, 0, 1) {
		key := d.Format(dateLayout)
		if existing, ok := byDate[key]; ok {
			out = append(out, existing)
			continue
		}
		out = append(out, Day{Date: key})
	}
	return out
}

// toSet converts a slice of int64 IDs to a lookup set.
func toSet(ids []int64) map[int64]bool {
	out := make(map[int64]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out
}

// startOfDay zeroes the wall-clock time so date-only comparisons are robust.
func startOfDay(t time.Time) time.Time {
	utc := t.UTC()
	return time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
}

// SortDays returns Days sorted ascending by Date. Use only when the upstream
// shouldn't have produced an out-of-order list but you want safety.
func SortDays(days []Day) []Day {
	out := append([]Day(nil), days...)
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out
}
