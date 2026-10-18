// Package handlers contains the HTTP handlers and middleware.
package handlers

import (
	"errors"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/gofiber/fiber/v3"

	"github.com/harshadptl/gcore/internal/auth"
	"github.com/harshadptl/gcore/internal/config"
	"github.com/harshadptl/gcore/internal/markdown"
	"github.com/harshadptl/gcore/internal/models"
	"github.com/harshadptl/gcore/internal/store"
	"github.com/harshadptl/gcore/internal/view"
)

// Locals keys set by middleware.
const (
	localUser    = "user"
	localSession = "session"
)

// Handler bundles dependencies shared by every route.
type Handler struct {
	Cfg      *config.Config
	Store    *store.Store
	Google   *auth.Google // nil when Google OAuth is not configured
	Markdown *markdown.Renderer
	Signer   auth.Signer
	Log      *slog.Logger
}

// New wires a Handler from its dependencies.
func New(cfg *config.Config, st *store.Store, log *slog.Logger) *Handler {
	h := &Handler{
		Cfg:      cfg,
		Store:    st,
		Markdown: markdown.New(),
		Signer:   auth.NewSigner(cfg.SessionSecret),
		Log:      log,
	}
	if cfg.GoogleEnabled() {
		h.Google = auth.NewGoogle(cfg.GoogleClientID, cfg.GoogleClientSecret, cfg.GoogleRedirectURL)
	}
	return h
}

// CurrentUser returns the signed-in user, or nil.
func CurrentUser(c fiber.Ctx) *models.User {
	u, _ := c.Locals(localUser).(*models.User)
	return u
}

func currentSession(c fiber.Ctx) *models.Session {
	s, _ := c.Locals(localSession).(*models.Session)
	return s
}

// base builds the data every page shares. Call it once per request: it
// consumes pending flash messages.
func (h *Handler) base(c fiber.Ctx, title string) view.Base {
	b := view.Base{
		SiteName:  h.Cfg.AppName,
		BaseURL:   h.Cfg.BaseURL,
		Title:     title,
		OGType:    "website",
		Canonical: h.Cfg.BaseURL + c.Path(),
		Path:      c.Path(),
		User:      CurrentUser(c),
		Year:      time.Now().Year(),
	}
	if s := currentSession(c); s != nil {
		b.CSRF = s.CSRFToken
	}
	for _, m := range c.Redirect().Messages() {
		b.Flashes = append(b.Flashes, view.Flash{Kind: m.Key, Message: m.Value})
	}
	return b
}

// render writes a templ component as the HTML response.
func render(c fiber.Ctx, status int, comp templ.Component) error {
	c.Status(status)
	c.Set(fiber.HeaderContentType, fiber.MIMETextHTMLCharsetUTF8)
	return comp.Render(c, c.Response().BodyWriter())
}

// isFetch reports whether the request came from our progressive-enhancement
// JavaScript (which expects JSON instead of a redirect).
func isFetch(c fiber.Ctx) bool { return c.Get("X-Requested-With") == "fetch" }

// pageParam reads ?page= as a positive int.
func pageParam(c fiber.Ctx) int {
	n := fiber.Query[int](c, "page", 1)
	if n < 1 {
		return 1
	}
	return n
}

// canEdit reports whether u may modify p.
func canEdit(u *models.User, p *models.Post) bool {
	return u != nil && (u.ID == p.AuthorID || u.IsAdmin())
}

// notFound returns a 404 error for the error handler.
func notFound() error { return fiber.ErrNotFound }

// orNotFound maps store.ErrNotFound to a 404.
func orNotFound(err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return fiber.ErrNotFound
	}
	return err
}

func loginURL(next string) string {
	if next == "" || next == "/" {
		return "/login"
	}
	return "/login?next=" + url.QueryEscape(next)
}

// formNext returns a safe post-action redirect target from a hidden "next"
// form field, falling back to fallback.
func formNext(c fiber.Ctx, fallback string) string {
	next := strings.TrimSpace(c.FormValue("next"))
	if next == "" {
		return fallback
	}
	return auth.SafeNext(next)
}

// ErrorHandler renders HTML (or JSON for fetch requests) for any error
// returned by a handler.
func (h *Handler) ErrorHandler(c fiber.Ctx, err error) error {
	code := fiber.StatusInternalServerError
	var fe *fiber.Error
	switch {
	case errors.As(err, &fe):
		code = fe.Code
	case errors.Is(err, store.ErrNotFound):
		code = fiber.StatusNotFound
	}

	data := view.ErrorData{Code: code}
	switch code {
	case fiber.StatusNotFound:
		data.Title, data.Message = "Page not found", "The page you're looking for doesn't exist or has been moved."
	case fiber.StatusForbidden:
		data.Title, data.Message = "Forbidden", "You don't have permission to do that."
		if fe != nil && fe.Message != "" && fe.Message != "Forbidden" {
			data.Message = fe.Message
		}
	case fiber.StatusTooManyRequests:
		data.Title, data.Message = "Slow down", "You're doing that too often. Please wait a minute and try again."
	case fiber.StatusRequestEntityTooLarge:
		data.Title, data.Message = "Upload too large", "That file is bigger than the upload limit."
	default:
		if code >= 500 {
			h.Log.Error("request failed", "method", c.Method(), "path", c.Path(), "err", err)
			data.Title, data.Message = "Something went wrong", "An unexpected error occurred. Please try again."
		} else {
			data.Title = "Request error"
			if fe != nil {
				data.Message = fe.Message
			}
		}
	}

	if isFetch(c) || strings.HasPrefix(c.Path(), "/api/") {
		return c.Status(code).JSON(fiber.Map{"error": data.Message})
	}

	b := h.base(c, data.Title)
	b.NoIndex = true
	return render(c, code, view.ErrorPage(b, data))
}
