package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/harshadptl/gcore/internal/config"
	"github.com/harshadptl/gcore/internal/database"
	"github.com/harshadptl/gcore/internal/gcore"
	"github.com/harshadptl/gcore/internal/models"
	"github.com/harshadptl/gcore/internal/seed"
	"github.com/harshadptl/gcore/internal/server"
	"github.com/harshadptl/gcore/internal/store"
)

// client is a tiny cookie-keeping wrapper around app.Test.
type client struct {
	t       *testing.T
	app     *fiber.App
	cookies map[string]*http.Cookie
	st      *store.Store
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
	return &client{t: t, app: server.New(cfg, st, log), st: st, cookies: map[string]*http.Cookie{}}
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
type fakeGcore struct {
	mu         sync.Mutex
	streams    map[int64]*gcore.Stream
	nextID     int64
	calls      []string // "METHOD /path"
	failList   bool
	failDelete bool
	patchErr   string // non-empty => PATCH replies 403 with this body
	onCreate   func()
}

func newFakeGcore(t *testing.T) (*fakeGcore, *httptest.Server) {
	f := &fakeGcore{streams: map[int64]*gcore.Stream{}, nextID: 100}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeGcore) serve(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "APIKey test-key" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	f.mu.Lock()
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	onCreate := f.onCreate
	f.mu.Unlock()
	if r.Method == http.MethodPost && onCreate != nil {
		onCreate() // outside the lock: it may call back into the app
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	reply := func(v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	id, _ := strconv.ParseInt(r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:], 10, 64)
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/streaming/streams":
		f.nextID++
		s := &gcore.Stream{ID: f.nextID, PushURL: fmt.Sprintf("rtmp://push.test/in/%d?tok", f.nextID),
			PushURLSRT: "srt://push.test:5001", IframeURL: "https://iframe.test/x"}
		f.streams[s.ID] = s
		reply(s)
	case r.Method == http.MethodGet && r.URL.Path == "/streaming/streams":
		if f.failList {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		out := []*gcore.Stream{}
		for _, s := range f.streams {
			out = append(out, s)
		}
		reply(out)
	case r.Method == http.MethodPatch && f.streams[id] != nil:
		if f.patchErr != "" {
			http.Error(w, f.patchErr, http.StatusForbidden)
			return
		}
		var body struct {
			Active bool `json:"active"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.streams[id].Active = body.Active
		reply(f.streams[id])
	case r.Method == http.MethodDelete && f.streams[id] != nil:
		if f.failDelete {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		delete(f.streams, id)
		w.WriteHeader(http.StatusNoContent)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeGcore) setLive(id int64, live bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.streams[id].Live = live
}

func (f *fakeGcore) count(call string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == call {
			n++
		}
	}
	return n
}

func withGcore(url string) func(*config.Config) {
	return func(cfg *config.Config) { cfg.GcoreAPIKey, cfg.GcoreAPIBase = "test-key", url }
}

func (c *client) userID(username string) int64 {
	c.t.Helper()
	u, err := c.st.UserByUsername(context.Background(), username)
	if err != nil {
		c.t.Fatal(err)
	}
	return u.ID
}

func TestGoLiveFlow(t *testing.T) {
	fake, srv := newFakeGcore(t)
	c := newApp(t, withGcore(srv.URL))
	csrf := c.login("grace@example.com")
	uid := c.userID("grace")

	resp, body := c.do("GET", "/dashboard/live", nil)
	if resp.StatusCode != 200 || !strings.Contains(body, "Start stream") {
		t.Fatalf("live page: %d", resp.StatusCode)
	}

	resp, _ = c.do("POST", "/stream/start", url.Values{"_csrf": {csrf}, "title": {"My first stream"}})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/dashboard/live" {
		t.Fatalf("start: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	if len(fake.streams) != 1 {
		t.Fatalf("fake streams = %d", len(fake.streams))
	}
	for _, s := range fake.streams {
		if !s.Active {
			t.Fatal("gcore stream not armed")
		}
	}
	ls, err := c.st.LiveStreamByUser(context.Background(), uid)
	if err != nil || !ls.Active || ls.ArmedAt.IsZero() || !ls.StartedAt.IsZero() {
		t.Fatalf("row after start: %+v %v", ls, err)
	}

	c.do("POST", "/stream/start", url.Values{"_csrf": {csrf}, "title": {"again"}})
	if n := fake.count("POST /streaming/streams"); n != 1 {
		t.Fatalf("create called %d times, want 1 (reuse)", n)
	}

	resp, _ = c.do("POST", "/stream/stop", url.Values{"_csrf": {csrf}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("stop: %d", resp.StatusCode)
	}
	for _, s := range fake.streams {
		if s.Active {
			t.Fatal("gcore stream still armed after stop")
		}
	}
	if ls, _ := c.st.LiveStreamByUser(context.Background(), uid); ls.Active {
		t.Fatal("row still active after stop")
	}
}

func TestStartStreamValidatesTitle(t *testing.T) {
	fake, srv := newFakeGcore(t)
	c := newApp(t, withGcore(srv.URL))
	csrf := c.login("grace@example.com")

	resp, _ := c.do("POST", "/stream/start", url.Values{"_csrf": {csrf}, "title": {strings.Repeat("é", 101)}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("gcore calls: %v", fake.calls)
	}
	if _, err := c.st.LiveStreamByUser(context.Background(), c.userID("grace")); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("row exists: %v", err)
	}

	c.do("POST", "/stream/start", url.Values{"_csrf": {csrf}, "title": {"<b>x</b>"}})
	_, body := c.do("GET", "/dashboard/live", nil)
	if !strings.Contains(body, "&lt;b&gt;") || strings.Contains(body, "<b>x</b>") {
		t.Fatal("title not escaped")
	}
}

func TestStartStreamQuotaError(t *testing.T) {
	fake, srv := newFakeGcore(t)
	fake.patchErr = `{"error":"quota exceeded"}`
	c := newApp(t, withGcore(srv.URL))
	csrf := c.login("grace@example.com")

	c.do("POST", "/stream/start", url.Values{"_csrf": {csrf}, "title": {"t"}})
	_, body := c.do("GET", "/dashboard/live", nil)
	if !strings.Contains(body, "quota exceeded") {
		t.Fatal("quota error not shown")
	}
	if strings.Contains(body, "test-key") {
		t.Fatal("API key leaked")
	}
	if ls, err := c.st.LiveStreamByUser(context.Background(), c.userID("grace")); err != nil || ls.Active {
		t.Fatalf("row should be disarmed: %+v %v", ls, err)
	}
}

func TestStartStreamCreateRace(t *testing.T) {
	fake, srv := newFakeGcore(t)
	c := newApp(t, withGcore(srv.URL))
	csrf := c.login("grace@example.com")
	uid := c.userID("grace")
	fake.onCreate = func() {
		_, _ = c.st.CreateLiveStream(context.Background(), &models.LiveStream{UserID: uid, GcoreStreamID: 7, PushURL: "rtmp://w/in/7?t"})
	}

	resp, _ := c.do("POST", "/stream/start", url.Values{"_csrf": {csrf}, "title": {"t"}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if n := fake.count("DELETE /streaming/streams/101"); n != 1 {
		t.Fatalf("loser stream not deleted: %v", fake.calls)
	}
	ls, err := c.st.LiveStreamByUser(context.Background(), uid)
	if err != nil || ls.GcoreStreamID != 7 {
		t.Fatalf("row = %+v %v", ls, err)
	}
}

func TestStartStreamWithoutGcoreConfigured(t *testing.T) {
	c := newApp(t)
	csrf := c.login("grace@example.com")
	c.do("POST", "/stream/start", url.Values{"_csrf": {csrf}, "title": {"t"}})
	_, body := c.do("GET", "/dashboard/live", nil)
	if !strings.Contains(body, "isn&#39;t configured") && !strings.Contains(body, "isn't configured") && !strings.Contains(body, `isn't configured`) {
		t.Fatal("missing not-configured flash")
	}
	if resp, _ := c.do("GET", "/", nil); resp.StatusCode != 200 {
		t.Fatalf("home: %d", resp.StatusCode)
	}
}
