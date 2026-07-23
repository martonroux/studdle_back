package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	billingadapter "studdle/backend/internal/billing"
	"studdle/backend/internal/config"
	"studdle/backend/internal/db"
	"studdle/backend/pkg/billing"
)

// seedFlags holds the CLI flags accepted by the test-account seed script.
type seedFlags struct {
	Username string // Username is the login username to create or reset
	Email    string // Email is the login email to create or reset
	Password string // Password is the plaintext password to hash and set
}

// main seeds (or resets) a single dev/test-only user with a verified email
// and comped AI access, so Playwright E2E can log in deterministically
// without hand-editing SQL. Refuses to run when ENV=prod.
func main() {
	if err := run(parseFlags()); err != nil {
		log.Fatalf("fatal: %v", err)
	}
}

// run loads config, opens the database, and seeds the test account.
func run(f seedFlags) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config:\n%w", err)
	}
	if cfg.Env == "prod" {
		return errors.New("refusing to seed a test account: ENV=prod")
	}

	ctx := context.Background()
	pool, err := db.OpenPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("open pool:\n%w", err)
	}
	defer pool.Close()

	uid, err := upsertTestUser(ctx, pool, f)
	if err != nil {
		return err
	}
	if err := grantAIAccess(ctx, pool, uid); err != nil {
		return err
	}
	log.Printf("seeded test user %q <%s> (id=%d): verified + comped AI access", f.Username, f.Email, uid)
	return nil
}

// parseFlags reads CLI flags, defaulting to a fixed, well-known E2E account
// so Playwright specs can hardcode the same credentials every run.
func parseFlags() seedFlags {
	var f seedFlags
	flag.StringVar(&f.Username, "username", "e2e_test_user", "seed account username")
	flag.StringVar(&f.Email, "email", "e2e-test@studdle.dev", "seed account email")
	flag.StringVar(&f.Password, "password", "E2eTestPass123!", "seed account password")
	flag.Parse()
	return f
}

// upsertTestUser inserts the seed user, or resets its password hash and
// verification state if it already exists — idempotent across repeated runs.
// Note: the ON CONFLICT target is the email unique constraint; a pre-existing,
// unrelated row with the same username but a different email would still
// fail on the username constraint, which is an acceptable limitation for a
// single, deliberately-namespaced dev/test fixture identity.
func upsertTestUser(ctx context.Context, pool *pgxpool.Pool, f seedFlags) (int64, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(f.Password), bcrypt.DefaultCost)
	if err != nil {
		return 0, fmt.Errorf("hash password:\n%w", err)
	}
	const upsert = `
        INSERT INTO users (username, email, password_hash, email_verified, verified_at)
        VALUES ($1, $2, $3, true, now())
        ON CONFLICT (email) DO UPDATE SET
            password_hash = EXCLUDED.password_hash,
            email_verified = true,
            verified_at = now(),
            updated_at = now()
        RETURNING id
    `
	var uid int64
	if err := pool.QueryRow(ctx, upsert, f.Username, f.Email, string(hash)).Scan(&uid); err != nil {
		return 0, fmt.Errorf("upsert test user:\n%w", err)
	}
	return uid, nil
}

// grantAIAccess grants the seed user indefinite comped AI access, reusing
// the same billing.Service.GrantCompWithExpiry path that backs the
// POST /admin/comp-subscription endpoint rather than duplicating its SQL.
func grantAIAccess(ctx context.Context, pool *pgxpool.Pool, uid int64) error {
	svc := billing.NewService(pool, billingadapter.NoopClient{}, billing.PriceMap{})
	grant := billing.CompGrant{UserID: uid, Reason: "seed: e2e test account"}
	if err := svc.GrantCompWithExpiry(ctx, grant); err != nil {
		return fmt.Errorf("grant AI access:\n%w", err)
	}
	return nil
}
