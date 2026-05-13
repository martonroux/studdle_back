package aipipeline

// queries.go centralizes the raw SQL used by the pipeline service.
// Keep queries co-located to ease review and future migration work.

const sqlEnsureQuotaRow = `
INSERT INTO ai_quota_daily (user_id, day)
VALUES ($1, current_date)
ON CONFLICT (user_id, day) DO NOTHING
`

const sqlSelectQuotaRow = `
SELECT prompt_calls, pdf_calls, pdf_pages, check_calls, plan_calls, cross_subject_rank_calls, quiz_calls
FROM ai_quota_daily
WHERE user_id = $1 AND day = current_date
`

const sqlDebitPromptCalls = `
UPDATE ai_quota_daily SET prompt_calls = prompt_calls + $2
WHERE user_id = $1 AND day = current_date
`

const sqlDebitPDFCalls = `
UPDATE ai_quota_daily SET pdf_calls = pdf_calls + $2
WHERE user_id = $1 AND day = current_date
`

const sqlDebitPDFPages = `
UPDATE ai_quota_daily SET pdf_pages = pdf_pages + $2
WHERE user_id = $1 AND day = current_date
`

const sqlDebitCheckCalls = `
UPDATE ai_quota_daily SET check_calls = check_calls + $2
WHERE user_id = $1 AND day = current_date
`

const sqlDebitPlanCalls = `
UPDATE ai_quota_daily SET plan_calls = plan_calls + $2
WHERE user_id = $1 AND day = current_date
`

const sqlDebitCrossSubjectRankCalls = `
UPDATE ai_quota_daily SET cross_subject_rank_calls = cross_subject_rank_calls + $2
WHERE user_id = $1 AND day = current_date
`

const sqlDebitQuizCalls = `
UPDATE ai_quota_daily SET quiz_calls = quiz_calls + $2
WHERE user_id = $1 AND day = current_date
`

const sqlCountRunningGenerations = `
SELECT count(*) FROM ai_jobs
WHERE user_id = $1
  AND status = 'running'
  AND feature_key IN ('generate_prompt','generate_pdf')
`

const sqlSelectRunningGenerationID = `
SELECT id FROM ai_jobs
WHERE user_id = $1
  AND status = 'running'
  AND feature_key IN ('generate_prompt','generate_pdf')
ORDER BY started_at DESC
LIMIT 1
`

const sqlInsertAIJob = `
INSERT INTO ai_jobs
  (user_id, feature_key, model, status, subject_id, flashcard_id, pdf_page_count, metadata)
VALUES ($1, $2, $3, 'running', $4, $5, $6, $7)
RETURNING id
`

const sqlFinalizeAIJobSuccess = `
UPDATE ai_jobs SET
  status         = 'complete',
  finished_at    = now(),
  input_tokens   = $2,
  output_tokens  = $3,
  cents_spent    = $4,
  items_emitted  = $5,
  items_dropped  = $6
WHERE id = $1
`

const sqlFinalizeAIJobFailure = `
UPDATE ai_jobs SET
  status         = $2,
  finished_at    = now(),
  input_tokens   = $3,
  output_tokens  = $4,
  cents_spent    = $5,
  items_emitted  = $6,
  items_dropped  = $7,
  error_kind     = $8,
  error          = $9
WHERE id = $1
`

const sqlIncrementItemsDropped = `
UPDATE ai_jobs SET items_dropped = items_dropped + 1 WHERE id = $1
`

const sqlReapOrphanJobs = `
UPDATE ai_jobs SET
  status      = 'failed',
  finished_at = now(),
  error_kind  = 'orphaned',
  error       = 'reaped: running > 1h'
WHERE status = 'running'
  AND started_at < now() - interval '1 hour'
`
