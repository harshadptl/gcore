package server_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/harshadptl/gcore/internal/config"
	"github.com/harshadptl/gcore/internal/database"
	"github.com/harshadptl/gcore/internal/seed"
	"github.com/harshadptl/gcore/internal/server"
	"github.com/harshadptl/gcore/internal/store"
)

// client is a tiny cookie-keeping wrapper around app.Test.
type client struct {
	t       *testing.T
	app     *fiber.App
	cookies map[string]*http.Cookie
}

func newApp(t *testing.T, mutate ...func(*config.Config)) *client {
	t.Helper()
	ctx := context.Background()
	db, err := database.Open(ctx, database.Options{URL: "sqlite::memory:"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := database.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	st := store.New(db)
	if err := seed.Run(ctx, st, "admin@example.com"); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Env: "development", AppName: "Test", BaseURL: "http://example.test",
		SessionSecret: strings.Repeat("x", 32), SessionTTL: time.Hour,
		DevLogin: true, AdminEmails: []string{"admin@example.com"},
		UploadDir: t.TempDir(), MaxUploadBytes: 1 << 20, PostsPerPage: 9,
	}
	for _, m := range mutate {
		m(cfg)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return &client{t: t, app: server.New(cfg, st, log), cookies: map[string]*http.Cookie{}}
}

func (c *client) do(method, path string, form url.Values, headers ...string) (*http.Response, string) {
	c.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req := httptest.NewRequest(method, path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	for _, ck := range c.cookies {
		req.AddCookie(ck)
	}
	resp, err := c.app.Test(req, fiber.TestConfig{Timeout: 5 * time.Second})
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	for _, ck := range resp.Cookies() {
		if ck.MaxAge < 0 || ck.Value == "" {
			delete(c.cookies, ck.Name)
		} else {
			c.cookies[ck.Name] = ck
		}
	}
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

var csrfRe = regexp.MustCompile(`name="_csrf" value="([^"]+)"`)

func (c *client) login(email string) string {
	c.t.Helper()
	resp, _ := c.do("POST", "/auth/dev-login", url.Values{"email": {email}, "next": {"/dashboard"}})
	if resp.StatusCode != http.StatusSeeOther {
		c.t.Fatalf("login: status %d", resp.StatusCode)
	}
	_, body := c.do("GET", "/dashboard", nil)
	m := csrfRe.FindStringSubmatch(body)
	if m == nil {
		c.t.Fatal("no CSRF token on dashboard")
	}
	return m[1]
}

func TestPublicPages(t *testing.T) {
	c := newApp(t)
	for path, want := range map[string]int{
		"/":                                 200,
		"/?q=templ":                         200,
		"/tags":                             200,
		"/tags/go":                          200,
		"/posts/welcome-to-gcore":           200,
		"/posts/notes-for-an-upcoming-post": 404, // drafts are private
		"/u/grace":                          200,
		"/feed.xml":                         200,
		"/sitemap.xml":                      200,
		"/robots.txt":                       200,
		"/healthz":                          200,
		"/static/css/app.css":               200,
		"/static/vendor/oat/oat.min.css":    200,
		"/does-not-exist":                   404,
		"/dashboard":                        303, // redirect to login
		"/admin/":                           303,
	} {
		resp, _ := c.do("GET", path, nil)
		if resp.StatusCode != want {
			t.Errorf("GET %s = %d, want %d", path, resp.StatusCode, want)
		}
	}
}

func TestWriterFlow(t *testing.T) {
	c := newApp(t)
	csrf := c.login("grace@example.com")

	// Unsafe requests without the CSRF token are rejected.
	resp, _ := c.do("POST", "/posts", url.Values{"title": {"x"}, "status": {"draft"}})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("missing CSRF: got %d", resp.StatusCode)
	}

	// Validation errors re-render the form.
	resp, body := c.do("POST", "/posts", url.Values{"_csrf": {csrf}, "title": {""}, "status": {"published"}})
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "Give your post a title") {
		t.Fatalf("validation: got %d", resp.StatusCode)
	}

	resp, _ = c.do("POST", "/posts", url.Values{
		"_csrf": {csrf}, "title": {"Testing Fiber"}, "content": {"# Hello\n\n<script>x</script>"},
		"status": {"published"}, "tags": {"go, testing"},
	})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/posts/testing-fiber" {
		t.Fatalf("create: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	_, body = c.do("GET", "/posts/testing-fiber", nil)
	if strings.Contains(body, "<script>x</script>") {
		t.Fatal("raw HTML from Markdown must be stripped")
	}
	if !strings.Contains(body, "#testing") {
		t.Fatal("tags not shown")
	}

	// Like via fetch returns JSON.
	resp, body = c.do("POST", "/posts/testing-fiber/like", url.Values{"_csrf": {csrf}}, "X-Requested-With", "fetch")
	if resp.StatusCode != 200 || !strings.Contains(body, `"active":true`) {
		t.Fatalf("like: %d %s", resp.StatusCode, body)
	}

	resp, _ = c.do("POST", "/posts/testing-fiber/comments", url.Values{"_csrf": {csrf}, "body": {"First!"}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("comment: %d", resp.StatusCode)
	}

	// Grace cannot edit Ada's post or open the admin area.
	if resp, _ := c.do("GET", "/posts/welcome-to-gcore/edit", nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("edit other's post: %d", resp.StatusCode)
	}
	if resp, _ := c.do("GET", "/admin/", nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("admin as user: %d", resp.StatusCode)
	}

	// Logging out invalidates the session.
	c.do("POST", "/logout", url.Values{"_csrf": {csrf}})
	if resp, _ := c.do("GET", "/dashboard", nil); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("dashboard after logout: %d", resp.StatusCode)
	}
}

func TestAdminCanModerate(t *testing.T) {
	c := newApp(t)
	csrf := c.login("admin@example.com")
	if resp, _ := c.do("GET", "/admin/", nil); resp.StatusCode != 200 {
		t.Fatalf("admin page: %d", resp.StatusCode)
	}
	// The seeded draft is visible to admins.
	if resp, _ := c.do("GET", "/posts/notes-for-an-upcoming-post", nil); resp.StatusCode != 200 {
		t.Fatalf("draft as admin: %d", resp.StatusCode)
	}
	resp, _ := c.do("POST", "/comments/1/delete", url.Values{"_csrf": {csrf}, "next": {"/admin"}})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/admin" {
		t.Fatalf("delete comment: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
}

func TestOpenRedirectIsBlocked(t *testing.T) {
	c := newApp(t)
	resp, _ := c.do("POST", "/auth/dev-login", url.Values{"email": {"x@example.com"}, "next": {"//evil.example"}})
	if loc := resp.Header.Get("Location"); loc != "/" {
		t.Fatalf("next=//evil.example redirected to %q", loc)
	}
}

func TestGoogleOAuthStart(t *testing.T) {
	c := newApp(t, func(cfg *config.Config) {
		cfg.GoogleClientID, cfg.GoogleClientSecret = "client-id", "client-secret"
		cfg.GoogleRedirectURL = cfg.BaseURL + "/auth/google/callback"
	})
	resp, _ := c.do("GET", "/auth/google?next=/dashboard", nil)
	loc, err := url.Parse(resp.Header.Get("Location"))
	if resp.StatusCode != http.StatusFound || err != nil || loc.Host != "accounts.google.com" {
		t.Fatalf("start: %d %v", resp.StatusCode, resp.Header.Get("Location"))
	}
	q := loc.Query()
	if q.Get("state") == "" || q.Get("code_challenge_method") != "S256" || q.Get("client_id") != "client-id" {
		t.Fatalf("missing OAuth params: %v", q)
	}
	if _, ok := c.cookies["oauth_state"]; !ok {
		t.Fatal("state cookie not set")
	}

	// A callback with a forged state is rejected before any token exchange.
	resp, _ = c.do("GET", "/auth/google/callback?state=forged&code=abc", nil)
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/login" {
		t.Fatalf("forged state: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
}
