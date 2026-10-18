// Package config loads application settings from the environment (and an
// optional .env file) into a typed struct.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// Config holds every runtime setting. Values come from environment variables;
// see .env.example for the full list with descriptions.
type Config struct {
	Env      string // "development" or "production"
	AppName  string
	BaseURL  string // public URL, no trailing slash, e.g. https://blog.example.com
	Addr     string // listen address, e.g. ":3000"
	LogLevel string

	DatabaseURL string // postgres://... or sqlite:path/to/file.db
	DBDebug     bool   // log every SQL query
	AutoMigrate bool   // apply pending migrations when the server starts

	SessionSecret string        // HMAC key for signed cookies (OAuth state)
	SessionTTL    time.Duration // how long a login lasts

	GoogleClientID     string
	GoogleClientSecret string
	GoogleRedirectURL  string

	AdminEmails []string // these Google accounts become admins on login
	DevLogin    bool     // enables password-less "dev login"; always off in production

	UploadDir      string
	MaxUploadBytes int64
	PostsPerPage   int

	TrustProxy     bool     // trust X-Forwarded-* headers from TrustedProxies
	TrustedProxies []string // CIDRs/IPs of your reverse proxy
}

// Load reads configuration. A missing .env file is not an error.
func Load() (*Config, error) {
	_ = godotenv.Load()

	cfg := &Config{
		Env:      env("APP_ENV", "development"),
		AppName:  env("APP_NAME", "gcore"),
		BaseURL:  strings.TrimRight(env("BASE_URL", "http://localhost:3000"), "/"),
		Addr:     env("ADDR", ":"+env("PORT", "3000")),
		LogLevel: env("LOG_LEVEL", "info"),

		DatabaseURL: env("DATABASE_URL", "sqlite:data/blog.db"),
		DBDebug:     envBool("DB_DEBUG", false),
		AutoMigrate: envBool("AUTO_MIGRATE", true),

		SessionSecret: env("SESSION_SECRET", ""),
		SessionTTL:    envDuration("SESSION_TTL", 30*24*time.Hour),

		GoogleClientID:     env("GOOGLE_CLIENT_ID", ""),
		GoogleClientSecret: env("GOOGLE_CLIENT_SECRET", ""),
		GoogleRedirectURL:  env("GOOGLE_REDIRECT_URL", ""),

		AdminEmails: envList("ADMIN_EMAILS"),
		DevLogin:    envBool("DEV_LOGIN", false),

		UploadDir:      env("UPLOAD_DIR", "data/uploads"),
		MaxUploadBytes: int64(envInt("MAX_UPLOAD_MB", 5)) << 20,
		PostsPerPage:   envInt("POSTS_PER_PAGE", 9),

		TrustProxy:     envBool("TRUST_PROXY", false),
		TrustedProxies: envList("TRUSTED_PROXIES"),
	}

	if cfg.GoogleRedirectURL == "" {
		cfg.GoogleRedirectURL = cfg.BaseURL + "/auth/google/callback"
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	var errs []error

	if _, err := url.ParseRequestURI(c.BaseURL); err != nil {
		errs = append(errs, fmt.Errorf("BASE_URL is not a valid URL: %w", err))
	}

	if c.IsProduction() {
		if len(c.SessionSecret) < 32 {
			errs = append(errs, errors.New("SESSION_SECRET must be at least 32 characters in production"))
		}
		if !c.GoogleEnabled() {
			errs = append(errs, errors.New("GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET are required in production"))
		}
	} else if c.SessionSecret == "" {
		// Convenience for local development only.
		c.SessionSecret = "dev-only-insecure-secret-change-me-please-0123456789"
	}

	if c.PostsPerPage < 1 {
		c.PostsPerPage = 9
	}

	return errors.Join(errs...)
}

// IsProduction reports whether APP_ENV=production.
func (c *Config) IsProduction() bool { return c.Env == "production" }

// GoogleEnabled reports whether Google OAuth credentials are configured.
func (c *Config) GoogleEnabled() bool {
	return c.GoogleClientID != "" && c.GoogleClientSecret != ""
}

// DevLoginEnabled reports whether the development login shortcut is usable.
func (c *Config) DevLoginEnabled() bool { return c.DevLogin && !c.IsProduction() }

// SecureCookies reports whether cookies should carry the Secure flag.
func (c *Config) SecureCookies() bool { return strings.HasPrefix(c.BaseURL, "https://") }

// IsAdminEmail reports whether email is listed in ADMIN_EMAILS.
func (c *Config) IsAdminEmail(email string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	for _, e := range c.AdminEmails {
		if e == email {
			return true
		}
	}
	return false
}

func env(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	v, err := strconv.ParseBool(env(key, strconv.FormatBool(fallback)))
	if err != nil {
		return fallback
	}
	return v
}

func envInt(key string, fallback int) int {
	v, err := strconv.Atoi(env(key, strconv.Itoa(fallback)))
	if err != nil {
		return fallback
	}
	return v
}

func envDuration(key string, fallback time.Duration) time.Duration {
	v, err := time.ParseDuration(env(key, fallback.String()))
	if err != nil {
		return fallback
	}
	return v
}

func envList(key string) []string {
	var out []string
	for _, part := range strings.Split(env(key, ""), ",") {
		if p := strings.ToLower(strings.TrimSpace(part)); p != "" {
			out = append(out, p)
		}
	}
	return out
}
