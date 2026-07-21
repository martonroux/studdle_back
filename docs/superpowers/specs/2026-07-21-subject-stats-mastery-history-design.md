# Subject Stats: History Backend (Sessions / Heatmap / Chapter Aggregation / Mastery Trend)

**Date:** 2026-07-21
**Issue:** STU-16 (F-3b, spun out of F-3 in the STU-8 `qa-report`, per the STU-9 triage confirmation)
**Scope:** Backend design for the four `SubjectStatsView` widgets not covered by F-3a (`fix/f3a-subject-stats-wiring`, already wired to real data): `StatsSessions`, `StatsActivityHeatmap`, `StatsByChapter`, `StatsHero`'s mastery time-series.

This is a design doc only — no implementation in this branch. Per the STU-9 rule that structural work returns to the board before it starts, and because the mastery-trend mechanism below is a real architectural choice (new table + new cron job), this doc needs board sign-off before any code lands.

## Motivation

F-3 in the QA1 audit found that `SubjectStatsView.vue` renders `statsMockData.ts` constants everywhere instead of calling a real backend. F-3a fixed the two widgets that were trivial wiring against the *existing* `GET /subject-stats` endpoint (`StatsCardDistribution`, `StatsStreakRibbon`). The remaining four widgets need backend work first:

| Widget | Needs | Status before this doc |
|---|---|---|
| `StatsActivityHeatmap` | Daily session/card counts, last 8 weeks | Fully derivable today, zero backend changes |
| `StatsSessions` | Per-session list: when, chapter, cards, minutes, accuracy | Blocked — sessions don't record which chapter was trained |
| `StatsByChapter` | Per-chapter cards trained, minutes, mastery % | Blocked — same chapter gap; mastery sub-metric is derivable today |
| `StatsHero` mastery time-series (7d/30d/all deltas) | Mastery % *at points in the past* | Blocked — no historical record exists anywhere |

## Current State (verified against code, not just the issue description)

The issue text says `training_sessions` "has chapter_id, goods/oks/bads, total_cards, duration_ms, completed_at per row" and concludes sessions/heatmap/chapter are derivable with "no schema change needed." That's half right and worth correcting before anyone scopes work off it:

- The **columns do exist** (`backend/db_sql/setup_core.go:194-204`): `chapter_id BIGINT NULL`, `goods INT`, `oks INT`, `bads INT` are all in the `CREATE TABLE`. No migration is needed.
- But **nothing ever writes them**. `Service.insertSession` (`pkg/gamification/service.go:68-82`) runs `INSERT INTO training_sessions (user_id, subject_id, total_cards, duration_ms) VALUES (...)` — `chapter_id`/`goods`/`oks`/`bads` are absent from the column list, so every row ever recorded has `chapter_id = NULL`, `goods = oks = bads = 0`.
- The frontend caller (`frontend/src/features/training/composables/useTrainingPlay.ts:78-96`) groups trained cards **by `subject_id` only** (`bySubject` map) before calling `recordSession`, even though each card already carries its own `chapter_id` (used elsewhere in the same file, line 163). Multi-chapter study sessions — the common case, since due cards surface across all of a subject's chapters together — collapse into one session row with no chapter attribution at all.
- `RecordSessionInput` (`pkg/gamification/model.go:34-39`) has no `ChapterID` field, and the frontend sends a single summed `score` (`items.reduce((s,t) => s + t.result, 0)`, where `t.result ∈ {0,1,2}`), not per-outcome counts.

So "no schema change" is true, but it's not "no backend change" — the write path needs real code changes before chapter-scoped or accuracy data exists. This is scope the original F-3 finding missed and that FIX1 should account for.

One thing that *is* already fully correct: `flashcards.last_result` (`SMALLINT`, -1..2) is live and queryable per chapter today, since `flashcards.chapter_id` exists and is indexed (`idx_flashcards_chapter`). Per-chapter *mastery* (the stock number) doesn't depend on session history at all.

## Design Decisions

| Decision | Choice | Rationale |
|---|---|---|
| Chapter attribution for sessions | Extend the existing subject-splitting logic in `useTrainingPlay.ts` to split by `(subject_id, chapter_id)` instead of `subject_id` alone; add `ChapterID *int64` to `RecordSessionInput` and thread it into the `INSERT` | Smallest change that makes chapter attribution *correct* per row, reusing a pattern the code already has (it already splits by subject for multi-subject sessions) rather than inventing new session-splitting machinery |
| Session accuracy | Derive from the existing `score` field: `accuracy = score / (2 * card_count)` | Mathematically identical to the existing `masteryPercent` weighting (good=1, ok=0.5, bad=0 → dividing a 0/1/2 sum by 2 reproduces exactly that). No need to populate `goods`/`oks`/`bads` to get a per-session accuracy number. |
| `goods`/`oks`/`bads` columns | Leave unpopulated (out of scope) | They're not needed for any widget in this issue given the `score`-based accuracy derivation above. They already exist in the schema at no cost; populating them would be scope creep with no consumer. Flagged as a known dead column set, not silently ignored. |
| Sessions/heatmap/chapter query source | New read-only aggregation queries against `training_sessions`, one new endpoint | No new tables. Duration/card-count aggregates are cheap `GROUP BY` queries on an already-indexed-by-subject table. |
| Mastery time-series mechanism | **Periodic snapshot table** (`subject_mastery_daily`), populated by a new daily cron job | See "Mastery Trend Mechanism" below — this is the one genuine architectural decision in this doc. |
| Access control | Reuse `s.access.SubjectLevel(ctx, uid, subjectID)`, same viewer-or-above check as the existing `Stats` method | Consistency; these are read endpoints on the same resource, no reason for a different policy |

## Mastery Trend Mechanism — the actual decision

`StatsHero` wants a mastery % trend line with 7d/30d/all deltas. `flashcards.last_result` is a **stock** value — every review overwrites it in place (`RecordReview`, `pkg/flashcard/service.go:202`). There is no history table anywhere, so "what was mastery on day X" is unanswerable from current data for any X before this ships. Two options were considered.

### Option A — Periodic mastery snapshot (recommended)

Add a table:

```sql
CREATE TABLE IF NOT EXISTS subject_mastery_daily (
    user_id         BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    subject_id      BIGINT NOT NULL REFERENCES subjects(id) ON DELETE CASCADE,
    day             DATE NOT NULL,
    total_cards     INT NOT NULL,
    good_count      INT NOT NULL,
    ok_count        INT NOT NULL,
    bad_count       INT NOT NULL,
    new_count       INT NOT NULL,
    mastery_percent NUMERIC(6,4) NOT NULL,
    PRIMARY KEY (user_id, subject_id, day)
);
CREATE INDEX IF NOT EXISTS idx_mastery_daily_lookup ON subject_mastery_daily(subject_id, day);
```

Populate it with a new cron job, registered the same way `billingReconcile` already is (`cmd/app/main.go:59-65`, 24h interval, existing `internal/cron.Scheduler` — no new infra):

```go
d.scheduler.Register(cron.Job{
    Name:     "masterySnapshot",
    Interval: 24 * time.Hour,
    Run: func(ctx context.Context) error {
        return d.subjectSvc.SnapshotMastery(ctx)
    },
})
```

`SnapshotMastery` runs one `INSERT ... SELECT ... ON CONFLICT (user_id, subject_id, day) DO UPDATE` per invocation, computing the same aggregate `Service.Stats` already computes (`GROUP BY user_id, subject_id` over `flashcards` joined to `subjects`, using `CURRENT_DATE`), for every subject that has at least one flashcard. Idempotent — re-running it the same day just overwrites that day's row, so a missed tick or a restart mid-day self-heals on the next run.

**Pros:** Answers the actual product question ("how has my mastery level changed"), supports arbitrary date ranges with a cheap range query (`WHERE subject_id = $1 AND day BETWEEN $2 AND $3 ORDER BY day`), bounded row growth (one row per subject per day, not per review), reuses existing cron infra so it's genuinely small to add.

**Cons:** No historical backfill — the trend line starts flat/empty for every subject until the job has run a few times after deploy (see Rollout below). A day granularity means a subject studied heavily then not touched won't show intra-day movement, which is fine for a 7d/30d/all view.

### Option B — Reconstruct from `training_sessions` score deltas

Once the chapter-attribution write-path fix above lands, `training_sessions` will have `score`/`total_cards` per session. Option B would treat the trend as a cumulative or rolling function of session scores over time instead of adding a new table.

**Rejected**, for the reason the issue itself flags: this measures *review activity/accuracy* (flow — how well did each study session go), not *mastery level* (stock — what fraction of the deck currently sits at good/ok). The two diverge in an important way: re-reviewing a card that's already `good` and getting `good` again contributes a fresh positive delta to any score-accumulation scheme even though it changes nothing about the deck's actual mastery composition, which was already counted once. A user who repeatedly restudies their easiest cards would show a permanently climbing "mastery" trend under Option B while their real stock mastery is flat. There's also no way to periodically reconcile a delta-based reconstruction back to ground truth without computing the ground-truth snapshot anyway — at which point it's Option A with extra steps. Recommend against this even as a stopgap.

## Architecture — Sessions / Heatmap / Chapter Endpoint

One new read endpoint, `GET /subject-stats-history?id=<subjectId>`, added to `SubjectHandler` alongside the existing `Stats`. Single response bundles all three widgets' data to avoid three round trips from one view:

```go
// HistoryResponse is returned from GET /subject-stats-history.
type HistoryResponse struct {
    Sessions []SessionEntry  `json:"sessions"` // Sessions is the most recent sessions, newest first
    Heatmap  []DayIntensity  `json:"heatmap"`  // Heatmap is the last 8 full weeks, oldest first
    Chapters []ChapterEntry  `json:"chapters"` // Chapters is per-chapter aggregation
}

type SessionEntry struct {
    CompletedAt time.Time `json:"completedAt"`
    ChapterID   *int64    `json:"chapterId"`   // null if the row predates the chapter-attribution fix, or cards spanned no single chapter
    ChapterName *string   `json:"chapterName"` // joined from chapters.title; null when ChapterID is null
    Cards       int       `json:"cards"`
    DurationMs  int       `json:"durationMs"`
    Accuracy    float64   `json:"accuracy"`    // score / (2 * cards); 0 when cards == 0
}

type DayIntensity struct {
    Day   string `json:"day"`   // "2026-07-21"
    Cards int    `json:"cards"` // sum of total_cards across sessions that day
}

type ChapterEntry struct {
    ChapterID      int64   `json:"chapterId"`
    ChapterName    string  `json:"chapterName"`
    Cards          int     `json:"cards"`          // sum of total_cards from training_sessions
    MinutesTrained int     `json:"minutesTrained"` // sum of duration_ms / 60000
    MasteryPercent float64 `json:"masteryPercent"` // live, from flashcards.last_result — independent of session history
}
```

Query shapes:

- **Sessions** (limit to last 20, matching the mock's implied list length): `SELECT ts.completed_at, ts.chapter_id, c.title, ts.total_cards, ts.duration_ms, ts.score FROM training_sessions ts LEFT JOIN chapters c ON c.id = ts.chapter_id WHERE ts.subject_id = $1 AND ts.user_id = $2 ORDER BY ts.completed_at DESC LIMIT 20`. Accuracy computed in Go from `score`/`total_cards` (see Design Decisions).
- **Heatmap**: `SELECT completed_at::date AS day, sum(total_cards) FROM training_sessions WHERE subject_id = $1 AND user_id = $2 AND completed_at >= now() - interval '8 weeks' GROUP BY day`. Days with no session are filled to 0 in Go (56 days, oldest first) rather than in SQL, matching the existing pattern of doing shaping in the service layer.
- **Chapters**: two queries joined in Go rather than one complex SQL join — (1) `SELECT chapter_id, count(*) FILTER (WHERE last_result=2), count(*) FILTER (WHERE last_result=1), count(*) FILTER (WHERE last_result=0), count(*) FILTER (WHERE last_result=-1), count(*) FROM flashcards WHERE subject_id=$1 AND chapter_id IS NOT NULL GROUP BY chapter_id` for mastery per chapter (mirrors `Service.Stats`'s existing query, scoped down); (2) `SELECT chapter_id, sum(total_cards), sum(duration_ms) FROM training_sessions WHERE subject_id=$1 AND user_id=$2 AND chapter_id IS NOT NULL GROUP BY chapter_id` for cards/minutes. Chapters with flashcards but no recorded sessions still appear, with `cards=0, minutesTrained=0`.

Access control: same `s.access.SubjectLevel` viewer-or-above check as `Stats`, at the top of the new service method — copy, don't refactor the existing `Stats` method's shape.

## Architecture — Mastery Trend Endpoint

`GET /subject-stats-mastery-trend?id=<subjectId>&period=7d|30d|all`:

```go
type MasteryTrendResponse struct {
    Period string    `json:"period"`
    Series []float64 `json:"series"` // one point per day in range, oldest first
    Delta  float64   `json:"delta"`  // series[last] - series[0]
}
```

`period` maps to a day count (`7d`→7, `30d`→30, `all`→since the subject's `created_at`), validated against an allow-list (no existing `httpx` helper for string enums — parse via `r.URL.Query().Get("period")` and reject anything else with 400, following the same `myErrors.ErrInvalidInput` pattern used elsewhere). Query: `SELECT day, mastery_percent FROM subject_mastery_daily WHERE subject_id=$1 AND user_id=$2 AND day >= $3 ORDER BY day`. Gaps (no snapshot that day — shouldn't normally happen once the cron job is live, but possible around deploy day) are forward-filled from the last known value in Go, not interpolated.

## Rollout / Cold Start

Both the chapter-attribution fix and the mastery snapshot table start with zero history on deploy day:

- `StatsSessions` older than the deploy will show `chapterName: null` (rows recorded before the write-path fix). The frontend should render those as "General" or omit the chapter chip rather than erroring — a frontend concern for the implementation PR, noted here so it isn't missed.
- `StatsByChapter` cards/minutes will read 0 for chapters not yet re-trained after deploy; mastery % is unaffected since it's live.
- `StatsHero`'s trend line has no data until the snapshot cron has run at least once (first tick up to 24h after deploy) and no meaningful *delta* until it's run across the requested period (e.g. a 30d delta needs 30 days of accumulated snapshots). Recommend the frontend show an explicit "not enough history yet" state for a range where `series` has fewer than 2 points, rather than rendering a flat/zero line that looks like a real (bad) trend.

This is a real product tradeoff, not an implementation detail — flagging it explicitly for board sign-off alongside the mechanism choice, since "the mastery chart will be empty for up to 30 days after ship" is the kind of thing that should be a known, accepted cost rather than a surprise.

## Testing

- **`pkg/gamification`**: unit test that `insertSession` with cards from two chapters produces two `training_sessions` rows with correct per-chapter `total_cards`/`duration_ms` (duration is *not* apportioned — each split row gets the full session duration, matching the existing subject-split behavior, which already has this same simplification for multi-subject sessions today; call this out as an accepted pre-existing limitation, not a new one).
- **`pkg/subject`**: unit tests for the new history query (sessions ordering/limit, heatmap zero-fill over 56 days, chapter aggregation including chapters with zero sessions) and the mastery-trend query (period→day-count mapping, gap forward-fill, delta calc with 0/1 data points).
- **`pkg/subject` (or wherever `SnapshotMastery` lands)**: test the upsert is idempotent (running twice same day doesn't duplicate rows, second run's numbers win) and covers a subject with zero flashcards (should not error, should just not produce a row — a template `WHERE` clause naturally excludes it via the `flashcards` join).
- **Access control**: viewer-with-no-access gets 403 on both new endpoints, mirroring existing `Stats` test coverage.

## Files Touched (implementation PR, not this doc)

Backend:
| File | Change |
|---|---|
| `db_sql/setup_core.go` | Add `subject_mastery_daily` table + index |
| `pkg/gamification/model.go` | `RecordSessionInput` gains `ChapterID *int64`; `TrainingSession` gains `ChapterID *int64` |
| `pkg/gamification/service.go` | `insertSession` writes `chapter_id`; `RecordSession` (or a new split step) accepts pre-grouped-by-chapter input |
| `pkg/subject/model.go` | `HistoryResponse`, `SessionEntry`, `DayIntensity`, `ChapterEntry`, `MasteryTrendResponse` |
| `pkg/subject/service.go` | `History(ctx, uid, subjectID)`, `MasteryTrend(ctx, uid, subjectID, period)`, `SnapshotMastery(ctx)` |
| `api/handler/subject.go` | `History`, `MasteryTrend` handlers |
| `cmd/app/routes.go` | Register `GET /subject-stats-history`, `GET /subject-stats-mastery-trend` |
| `cmd/app/main.go` | Register `masterySnapshot` cron job |
| `docs/API.md` | Document both new endpoints |

Frontend (`studbud_front`):
| File | Change |
|---|---|
| `src/features/training/composables/useTrainingPlay.ts` | Group `bySubject` → `bySubjectChapter`; send `chapter_id` per call |
| `src/api/endpoints.ts` | `TrainingSessionInput` gains `chapter_id`; new `subjects.history()`, `subjects.masteryTrend()` calls |
| `src/features/subjects/composables/useSubjectStats.ts` | Load history + mastery trend alongside existing `stats()` call |
| `src/shared/stores/statsStore.ts` | Hold history/trend state |
| `src/features/subjects/components/StatsSessions.vue`, `StatsActivityHeatmap.vue`, `StatsByChapter.vue`, `StatsHero.vue` | Consume real data instead of `statsMockData.ts` |

## Out of Scope

- Populating `goods`/`oks`/`bads` on `training_sessions` (see Design Decisions — not needed given the `score`-based accuracy derivation).
- Backfilling history for sessions recorded before this ships. Not possible — the data (chapter attribution, daily mastery) simply doesn't exist for the past.
- Per-card mastery history (e.g. "this card's result over time"). `StatsHero` only needs subject-level aggregate trend; a per-card history table would be a much larger addition with no current consumer.
- Global (cross-subject) heatmap or trend. Everything here is scoped to one `subject_id`, matching how `SubjectStatsView` is scoped today.
