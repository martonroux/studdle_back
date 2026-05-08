package db_sql

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// planSchema is idempotent: safe to run on every boot and on every test setup.
// Schema shape follows Spec B §4 (exams, revision_plans, revision_plan_progress).
const planSchema = `
CREATE TABLE IF NOT EXISTS exams (
    id                BIGSERIAL    PRIMARY KEY,
    user_id           BIGINT       NOT NULL REFERENCES users(id)    ON DELETE CASCADE,
    subject_id        BIGINT       NOT NULL REFERENCES subjects(id) ON DELETE CASCADE,
    date              DATE         NOT NULL,
    title             TEXT         NOT NULL,
    notes             TEXT,
    annales_image_id  TEXT         REFERENCES images(id) ON DELETE SET NULL,
    created_at        TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ  NOT NULL DEFAULT now()
);
-- Plain (non-partial) index: Spec B §4.1 calls for WHERE date >= CURRENT_DATE,
-- but Postgres rejects CURRENT_DATE in index predicates (STABLE, not IMMUTABLE).
-- The application query filters by date instead.
CREATE INDEX IF NOT EXISTS idx_exams_user_active ON exams (user_id, date);

CREATE TABLE IF NOT EXISTS revision_plans (
    id            BIGSERIAL    PRIMARY KEY,
    exam_id       BIGINT       NOT NULL UNIQUE REFERENCES exams(id) ON DELETE CASCADE,
    generated_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    days          JSONB        NOT NULL,
    model         TEXT         NOT NULL,
    prompt_hash   TEXT         NOT NULL
);
ALTER TABLE revision_plans
    ADD COLUMN IF NOT EXISTS generation_id BIGINT NULL REFERENCES ai_jobs(id) ON DELETE SET NULL;

CREATE TABLE IF NOT EXISTS revision_plan_progress (
    user_id   BIGINT      NOT NULL REFERENCES users(id)      ON DELETE CASCADE,
    fc_id     BIGINT      NOT NULL REFERENCES flashcards(id) ON DELETE CASCADE,
    plan_date DATE        NOT NULL,
    done_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, fc_id, plan_date)
);
CREATE INDEX IF NOT EXISTS idx_rpp_user_today ON revision_plan_progress (user_id, plan_date);
`

// setupPlan installs the Spec B revision-plan schema. Idempotent.
func setupPlan(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, planSchema); err != nil {
		return fmt.Errorf("exec plan schema:\n%w", err)
	}
	return nil
}
