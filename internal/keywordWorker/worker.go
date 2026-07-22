package keywordWorker

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/time/rate"

	"studdle/backend/pkg/aipipeline"
)

// Config tunes the worker. All fields fall back to safe defaults when zero.
type Config struct {
	Workers      int           // Workers is the number of concurrent runner goroutines (default 2)
	RatePerMin   int           // RatePerMin is the global API call cap (default 60)
	Burst        int           // Burst is the rate-limiter burst size (default 120)
	PollInterval time.Duration // PollInterval is the idle backoff floor (default 500ms; backs off to 5s)
}

// Worker polls ai_extraction_jobs and runs keyword extraction.
type Worker struct {
	cfg     Config              // cfg is the resolved configuration
	db      *pgxpool.Pool       // db is the shared pool
	ai      *aipipeline.Service // ai is the keyword-extraction primitive
	limiter *rate.Limiter       // limiter caps the global per-second AI call rate
	runner  *Runner             // runner executes one claimed job

	stop chan struct{}  // stop signals all goroutines to exit
	wg   sync.WaitGroup // wg waits for poller + consumers to drain
}

// New constructs a Worker. Use Start to begin polling.
func New(db *pgxpool.Pool, ai *aipipeline.Service, cfg Config) *Worker {
	cfg = applyDefaults(cfg)

	limiter := rate.NewLimiter(rate.Limit(float64(cfg.RatePerMin)/60.0), cfg.Burst)

	return &Worker{
		cfg:     cfg,
		db:      db,
		ai:      ai,
		limiter: limiter,
		runner:  NewRunner(db, ai),
		stop:    make(chan struct{}),
	}
}

// applyDefaults fills zero-valued fields with safe defaults.
func applyDefaults(cfg Config) Config {
	if cfg.Workers <= 0 {
		cfg.Workers = 2
	}

	if cfg.RatePerMin <= 0 {
		cfg.RatePerMin = 60
	}

	if cfg.Burst <= 0 {
		cfg.Burst = 120
	}

	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 500 * time.Millisecond
	}

	return cfg
}

// Start launches the poller and N runner goroutines. Non-blocking.
func (w *Worker) Start(ctx context.Context) {
	jobs := make(chan claimedJob, w.cfg.Workers)

	w.wg.Add(1)
	go w.poll(ctx, jobs)

	for i := 0; i < w.cfg.Workers; i++ {
		w.wg.Add(1)
		go w.consume(ctx, jobs)
	}

	log.Printf("keywordWorker: started (workers=%d, rate=%d/min, burst=%d)",
		w.cfg.Workers, w.cfg.RatePerMin, w.cfg.Burst)
}

// Stop signals the worker to exit and waits for goroutines to drain.
func (w *Worker) Stop() {
	close(w.stop)
	w.wg.Wait()
}

// poll claims pending jobs and pushes them to the workers channel.
func (w *Worker) poll(ctx context.Context, out chan<- claimedJob) {
	defer w.wg.Done()
	defer close(out)

	delay := w.cfg.PollInterval
	maxDelay := 5 * time.Second

	for {
		if w.shouldStop(ctx) {
			return
		}

		jobs, err := w.runner.claim(ctx, w.cfg.Workers)
		if err != nil {
			log.Printf("keywordWorker: claim error: %v", err)
			time.Sleep(delay)
			continue
		}

		if len(jobs) == 0 {
			time.Sleep(delay)
			delay = nextDelay(delay, maxDelay)
			continue
		}

		delay = w.cfg.PollInterval

		if !w.dispatch(ctx, jobs, out) {
			return
		}
	}
}

// shouldStop returns true if the worker has been told to exit.
func (w *Worker) shouldStop(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	case <-w.stop:
		return true
	default:
		return false
	}
}

// dispatch sends claimed jobs into the consumer channel. Returns false if a
// stop signal was observed (caller exits).
func (w *Worker) dispatch(ctx context.Context, jobs []claimedJob, out chan<- claimedJob) bool {
	for _, j := range jobs {
		select {
		case <-ctx.Done():
			return false
		case <-w.stop:
			return false
		case out <- j:
		}
	}

	return true
}

// nextDelay doubles the idle delay up to maxDelay.
func nextDelay(current, maxDelay time.Duration) time.Duration {
	next := current * 2

	if next > maxDelay {
		return maxDelay
	}

	return next
}

// consume runs claimed jobs subject to the rate limiter.
func (w *Worker) consume(ctx context.Context, in <-chan claimedJob) {
	defer w.wg.Done()

	for j := range in {
		if err := w.limiter.Wait(ctx); err != nil {
			log.Printf("keywordWorker: rate-limit wait cancelled for job %d: %v", j.id, err)
			return
		}

		w.runner.run(ctx, j)
	}
}
