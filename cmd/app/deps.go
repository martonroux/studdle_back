package main

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"studbud/backend/internal/aiProvider"
	billingadapter "studbud/backend/internal/billing"
	"studbud/backend/internal/config"
	"studbud/backend/internal/cron"
	"studbud/backend/internal/db"
	"studbud/backend/internal/duelHub"
	"studbud/backend/internal/email"
	jwtsigner "studbud/backend/internal/jwt"
	"studbud/backend/internal/keywordWorker"
	"studbud/backend/internal/storage"
	"studbud/backend/pkg/access"
	"studbud/backend/pkg/aipipeline"
	pkgbilling "studbud/backend/pkg/billing"
	"studbud/backend/pkg/chapter"
	"studbud/backend/pkg/collaboration"
	"studbud/backend/pkg/duel"
	"studbud/backend/pkg/emailverification"
	"studbud/backend/pkg/exam"
	"studbud/backend/pkg/flashcard"
	"studbud/backend/pkg/friendship"
	"studbud/backend/pkg/gamification"
	"studbud/backend/pkg/image"
	pkgplan "studbud/backend/pkg/plan"
	"studbud/backend/pkg/preferences"
	"studbud/backend/pkg/quiz"
	"studbud/backend/pkg/search"
	"studbud/backend/pkg/subject"
	"studbud/backend/pkg/subjectsub"
	"studbud/backend/pkg/user"
)

// deps bundles every constructed service and shared resource for the router.
type deps struct {
	cfg          *config.Config             // cfg is the loaded runtime configuration
	db           *pgxpool.Pool              // db is the shared PostgreSQL connection pool
	signer       *jwtsigner.Signer          // signer issues and verifies JWTs
	scheduler    *cron.Scheduler            // scheduler drives periodic background jobs
	worker       *keywordWorker.Worker      // worker processes keyword extraction tasks
	access       *access.Service            // access gates subject/chapter entitlements
	user         *user.Service              // user handles registration, login, and profiles
	emailVer     *emailverification.Service // emailVer manages email verification flows
	image        *image.Service             // image manages uploads and retrieval
	subject      *subject.Service           // subject owns study subject CRUD
	chapter      *chapter.Service           // chapter owns chapter CRUD within subjects
	flashcard    *flashcard.Service         // flashcard owns card CRUD within chapters
	search       *search.Service            // search provides full-text search
	friendship   *friendship.Service        // friendship manages friend requests and lists
	subjectSub   *subjectsub.Service        // subjectSub handles subject subscriptions
	collab       *collaboration.Service     // collab manages collaborative editing sessions
	preferences  *preferences.Service       // preferences stores per-user settings
	gamification *gamification.Service      // gamification tracks badges and XP
	exam         *exam.Service              // exam owns exam CRUD
	ai           *aipipeline.Service        // ai provides AI pipeline stubs
	quiz         *quiz.Service              // quiz provides quiz generation stubs
	plan         *pkgplan.Service           // plan provides study plan stubs
	duel         *duel.Service              // duel handles real-time duel sessions
	billing      *pkgbilling.Service        // billing manages subscription and payments
	prices       billingadapter.PriceProvider // prices fetches live Stripe price data
}

// infra groups infrastructure-level singletons built before domain services.
type infra struct {
	signer    *jwtsigner.Signer     // signer is the JWT signer
	store     *storage.FileStore    // store is the filesystem image store
	emailer   email.Sender          // emailer sends transactional email
	scheduler *cron.Scheduler       // scheduler drives cron jobs
	worker    *keywordWorker.Worker // worker processes keyword tasks
	aiClient  aiProvider.Client     // aiClient wraps the AI provider
	billing   billingadapter.Client // billing wraps the payment provider
	hub       *duelHub.Hub          // hub manages active duel websocket sessions
}

// buildDeps constructs every service and returns deps plus a cleanup func.
func buildDeps(ctx context.Context, cfg *config.Config) (*deps, func(), error) {
	pool, err := openPool(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { pool.Close() }

	inf, err := buildInfra(cfg, pool)
	if err != nil {
		cleanup()
		return nil, nil, err
	}

	dom := buildDomainServices(cfg, pool, inf)
	stubs := buildStubServices(cfg, pool, inf, dom)

	worker, enq := wireKeywordWorker(pool, cfg, stubs.ai)
	inf.worker = worker
	dom.flashcard = flashcard.NewService(pool, dom.access, enq)

	d := assembleDeps(cfg, pool, inf, dom, stubs)
	return d, cleanup, nil
}

// openPool opens and pings the PostgreSQL pool using the config DSN.
func openPool(ctx context.Context, cfg *config.Config) (*pgxpool.Pool, error) {
	pool, err := db.OpenPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("open pool:\n%w", err)
	}
	return pool, nil
}

// buildEmailer returns a real SMTP sender or a recorder for non-prod environments.
func buildEmailer(cfg *config.Config) email.Sender {
	if cfg.Env == "test" || cfg.SMTPHost == "" {
		return email.NewRecorder()
	}
	return email.NewSMTPSender(cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPUser, cfg.SMTPPass, cfg.SMTPFrom)
}

// buildInfra constructs all infrastructure singletons from config and pool.
func buildInfra(cfg *config.Config, pool *pgxpool.Pool) (infra, error) {
	store, err := storage.NewFileStore(cfg.UploadDir)
	if err != nil {
		return infra{}, fmt.Errorf("init file store:\n%w", err)
	}
	return infra{
		signer:    jwtsigner.NewSigner(cfg.JWTSecret, cfg.JWTIssuer, cfg.JWTTTL),
		store:     store,
		emailer:   buildEmailer(cfg),
		scheduler: cron.New(),
		worker:    nil, // wired by wireKeywordWorker after the aipipeline service exists
		aiClient:  selectAIClient(cfg),
		billing:   selectBillingClient(cfg),
		hub:       duelHub.New(),
	}, nil
}

// wireKeywordWorker constructs the keyword worker (depends on aipipeline.Service)
// and the enqueuer that bridges to flashcard.KeywordEnqueuer. It also points
// flashcard.shouldReindex at MaterialChange. Call after stub services are built.
func wireKeywordWorker(pool *pgxpool.Pool, cfg *config.Config, ai *aipipeline.Service) (*keywordWorker.Worker, flashcard.KeywordEnqueuer) {
	enq := keywordWorker.NewEnqueuer(pool)

	w := keywordWorker.New(pool, ai, keywordWorker.Config{
		Workers:    cfg.KeywordWorkers,
		RatePerMin: cfg.KeywordRatePerMin,
		Burst:      cfg.KeywordBurst,
	})

	flashcard.SetReindexPredicate(keywordWorker.MaterialChange)

	return w, enqueuerAdapter{inner: enq}
}

// enqueuerAdapter bridges *keywordWorker.Enqueuer (Priority) to
// flashcard.KeywordEnqueuer (int16). Keeps pkg/flashcard free of an
// internal/keywordWorker import.
type enqueuerAdapter struct {
	inner *keywordWorker.Enqueuer // inner is the real DB-backed enqueuer
}

// EnqueueForFlashcard forwards to the inner enqueuer with the priority
// converted to keywordWorker.Priority.
func (a enqueuerAdapter) EnqueueForFlashcard(ctx context.Context, fcID int64, prio int16) error {
	return a.inner.EnqueueForFlashcard(ctx, fcID, keywordWorker.Priority(prio))
}

// domainSvcs groups all domain-layer services.
type domainSvcs struct {
	access       *access.Service            // access gates subject/chapter entitlements
	user         *user.Service              // user handles registration, login, and profiles
	emailVer     *emailverification.Service // emailVer manages email verification flows
	image        *image.Service             // image manages uploads and retrieval
	subject      *subject.Service           // subject owns study subject CRUD
	chapter      *chapter.Service           // chapter owns chapter CRUD within subjects
	flashcard    *flashcard.Service         // flashcard owns card CRUD within chapters
	search       *search.Service            // search provides full-text search
	friendship   *friendship.Service        // friendship manages friend requests and lists
	subjectSub   *subjectsub.Service        // subjectSub handles subject subscriptions
	collab       *collaboration.Service     // collab manages collaborative editing sessions
	preferences  *preferences.Service       // preferences stores per-user settings
	gamification *gamification.Service      // gamification tracks badges and XP
	exam         *exam.Service              // exam owns exam CRUD
}

// buildDomainServices constructs all real domain services.
func buildDomainServices(cfg *config.Config, pool *pgxpool.Pool, inf infra) domainSvcs {
	acc := access.NewService(pool)
	return domainSvcs{
		access:       acc,
		user:         user.NewService(pool, inf.signer),
		emailVer:     emailverification.NewService(pool, inf.emailer, cfg.FrontendURL),
		image:        image.NewService(pool, inf.store, cfg.BackendURL),
		subject:      subject.NewService(pool, acc),
		chapter:      chapter.NewService(pool, acc),
		flashcard:    flashcard.NewService(pool, acc, nil),
		search:       search.NewService(pool),
		friendship:   friendship.NewService(pool),
		subjectSub:   subjectsub.NewService(pool, acc),
		collab:       collaboration.NewService(pool, acc),
		preferences:  preferences.NewService(pool),
		gamification: gamification.NewService(pool),
		exam:         exam.NewService(pool, acc),
	}
}

// stubSvcs groups AI-backed and billing stub services.
type stubSvcs struct {
	ai      *aipipeline.Service // ai is the AI pipeline stub (Spec A replaces)
	quiz    *quiz.Service       // quiz is the quiz stub (Spec D replaces)
	plan    *pkgplan.Service    // plan is the plan stub (Spec B replaces)
	duel    *duel.Service       // duel is the duel stub (Spec E replaces)
	billing *pkgbilling.Service // billing is the billing stub (Spec C replaces)
}

// buildStubServices constructs stub/AI-backed services.
// The plan service requires the exam service, so domain services are constructed first.
func buildStubServices(cfg *config.Config, pool *pgxpool.Pool, inf infra, dom domainSvcs) stubSvcs {
	ai := aipipeline.NewService(pool, inf.aiClient, dom.access, aipipeline.DefaultQuotaLimits(), cfg.AIModel)
	return stubSvcs{
		ai:      ai,
		quiz:    quiz.NewService(pool),
		plan:    pkgplan.NewService(pool, ai, dom.exam, dom.image, dom.access, cfg.AIModel),
		duel:    duel.NewService(pool, inf.hub),
		billing: pkgbilling.NewService(pool, inf.billing, pkgbilling.PriceMap{
			Monthly: cfg.StripePriceProMonth,
			Annual:  cfg.StripePriceProAnnual,
		}),
	}
}

// assembleDeps merges all constructed pieces into a single deps value.
func assembleDeps(cfg *config.Config, pool *pgxpool.Pool, inf infra, dom domainSvcs, stubs stubSvcs) *deps {
	pricesProvider := billingadapter.NewCachedPriceProvider(
		inf.billing,
		cfg.StripePriceProMonth,
		cfg.StripePriceProAnnual,
		5*time.Minute,
	)
	return &deps{
		cfg:          cfg,
		db:           pool,
		signer:       inf.signer,
		scheduler:    inf.scheduler,
		worker:       inf.worker,
		access:       dom.access,
		user:         dom.user,
		emailVer:     dom.emailVer,
		image:        dom.image,
		subject:      dom.subject,
		chapter:      dom.chapter,
		flashcard:    dom.flashcard,
		search:       dom.search,
		friendship:   dom.friendship,
		subjectSub:   dom.subjectSub,
		collab:       dom.collab,
		preferences:  dom.preferences,
		gamification: dom.gamification,
		exam:         dom.exam,
		ai:           stubs.ai,
		quiz:         stubs.quiz,
		plan:         stubs.plan,
		duel:         stubs.duel,
		billing:      stubs.billing,
		prices:       pricesProvider,
	}
}

// selectAIClient returns the real ClaudeProvider when an API key is configured
// and the environment is not "test"; otherwise the NoopClient.
func selectAIClient(cfg *config.Config) aiProvider.Client {
	if cfg.Env == "test" || cfg.AnthropicAPIKey == "" {
		return aiProvider.NoopClient{}
	}
	return aiProvider.NewClaudeProvider("https://api.anthropic.com", cfg.AnthropicAPIKey)
}

// selectBillingClient returns the real StripeClient when a secret key is
// configured and the environment is not "test"; otherwise the NoopClient.
func selectBillingClient(cfg *config.Config) billingadapter.Client {
	if cfg.Env == "test" || cfg.StripeSecretKey == "" {
		return billingadapter.NoopClient{}
	}
	return billingadapter.NewStripeClient(cfg.StripeSecretKey, cfg.StripeWebhookSecret)
}

// TestingT is the minimal testing.T surface this helper uses.
// Defined here so the helper can be exported without importing "testing" in prod builds.
type TestingT interface {
	Fatalf(format string, args ...any)
}

// mustBuildDepsWithFake builds deps with a provided AI client, intended for tests.
// Unit tests import this to avoid real HTTP to Anthropic.
func mustBuildDepsWithFake(t TestingT, pool *pgxpool.Pool, cfg *config.Config, fake aiProvider.Client) (*deps, func()) {
	inf, err := buildInfra(cfg, pool)
	if err != nil {
		t.Fatalf("buildInfra: %v", err)
	}
	inf.aiClient = fake
	dom := buildDomainServices(cfg, pool, inf)
	stubs := buildStubServices(cfg, pool, inf, dom)
	worker, enq := wireKeywordWorker(pool, cfg, stubs.ai)
	inf.worker = worker
	dom.flashcard = flashcard.NewService(pool, dom.access, enq)
	d := assembleDeps(cfg, pool, inf, dom, stubs)
	return d, func() {}
}
