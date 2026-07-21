# StudBud API Documentation

**Base URL:** `http://localhost:8080` (default; set via `PORT` env var)
**Version:** Skeleton 1.0 (matches `cmd/app/routes.go`)
**Scope:** This document describes the routes that are actually wired in the current skeleton. Routes that belong to Spec A (AI), Spec B (study plans), Spec D (quizzes), Spec E (duels), or Spec C (billing) are registered as stubs and return `501 Not Implemented` until their respective specs ship.

---

## Table of Contents

- [Authentication](#authentication)
- [Error Responses](#error-responses)
- [Access Model](#access-model)
- [Data Models](#data-models)
- [Route Index](#route-index)
- [Public Endpoints](#public-endpoints)
- [User Endpoints](#user-endpoints)
- [Email Verification Endpoints](#email-verification-endpoints)
- [Image Endpoints](#image-endpoints)
- [Subject Endpoints](#subject-endpoints)
- [Chapter Endpoints](#chapter-endpoints)
- [Flashcard Endpoints](#flashcard-endpoints)
- [Search Endpoints](#search-endpoints)
- [Friendship Endpoints](#friendship-endpoints)
- [Subscription Endpoints](#subscription-endpoints)
- [Collaboration Endpoints](#collaboration-endpoints)
- [Preferences Endpoints](#preferences-endpoints)
- [Gamification Endpoints](#gamification-endpoints)
- [Billing Endpoints (stub)](#billing-endpoints-stub)
- [AI Endpoints (stub)](#ai-endpoints-stub)
- [Quiz Endpoints (stub)](#quiz-endpoints-stub)
- [Plan Endpoints (stub)](#plan-endpoints-stub)
- [Duel Endpoints (stub)](#duel-endpoints-stub)

---

## Authentication

Most endpoints require a valid JWT token in the `Authorization` header.

```
Authorization: Bearer <token>
```

**Token details:**
- Algorithm: HS256
- Issuer: `JWT_ISSUER` env var (default `studbud`)
- Expiration: `JWT_TTL` env var (default `720h` — 30 days)
- Claims: `sub` (user id), `email_verified`, `is_admin`, `iss`, `exp`, `iat`

Tokens are obtained via `POST /user-register` or `POST /user-login`.

**Email-verification gate.** A second middleware (`RequireVerified`) sits in front of most mutating routes and rejects any token with `email_verified: false`. After verifying, the user must **re-login** to obtain a fresh JWT — the token is not refreshed server-side. The two escape-hatch routes for unverified users are `POST /user-test-jwt` and `POST /resend-verification`.

---

## Error Responses

All handler errors flow through `httpx.WriteError` and serialize to a single JSON envelope:

```json
{
  "error": {
    "code": "string",
    "message": "string"
  }
}
```

| Status | Code                  | Typical cause                                            |
|--------|-----------------------|----------------------------------------------------------|
| 400    | `invalid_input`       | Malformed JSON, missing required fields, bad query param |
| 400    | `validation`          | Business-rule validation (e.g., password too short)      |
| 401    | `unauthenticated`     | Missing or invalid JWT                                   |
| 403    | `forbidden`           | Authenticated but not authorized for the resource        |
| 403    | `email_not_verified`  | JWT present but `email_verified: false`                  |
| 403    | `already_verified`    | Resend attempted on a verified account                   |
| 404    | `not_found`           | Resource does not exist or user has no access            |
| 409    | `conflict`            | Duplicate (e.g., username/email taken)                   |
| 500    | `internal_error`      | Database or server-side failure                          |
| 501    | `not_implemented`     | Stub endpoint; feature pending its spec                  |

A panic in any handler is caught by the `Recoverer` middleware and returned as `500 internal_error` in the same envelope.

---

## Access Model

Subjects have 3-level visibility: `private`, `friends`, `public`. Access to a subject's resources (chapters, flashcards) is resolved in this order:

| Level        | How granted                                                                    |
|--------------|--------------------------------------------------------------------------------|
| **owner**    | User created the subject                                                       |
| **editor**   | Added as collaborator with `role: "editor"`, or redeemed an editor invite      |
| **viewer**   | Added as collaborator with `role: "viewer"`; friend of owner (`friends` vis.); subscriber (`public` vis.); redeemed a viewer invite |
| **none**     | No relationship                                                                |

**Minimum access per operation:**

| Operation                                                   | Minimum |
|-------------------------------------------------------------|---------|
| Read subject / chapters / flashcards                        | viewer  |
| Create / update / delete chapters and flashcards            | editor  |
| Record a flashcard review (`/flashcard-review`)             | viewer  |
| Update / delete subject; manage collaborators / invites     | owner   |

---

## Data Models

All Go models are located under `pkg/<domain>/model.go`. Unless noted, JSON field names are **snake_case** matching the struct tags.

### User
```json
{
  "ID": 1,
  "Username": "alice",
  "Email": "alice@example.com",
  "EmailVerified": true,
  "CreatedAt": "2026-04-22T10:00:00Z",
  "IsAdmin": false
}
```

The `user.User` struct has no JSON tags — field names are serialized verbatim (PascalCase). This struct is currently only used internally for auth bookkeeping; no handler returns it directly.

### Subject
```json
{
  "id": 1,
  "owner_id": 7,
  "name": "Organic Chemistry",
  "color": "#3B82F6",
  "icon": "⚗️",
  "tags": "chem science",
  "visibility": "private",
  "archived": false,
  "description": "Second-semester organic chemistry",
  "last_used": "2026-04-21T18:04:00Z",
  "created_at": "2026-01-10T12:00:00Z",
  "updated_at": "2026-04-21T18:04:00Z"
}
```

| Field        | Notes                                                          |
|--------------|----------------------------------------------------------------|
| `visibility` | `"private"`, `"friends"`, or `"public"`. Default `"private"`   |
| `archived`   | Archived subjects are excluded from list responses unless `?archived=true` |
| `last_used`  | Nullable; set by gamification when a training session is recorded |

### Chapter
```json
{
  "id": 1,
  "subject_id": 7,
  "title": "Alkenes",
  "position": 2,
  "created_at": "2026-01-10T12:00:00Z",
  "updated_at": "2026-01-10T12:00:00Z"
}
```

### Flashcard
```json
{
  "id": 1,
  "subject_id": 7,
  "chapter_id": 3,
  "title": "",
  "question": "What is E2 elimination?",
  "answer": "Concerted, anti-periplanar, strong base.",
  "image_id": null,
  "source": "manual",
  "due_at": "2026-04-23T00:00:00Z",
  "last_result": 2,
  "last_used": "2026-04-22T08:12:00Z",
  "created_at": "2026-04-01T00:00:00Z",
  "updated_at": "2026-04-22T08:12:00Z"
}
```

| Field          | Notes                                                                 |
|----------------|-----------------------------------------------------------------------|
| `chapter_id`   | Nullable — when `null`, the card lives at the subject root            |
| `source`       | `"manual"` (default) or `"ai"`                                        |
| `image_id`     | Nullable; when set, points to an image served from `GET /images/{id}` |
| `last_result`  | `-1` never reviewed, `0` bad, `1` ok, `2` good                        |
| `due_at`       | Nullable; reserved for future SRS scheduling                          |

### Friendship
```json
{
  "id": 1,
  "sender_id": 7,
  "receiver_id": 12,
  "status": "pending",
  "created_at": "2026-04-20T09:00:00Z",
  "updated_at": "2026-04-20T09:00:00Z"
}
```

`status` is one of `"pending"`, `"accepted"`, `"declined"`.

### Collaborator
```json
{
  "id": 1,
  "subject_id": 7,
  "user_id": 12,
  "role": "editor",
  "created_at": "2026-04-20T09:00:00Z"
}
```

### InviteLink
```json
{
  "token": "9f2c8b1e3a...",
  "subject_id": 7,
  "role": "editor",
  "expires_at": "2026-05-20T09:00:00Z",
  "created_at": "2026-04-20T09:00:00Z"
}
```

`expires_at` is nullable — `null` means the invite never expires.

### Streak
```json
{
  "user_id": 7,
  "current_streak": 4,
  "longest_streak": 11,
  "last_day": "2026-04-22T00:00:00Z",
  "updated_at": "2026-04-22T18:30:00Z"
}
```

### DailyGoal
```json
{
  "user_id": 7,
  "day": "2026-04-22T00:00:00Z",
  "done_today": 8,
  "target": 20
}
```

### TrainingSession
```json
{
  "id": 42,
  "user_id": 7,
  "subject_id": 1,
  "card_count": 18,
  "duration_ms": 274000,
  "score": 80,
  "created_at": "2026-04-22T18:30:00Z"
}
```

### Achievement
```json
{
  "code": "first_session",
  "title": "First Session",
  "description": "Complete your first training session.",
  "unlocked_at": "2026-04-22T18:30:00Z"
}
```

`unlocked_at` is `null` for locked achievements returned from `GET /achievements`.

### Preferences (`Prefs`)
```json
{
  "user_id": 7,
  "ai_planning_enabled": false,
  "daily_goal_target": 20
}
```

### UserStats (from `/get-user-stats`)
```json
{
  "masteryPercent": 0.62,
  "cardsStudied": 184,
  "totalCards": 297,
  "goodCount": 132,
  "okCount": 36,
  "badCount": 16,
  "newCount": 113,
  "badgesUnlocked": 4,
  "badgesTotal": 12
}
```

Note: this endpoint uses **camelCase**; the gamification `UserStats` payload at `GET /user-stats` uses a different schema (see that endpoint).

### Image
```json
{ "id": "abcd_efgh", "url": "/images/abcd_efgh" }
```

Returned from `POST /upload-image`. Images are served publicly via `GET /images/{id}`.

### TokenResponse
```json
{ "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9..." }
```

---

## Route Index

| # | Method | Path                              | Access        |
|---|--------|-----------------------------------|---------------|
| **Public** |
|   | POST   | `/user-register`                  | public        |
|   | POST   | `/user-login`                     | public        |
|   | GET    | `/verify-email`                   | public        |
|   | GET    | `/images/{id}`                    | public        |
|   | POST   | `/billing/webhook`                | public (stub) |
| **Auth only** |
|   | POST   | `/user-test-jwt`                  | auth          |
|   | POST   | `/resend-verification`            | auth          |
|   | GET    | `/subject-list`                   | auth          |
|   | GET    | `/subject`                        | auth          |
|   | GET    | `/subject-stats`                  | auth          |
|   | GET    | `/subject-stats-history`          | auth          |
|   | GET    | `/subject-stats-mastery-trend`    | auth          |
|   | GET    | `/chapter-list`                   | auth          |
|   | GET    | `/flashcard-list`                 | auth          |
|   | GET    | `/flashcard`                      | auth          |
|   | POST   | `/flashcard-review`               | auth          |
|   | GET    | `/search/subjects`                | auth          |
|   | GET    | `/search/users`                   | auth          |
|   | POST   | `/friendship-accept`              | auth          |
|   | POST   | `/friendship-decline`             | auth          |
|   | POST   | `/friendship-unfriend`            | auth          |
|   | GET    | `/friendship-list`                | auth          |
|   | GET    | `/friendship-pending`             | auth          |
|   | POST   | `/subject-subscribe`              | auth          |
|   | POST   | `/subject-unsubscribe`            | auth          |
|   | GET    | `/subject-subscriptions`          | auth          |
|   | GET    | `/collaborators`                  | auth          |
|   | GET    | `/preferences`                    | auth          |
|   | POST   | `/preferences-update`             | auth          |
|   | GET    | `/gamification-state`             | auth          |
|   | POST   | `/training-session-record`        | auth          |
|   | GET    | `/user-stats`                     | auth          |
|   | GET    | `/achievements`                   | auth          |
|   | POST   | `/billing/checkout`               | auth (stub)   |
|   | POST   | `/billing/portal`                 | auth (stub)   |
| **Auth + verified** |
|   | POST   | `/set-profile-picture`            | verified      |
|   | GET    | `/get-user-stats`                 | verified      |
|   | POST   | `/upload-image`                   | verified      |
|   | POST   | `/delete-image`                   | verified      |
|   | POST   | `/subject-create`                 | verified      |
|   | POST   | `/subject-update`                 | verified      |
|   | POST   | `/subject-delete`                 | verified      |
|   | POST   | `/chapter-create`                 | verified      |
|   | POST   | `/chapter-update`                 | verified      |
|   | POST   | `/chapter-delete`                 | verified      |
|   | POST   | `/flashcard-create`               | verified      |
|   | POST   | `/flashcard-update`               | verified      |
|   | POST   | `/flashcard-delete`               | verified      |
|   | POST   | `/friendship-request`             | verified      |
|   | POST   | `/collaborators`                  | verified      |
|   | POST   | `/collaborator-remove`            | verified      |
|   | POST   | `/collaboration-invites`          | verified      |
|   | POST   | `/collaboration-invite-redeem`    | verified      |
| **Stubs (501 Not Implemented)** |
|   | POST   | `/ai/flashcards/prompt`           | verified      |
|   | POST   | `/ai/flashcards/pdf`              | verified      |
|   | POST   | `/ai/check`                       | verified      |
|   | POST   | `/quiz/generate`                  | verified      |
|   | POST   | `/quiz/attempt`                   | verified      |
|   | POST   | `/quiz/share`                     | verified      |
|   | POST   | `/plan/generate`                  | verified      |
|   | GET    | `/plan/progress`                  | verified      |
|   | POST   | `/duel/invite`                    | verified      |
|   | POST   | `/duel/accept`                    | verified      |
|   | GET    | `/duel/connect`                   | verified      |

---

## Public Endpoints

### Register a new user
`POST /user-register` — Creates an account, issues a verification email, returns a JWT (with `email_verified: false`).

**Body:**
```json
{ "username": "alice", "email": "alice@example.com", "password": "hunter2!!" }
```
Password must be ≥ 8 characters. Email must contain `@`. Username and email must both be unique.

**200 Response:** [`TokenResponse`](#tokenresponse)

**Errors:** 400 `invalid_input` / `validation`, 409 `conflict`, 500.

---

### Login
`POST /user-login` — Authenticates and returns a JWT reflecting the user's current `email_verified` state.

**Body:**
```json
{ "identifier": "alice or alice@example.com", "password": "hunter2!!" }
```

**200 Response:** [`TokenResponse`](#tokenresponse)

**Errors:** 401 `unauthenticated`, 404 `not_found`, 500.

---

### Verify email
`GET /verify-email?token=<token>` — Consumes a one-shot verification token and flips `users.email_verified`. The user must re-login after this call to get a new JWT.

**200 Response:**
```json
{ "message": "email verified successfully" }
```

**Errors:** 400 `invalid_input`, 400 `validation` (expired), 403 `already_verified` (token already used), 404 `not_found`, 500.

---

### Serve an image
`GET /images/{id}` — Streams the image bytes. `Content-Type` is sniffed, `Cache-Control: public, max-age=86400`. No authentication — the opaque image id is treated as a capability.

**404** on unknown id.

---

### Billing webhook (stub)
`POST /billing/webhook` — Stripe webhook landing pad. Reads and discards the body, returns **501 `not_implemented`** until Spec C ships.

---

## User Endpoints

### Validate JWT
`POST /user-test-jwt` — Returns **201** if the token is valid. No body.

**Errors:** 401.

---

### Resend verification email
`POST /resend-verification` — Issues a fresh verification token to the authenticated user. Throttled to 1 request per 60 seconds per user.

**200 Response:**
```json
{ "message": "verification email sent" }
```

**Errors:** 400 `validation` (rate-limited), 403 `already_verified`, 500.

---

### Set profile picture (verified)
`POST /set-profile-picture` — Attaches a previously-uploaded image to the user. The image must be owned by the caller.

**Body:**
```json
{ "image_id": "abcd_efgh" }
```

**200 Response:**
```json
{ "message": "profile picture updated" }
```

**Errors:** 400, 401, 403, 404.

---

### Get user stats (verified)
`GET /get-user-stats` — Aggregate mastery + achievement progress across the user's **owned** subjects.

**200 Response:** [`UserStats` (camelCase)](#userstats-from-get-user-stats)

**Errors:** 401, 403, 500.

---

## Email Verification Endpoints

`/verify-email` is listed under [Public Endpoints](#verify-email). `/resend-verification` is listed under [User Endpoints](#resend-verification-email).

---

## Image Endpoints

### Upload an image (verified)
`POST /upload-image` — Multipart upload. Body cap: 6 MiB hard limit; form parser cap: 5 MiB.

**Form:** `file` (required, the image)

**200 Response:**
```json
{ "id": "abcd_efgh", "url": "/images/abcd_efgh" }
```

**Errors:** 400 `invalid_input` (missing file, oversize, bad form), 401, 403, 500.

---

### Delete an image (verified)
`POST /delete-image?id=<image_id>` — Deletes the image row and the underlying file. Caller must own the image.

**204 Response.** **Errors:** 400, 401, 403, 404.

---

## Subject Endpoints

### Create a subject (verified)
`POST /subject-create`

**Body:**
```json
{
  "name": "Organic Chemistry",
  "color": "#3B82F6",
  "icon": "⚗️",
  "tags": "chem science",
  "visibility": "private",
  "description": "Second-semester organic chemistry"
}
```
`name` is required. `visibility` defaults to `"private"` if empty/unset.

**201 Response:** [`Subject`](#subject)

**Errors:** 400 `validation`, 401, 403, 500.

---

### List owned subjects (auth)
`GET /subject-list[?archived=true]` — Returns subjects the caller owns. Archived subjects are excluded unless `archived=true`.

**200 Response:** `[Subject]`

---

### Get a subject (auth)
`GET /subject?id=<id>` — Returns the subject if the caller has at least `viewer` access.

**200 Response:** [`Subject`](#subject). **Errors:** 400, 401, 403, 404.

---

### Get subject history (auth)
`GET /subject-stats-history?id=<id>` — Viewer-or-above. Bundles three `SubjectStatsView` widgets in one round trip: the caller's most recent 20 training sessions (newest first), an 8-week activity heatmap (zero-filled for days with no session, oldest first), and per-chapter card/mastery aggregation.

**200 Response:**
```json
{
  "sessions": [
    {
      "completedAt": "2026-07-20T18:04:00Z",
      "chapterId": 12,
      "chapterName": "Alkenes",
      "cards": 8,
      "durationMs": 240000,
      "accuracy": 0.875
    }
  ],
  "heatmap": [
    { "day": "2026-05-26", "cards": 0 }
  ],
  "chapters": [
    { "chapterId": 12, "chapterName": "Alkenes", "cards": 8, "minutesTrained": 4, "masteryPercent": 0.75 }
  ]
}
```
`chapterId`/`chapterName` on a session entry are `null` when the session predates the chapter-attribution write-path fix, or when the reviewed cards spanned no single chapter. `chapters` includes every chapter that has at least one flashcard, even if the caller has never recorded a session against it (`cards: 0, minutesTrained: 0`); `masteryPercent` is live (derived from `flashcards.last_result`), independent of session history.

**Errors:** 400, 401, 403.

---

### Get subject mastery trend (auth)
`GET /subject-stats-mastery-trend?id=<id>&period=7d|30d|all` — Viewer-or-above. Returns the subject's mastery-percent trend, one point per day, sourced from the `subject_mastery_daily` snapshot table populated by the daily `masterySnapshot` cron job. `period` must be exactly `7d`, `30d`, or `all` (since the subject's `created_at`); anything else is `400 invalid_input`.

**200 Response:**
```json
{ "period": "30d", "series": [0.41, 0.41, 0.46, 0.52], "delta": 0.11 }
```
`series` is oldest-first; gaps in the snapshot history are forward-filled from the last known value, and days before the first available snapshot are omitted rather than backfilled — so `series` can have fewer points than the requested period implies, especially in the days right after deploy. `delta` is `series[last] - series[0]`, and is `0` when `series` has fewer than 2 points.

**Errors:** 400 (`invalid_input` for a missing/unrecognized `period`), 401, 403.

---

### Update a subject (verified)
`POST /subject-update?id=<id>` — Owner-only. Body is `subject.UpdateInput` — any non-null field is applied.

**Body:**
```json
{ "name": "New name", "visibility": "friends", "archived": true }
```

**200 Response:** updated [`Subject`](#subject).

---

### Delete a subject (verified)
`POST /subject-delete?id=<id>` — Owner-only cascade delete.

**204 Response.**

---

## Chapter Endpoints

### Create a chapter (verified)
`POST /chapter-create`

**Body:**
```json
{ "subject_id": 7, "title": "Alkenes" }
```

**201 Response:** [`Chapter`](#chapter)

---

### List chapters in a subject (auth)
`GET /chapter-list?subject_id=<id>` — Viewer-or-above. Ordered by `position`.

**200 Response:** `[Chapter]`

---

### Update a chapter (verified)
`POST /chapter-update?id=<id>` — Editor-or-above.

**Body:**
```json
{ "title": "New title", "position": 3 }
```

**200 Response:** updated [`Chapter`](#chapter).

---

### Delete a chapter (verified)
`POST /chapter-delete?id=<id>` — Editor-or-above. **204 Response.**

---

## Flashcard Endpoints

### Create a flashcard (verified)
`POST /flashcard-create`

**Body:**
```json
{
  "subject_id": 7,
  "chapter_id": 3,
  "title": "",
  "question": "What is E2 elimination?",
  "answer": "Concerted, anti-periplanar, strong base.",
  "image_id": null,
  "source": "manual"
}
```

**201 Response:** [`Flashcard`](#flashcard)

---

### List flashcards by subject (auth)
`GET /flashcard-list?subject_id=<id>` — Viewer-or-above.

**200 Response:** `[Flashcard]`

---

### Get a flashcard (auth)
`GET /flashcard?id=<id>` — Viewer-or-above.

**200 Response:** [`Flashcard`](#flashcard)

---

### Update a flashcard (verified)
`POST /flashcard-update?id=<id>` — Editor-or-above.

**Body:**
```json
{ "chapter_id": 4, "title": null, "question": "Updated?", "answer": "Yes.", "image_id": null }
```

**200 Response:** updated [`Flashcard`](#flashcard).

---

### Delete a flashcard (verified)
`POST /flashcard-delete?id=<id>` — Editor-or-above. **204 Response.**

---

### Record a flashcard review (auth)
`POST /flashcard-review?id=<id>` — Viewer-or-above. Does not advance the streak or daily goal on its own; call `/training-session-record` at the end of the session for gamification state.

**Body:**
```json
{ "result": 2 }
```
`result` is `0` (bad), `1` (ok), or `2` (good).

**200 Response:** updated [`Flashcard`](#flashcard) with fresh `last_result` / `last_used`.

---

## Search Endpoints

### Search subjects (auth)
`GET /search/subjects?q=<query>` — Matches subjects the caller can see. Limit: 20 results.

**200 Response:** implementation-defined result shape (see `pkg/search`). Returns `[]` when no matches.

---

### Search users (auth)
`GET /search/users?q=<query>` — Public user search. Limit: 20 results. `q` is required (empty returns `[]`).

---

## Friendship Endpoints

### Send a friend request (verified)
`POST /friendship-request`

**Body:**
```json
{ "receiver_id": 12 }
```

**201 Response:** [`Friendship`](#friendship) with `status: "pending"`.

---

### Accept a friend request (auth)
`POST /friendship-accept?id=<friendship_id>` — Receiver-only. **200 Response:** updated [`Friendship`](#friendship) (`status: "accepted"`).

---

### Decline a friend request (auth)
`POST /friendship-decline?id=<friendship_id>` — Receiver-only. **200 Response:** updated [`Friendship`](#friendship) (`status: "declined"`).

---

### Unfriend (auth)
`POST /friendship-unfriend?id=<friendship_id>` — Either party. **204 Response.**

---

### List friends (auth)
`GET /friendship-list` — All accepted friendships for the caller. **200 Response:** `[Friendship]`.

---

### List pending incoming requests (auth)
`GET /friendship-pending` — Pending requests where the caller is the receiver. **200 Response:** `[Friendship]`.

---

## Subscription Endpoints

Subscriptions grant `viewer` access to a **public** subject owned by someone else.

### Subscribe (auth)
`POST /subject-subscribe?subject_id=<id>` — Target subject must have `visibility: "public"`. **204 Response.**

### Unsubscribe (auth)
`POST /subject-unsubscribe?subject_id=<id>` — **204 Response.**

### List my subscriptions (auth)
`GET /subject-subscriptions` — **200 Response:** `[int64]` (array of subject ids; `[]` when empty).

---

## Collaboration Endpoints

### Add a collaborator (verified)
`POST /collaborators` — Owner-only.

**Body:**
```json
{ "subject_id": 7, "user_id": 12, "role": "editor" }
```
`role` is `"viewer"` or `"editor"`.

**201 Response:** [`Collaborator`](#collaborator)

---

### Remove a collaborator (verified)
`POST /collaborator-remove?subject_id=<id>&user_id=<uid>` — Owner-only. **204 Response.**

---

### List collaborators (auth)
`GET /collaborators?subject_id=<id>` — Any user with at least `viewer` access. **200 Response:** `[Collaborator]` (always an array, never `null`).

---

### Create an invite link (verified)
`POST /collaboration-invites` — Owner-only.

**Body:**
```json
{ "subject_id": 7, "role": "editor", "ttl_hours": 72 }
```
`ttl_hours <= 0` means no expiry.

**201 Response:** [`InviteLink`](#invitelink)

---

### Redeem an invite link (verified)
`POST /collaboration-invite-redeem?token=<token>` — Upgrades the caller's access to the subject according to the invite's role.

**200 Response:** [`Collaborator`](#collaborator)

**Errors:** 400 `invalid_input` (missing token / expired / already redeemed), 401, 403, 404.

---

## Preferences Endpoints

### Get preferences (auth)
`GET /preferences` — Lazily creates a default row on first access.

**200 Response:** [`Prefs`](#preferences-prefs)

### Update preferences (auth)
`POST /preferences-update`

**Body:**
```json
{ "ai_planning_enabled": true, "daily_goal_target": 30 }
```
Both fields are optional; `null` leaves the existing value untouched.

**200 Response:** updated [`Prefs`](#preferences-prefs).

---

## Gamification Endpoints

### Get gamification state (auth)
`GET /gamification-state`

**200 Response:**
```json
{
  "streak":     { "...": "Streak" },
  "daily_goal": { "...": "DailyGoal" }
}
```
See [`Streak`](#streak) and [`DailyGoal`](#dailygoal).

---

### Record a training session (auth)
`POST /training-session-record` — Inserts a session row, advances streak + daily goal, unlocks any newly-earned achievements.

**Body:**
```json
{ "subject_id": 7, "card_count": 18, "duration_ms": 274000, "score": 80 }
```

**200 Response:**
```json
{
  "session":       { "...": "TrainingSession" },
  "streak":        { "...": "Streak" },
  "daily_goal":    { "...": "DailyGoal" },
  "newly_awarded": [{ "...": "Achievement" }]
}
```

---

### Get user stats (auth)
`GET /user-stats` — Gamification-scope stats (different shape from `/get-user-stats`).

**200 Response:**
```json
{
  "total_cards": 297,
  "total_sessions": 14,
  "current_streak": 4,
  "longest_streak": 11
}
```

---

### List achievements (auth)
`GET /achievements` — Returns the full achievement catalogue, each entry annotated with `unlocked_at` for the caller (or `null` if locked).

**200 Response:** `[Achievement]`

---

## Billing Endpoints (stub)

All billing routes return **501 `not_implemented`** until Spec C lands.

- `POST /billing/checkout` — auth
- `POST /billing/portal` — auth
- `POST /billing/webhook` — public (Stripe will post here)

---

## AI Endpoints (stub)

All AI routes require auth + verified and return **501 `not_implemented`** until Spec A lands.

- `POST /ai/flashcards/prompt` — generate flashcards from a text prompt
- `POST /ai/flashcards/pdf` — generate flashcards from an uploaded PDF
- `POST /ai/check` — AI-driven answer checking

---

## Quiz Endpoints (stub)

All quiz routes require auth + verified and return **501 `not_implemented`** until Spec D lands.

- `POST /quiz/generate` — generate a quiz from a subject/chapter
- `POST /quiz/attempt?id=<quiz_id>` — submit an attempt
- `POST /quiz/share?id=<quiz_id>` — share a quiz to friends

---

## Plan Endpoints (stub)

All study-plan routes require auth + verified and return **501 `not_implemented`** until Spec B lands.

- `POST /plan/generate` — generate a study plan
- `GET /plan/progress?id=<plan_id>` — read plan progress

---

## Duel Endpoints (stub)

All duel routes require auth + verified and return **501 `not_implemented`** until Spec E lands. `GET /duel/connect` will upgrade to WebSocket once implemented.

- `POST /duel/invite` — invite a friend to a duel
- `POST /duel/accept?id=<duel_id>` — accept a duel invite
- `GET /duel/connect?id=<duel_id>` — subscribe to duel events (WebSocket)
