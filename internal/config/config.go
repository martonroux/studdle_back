package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all runtime settings loaded from environment variables.
type Config struct {
	Port        string        // Port is the HTTP listen port (e.g. "8080")
	Env         string        // Env is "dev", "test", or "prod"
	FrontendURL  string   // FrontendURL is the primary Vue client origin (used for email links)
	CORSOrigins  []string // CORSOrigins is the full allowlist echoed back for Access-Control-Allow-Origin
	BackendURL   string   // BackendURL is this server's public URL
	AppURL       string   // AppURL is the user-facing app origin for Stripe redirects (defaults to FrontendURL)
	DatabaseURL string        // DatabaseURL is the full pgx connection string
	JWTSecret   string        // JWTSecret signs auth tokens (>=32 bytes)
	JWTIssuer   string        // JWTIssuer is the "iss" claim value
	JWTTTL      time.Duration // JWTTTL is the token expiration window

	SMTPHost string // SMTPHost is the outbound mail server hostname
	SMTPPort string // SMTPPort is the outbound mail server port
	SMTPUser string // SMTPUser is the SMTP auth username
	SMTPPass string // SMTPPass is the SMTP auth password
	SMTPFrom string // SMTPFrom is the From: header for outbound email

	UploadDir string // UploadDir is the filesystem root for uploaded images

	AnthropicAPIKey string // AnthropicAPIKey is the Anthropic API key (Spec A)
	AIModel         string // AIModel is the default model identifier

	KeywordWorkers    int // KeywordWorkers is the number of concurrent keyword-extraction goroutines
	KeywordRatePerMin int // KeywordRatePerMin is the global keyword-extraction API call cap
	KeywordBurst      int // KeywordBurst is the rate-limiter burst for keyword extraction

	StripeMode           string // StripeMode is "test" or "live"
	StripeSecretKey      string // StripeSecretKey is the Stripe API secret
	StripeWebhookSecret  string // StripeWebhookSecret verifies webhook signatures
	StripePriceProMonth  string // StripePriceProMonth is the monthly plan price ID
	StripePriceProAnnual string // StripePriceProAnnual is the annual plan price ID

	AdminBootstrapEmail string // AdminBootstrapEmail is auto-promoted to is_admin on boot
}

// Load reads environment variables, validates them, and returns a Config.
// Returns an error describing the first validation failure.
func Load() (*Config, error) {
	cfg := loadFromEnv()
	ttl, err := parseTTL(getEnvDefault("JWT_TTL", "720h"))
	if err != nil {
		return nil, fmt.Errorf("parse JWT_TTL:\n%w", err)
	}
	cfg.JWTTTL = ttl
	if cfg.AppURL == "" {
		cfg.AppURL = cfg.FrontendURL
	}
	if err := validate(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// loadFromEnv reads all environment variables into a Config struct (no validation).
func loadFromEnv() *Config {
	return &Config{
		Port:        getEnvDefault("PORT", "8080"),
		Env:         getEnvDefault("ENV", "dev"),
		FrontendURL: firstCSV(os.Getenv("FRONTEND_URL")),
		CORSOrigins: splitCSV(os.Getenv("FRONTEND_URL")),
		BackendURL:  os.Getenv("BACKEND_URL"),
		AppURL:      getEnvDefault("APP_URL", ""),
		DatabaseURL: os.Getenv("DATABASE_URL"),
		JWTSecret:   os.Getenv("JWT_SECRET"),
		JWTIssuer:   getEnvDefault("JWT_ISSUER", "studbud"),

		SMTPHost: os.Getenv("SMTP_HOST"),
		SMTPPort: os.Getenv("SMTP_PORT"),
		SMTPUser: os.Getenv("SMTP_USER"),
		SMTPPass: os.Getenv("SMTP_PASS"),
		SMTPFrom: os.Getenv("SMTP_FROM"),

		UploadDir: getEnvDefault("UPLOAD_DIR", "./uploads"),

		AnthropicAPIKey: os.Getenv("ANTHROPIC_API_KEY"),
		AIModel:         getEnvDefault("AI_MODEL", "claude-sonnet-4-6"),

		KeywordWorkers:    getEnvInt("KEYWORD_EXTRACT_WORKERS", 2),
		KeywordRatePerMin: getEnvInt("KEYWORD_EXTRACT_RATE_PER_MIN", 60),
		KeywordBurst:      getEnvInt("KEYWORD_EXTRACT_BURST", 120),

		StripeMode:           getEnvDefault("STRIPE_MODE", "test"),
		StripeSecretKey:      os.Getenv("STRIPE_SECRET_KEY"),
		StripeWebhookSecret:  os.Getenv("STRIPE_WEBHOOK_SECRET"),
		StripePriceProMonth:  os.Getenv("STRIPE_PRICE_PRO_MONTHLY"),
		StripePriceProAnnual: os.Getenv("STRIPE_PRICE_PRO_ANNUAL"),

		AdminBootstrapEmail: os.Getenv("ADMIN_BOOTSTRAP_EMAIL"),
	}
}

// getEnvDefault returns the value of key if set and non-empty, else fallback.
func getEnvDefault(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

// getEnvInt parses key as an int and returns it; returns fallback on missing or invalid input.
func getEnvInt(key string, fallback int) int {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback
	}

	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}

	return n
}

// parseTTL parses a duration string and returns it or a wrapped error.
func parseTTL(s string) (time.Duration, error) {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q:\n%w", s, err)
	}
	return d, nil
}

// validate runs all validation groups in order and returns the first error.
func validate(c *Config) error {
	if err := validateCore(c); err != nil {
		return err
	}
	if err := validateAuth(c); err != nil {
		return err
	}
	if err := validateSMTP(c); err != nil {
		return err
	}
	if err := validateStripeMode(c); err != nil {
		return err
	}
	if c.Env == "prod" {
		if err := validateProdRequirements(c); err != nil {
			return err
		}
	}
	return nil
}

// validateCore ensures the three mandatory URL/DSN fields are present.
func validateCore(c *Config) error {
	missing := []string{}
	if c.FrontendURL == "" {
		missing = append(missing, "FRONTEND_URL")
	}
	if c.BackendURL == "" {
		missing = append(missing, "BACKEND_URL")
	}
	if c.DatabaseURL == "" {
		missing = append(missing, "DATABASE_URL")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required env vars: %s", strings.Join(missing, ", "))
	}
	return nil
}

// validateAuth rejects JWT secrets shorter than 32 bytes.
func validateAuth(c *Config) error {
	if len(c.JWTSecret) < 32 {
		return fmt.Errorf("JWT_SECRET must be at least 32 bytes (got %d)", len(c.JWTSecret))
	}
	return nil
}

// validateSMTP ensures the three required SMTP fields are present.
func validateSMTP(c *Config) error {
	if c.SMTPHost == "" || c.SMTPPort == "" || c.SMTPFrom == "" {
		return fmt.Errorf("SMTP_HOST, SMTP_PORT, SMTP_FROM are required")
	}
	return nil
}

// validateStripeMode rejects unknown modes, live-mode outside prod, and
// secret keys whose prefix does not match the configured mode.
func validateStripeMode(c *Config) error {
	if c.StripeMode != "test" && c.StripeMode != "live" {
		return fmt.Errorf("STRIPE_MODE must be 'test' or 'live' (got %q)", c.StripeMode)
	}
	if c.StripeMode == "live" && c.Env != "prod" {
		return fmt.Errorf("STRIPE_MODE=live is not allowed when ENV=%q", c.Env)
	}
	if c.StripeSecretKey == "" {
		return nil
	}
	wantPrefix := "sk_test_"
	if c.StripeMode == "live" {
		wantPrefix = "sk_live_"
	}
	if !strings.HasPrefix(c.StripeSecretKey, wantPrefix) {
		return fmt.Errorf("STRIPE_SECRET_KEY prefix does not match STRIPE_MODE=%s (want %s*)", c.StripeMode, wantPrefix)
	}
	return nil
}

// validateProdRequirements ensures prod-only secrets are present.
func validateProdRequirements(c *Config) error {
	if c.AnthropicAPIKey == "" {
		return fmt.Errorf("ANTHROPIC_API_KEY required in prod")
	}
	if c.StripeSecretKey == "" || c.StripeWebhookSecret == "" {
		return fmt.Errorf("Stripe keys required in prod")
	}
	return nil
}

// splitCSV parses a comma-separated env value into a trimmed, non-empty slice.
func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// firstCSV returns the first non-empty entry from a comma-separated env value.
func firstCSV(raw string) string {
	if s := splitCSV(raw); len(s) > 0 {
		return s[0]
	}
	return ""
}
