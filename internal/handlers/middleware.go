package handlers

import (
	"crypto/subtle"
	"errors"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/harshadptl/gcore/internal/models"
	"github.com/harshadptl/gcore/internal/store"
)

// sessionTouchInterval limits how often a session's expiry is extended, so
// browsing doesn't write to the database on every request.
const sessionTouchInterval = 15 * time.Minute

// SessionCookieName uses the __Host- prefix on HTTPS, which browsers only
// accept with Secure, Path=/ and no Domain — pinning the cookie to this host.
func (h *Handler) SessionCookieName() string {
	if h.Cfg.SecureCookies() {
		return "__Host-sid"
	}
	return "sid"
}

func (h *Handler) setSessionCookie(c fiber.Ctx, token string, expires time.Time) {
	c.Cookie(&fiber.Cookie{
		Name:     h.SessionCookieName(),
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HTTPOnly: true,
		Secure:   h.Cfg.SecureCookies(),
		SameSite: fiber.CookieSameSiteLaxMode,
	})
}

func (h *Handler) clearSessionCookie(c fiber.Ctx) {
	c.Cookie(&fiber.Cookie{
		Name:     h.SessionCookieName(),
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HTTPOnly: true,
		Secure:   h.Cfg.SecureCookies(),
		SameSite: fiber.CookieSameSiteLaxMode,
	})
}

// startSession creates a fresh session for user (never reusing an old ID,
// which prevents session fixation) and sets the cookie.
func (h *Handler) startSession(c fiber.Ctx, user *models.User) error {
	token, sess, err := h.Store.CreateSession(c, user.ID, c.Get(fiber.HeaderUserAgent), c.IP(), h.Cfg.SessionTTL)
	if err != nil {
		return err
	}
	h.setSessionCookie(c, token, sess.ExpiresAt)
	return nil
}

// LoadSession resolves the session cookie into the current user.
func (h *Handler) LoadSession(c fiber.Ctx) error {
	token := c.Cookies(h.SessionCookieName())
	if token == "" {
		return c.Next()
	}

	sess, err := h.Store.SessionByToken(c, token)
	if errors.Is(err, store.ErrNotFound) {
		h.clearSessionCookie(c)
		return c.Next()
	}
	if err != nil {
		return err
	}

	if time.Since(sess.LastSeenAt) > sessionTouchInterval {
		if err := h.Store.TouchSession(c, sess, h.Cfg.SessionTTL); err != nil {
			h.Log.Warn("touch session", "err", err)
		} else {
			h.setSessionCookie(c, token, sess.ExpiresAt)
		}
	}

	c.Locals(localSession, sess)
	c.Locals(localUser, sess.User)
	return c.Next()
}

// CSRF enforces the synchronizer-token pattern for signed-in users: every
// unsafe request must echo the session's CSRF token, either in the "_csrf"
// form field or the X-CSRF-Token header. Anonymous requests are covered by
// Fiber's cross-origin protection middleware registered in the server.
func (h *Handler) CSRF(c fiber.Ctx) error {
	switch c.Method() {
	case fiber.MethodGet, fiber.MethodHead, fiber.MethodOptions:
		return c.Next()
	}
	sess := currentSession(c)
	if sess == nil {
		return c.Next()
	}
	token := c.Get("X-CSRF-Token")
	if token == "" {
		token = c.FormValue("_csrf")
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(sess.CSRFToken)) != 1 {
		return fiber.NewError(fiber.StatusForbidden,
			"Your form expired or was submitted from another site. Please reload the page and try again.")
	}
	return c.Next()
}

// RequireUser redirects anonymous visitors to the sign-in page.
func (h *Handler) RequireUser(c fiber.Ctx) error {
	if CurrentUser(c) != nil {
		return c.Next()
	}
	if isFetch(c) {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Please sign in first.", "login": "/login"})
	}
	next := ""
	if c.Method() == fiber.MethodGet {
		next = c.OriginalURL()
	}
	return c.Redirect().With("warning", "Please sign in to continue.").To(loginURL(next))
}

// RequireAdmin allows only admins.
func (h *Handler) RequireAdmin(c fiber.Ctx) error {
	if u := CurrentUser(c); u != nil && u.IsAdmin() {
		return c.Next()
	}
	return fiber.ErrForbidden
}

// NoStore marks private pages as uncacheable.
func NoStore(c fiber.Ctx) error {
	c.Set(fiber.HeaderCacheControl, "no-store")
	return c.Next()
}
