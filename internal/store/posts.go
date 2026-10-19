package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/uptrace/bun"

	"github.com/harshadptl/gcore/internal/models"
	"github.com/harshadptl/gcore/internal/textutil"
)

const (
	likeCountExpr    = "(SELECT COUNT(*) FROM gcore_likes likes WHERE likes.post_id = p.id) AS like_count"
	commentCountExpr = "(SELECT COUNT(*) FROM gcore_comments comments WHERE comments.post_id = p.id) AS comment_count"
)

// PostFilter narrows ListPosts.
type PostFilter struct {
	Query        string // full-text-ish search on title, excerpt and content
	TagSlug      string
	AuthorID     int64
	Status       string // "" = any status
	BookmarkedBy int64  // only posts bookmarked by this user
	ExcludeID    int64
}

// ListPosts returns posts matching f with author, tags and counts loaded.
// Published posts are sorted by publish date, others by last update.
func (s *Store) ListPosts(ctx context.Context, f PostFilter, page Page) ([]models.Post, Page, error) {
	var posts []models.Post
	q := s.DB.NewSelect().Model(&posts).
		ColumnExpr("p.*").
		ColumnExpr(likeCountExpr).
		ColumnExpr(commentCountExpr).
		Relation("Author").
		Relation("Tags", func(q *bun.SelectQuery) *bun.SelectQuery { return q.Order("t.name ASC") })

	if f.Status != "" {
		q = q.Where("p.status = ?", f.Status)
	}
	if f.AuthorID != 0 {
		q = q.Where("p.author_id = ?", f.AuthorID)
	}
	if f.ExcludeID != 0 {
		q = q.Where("p.id <> ?", f.ExcludeID)
	}
	if f.TagSlug != "" {
		q = q.Where(`EXISTS (SELECT 1 FROM gcore_post_tags pt2 JOIN gcore_tags t2 ON t2.id = pt2.tag_id
			WHERE pt2.post_id = p.id AND t2.slug = ?)`, f.TagSlug)
	}
	if f.BookmarkedBy != 0 {
		q = q.Where("EXISTS (SELECT 1 FROM gcore_bookmarks bm WHERE bm.post_id = p.id AND bm.user_id = ?)", f.BookmarkedBy)
	}
	if term := strings.TrimSpace(f.Query); term != "" {
		pattern := likePattern(term)
		q = q.WhereGroup(" AND ", func(q *bun.SelectQuery) *bun.SelectQuery {
			return q.Where(`LOWER(p.title) LIKE ? ESCAPE '\'`, pattern).
				WhereOr(`LOWER(p.excerpt) LIKE ? ESCAPE '\'`, pattern).
				WhereOr(`LOWER(p.content) LIKE ? ESCAPE '\'`, pattern)
		})
	}

	if f.Status == models.StatusPublished {
		q = q.Order("p.published_at DESC", "p.id DESC")
	} else {
		q = q.Order("p.updated_at DESC", "p.id DESC")
	}

	total, err := q.Limit(page.Limit()).Offset(page.Offset()).ScanAndCount(ctx)
	page.Total = int(total)
	return posts, page, err
}

// PostBySlug returns the post with slug, including author, tags and counts.
func (s *Store) PostBySlug(ctx context.Context, slug string) (*models.Post, error) {
	var p models.Post
	err := s.DB.NewSelect().Model(&p).
		ColumnExpr("p.*").
		ColumnExpr(likeCountExpr).
		ColumnExpr(commentCountExpr).
		Relation("Author").
		Relation("Tags", func(q *bun.SelectQuery) *bun.SelectQuery { return q.Order("t.name ASC") }).
		Where("p.slug = ?", slug).
		Scan(ctx)
	if err != nil {
		return nil, notFound(err)
	}
	return &p, nil
}

// PostInput is the editable part of a post.
type PostInput struct {
	Title          string
	Slug           string // optional; generated from the title when empty
	Excerpt        string
	Content        string
	ContentHTML    string
	CoverImage     string
	Status         string
	ReadingMinutes int
	Tags           []string
}

// CreatePost inserts a post owned by authorID and attaches its tags.
func (s *Store) CreatePost(ctx context.Context, authorID int64, in PostInput) (*models.Post, error) {
	post := &models.Post{AuthorID: authorID}
	err := s.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := applyInput(ctx, tx, post, in); err != nil {
			return err
		}
		if _, err := tx.NewInsert().Model(post).Returning("*").Exec(ctx); err != nil {
			return err
		}
		return setPostTags(ctx, tx, post.ID, in.Tags)
	})
	return post, err
}

// UpdatePost saves changes to post and replaces its tags.
func (s *Store) UpdatePost(ctx context.Context, post *models.Post, in PostInput) error {
	return s.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := applyInput(ctx, tx, post, in); err != nil {
			return err
		}
		_, err := tx.NewUpdate().Model(post).
			Column("title", "slug", "excerpt", "content", "content_html", "cover_image",
				"status", "reading_minutes", "published_at", "updated_at").
			WherePK().Exec(ctx)
		if err != nil {
			return err
		}
		return setPostTags(ctx, tx, post.ID, in.Tags)
	})
}

func applyInput(ctx context.Context, tx bun.Tx, post *models.Post, in PostInput) error {
	slug := textutil.Slugify(in.Slug)
	if slug == "" {
		if post.Slug != "" {
			slug = post.Slug // keep existing URLs stable when the title changes
		} else {
			slug = textutil.Slugify(in.Title)
		}
	}
	if slug == "" {
		slug = "post"
	}
	unique, err := uniquePostSlug(ctx, tx, slug, post.ID)
	if err != nil {
		return err
	}

	post.Title = in.Title
	post.Slug = unique
	post.Excerpt = in.Excerpt
	post.Content = in.Content
	post.ContentHTML = in.ContentHTML
	post.CoverImage = in.CoverImage
	post.ReadingMinutes = in.ReadingMinutes
	post.Status = in.Status
	if post.Status == models.StatusPublished && post.PublishedAt.IsZero() {
		post.PublishedAt = time.Now().UTC()
	}
	return nil
}

func uniquePostSlug(ctx context.Context, db bun.IDB, base string, exceptID int64) (string, error) {
	candidate := base
	for i := 2; i < 500; i++ {
		exists, err := db.NewSelect().Model((*models.Post)(nil)).
			Where("slug = ?", candidate).Where("id <> ?", exceptID).Exists(ctx)
		if err != nil {
			return "", err
		}
		if !exists && candidate != "new" {
			return candidate, nil
		}
		candidate = fmt.Sprintf("%s-%d", base, i)
	}
	return "", fmt.Errorf("could not find a free slug for %q", base)
}

func setPostTags(ctx context.Context, tx bun.Tx, postID int64, names []string) error {
	if _, err := tx.NewDelete().Model((*models.PostTag)(nil)).Where("post_id = ?", postID).Exec(ctx); err != nil {
		return err
	}
	for _, name := range names {
		slug := textutil.Slugify(name)
		if slug == "" {
			continue
		}
		tag := &models.Tag{Name: name, Slug: slug}
		if _, err := tx.NewInsert().Model(tag).On("CONFLICT (slug) DO NOTHING").Returning("NULL").Exec(ctx); err != nil {
			return err
		}
		if err := tx.NewSelect().Model(tag).Where("slug = ?", slug).Scan(ctx); err != nil {
			return err
		}
		if _, err := tx.NewInsert().Model(&models.PostTag{PostID: postID, TagID: tag.ID}).
			On("CONFLICT DO NOTHING").Returning("NULL").Exec(ctx); err != nil {
			return err
		}
	}
	// Garbage-collect tags no longer used by any post.
	_, err := tx.NewDelete().Model((*models.Tag)(nil)).
		Where("NOT EXISTS (SELECT 1 FROM gcore_post_tags WHERE gcore_post_tags.tag_id = t.id)").Exec(ctx)
	return err
}

// DeletePost removes a post (comments, likes, tags links cascade).
func (s *Store) DeletePost(ctx context.Context, id int64) error {
	return s.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewDelete().Model((*models.Post)(nil)).Where("id = ?", id).Exec(ctx); err != nil {
			return err
		}
		_, err := tx.NewDelete().Model((*models.Tag)(nil)).
			Where("NOT EXISTS (SELECT 1 FROM gcore_post_tags WHERE gcore_post_tags.tag_id = t.id)").Exec(ctx)
		return err
	})
}

// IncrementViews bumps a post's view counter.
func (s *Store) IncrementViews(ctx context.Context, id int64) error {
	_, err := s.DB.NewUpdate().Model((*models.Post)(nil)).
		Set("views = views + 1").Where("id = ?", id).Exec(ctx)
	return err
}

// AllPublishedForFeed returns the newest published posts (no pagination
// count), used by RSS and sitemap.
func (s *Store) AllPublishedForFeed(ctx context.Context, limit int) ([]models.Post, error) {
	var posts []models.Post
	err := s.DB.NewSelect().Model(&posts).
		Relation("Author").
		Where("p.status = ?", models.StatusPublished).
		Order("p.published_at DESC").
		Limit(int64(limit)).
		Scan(ctx)
	return posts, err
}
