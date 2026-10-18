// Package database opens the Bun database (PostgreSQL or SQLite) and runs
// migrations.
package database

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"       // registers the "pgx" driver
	_ "github.com/ncruces/go-sqlite3/driver" // registers the "sqlite3" driver (pure Go, no CGO)
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	"github.com/uptrace/bun/extra/bunslog"
	"github.com/uptrace/bun/migrate"

	"github.com/harshadptl/gcore/internal/database/migrations"
	"github.com/harshadptl/gcore/internal/models"
)

// Options configures Open.
type Options struct {
	URL    string // postgres://... | postgresql://... | sqlite:path.db | sqlite::memory:
	Debug  bool   // log every query
	Logger *slog.Logger
}

// Open connects to the database described by opts.URL and verifies the
// connection.
func Open(ctx context.Context, opts Options) (*bun.DB, error) {
	var db *bun.DB

	switch {
	case strings.HasPrefix(opts.URL, "postgres://"), strings.HasPrefix(opts.URL, "postgresql://"):
		sqldb, err := sql.Open("pgx", opts.URL)
		if err != nil {
			return nil, fmt.Errorf("open postgres: %w", err)
		}
		sqldb.SetMaxOpenConns(25)
		sqldb.SetMaxIdleConns(25)
		sqldb.SetConnMaxIdleTime(5 * time.Minute)
		db = bun.NewDB(sqldb, pgdialect.New())

	case strings.HasPrefix(opts.URL, "sqlite:"):
		dsn, memory, err := sqliteDSN(strings.TrimPrefix(opts.URL, "sqlite:"))
		if err != nil {
			return nil, err
		}
		sqldb, err := sql.Open("sqlite3", dsn)
		if err != nil {
			return nil, fmt.Errorf("open sqlite: %w", err)
		}
		if memory {
			// Each connection to :memory: is a separate database.
			sqldb.SetMaxOpenConns(1)
		} else {
			sqldb.SetMaxOpenConns(4)
		}
		sqldb.SetConnMaxLifetime(0)
		db = bun.NewDB(sqldb, sqlitedialect.New())

	default:
		return nil, fmt.Errorf("unsupported DATABASE_URL %q (use postgres://... or sqlite:path.db)", opts.URL)
	}

	db.RegisterModel(models.All()...)

	if opts.Debug {
		logger := opts.Logger
		if logger == nil {
			logger = slog.Default()
		}
		db.AddQueryHook(bunslog.NewQueryHook(
			bunslog.WithLogger(logger),
			bunslog.WithQueryLogLevel(slog.LevelDebug),
			bunslog.WithSlowQueryLogLevel(slog.LevelWarn),
			bunslog.WithSlowQueryThreshold(200*time.Millisecond),
		))
	}

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return db, nil
}

// sqliteDSN turns "data/blog.db" into a driver DSN with sensible pragmas and
// makes sure the parent directory exists.
func sqliteDSN(path string) (dsn string, memory bool, err error) {
	if path == "" {
		return "", false, fmt.Errorf("empty sqlite path")
	}
	if strings.Contains(path, "?") || strings.HasPrefix(path, "file:") {
		return path, strings.Contains(path, ":memory:"), nil // caller knows best
	}
	if path == ":memory:" {
		return "file::memory:?_pragma=foreign_keys(1)", true, nil
	}
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", false, fmt.Errorf("create sqlite dir: %w", err)
		}
	}
	pragmas := "_pragma=foreign_keys(1)&_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)&_pragma=synchronous(normal)&_txlock=immediate"
	return "file:" + path + "?" + pragmas, false, nil
}

// Migrator returns a migrator wired to the registered migrations.
func Migrator(db *bun.DB) *migrate.Migrator {
	return migrate.NewMigrator(db, migrations.Migrations)
}

// Migrate applies all pending migrations and returns the applied group.
func Migrate(ctx context.Context, db *bun.DB) (*migrate.MigrationGroup, error) {
	m := Migrator(db)
	if err := m.Init(ctx); err != nil {
		return nil, fmt.Errorf("init migrations: %w", err)
	}
	if err := m.Lock(ctx); err != nil {
		return nil, fmt.Errorf("lock migrations: %w", err)
	}
	defer m.Unlock(ctx) //nolint:errcheck

	group, err := m.Migrate(ctx)
	if err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return group, nil
}

// Rollback reverts the last applied migration group.
func Rollback(ctx context.Context, db *bun.DB) (*migrate.MigrationGroup, error) {
	m := Migrator(db)
	if err := m.Init(ctx); err != nil {
		return nil, err
	}
	if err := m.Lock(ctx); err != nil {
		return nil, err
	}
	defer m.Unlock(ctx) //nolint:errcheck
	return m.Rollback(ctx)
}

// Status lists all migrations with their applied state.
func Status(ctx context.Context, db *bun.DB) (migrate.MigrationSlice, error) {
	m := Migrator(db)
	if err := m.Init(ctx); err != nil {
		return nil, err
	}
	return m.MigrationsWithStatus(ctx)
}
