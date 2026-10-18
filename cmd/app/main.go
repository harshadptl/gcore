// Command app runs the blog server and its maintenance tasks.
//
//	app [serve]                 start the HTTP server (default)
//	app migrate [up|down|status] manage database migrations
//	app seed                    insert demo users and posts
//	app healthcheck             exit 0 if the local server answers /healthz
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/uptrace/bun"

	"github.com/harshadptl/gcore/internal/config"
	"github.com/harshadptl/gcore/internal/database"
	"github.com/harshadptl/gcore/internal/seed"
	"github.com/harshadptl/gcore/internal/server"
	"github.com/harshadptl/gcore/internal/store"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	log := newLogger(cfg)
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cmd, args := "serve", []string(nil)
	if len(os.Args) > 1 {
		cmd, args = os.Args[1], os.Args[2:]
	}

	switch cmd {
	case "serve":
		return serve(ctx, cfg, log)
	case "migrate":
		return migrate(ctx, cfg, log, args)
	case "seed":
		return withDB(ctx, cfg, log, func(db *bun.DB) error {
			if _, err := database.Migrate(ctx, db); err != nil {
				return err
			}
			admin := ""
			if len(cfg.AdminEmails) > 0 {
				admin = cfg.AdminEmails[0]
			}
			if err := seed.Run(ctx, store.New(db), admin); err != nil {
				return err
			}
			log.Info("seeded demo content", "admin", admin)
			return nil
		})
	case "healthcheck":
		return healthcheck(cfg)
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown command %q", cmd)
	}
}

func usage() {
	fmt.Println(`Usage: app <command>

Commands:
  serve                    Start the web server (default)
  migrate up               Apply pending migrations
  migrate down             Roll back the last migration group
  migrate status           List migrations and whether they're applied
  seed                     Insert demo users and posts (empty database only)
  healthcheck              Check that the local server is healthy (for Docker)`)
}

func serve(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
	return withDB(ctx, cfg, log, func(db *bun.DB) error {
		if cfg.AutoMigrate {
			group, err := database.Migrate(ctx, db)
			if err != nil {
				return err
			}
			if !group.IsZero() {
				log.Info("applied migrations", "group", group.String())
			}
		}

		st := store.New(db)
		go cleanupSessions(ctx, st, log)

		app := server.New(cfg, st, log)
		log.Info("starting server", "addr", cfg.Addr, "env", cfg.Env, "url", cfg.BaseURL,
			"google_login", cfg.GoogleEnabled(), "dev_login", cfg.DevLoginEnabled())
		return app.Listen(cfg.Addr, fiber.ListenConfig{
			GracefulContext:       ctx,
			ShutdownTimeout:       10 * time.Second,
			DisableStartupMessage: cfg.IsProduction(),
		})
	})
}

// healthcheck lets minimal container images (no curl) probe the server.
func healthcheck(cfg *config.Config) error {
	addr := cfg.Addr
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + addr + "/healthz")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unhealthy: %s", resp.Status)
	}
	return nil
}

func migrate(ctx context.Context, cfg *config.Config, log *slog.Logger, args []string) error {
	sub := "up"
	if len(args) > 0 {
		sub = args[0]
	}
	return withDB(ctx, cfg, log, func(db *bun.DB) error {
		switch sub {
		case "up":
			group, err := database.Migrate(ctx, db)
			if err != nil {
				return err
			}
			if group.IsZero() {
				fmt.Println("Nothing to migrate: database is up to date.")
			} else {
				fmt.Printf("Migrated to %s\n", group)
			}
		case "down":
			group, err := database.Rollback(ctx, db)
			if err != nil {
				return err
			}
			if group.IsZero() {
				fmt.Println("Nothing to roll back.")
			} else {
				fmt.Printf("Rolled back %s\n", group)
			}
		case "status":
			ms, err := database.Status(ctx, db)
			if err != nil {
				return err
			}
			for _, m := range ms {
				state := "pending"
				if m.IsApplied() {
					state = fmt.Sprintf("applied (group %d)", m.GroupID)
				}
				fmt.Printf("%-40s %s\n", m.Name+"_"+m.Comment, state)
			}
		default:
			return fmt.Errorf("unknown migrate subcommand %q (use up, down or status)", sub)
		}
		return nil
	})
}

func withDB(ctx context.Context, cfg *config.Config, log *slog.Logger, fn func(*bun.DB) error) error {
	db, err := database.Open(ctx, database.Options{URL: cfg.DatabaseURL, Debug: cfg.DBDebug, Logger: log})
	if err != nil {
		return err
	}
	defer db.Close()
	if err := fn(db); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

// cleanupSessions deletes expired sessions every hour.
func cleanupSessions(ctx context.Context, st *store.Store, log *slog.Logger) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		if n, err := st.DeleteExpiredSessions(ctx); err != nil && ctx.Err() == nil {
			log.Warn("session cleanup failed", "err", err)
		} else if n > 0 {
			log.Info("removed expired sessions", "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func newLogger(cfg *config.Config) *slog.Logger {
	var level slog.Level
	if err := level.UnmarshalText([]byte(strings.ToUpper(cfg.LogLevel))); err != nil {
		level = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: level}
	if cfg.IsProduction() {
		return slog.New(slog.NewJSONHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stdout, opts))
}
