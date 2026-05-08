# Spec B — AI Revision Plan

**Status:** Design approved, ready for implementation planning.
**Date:** 2026-04-19
**Scope:** Single spec. Exam-driven revision planning only. Depends on Spec A (AI pipeline primitive + quota infrastructure) and Spec B.0 (flashcard keyword index). Does not cover real subscription billing (Spec C), quiz generation (Spec D), or duel/social features (Spec E).

---

## 1. Purpose

Give AI-subscribed StudBud users an automatically-generated daily study plan leading up to a target exam. The plan:

- Spans today → exam date, one bucket per day.
- Mixes same-subject flashcards with cross-subject flashcards sharing related concepts (via the Spec B.0 keyword index).
- Surfaces as the primary Home-screen widget when AI planning mode is on.
- Can be regenerated on demand when the user falls behind.

The daily plan replaces the reactive "N cards are ready" pattern for users who opt into AI mode and have declared an upcoming exam. Users without an upcoming exam in AI mode see a degraded state (existing streak/goal chrome from the gamification layer).

## 2. Product Decisions (Locked)

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Sequencing | Ships after Spec A (generation + check) and Spec B.0 (keyword index). Reuses both. | Cross-subject pull requires the keyword index; orchestration reuses the AI pipeline primitive. |
| Plan scope | Exam-driven only. User must create an `Exam` (subject, date) before a plan can exist. | Keeps output concrete and actionable. General "study more" plans are out of scope. |
| Plan granularity | Dated day-plan, one bucket per day from today → exam date. Each day has primary-subject cards, optional cross-subject cards, and optional deeper-dive cards (unlocked when daily goal is met). | Matches how students actually pace revision. Deeper dives reward staying ahead. |
| Exam entity shape | Single-subject per exam (`subject_id`, `date`, `title`, optional `notes`, optional `annales_image_id`). | Simplifies plan generation. Multi-subject exams (e.g. "finals week") are deferred. |
| Cross-subject pull | Hybrid: keyword-overlap shortlist via B.0 (find FCs in other subjects sharing ≥2 keywords), then AI re-ranks the shortlist by relevance to the exam subject. | Keyword overlap alone is noisy; full-content AI ranking on every candidate is expensive. Shortlist-then-rank is the cost-quality sweet spot. |
| Annales (past-paper PDFs) | Optional PDF persisted on the exam. Debits the existing `pdf.pagesUsed` quota counter (shared with Spec A's generation flow). Shown to the AI as context when generating/regenerating the plan. | One source of PDF page spend rather than a parallel counter. Inline warning at attach time keeps the user informed. |
| Adaptivity | Manual rebalance. Banner on Home card ("You're N days behind — regenerate plan?") when drift exceeds threshold. User must tap to regenerate. | Avoids silent plan churn. User stays in control. |
| Quota | New per-user daily `plan` counter on top of existing `prompt` / `pdf` / `check`. Costs 1 `plan` unit per generation (initial or regeneration). | Discrete, predictable. Generation is the expensive operation; rendering is free. |
| Fallback | If a flashcard has no keywords yet (B.0 still processing), plan generation uses same-subject-only for that card's subject. | Graceful degradation; plan remains usable while B.0 catches up. |
| Entitlement | AI subscribers only. Same `ai_subscription_active` stub flag from Spec A. | Consistent with the generation/check gating. |
| UI surface | Home screen (AI mode) via new `TodayPlanCard`, replacing the existing placeholder. Exam management on Profile or under Subject detail. | Fits the existing Home-slot split. Existing reactive-mode home is untouched. |

## 3. Architecture Overview

### 3.1 Module map

**Backend (`study_buddy_backend/`):**

- `pkg/ai/` — extend `FeatureKey` enum with `FeatureGenerateRevisionPlan`. Add `prompts/revision_plan.txt` and `prompts/cross_subject_rank.txt`.
- `api/service/examService.go` — **new**. Exam CRUD.
- `api/service/revisionPlanService.go` — **new**. `GenerateForExam(userId, examId)` orchestrator. Pulls candidate FCs, calls the pipeline, persists the plan.
- `api/service/crossSubjectShortlistService.go` — **new**. Pure SQL query against `flashcard_keywords` + subject access list. Returns `[]FcCandidate`.
- `api/service/aiQuotaService.go` — extend with `plan` counter (daily).
- `api/handler/examHandler.go` — **new**. `POST /exams`, `GET /exams`, `GET /exams/:id`, `PUT /exams/:id`, `DELETE /exams/:id`, `POST /exams/:id/annales` (reuses Spec A's image-upload pattern).
- `api/handler/revisionPlanHandler.go` — **new**. `POST /exams/:id/generate-plan` (SSE), `GET /exams/:id/plan`, `POST /exams/:id/mark-done` (mark a card complete for today).

**Frontend (`studbud/src/`):**

- `api/exams.ts` — **new**. Exam CRUD client.
- `api/revisionPlan.ts` — **new**. Plan fetch + SSE generation client.
- `stores/exams.ts` — **new**. Pinia: exam list, per-exam plan cache.
- `stores/revisionPlan.ts` — **new**. Active generation state (streaming, progress, error).
- `pages/ExamListPage.vue`, `pages/ExamEditPage.vue`, `pages/ExamGeneratePlanPage.vue` — **new**.
- `components/home/TodayPlanCard.vue` — **replace placeholder**. Reads from the active exam's plan.
- `components/plan/DayBucket.vue`, `components/plan/PlanCardRow.vue` — **new**. Render a day's assigned cards.
- `components/plan/BehindScheduleBanner.vue` — **new**. Detects drift and surfaces regenerate CTA.
- Home wiring: when AI mode is on AND user has an active exam, `TodayPlanCard` replaces today's existing "plan placeholder" slot in `HomePage.vue`.

### 3.2 Hard boundaries

- Cross-subject pulls go through `crossSubjectShortlistService` only. No ad-hoc queries against `flashcard_keywords`.
- All AI calls route through Spec A's `RunStructuredGeneration` with `FeatureGenerateRevisionPlan`. No new provider glue.
- `revisionPlanService` is the only writer to `revision_plans`. Handlers are thin.
- The plan is pure derived data: deleting and regenerating is always safe. No migrations needed when the plan shape evolves.

## 4. Data Model

### 4.1 `exams`

```sql
CREATE TABLE exams (
    id                BIGSERIAL PRIMARY KEY,
    user_id           BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    subject_id        BIGINT      NOT NULL REFERENCES subjects(id) ON DELETE CASCADE,
    date              DATE        NOT NULL,
    title             TEXT        NOT NULL,
    notes             TEXT,
    annales_image_id  TEXT        REFERENCES images(id) ON DELETE SET NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_exams_user_active ON exams (user_id, date);
```

Constraints:
- Exam date must be today or later at insert time (enforced at the service layer, not in SQL — users can edit past exams for bookkeeping).
- Max active exams per user: `10` (service-layer limit; prevents runaway plan spam).
- User must have viewer-or-higher access to `subject_id` — enforced on create.

### 4.2 `revision_plans`

```sql
CREATE TABLE revision_plans (
    id            BIGSERIAL PRIMARY KEY,
    exam_id       BIGINT      NOT NULL UNIQUE REFERENCES exams(id) ON DELETE CASCADE,
    generated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    days          JSONB       NOT NULL,
    model         TEXT        NOT NULL,  -- e.g. "claude-sonnet-4-6"
    prompt_hash   TEXT        NOT NULL,  -- for debugging plan drift across prompt edits
    generation_id BIGINT                 -- FK to ai_audit_log if retained
);
```

`days` JSONB shape:

```json
[
  {
    "date": "2026-04-20",
    "primarySubjectCards": [12, 37, 49],
    "crossSubjectCards":   [205, 308],
    "deeperDives":         [71, 88, 112]
  },
  ...
]
```

Card IDs are FC IDs. Ordering within each bucket is intentional (AI-assigned study order).

### 4.3 `revision_plan_progress`

Per-user per-day completion tracking separate from the plan itself so regeneration doesn't wipe progress:

```sql
CREATE TABLE revision_plan_progress (
    user_id   BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    fc_id     BIGINT NOT NULL REFERENCES flashcards(id) ON DELETE CASCADE,
    plan_date DATE   NOT NULL,
    done_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, fc_id, plan_date)
);
```

When a card completes a training session on its plan day, a row is inserted. Today's card completion is queried by `(user_id, plan_date = CURRENT_DATE)`.

### 4.4 Quota extension (Spec A table)

`ai_quota_daily` adds `plan_calls` and `cross_subject_rank_calls` columns (default 0), parallel to the existing `prompt_calls` / `pdf_calls` / `check_calls` counters. No schema-breaking migration — existing rows default gracefully.

## 5. AI Contract

### 5.1 Feature key

```go
const FeatureGenerateRevisionPlan FeatureKey = "revision_plan"
```

Plus the cross-subject ranker reuses the pipeline with a lighter prompt:

```go
const FeatureCrossSubjectRank FeatureKey = "cross_subject_rank"
```

`FeatureCrossSubjectRank` does **not** debit the `plan` counter (it's a sub-step of plan generation, counted once at the outer call).

### 5.2 Inputs to `FeatureGenerateRevisionPlan`

```json
{
  "exam": {
    "date": "2026-06-15",
    "daysRemaining": 57,
    "title": "Partiel Biologie Cellulaire",
    "notes": "Focus on mitosis and signal transduction",
    "subject": { "id": 3, "name": "Biologie Cellulaire" },
    "annales": [
      { "page": 1, "imageDataUrl": "..." },
      { "page": 2, "imageDataUrl": "..." }
    ]
  },
  "primaryCards": [
    { "id": 12, "title": "Mitose", "keywords": ["mitose","chromosome","division"] },
    ...
  ],
  "crossSubjectCandidates": [
    { "id": 205, "title": "Cycle cellulaire procaryote", "subjectName": "Microbiologie", "keywords": [...], "overlapScore": 3 },
    ...
  ],
  "userStats": {
    "cardsByState": { "new": 42, "bad": 8, "ok": 15, "good": 67 }
  }
}
```

### 5.3 Response schema

```json
{
  "type": "object",
  "required": ["days"],
  "properties": {
    "days": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["date", "primarySubjectCards", "crossSubjectCards", "deeperDives"],
        "properties": {
          "date": { "type": "string", "format": "date" },
          "primarySubjectCards": { "type": "array", "items": { "type": "integer" } },
          "crossSubjectCards":   { "type": "array", "items": { "type": "integer" } },
          "deeperDives":         { "type": "array", "items": { "type": "integer" } }
        }
      }
    }
  }
}
```

### 5.4 Post-processing

- Reject days outside `[today, examDate]`.
- Reject card IDs not in the candidate list (AI hallucination guard).
- Reject duplicate card IDs across days (a card appears at most once per plan).
- If the AI returns fewer days than `daysRemaining`, fill missing days with empty buckets.

## 6. Control Flow

### 6.1 Plan generation (outer orchestrator)

```
POST /exams/:id/generate-plan
→ auth + ownership check (user owns exam)
→ pipeline debits `plan_calls` only on successful completion (post-stream); failures never debit, so no refund step is required
→ crossSubjectShortlistService.ShortlistFor(examId, limit=30)
    SQL: find FCs in other accessible subjects sharing ≥2 keywords with any primary-subject FC
→ If any candidates exist:
    RunStructuredGeneration(FeatureCrossSubjectRank, ...)
    → receive ranked list, take top 15
→ Collect primary-subject FCs (up to configurable cap, default 200)
→ Build AI input (§5.2)
→ RunStructuredGeneration(FeatureGenerateRevisionPlan, ..., sseOut)
  streams plan-level events to client: { phase: "shortlist" | "ranking" | "planning" | "done" }
→ Post-process response (§5.4)
→ Persist:
    BEGIN
      DELETE FROM revision_plans WHERE exam_id = :id
      INSERT INTO revision_plans ...
    COMMIT
→ Emit final SSE event with plan summary
```

Failure at any step surfaces the error with context. Quota is never debited on failure (the pipeline debits only on a successful stream that emitted at least one valid day item), so no refund is needed.

### 6.2 Cross-subject shortlist SQL

```sql
WITH primary_keywords AS (
    SELECT DISTINCT fk.keyword
    FROM flashcards fc
    JOIN flashcard_keywords fk ON fk.fc_id = fc.id
    WHERE fc.subject_id = :examSubjectId
),
candidate_fcs AS (
    SELECT fk.fc_id, COUNT(*) AS overlap_score, SUM(fk.weight) AS weight_sum
    FROM flashcard_keywords fk
    JOIN primary_keywords pk ON pk.keyword = fk.keyword
    JOIN flashcards fc ON fc.id = fk.fc_id
    WHERE fc.subject_id <> :examSubjectId
      AND fc.subject_id IN (SELECT subject_id FROM user_accessible_subjects(:userId))
    GROUP BY fk.fc_id
    HAVING COUNT(*) >= 2
)
SELECT fc.id, fc.title, fc.subject_id, s.name AS subject_name, c.overlap_score, c.weight_sum
FROM candidate_fcs c
JOIN flashcards fc ON fc.id = c.fc_id
JOIN subjects s ON s.id = fc.subject_id
ORDER BY c.weight_sum DESC, c.overlap_score DESC
LIMIT :limit;
```

`user_accessible_subjects(userId)` is a SQL function or inlined CTE resolving owned + collaborated + subscribed subjects.

### 6.3 Plan consumption (Home screen)

`GET /exams/:id/plan` returns the stored plan + today's progress:

```json
{
  "exam": { ... },
  "generatedAt": "2026-04-19T08:12:03Z",
  "today": {
    "date": "2026-04-19",
    "primarySubjectCards": [12, 37, 49],
    "crossSubjectCards":   [205, 308],
    "deeperDives":         [71, 88, 112],
    "done": [12, 37],
    "dailyGoalMet": false
  },
  "drift": {
    "daysBehind": 0,
    "shouldSuggestRegen": false
  }
}
```

`dailyGoalMet` = `len(done) >= len(primarySubjectCards) + len(crossSubjectCards)`. Deeper-dives surface on the card only when this is true.

### 6.4 Drift detection

Computed server-side on `GET /exams/:id/plan`:

```
daysBehind = 0
for each past day d in plan where d.date < today:
    assigned = primary + crossSubject on day d
    done = rows in revision_plan_progress for (user, fc in assigned, plan_date = d)
    if len(done) / len(assigned) < 0.5:
        daysBehind += 1

shouldSuggestRegen = daysBehind >= 2
```

Banner shows on Home when `shouldSuggestRegen == true`.

### 6.5 Card completion wiring

When a training session ends on a card that's in today's plan:
- Existing `/update-flashcard-result` call unchanged.
- Additionally, `POST /exams/:activeExamId/mark-done { fcId }` inserts a `revision_plan_progress` row (ignored if duplicate).
- Frontend resolves `activeExamId` from the user's primary (nearest-date) active exam.

### 6.6 Annales upload

Reuses Spec A's `POST /upload-image` with `purpose: "exam_annales"`. Max 5 MB, max 10 pages on PDF annales, debits `pdf.pagesUsed` on generation (not on upload). Inline warning in `ExamEditPage.vue` before save: *"Attaching annales uses PDF page quota when a plan is generated."*

## 7. Error Handling & Retry

| Failure | Handling |
|---------|----------|
| User has no AI subscription | `402 no_ai_access` (shared with Spec A; mapped from `ErrNoAIAccess` in `internal/myErrors`). Frontend shows paywall. |
| `plan` quota exhausted | `429 quota_exhausted { kind: "plan" }`. Frontend shows quota-exhausted toast with reset time. |
| Exam date is in the past | `400 exam_date_past`. Frontend blocks at form level. |
| Subject has <5 flashcards | `400 subject_too_sparse`. Frontend shows helper text on the generate button. |
| Provider 5xx / 429 / timeout | One transparent retry (inherited from Spec A's `RunStructuredGeneration`). |
| Schema validation failure | `500 ai_schema_invalid` after retry. Plan quota refunded. |
| Cross-subject shortlist empty | Generation proceeds without cross-subject cards. Not an error. |
| User deletes exam mid-generation | Gracefully abort. `revision_plans` cascades on exam delete; no orphan rows. |
| Plan has zero primary cards | Happens when user has no FCs in the subject. Return `400 subject_empty`. |

## 8. UI

### 8.1 Home screen (AI mode) slot logic

```
if aiPlanningEnabled && activeExamExists:
    show TodayPlanCard
elif aiPlanningEnabled:
    show existing StreakCard + DailyGoalRing + CreateExamCTA
else:
    reactive-mode home (unchanged)
```

### 8.2 `TodayPlanCard`

- Header: exam title, countdown ("12 days until Partiel Biologie Cellulaire")
- Progress ring: today's completion vs assigned
- List of today's primary-subject cards with state badges; tap → training session scoped to those cards
- Collapsed row: "+2 related from Microbiologie" → expand → cross-subject cards
- Deeper-dive tray: gray-disabled until `dailyGoalMet`, then enabled
- Drift banner (conditional): `BehindScheduleBanner` with "Regenerate plan" button

### 8.3 Exam management

- Accessed from Profile → "My Exams" or Subject Detail → "Create Exam"
- `ExamListPage`: list of active exams sorted by date
- `ExamEditPage`: title, date picker, notes, annales PDF attach (shows pages-detected + quota warning)
- `ExamGeneratePlanPage`: intermediate screen showing "This will use 1 plan credit and X PDF pages" with confirm, then streams progress from the SSE backend (phase labels: "Finding related cards" → "Ranking relevance" → "Building schedule" → "Done")

### 8.4 Training integration

- When a training session is initiated via a plan card (not via the subject page), pass a query param `?planExam=123` so the client knows to call `/exams/123/mark-done` for each completed card.

## 9. Testing

### Unit (backend)
- `crossSubjectShortlistService`: no overlapping keywords → empty; overlap <2 → excluded; inaccessible subject → excluded; overlap ordering is correct.
- Plan post-processor: reject hallucinated card IDs; collapse duplicates; clamp days to valid range; fill missing days.
- Drift calculator: 0 past days → 0 behind; full completion → 0 behind; 3 consecutive days <50% done → 3 behind.

### Integration (backend)
- Full generate flow: exam + 10 primary FCs + 5 cross-subject candidates → plan persisted → `plan` counter incremented by 1.
- Quota exhaustion: second generation on a fresh user with `plan_calls=1` and limit `1` → 429, counter unchanged.
- Annales flow: upload PDF → generate → `pdf.pagesUsed` incremented by page count.
- Exam delete: plan + progress rows cascade away.
- Regeneration preserves progress: mark 2 cards done, regenerate → `revision_plan_progress` rows untouched.

### Frontend (component)
- `TodayPlanCard`: renders primary/cross/deeper buckets correctly; deeper disabled until goal met.
- `BehindScheduleBanner`: hidden when `shouldSuggestRegen=false`; visible with CTA otherwise.
- `ExamEditPage`: past-date blocks submit; notes empty OK; annales warning visible only when PDF attached.
- AI-mode home with no exam → shows CreateExamCTA, not TodayPlanCard.
- Reactive-mode home unchanged by any of the above.

### End-to-end (manual QA checklist)
- Create exam 30 days out → generate plan → today's card visible on Home → complete 3 cards → deeper dives unlock.
- Skip 3 days → open Home → drift banner appears → regenerate → drift resets.
- Delete exam → Home falls back to AI-mode-no-exam layout.
- Flip off AI planning → exam list still accessible from Profile, but Home falls back to reactive.

## 10. Out of Scope (Deferred)

- Multi-subject exams (finals week).
- Automatic plan regeneration on drift.
- Spaced-repetition scheduling beyond the current `dueHeuristic`.
- Custom card prioritization (user-marked "important" cards).
- Exam templates / preset study plans.
- Push notifications for day-rollover.
- Plan sharing with friends.
- Analytics/insights ("you've spent 4h on mitose this week").
- Real billing (Spec C).

## 11. Open Questions (Non-Blocking)

- Number of cross-subject cards per day (starting cap: 2). Tune post-launch based on completion rates.
- Whether to surface cross-subject relevance to the user ("Related to Cycle cellulaire because: mitose, chromosome"). Could land in a follow-up without schema change.
- Behavior when a flashcard referenced by a stored plan is later deleted: currently cascades (plan row persists, day array references stale ID). Frontend must filter unknown IDs on render. Consider a cleanup hook later.
