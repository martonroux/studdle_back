# Studdle

## Project Overview

Flashcard studying web app built with **Vue 3 + Vite + Pinia + Vue Router**, to be deployed as a native mobile app via **Capacitor**. Users create flashcards with markdown-supported questions and answers (text formatting, code blocks, math expressions, images), organize them into subjects and optional chapters, then study them through an interactive swipe-based training session.

All user data is persisted on an existing backend server. See `API.md` at the repository root for endpoint documentation.

## Data Model

### Users
- Fields: id, username, email (unique), emailVerified, profilePicture, createdAt
- **Email verification:** Required on registration. Account is fully blocked until verified (403 on all endpoints except `/resend-verification` and `/user-test-jwt`)
- **Login:** Accepts username or email as identifier
- **Profile picture:** Stored as an image ID referencing the `images` table, served via `/images/{id}`

### Subjects
- Unique name (per user), tags (comma-separated string), color, lastUsed timestamp
- Can be archived
- **Visibility:** `private` (default), `friends` (visible to owner's friends), `public` (discoverable and subscribable by anyone)
- Shared subjects include `accessLevel` (`owner`, `editor`, `viewer`) and `ownerUsername` fields

### Chapters
- Title unique within its parent subject
- Optional — flashcards can belong directly to a subject without a chapter

### Flashcards
- Belong to a subject (and optionally a chapter)
- Fields: title, question (markdown), answer (markdown), lastResult, lastUsed
- Content supports markdown (text formatting, code, LaTeX math) and images
- Images in markdown: upload via `/upload-image`, embed as `![](http://host/images/{id})`

### Images
- Uploaded by users for profile pictures or flashcard content
- Stored on local filesystem (`./uploads/` or `UPLOAD_DIR` env var)
- Served publicly via `GET /images/{imageID}` (no auth, enables `<img>` / markdown embedding)
- Validated: jpeg/png/gif/webp only, 5MB max, MIME detected from content (not header)
- Each image tracks: id, ownerUserID, filename, mimeType, sizeBytes, purpose

### Flashcard States

| State | `lastResult` value | Description |
|-------|-------------------|-------------|
| New   | -1                | Never trained on since creation |
| Bad   | 0                 | Answered incorrectly |
| OK    | 1                 | Partially correct |
| Good  | 2                 | Answered correctly |

Once a card leaves the "New" state, it never returns to it.

### Training Series
- Background concept, not user-facing
- Scoped to a single subject, but can mix chapters and flashcard states
- Shuffled before being served to the training screen

### Sharing & Collaboration
- **Friends:** Users send/accept friend requests by user ID. Friends can view each other's `friends`-visibility subjects (read-only)
- **Public subjects:** Discoverable via `/search-public-subjects`, subscribable by anyone, read-only access + ability to copy
- **Collaborators:** Owner invites users by ID or shareable invite link. Roles: `editor` (read-write) or `viewer` (read-only)
- **Copy to library:** Any user with viewer+ access can deep-copy a subject (+ chapters + flashcards) into their own library. The copy is always private with reset progress
- **Access hierarchy:** owner > editor > viewer > none. Centralized in `AccessService`

### Access Model
| Level | Granted by |
|-------|------------|
| owner | Created the subject |
| editor | Collaborator with `role: "editor"` |
| viewer | Collaborator with `role: "viewer"`, friend of owner (if `visibility: "friends"`), or subscriber (if `visibility: "public"`) |

Read operations require viewer access. Write operations (create/update/delete chapters/flashcards) require editor access. Subject management (update/delete subject, manage collaborators) requires owner access.

### Email Verification Flow
1. User registers with username + email + password
2. Account created with `email_verified = FALSE`, JWT returned immediately
3. Verification email sent (in dev: link logged to stdout when `SMTP_HOST` is unset)
4. User clicks link → `GET /verify-email?token=xxx` → account verified
5. Until verified, all protected routes return 403 except `/resend-verification` and `/user-test-jwt`
6. Verification tokens expire after 24 hours, resend rate-limited to 1 per 60 seconds

### Environment Variables
| Variable | Default | Description |
|----------|---------|-------------|
| `DATABASE_URL` | `postgres://postgres:postgres@localhost:5432/studbud?sslmode=disable` | PostgreSQL connection string |
| `JWT_SECRET` | `super-secret-dev-key-change-in-production` | JWT signing secret |
| `SMTP_HOST` | *(empty = dev mode)* | SMTP server host |
| `SMTP_PORT` | | SMTP server port |
| `SMTP_USERNAME` | | SMTP auth username |
| `SMTP_PASSWORD` | | SMTP auth password |
| `SMTP_FROM` | | Sender email address |
| `FRONTEND_URL` | | Base URL for verification links |
| `BACKEND_URL` | `http://localhost:8080` | Base URL for image serving |
| `UPLOAD_DIR` | `./uploads` | Local directory for image storage |
| `PORT` | `8080` | HTTP server port |

### Testing
- Tests use a real PostgreSQL test database (`studbud_test`), not mocks
- Run tests with `go test ./... -p 1 -count=1` (serial execution to avoid DB conflicts)
- Test helpers in `testutil/` (testdb.go for DB setup/cleanup, fixtures.go for data factories)
- `CreateTestUser` creates verified users by default; use `CreateTestUserWithEmail` for unverified

## Screens & Navigation

Three main screens accessed via a **bottom navigation menu** (left to right): **Subjects**, **Search**, **Account**.

- Pages with a parent (e.g. subject detail, flashcard editor) show a **Back button** (top-left, outside widgets) with a return icon
- Main screens use only the bottom menu for navigation
- All pages are scrollable, except the training page
- During training, the bottom menu is hidden; **Quit** and **Pause** buttons appear at the top

### Subjects Screen (home)
1. Most recently modified subjects
2. Create Subject button
3. Archive button
4. Full subject list with search (by title) and tag filter

### Create / Edit Subject
Form with: title, tags, color picker.

### Subject Detail
- Subject name (top) + edit button (right) + tags (below title)
- Buttons: Statistics, Create Flashcard, Start Training
- Chapter/Flashcard list (see display rules below)

### Chapter/Flashcard List Display Rules
- **All flashcards have no chapter:** show flashcards directly
- **All flashcards have chapters:** show chapters only
- **Mix:** unassigned flashcards are grouped into a fake "Unassigned" chapter; list shows chapters only
- The list never shows both chapters and flashcards simultaneously

### Flashcard Row (when list shows flashcards)
- Beginning of the question text (truncated)
- Flashcard state badge
- Time since last training (relative: days/weeks/months/years)
- Tap to open flashcard editor

### Chapter Row (when list shows chapters)
- Chapter name + flashcard count
- Tap to open chapter detail

### Chapter Detail
- Title format: `Subject Name / Chapter Title`
- Same layout as subject detail, but list only shows flashcards from that chapter
- No nested chapters

### Flashcard Create / Edit
- AI button (placeholder, not yet functional)
- Question button and Answer button — each opens a markdown editor
- Save button
- Delete button (edit mode only)
- Markdown editor includes: preview toggle, formatting error indicators (markdown/LaTeX)

### Training Screen
- Tap card to flip (180deg rotation, simulating a real paper flashcard); tap again to flip back
- Swiping is only enabled after the answer is revealed
- Card follows the user's finger during swipe
- Swipe right = correct (Good), swipe up = partial (OK), swipe left = incorrect (Bad)

## Design System

### Colors

| Token | Hex | Usage |
|-------|-----|-------|
| Background | `#090909` | Main page background |
| Text | `#F5F5F5` | Default text color |
| Widget BG | `#18181A` | Card/widget surfaces |
| Primary | `#007AFF` | Buttons, links |
| Danger | `#FF3B30` | Delete/destructive actions |
| Succeed | `#34C759` | Success indicators |
| Warning | `#FF9500` | Warning indicators |
| Secondary | `#AF52DE` | Accent/secondary actions |

### Typography

Font: **Inter**

| Level | Size | Weight |
|-------|------|--------|
| Title | 32px | Semi Bold |
| Subtitle | 24px | Semi Bold |
| Main text | 16px | Semi Bold |
| Sub text | 10px | Semi Bold |
| Small text | 8px | Regular |

### Widget Style
- Background: Widget BG color
- Corner radius: 12px
- Margin: 20px (all sides)
- Padding: 20px (all sides)
- Subtle shadow
- Related content shares a widget; unrelated content gets separate widgets
- Titles stay outside widgets, at the top of the screen

## Capacitor Constraints

- **Routing:** Use hash-based routing (`createWebHashHistory`). History mode will 404 on refresh in WebView `file://` serving.
- **Storage:** Do not rely on `localStorage` — WebView storage can be wiped under memory pressure. Use `@capacitor/preferences` or `@capacitor-community/sqlite`.
- **No hover states:** Design all interactions for tap/click only.
- **Safe areas:** Respect `env(safe-area-inset-*)` CSS variables (iPhone notch, home indicator).
- **Viewport:** Set a proper viewport meta tag and verify font sizing on real devices.
