// Package server assembles the Fiber app: middleware stack and routes.
package server

import (
	"io/fs"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/compress"
	"github.com/gofiber/fiber/v3/middleware/csrf"
	"github.com/gofiber/fiber/v3/middleware/helmet"
	"github.com/gofiber/fiber/v3/middleware/limiter"
	"github.com/gofiber/fiber/v3/middleware/logger"
	"github.com/gofiber/fiber/v3/middleware/recover"
	"github.com/gofiber/fiber/v3/middleware/requestid"
	"github.com/gofiber/fiber/v3/middleware/static"

	"github.com/harshadptl/gcore/internal/config"
	"github.com/harshadptl/gcore/internal/handlers"
	"github.com/harshadptl/gcore/internal/store"
	"github.com/harshadptl/gcore/web"
)

// contentSecurityPolicy allows our own scripts/styles plus images from any
// HTTPS origin (Google avatars, external cover images).
const contentSecurityPolicy = "default-src 'self'; " +
	"img-src 'self' https: data:; " +
	"style-src 'self' 'unsafe-inline'; " +
	"script-src 'self'; " +
	"frame-src 'self' https://player.gvideo.co; " +
	"connect-src 'self' https://whip.gvideo.co; " +
	"form-action 'self'; " +
	"frame-ancestors 'self'; " +
	"base-uri 'self'; " +
	"object-src 'none'"

// New builds the Fiber application.
func New(cfg *config.Config, st *store.Store, log *slog.Logger) *fiber.App {
	h := handlers.New(cfg, st, log)

	fcfg := fiber.Config{
		AppName:      cfg.AppName,
		ErrorHandler: h.ErrorHandler,
		BodyLimit:    int(cfg.MaxUploadBytes) + 1<<20, // room for the form around an upload
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
		// Strings from c.Params/c.Query/c.FormValue stay valid after the handler
		// returns. Slightly slower, but removes a whole class of subtle bugs.
		Immutable: true,
	}
	if cfg.TrustProxy {
		fcfg.TrustProxy = true
		fcfg.TrustProxyConfig = fiber.TrustProxyConfig{Proxies: cfg.TrustedProxies, Loopback: true, Private: true}
		fcfg.ProxyHeader = fiber.HeaderXForwardedFor
	}
	app := fiber.New(fcfg)

	// --- Global middleware --------------------------------------------------
	app.Use(recover.New(recover.Config{EnableStackTrace: !cfg.IsProduction()}))
	app.Use(requestid.New())
	app.Use(logger.New(logger.Config{
		Format: "${time} ${status} ${method} ${path} ${latency} ${ip} ${respHeader:X-Request-ID}\n",
		Next:   func(c fiber.Ctx) bool { return strings.HasPrefix(c.Path(), "/static/") },
	}))
	app.Use(helmet.New(helmet.Config{
		ContentSecurityPolicy:     contentSecurityPolicy,
		CrossOriginEmbedderPolicy: "unsafe-none", // allow cross-origin images (avatars, covers)
		CrossOriginResourcePolicy: "same-site",
		ReferrerPolicy:            "strict-origin-when-cross-origin",
		HSTSMaxAge:                hstsMaxAge(cfg),
	}))
	app.Use(compress.New(compress.Config{
		// Skipped in development so templ's live-reload proxy can inject its script.
		Next: func(fiber.Ctx) bool { return !cfg.IsProduction() },
	}))
	// Tokenless CSRF defence for every unsafe request (uses Sec-Fetch-Site /
	// Origin). Signed-in users additionally need a per-session token (h.CSRF).
	app.Use(csrf.NewCrossOriginProtection())

	// --- Static assets (served before session lookup: no DB work) -----------
	app.Get("/static*", static.New("", staticConfig(cfg)))
	app.Get("/uploads*", static.New(cfg.UploadDir, static.Config{MaxAge: 30 * 24 * 3600}))
	app.Get("/favicon.ico", func(c fiber.Ctx) error {
		return c.Redirect().Status(fiber.StatusMovedPermanently).To("/static/img/favicon.svg")
	})
	app.Get("/healthz", h.Health)
	app.Get("/robots.txt", h.Robots)

	// --- Session + CSRF for everything below ---------------------------------
	app.Use(h.LoadSession, h.CSRF)

	authLimiter := limiter.New(limiter.Config{Max: 20, Expiration: time.Minute})
	writeLimiter := limiter.New(limiter.Config{
		Max:        30,
		Expiration: time.Minute,
		KeyGenerator: func(c fiber.Ctx) string {
			if u := handlers.CurrentUser(c); u != nil {
				return "u:" + strconv.FormatInt(u.ID, 10) + c.Path()
			}
			return c.IP()
		},
	})
	requireUser := h.RequireUser

	// Public pages
	app.Get("/", h.Home)
	app.Get("/tags", h.Tags)
	app.Get("/tags/:slug", h.Tag)
	app.Get("/u/:username", h.Profile)
	app.Get("/live/:username", h.WatchLive)
	app.Get("/live/:username/status", h.LiveStatus)
	app.Get("/feed.xml", h.RSS)
	app.Get("/sitemap.xml", h.Sitemap)

	// Auth
	app.Get("/login", h.LoginPage, handlers.NoStore)
	authGroup := app.Group("/auth", authLimiter, handlers.NoStore)
	authGroup.Get("/google", h.GoogleStart)
	authGroup.Get("/google/callback", h.GoogleCallback)
	authGroup.Post("/dev-login", h.DevLogin)
	app.Post("/logout", requireUser, h.Logout)

	// Posts. "/posts/new" must be registered before "/posts/:slug".
	app.Get("/posts/new", requireUser, handlers.NoStore, h.NewPost)
	app.Post("/posts", requireUser, writeLimiter, h.CreatePost)
	app.Post("/preview", requireUser, writeLimiter, h.Preview)
	app.Get("/posts/:slug", h.ShowPost)
	app.Get("/posts/:slug/edit", requireUser, handlers.NoStore, h.EditPost)
	app.Post("/posts/:slug", requireUser, writeLimiter, h.UpdatePost)
	app.Post("/posts/:slug/delete", requireUser, h.DeletePost)
	app.Post("/posts/:slug/like", requireUser, writeLimiter, h.ToggleLike)
	app.Post("/posts/:slug/bookmark", requireUser, writeLimiter, h.ToggleBookmark)
	app.Post("/posts/:slug/comments", requireUser, writeLimiter, h.CreateComment)
	app.Post("/comments/:id<int>/delete", requireUser, h.DeleteComment)

	// Account
	app.Get("/dashboard", requireUser, handlers.NoStore, h.Dashboard)
	app.Get("/dashboard/saved", requireUser, handlers.NoStore, h.Saved)
	app.Get("/settings", requireUser, handlers.NoStore, h.Settings)
	app.Get("/dashboard/live", requireUser, handlers.NoStore, h.LiveDashboard)
	app.Post("/stream/start", requireUser, writeLimiter, h.StartStream)
	app.Post("/stream/stop", requireUser, h.StopStream)
	app.Post("/settings", requireUser, h.UpdateSettings)
	app.Post("/settings/sessions/revoke", requireUser, h.RevokeOtherSessions)
	app.Post("/settings/delete", requireUser, h.DeleteAccount)

	// Admin
	admin := app.Group("/admin", requireUser, h.RequireAdmin, handlers.NoStore)
	admin.Get("/", h.Admin)
	admin.Post("/users/:id<int>/role", h.SetUserRole)
	admin.Post("/live-streams/:user_id<int>/terminate", h.TerminateStream)

	// Anything else is a 404 rendered by the error handler.
	app.Use(func(c fiber.Ctx) error { return fiber.ErrNotFound })

	return app
}

// staticConfig serves the embedded assets in production. In development it
// reads web/static from disk (when run from the repo root) so CSS/JS edits
// show up on reload without rebuilding the binary.
func staticConfig(cfg *config.Config) static.Config {
	if !cfg.IsProduction() {
		if info, err := os.Stat("web/static"); err == nil && info.IsDir() {
			return static.Config{FS: os.DirFS("web/static"), CacheDuration: -1}
		}
	}
	embedded, err := fs.Sub(web.Static, "static")
	if err != nil {
		panic(err)
	}
	cacheFor := 0
	if cfg.IsProduction() {
		cacheFor = 7 * 24 * 3600
	}
	return static.Config{FS: embedded, Compress: true, MaxAge: cacheFor, CacheDuration: time.Minute}
}

func hstsMaxAge(cfg *config.Config) int {
	if cfg.IsProduction() && cfg.SecureCookies() {
		return 365 * 24 * 3600
	}
	return 0
}
