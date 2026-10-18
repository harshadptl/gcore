// Package auth implements "Sign in with Google" (OAuth 2.0 + OpenID Connect
// userinfo, with PKCE) and small helpers for signed cookies.
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/endpoints"

	"github.com/harshadptl/gcore/internal/store"
)

const googleUserInfoURL = "https://openidconnect.googleapis.com/v1/userinfo"

// ErrEmailNotVerified is returned when Google reports an unverified email.
var ErrEmailNotVerified = errors.New("google account email is not verified")

// Google performs the OAuth 2.0 authorization-code flow against Google.
type Google struct {
	cfg    *oauth2.Config
	client *http.Client
}

// NewGoogle configures the provider. redirectURL must exactly match an
// "Authorized redirect URI" in the Google Cloud console.
func NewGoogle(clientID, clientSecret, redirectURL string) *Google {
	return &Google{
		cfg: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURL,
			Endpoint:     endpoints.Google,
			Scopes:       []string{"openid", "email", "profile"},
		},
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// AuthCodeURL returns the Google consent-screen URL. state guards against
// CSRF; verifier is the PKCE code verifier kept by the client.
func (g *Google) AuthCodeURL(state, verifier string) string {
	return g.cfg.AuthCodeURL(state,
		oauth2.AccessTypeOnline,
		oauth2.S256ChallengeOption(verifier),
		oauth2.SetAuthURLParam("prompt", "select_account"),
	)
}

// NewVerifier returns a fresh PKCE code verifier.
func NewVerifier() string { return oauth2.GenerateVerifier() }

// Exchange trades the authorization code for a token and fetches the
// user's profile.
func (g *Google) Exchange(ctx context.Context, code, verifier string) (store.OAuthProfile, error) {
	ctx = context.WithValue(ctx, oauth2.HTTPClient, g.client)

	tok, err := g.cfg.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return store.OAuthProfile{}, fmt.Errorf("exchange code: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, googleUserInfoURL, nil)
	if err != nil {
		return store.OAuthProfile{}, err
	}
	resp, err := g.cfg.Client(ctx, tok).Do(req)
	if err != nil {
		return store.OAuthProfile{}, fmt.Errorf("fetch userinfo: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return store.OAuthProfile{}, fmt.Errorf("fetch userinfo: %s: %s", resp.Status, body)
	}

	var info struct {
		Sub           string `json:"sub"`
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Name          string `json:"name"`
		Picture       string `json:"picture"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&info); err != nil {
		return store.OAuthProfile{}, fmt.Errorf("decode userinfo: %w", err)
	}
	if !info.EmailVerified {
		return store.OAuthProfile{}, ErrEmailNotVerified
	}
	return store.OAuthProfile{
		Subject:   info.Sub,
		Email:     info.Email,
		Name:      info.Name,
		AvatarURL: info.Picture,
	}, nil
}
