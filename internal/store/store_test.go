package store_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/harshadptl/gcore/internal/database"
	"github.com/harshadptl/gcore/internal/models"
	"github.com/harshadptl/gcore/internal/store"
)

// newStore opens an in-memory SQLite database. Set TEST_DATABASE_URL to a
// throwaway PostgreSQL database to run the same tests against Postgres
// (its public schema is wiped first!).
func newStore(t *testing.T) *store.Store {
	t.Helper()
	ctx := context.Background()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		url = "sqlite::memory:"
	}
	db, err := database.Open(ctx, database.Options{URL: url})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if strings.HasPrefix(url, "postgres") {
		if _, err := db.ExecContext(ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatalf("reset schema: %v", err)
		}
	}
	if _, err := database.Migrate(ctx, db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return store.New(db)
}

func TestPostLifecycle(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	alice, err := s.UpsertOAuthUser(ctx, store.OAuthProfile{Subject: "g-1", Email: "Alice@Example.com", Name: "Alice Doe"}, true)
	if err != nil {
		t.Fatalf("upsert user: %v", err)
	}
	if alice.Username != "alice" || !alice.IsAdmin() || alice.Email != "alice@example.com" {
		t.Fatalf("unexpected user: %+v", alice)
	}
	// Logging in again must update, not duplicate, and must keep the
	// display name the user may have customised.
	again, err := s.UpsertOAuthUser(ctx, store.OAuthProfile{Subject: "g-1", Email: "alice@example.com", Name: "Alice D.", AvatarURL: "https://x/a.png"}, false)
	if err != nil || again.ID != alice.ID || again.Name != "Alice Doe" || again.AvatarURL != "https://x/a.png" || !again.IsAdmin() {
		t.Fatalf("re-login: %v %+v", err, again)
	}

	in := store.PostInput{
		Title: "Hello World", Content: "# Hi", ContentHTML: "<h1>Hi</h1>",
		Status: models.StatusPublished, ReadingMinutes: 1, Tags: []string{"Go", "Web Dev"},
	}
	p1, err := s.CreatePost(ctx, alice.ID, in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if p1.Slug != "hello-world" || p1.PublishedAt.IsZero() {
		t.Fatalf("unexpected post: slug=%q published=%v", p1.Slug, p1.PublishedAt)
	}
	p2, err := s.CreatePost(ctx, alice.ID, store.PostInput{Title: "Hello World", Status: models.StatusDraft, Tags: []string{"go"}})
	if err != nil {
		t.Fatalf("create 2: %v", err)
	}
	if p2.Slug != "hello-world-2" {
		t.Fatalf("slug not unique: %q", p2.Slug)
	}

	got, err := s.PostBySlug(ctx, "hello-world")
	if err != nil {
		t.Fatalf("by slug: %v", err)
	}
	if got.Author == nil || got.Author.ID != alice.ID || len(got.Tags) != 2 {
		t.Fatalf("relations not loaded: author=%v tags=%v", got.Author, got.Tags)
	}
	if time.Since(got.CreatedAt) > time.Minute {
		t.Fatalf("bad created_at: %v", got.CreatedAt)
	}

	posts, page, err := s.ListPosts(ctx, store.PostFilter{Status: models.StatusPublished, Query: "hello"}, store.NewPage(1, 10))
	if err != nil || len(posts) != 1 || page.Total != 1 {
		t.Fatalf("list published: err=%v n=%d total=%d", err, len(posts), page.Total)
	}
	posts, _, err = s.ListPosts(ctx, store.PostFilter{TagSlug: "go"}, store.NewPage(1, 10))
	if err != nil || len(posts) != 2 {
		t.Fatalf("list by tag: err=%v n=%d", err, len(posts))
	}
	// LIKE wildcards in user input must be treated literally.
	posts, _, err = s.ListPosts(ctx, store.PostFilter{Query: "%"}, store.NewPage(1, 10))
	if err != nil || len(posts) != 0 {
		t.Fatalf("wildcard search: err=%v n=%d", err, len(posts))
	}

	liked, n, err := s.ToggleLike(ctx, alice.ID, p1.ID)
	if err != nil || !liked || n != 1 {
		t.Fatalf("like: %v %v %d", err, liked, n)
	}
	liked, n, err = s.ToggleLike(ctx, alice.ID, p1.ID)
	if err != nil || liked || n != 0 {
		t.Fatalf("unlike: %v %v %d", err, liked, n)
	}

	c, err := s.CreateComment(ctx, p1.ID, alice.ID, "Nice post")
	if err != nil {
		t.Fatalf("comment: %v", err)
	}
	comments, err := s.CommentsForPost(ctx, p1.ID)
	if err != nil || len(comments) != 1 || comments[0].Author == nil {
		t.Fatalf("comments: %v %+v", err, comments)
	}

	in.Tags = []string{"Go"}
	if err := s.UpdatePost(ctx, got, in); err != nil {
		t.Fatalf("update: %v", err)
	}
	tags, err := s.PopularTags(ctx, 10)
	if err != nil || len(tags) != 1 || tags[0].PostCount != 1 {
		t.Fatalf("popular tags (unused tags should be removed): %v %+v", err, tags)
	}

	if err := s.DeletePost(ctx, p1.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.CommentByID(ctx, c.ID); err != store.ErrNotFound {
		t.Fatalf("comment should cascade-delete, got %v", err)
	}

	stats, err := s.SiteStats(ctx)
	if err != nil || stats.Users != 1 || stats.Drafts != 1 {
		t.Fatalf("stats: %v %+v", err, stats)
	}
}

func TestSessions(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	u, err := s.UpsertOAuthUser(ctx, store.OAuthProfile{Subject: "g-2", Email: "bob@example.com"}, false)
	if err != nil {
		t.Fatal(err)
	}
	token, sess, err := s.CreateSession(ctx, u.ID, "test-agent", "127.0.0.1", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if sess.ID == token {
		t.Fatal("session id must be a hash, not the raw token")
	}
	got, err := s.SessionByToken(ctx, token)
	if err != nil || got.User == nil || got.User.ID != u.ID {
		t.Fatalf("lookup: %v %+v", err, got)
	}
	if _, err := s.SessionByToken(ctx, "bogus"); err != store.ErrNotFound {
		t.Fatalf("bogus token: %v", err)
	}
	if err := s.DeleteSession(ctx, sess.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SessionByToken(ctx, token); err != store.ErrNotFound {
		t.Fatalf("deleted session still valid: %v", err)
	}
}

func TestLiveStreamStore(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	u, err := s.UpsertOAuthUser(ctx, store.OAuthProfile{Subject: "g-live", Email: "live@example.com", Name: "Live Person"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.LiveStreamByUser(ctx, u.ID); err != store.ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	ls := &models.LiveStream{UserID: u.ID, GcoreStreamID: 42, PushURL: "rtmp://x"}
	if created, err := s.CreateLiveStream(ctx, ls); err != nil || !created {
		t.Fatalf("first create: %v %v", created, err)
	}
	if created, err := s.CreateLiveStream(ctx, &models.LiveStream{UserID: u.ID, GcoreStreamID: 99}); err != nil || created {
		t.Fatalf("second create must be a no-op: %v %v", created, err)
	}
	got, err := s.LiveStreamByUser(ctx, u.ID)
	if err != nil || got.GcoreStreamID != 42 || got.PushURL != "rtmp://x" || got.Active || got.Live {
		t.Fatalf("get: %v %+v", err, got)
	}

	now := time.Now().UTC().Truncate(time.Second)
	if err := s.ArmLiveStream(ctx, u.ID, "My show", now); err != nil {
		t.Fatal(err)
	}
	if c, err := s.SyncCandidates(ctx); err != nil || len(c) != 1 || !c[0].Active || c[0].Title != "My show" {
		t.Fatalf("candidates: %v %+v", err, c)
	}
	if l, err := s.ListLive(ctx); err != nil || len(l) != 0 {
		t.Fatalf("armed is not live: %v %+v", err, l)
	}

	got, _ = s.LiveStreamByUser(ctx, u.ID)
	got.Apply(true, true, now.Add(time.Minute))
	if err := s.SaveLiveStatus(ctx, got); err != nil {
		t.Fatal(err)
	}
	l, err := s.ListLive(ctx)
	if err != nil || len(l) != 1 || l[0].User == nil || l[0].User.Username != "live" {
		t.Fatalf("live: %v %+v", err, l)
	}

	if err := s.DisarmLiveStream(ctx, u.ID, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, _ = s.LiveStreamByUser(ctx, u.ID)
	if got.Active || got.Live || got.EndedAt.IsZero() {
		t.Fatalf("disarm should end a live stream: %+v", got)
	}
	if l, err := s.ListLive(ctx); err != nil || len(l) != 0 {
		t.Fatalf("after disarm: %v %+v", err, l)
	}

	if err := s.DeleteUser(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LiveStreamByUser(ctx, u.ID); err != store.ErrNotFound {
		t.Fatalf("cascade: want ErrNotFound, got %v", err)
	}
}
