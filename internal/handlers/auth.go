package handlers

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/mail"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/harshadptl/gcore/internal/auth"
	"github.com/harshadptl/gcore/internal/store"
	"github.com/harshadptl/gcore/internal/view"
)

const (
	oauthStateCookie = "oauth_state"
	oauthStateTTL    = 10 * time.Minute
)

// LoginPage shows the sign-in options.
func (h *Handler) LoginPage(c fiber.Ctx) error {
	next := auth.SafeNext(c.Query("next"))
	if CurrentUser(c) != nil {
		return c.Redirect().To(next)
	}
	b := h.base(c, "Sign in")
	b.NoIndex = true
	return render(c, fiber.StatusOK, view.LoginPage(b, view.LoginData{
		Next:          next,
		GoogleEnabled: h.Google != nil,
		DevLogin:      h.Cfg.DevLoginEnabled(),
	}))
}

// GoogleStart redirects to Google's consent screen.
func (h *Handler) GoogleStart(c fiber.Ctx) error {
	if h.Google == nil {
		return c.Redirect().With("error", "Google sign-in is not configured. Set GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET.").To("/login")
	}

	st := auth.OAuthState{
		State:    store.NewToken(),
		Verifier: auth.NewVerifier(),
		Next:     auth.SafeNext(c.Query("next")),
		Issued:   time.Now().Unix(),
	}
	signed, err := h.Signer.Sign(st)
	if err != nil {
		return err
	}
	c.Cookie(&fiber.Cookie{
		Name:     oauthStateCookie,
		Value:    signed,
		Path:     "/auth/google",
		MaxAge:   int(oauthStateTTL.Seconds()),
		HTTPOnly: true,
		Secure:   h.Cfg.SecureCookies(),
		SameSite: fiber.CookieSameSiteLaxMode, // must survive the top-level redirect back from Google
	})
	return c.Redirect().Status(fiber.StatusFound).To(h.Google.AuthCodeURL(st.State, st.Verifier))
}

// GoogleCallback completes the OAuth flow and signs the user in.
func (h *Handler) GoogleCallback(c fiber.Ctx) error {
	if h.Google == nil {
		return notFound()
	}
	raw := c.Cookies(oauthStateCookie)
	c.Cookie(&fiber.Cookie{Name: oauthStateCookie, Value: "", Path: "/auth/google", MaxAge: -1,
		HTTPOnly: true, Secure: h.Cfg.SecureCookies(), SameSite: fiber.CookieSameSiteLaxMode})

	if e := c.Query("error"); e != "" {
		msg := "Google sign-in was cancelled."
		if e != "access_denied" {
			msg = "Google sign-in failed (" + e + ")."
		}
		return c.Redirect().With("warning", msg).To("/login")
	}

	var st auth.OAuthState
	if raw == "" || h.Signer.Verify(raw, &st) != nil || st.Expired(oauthStateTTL) ||
		subtle.ConstantTimeCompare([]byte(st.State), []byte(c.Query("state"))) != 1 {
		return c.Redirect().With("error", "Your sign-in attempt expired. Please try again.").To("/login")
	}

	ctx, cancel := context.WithTimeout(c, 15*time.Second)
	defer cancel()

	profile, err := h.Google.Exchange(ctx, c.Query("code"), st.Verifier)
	if errors.Is(err, auth.ErrEmailNotVerified) {
		return c.Redirect().With("error", "Your Google account's email address is not verified.").To("/login")
	}
	if err != nil {
		h.Log.Warn("google exchange failed", "err", err)
		return c.Redirect().With("error", "We couldn't sign you in with Google. Please try again.").To("/login")
	}

	return h.completeLogin(c, profile, st.Next)
}

// DevLogin signs in as any email without Google. Only available when
// APP_ENV is not production and DEV_LOGIN=true.
func (h *Handler) DevLogin(c fiber.Ctx) error {
	if !h.Cfg.DevLoginEnabled() {
		return notFound()
	}
	addr, err := mail.ParseAddress(strings.TrimSpace(c.FormValue("email")))
	if err != nil {
		return c.Redirect().With("error", "Enter a valid email address.").To("/login")
	}
	name := strings.TrimSpace(c.FormValue("name"))
	if name == "" {
		name = strings.SplitN(addr.Address, "@", 2)[0]
	}
	profile := store.OAuthProfile{
		Subject: "dev:" + strings.ToLower(addr.Address),
		Email:   addr.Address,
		Name:    name,
	}
	return h.completeLogin(c, profile, auth.SafeNext(c.FormValue("next")))
}

func (h *Handler) completeLogin(c fiber.Ctx, profile store.OAuthProfile, next string) error {
	user, err := h.Store.UpsertOAuthUser(c, profile, h.Cfg.IsAdminEmail(profile.Email))
	if err != nil {
		return err
	}
	// Drop any previous session tied to this browser before issuing a new one.
	if old := currentSession(c); old != nil {
		_ = h.Store.DeleteSession(c, old.ID)
	}
	if err := h.startSession(c, user); err != nil {
		return err
	}
	h.Log.Info("user signed in", "user_id", user.ID, "email", user.Email)
	return c.Redirect().With("success", "Welcome, "+user.Name+"!").To(next)
}

// Logout ends the current session.
func (h *Handler) Logout(c fiber.Ctx) error {
	if s := currentSession(c); s != nil {
		if err := h.Store.DeleteSession(c, s.ID); err != nil {
			return err
		}
	}
	h.clearSessionCookie(c)
	return c.Redirect().With("success", "You've been signed out.").To("/")
}
