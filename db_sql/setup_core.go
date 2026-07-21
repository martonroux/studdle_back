package db_sql

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

const coreSchema = `
CREATE TABLE IF NOT EXISTS users (
    id                        BIGSERIAL PRIMARY KEY,
    username                  TEXT NOT NULL UNIQUE,
    email                     TEXT NOT NULL UNIQUE,
    password_hash             TEXT NOT NULL,
    email_verified            BOOLEAN NOT NULL DEFAULT false,
    verified_at               TIMESTAMPTZ NULL,
    profile_picture_image_id  TEXT NULL,
    stripe_customer_id        TEXT UNIQUE NULL,
    is_admin                  BOOLEAN NOT NULL DEFAULT false,
    created_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS email_verifications (
    id         BIGSERIAL PRIMARY KEY,
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token      TEXT NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at    TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS email_verification_throttle (
    user_id     BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    last_sent   TIMESTAMPTZ NOT NULL DEFAULT now(),
    send_count  INT NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS images (
    id         TEXT PRIMARY KEY,
    owner_id   BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    filename   TEXT NOT NULL,
    mime_type  TEXT NOT NULL,
    bytes      BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'users_profile_pic_fk') THEN
    ALTER TABLE users
      ADD CONSTRAINT users_profile_pic_fk
      FOREIGN KEY (profile_picture_image_id) REFERENCES images(id) ON DELETE SET NULL;
  END IF;
END $$;

CREATE TABLE IF NOT EXISTS subjects (
    id          BIGSERIAL PRIMARY KEY,
    owner_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    color       TEXT NOT NULL DEFAULT '',
    icon        TEXT NOT NULL DEFAULT '',
    tags        TEXT NOT NULL DEFAULT '',
    visibility  TEXT NOT NULL DEFAULT 'private',
    archived    BOOLEAN NOT NULL DEFAULT false,
    last_used   TIMESTAMPTZ NULL,
    description TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    search_vec  tsvector,
    CONSTRAINT subjects_visibility_check CHECK (visibility IN ('private','friends','public'))
);
CREATE INDEX IF NOT EXISTS idx_subjects_owner ON subjects(owner_id);
CREATE INDEX IF NOT EXISTS idx_subjects_search ON subjects USING GIN (search_vec);

CREATE OR REPLACE FUNCTION subjects_search_vec_update() RETURNS trigger AS $$
BEGIN
  NEW.search_vec :=
    setweight(to_tsvector('simple', coalesce(NEW.name,'')), 'A') ||
    setweight(to_tsvector('simple', coalesce(NEW.tags,'')), 'B');
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_subjects_search_vec ON subjects;
CREATE TRIGGER trg_subjects_search_vec
  BEFORE INSERT OR UPDATE ON subjects
  FOR EACH ROW EXECUTE FUNCTION subjects_search_vec_update();

CREATE TABLE IF NOT EXISTS chapters (
    id         BIGSERIAL PRIMARY KEY,
    subject_id BIGINT NOT NULL REFERENCES subjects(id) ON DELETE CASCADE,
    title      TEXT NOT NULL,
    position   INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_chapters_subject ON chapters(subject_id);

CREATE TABLE IF NOT EXISTS flashcards (
    id            BIGSERIAL PRIMARY KEY,
    subject_id    BIGINT NOT NULL REFERENCES subjects(id) ON DELETE CASCADE,
    chapter_id    BIGINT NULL REFERENCES chapters(id) ON DELETE SET NULL,
    title         TEXT NOT NULL DEFAULT '',
    question      TEXT NOT NULL,
    answer        TEXT NOT NULL,
    image_id      TEXT NULL REFERENCES images(id) ON DELETE SET NULL,
    source        TEXT NOT NULL DEFAULT 'manual',
    due_at        TIMESTAMPTZ NULL,
    last_result   SMALLINT NOT NULL DEFAULT -1,
    last_used     TIMESTAMPTZ NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT flashcards_last_result_check CHECK (last_result BETWEEN -1 AND 2),
    CONSTRAINT flashcards_source_check CHECK (source IN ('manual','ai'))
);
CREATE INDEX IF NOT EXISTS idx_flashcards_subject ON flashcards(subject_id);
CREATE INDEX IF NOT EXISTS idx_flashcards_chapter ON flashcards(chapter_id);

CREATE EXTENSION IF NOT EXISTS pg_trgm;
DROP TRIGGER IF EXISTS trg_flashcards_search_vec ON flashcards;
DROP FUNCTION IF EXISTS flashcards_search_vec_update();
ALTER TABLE flashcards DROP COLUMN IF EXISTS search_vec;
CREATE INDEX IF NOT EXISTS idx_flashcards_title_trgm    ON flashcards USING GIN (title    gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_flashcards_question_trgm ON flashcards USING GIN (question gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_flashcards_answer_trgm   ON flashcards USING GIN (answer   gin_trgm_ops);

CREATE TABLE IF NOT EXISTS friendships (
    id           BIGSERIAL PRIMARY KEY,
    sender_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    receiver_id  BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status       TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT friendships_pair_chk CHECK (sender_id <> receiver_id),
    CONSTRAINT friendships_status_chk CHECK (status IN ('pending','accepted','declined')),
    UNIQUE (sender_id, receiver_id)
);

CREATE TABLE IF NOT EXISTS subject_subscriptions (
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    subject_id BIGINT NOT NULL REFERENCES subjects(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, subject_id)
);

CREATE TABLE IF NOT EXISTS collaborators (
    id         BIGSERIAL PRIMARY KEY,
    subject_id BIGINT NOT NULL REFERENCES subjects(id) ON DELETE CASCADE,
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role       TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT collaborators_role_chk CHECK (role IN ('viewer','editor')),
    UNIQUE (subject_id, user_id)
);

CREATE TABLE IF NOT EXISTS invite_links (
    id          BIGSERIAL PRIMARY KEY,
    subject_id  BIGINT NOT NULL REFERENCES subjects(id) ON DELETE CASCADE,
    token       TEXT NOT NULL UNIQUE,
    role        TEXT NOT NULL,
    created_by  BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at  TIMESTAMPTZ NULL,
    revoked_at  TIMESTAMPTZ NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT invite_links_role_chk CHECK (role IN ('viewer','editor'))
);

CREATE TABLE IF NOT EXISTS preferences (
    user_id              BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    ai_planning_enabled  BOOLEAN NOT NULL DEFAULT true,
    daily_goal_target    INT NOT NULL DEFAULT 20,
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS streaks (
    user_id            BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    current_days       INT NOT NULL DEFAULT 0,
    best_days          INT NOT NULL DEFAULT 0,
    last_studied_date  DATE NULL,
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS daily_goals (
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    day         DATE NOT NULL,
    target      INT NOT NULL,
    done_today  INT NOT NULL DEFAULT 0,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, day)
);

CREATE TABLE IF NOT EXISTS training_sessions (
    id            BIGSERIAL PRIMARY KEY,
    user_id       BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    subject_id    BIGINT NULL REFERENCES subjects(id) ON DELETE SET NULL,
    chapter_id    BIGINT NULL REFERENCES chapters(id) ON DELETE SET NULL,
    goods         INT NOT NULL DEFAULT 0,
    oks           INT NOT NULL DEFAULT 0,
    bads          INT NOT NULL DEFAULT 0,
    total_cards   INT NOT NULL DEFAULT 0,
    duration_ms   BIGINT NOT NULL DEFAULT 0,
    completed_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- score was never in the original CREATE TABLE; the Go layer echoed it back
-- from the request without persisting it. STU-16's session-history endpoint
-- needs it durable to compute per-session accuracy (score/(2*cards)).
ALTER TABLE training_sessions ADD COLUMN IF NOT EXISTS score INT NOT NULL DEFAULT 0;

-- subject_mastery_daily is a once-per-day snapshot of each subject's mastery
-- composition, populated by the masterySnapshot cron job. It is the only
-- source of historical mastery data (flashcards.last_result only holds the
-- current value, overwritten on every review) — see STU-16 design doc.
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

CREATE TABLE IF NOT EXISTS user_session_bests (
    user_id       BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    best_accuracy NUMERIC(5,2) NOT NULL DEFAULT 0,
    best_cards    INT NOT NULL DEFAULT 0,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS unlocked_achievements (
    user_id         BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    achievement_key TEXT NOT NULL,
    unlocked_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, achievement_key)
);
`

func setupCore(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, coreSchema); err != nil {
		return fmt.Errorf("exec core schema:\n%w", err)
	}
	return nil
}
