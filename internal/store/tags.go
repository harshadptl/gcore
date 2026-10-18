package store

import (
	"context"

	"github.com/harshadptl/gcore/internal/models"
)

const publishedPostCountExpr = `(SELECT COUNT(*) FROM post_tags pt
	JOIN posts ON posts.id = pt.post_id
	WHERE pt.tag_id = t.id AND posts.status = 'published') AS post_count`

// TagBySlug returns a single tag.
func (s *Store) TagBySlug(ctx context.Context, slug string) (*models.Tag, error) {
	var t models.Tag
	err := s.DB.NewSelect().Model(&t).ColumnExpr("t.*").ColumnExpr(publishedPostCountExpr).
		Where("t.slug = ?", slug).Scan(ctx)
	if err != nil {
		return nil, notFound(err)
	}
	return &t, nil
}

// PopularTags returns tags ordered by number of published posts. limit <= 0
// returns all tags that have at least one published post.
func (s *Store) PopularTags(ctx context.Context, limit int) ([]models.Tag, error) {
	var tags []models.Tag
	q := s.DB.NewSelect().Model(&tags).
		ColumnExpr("t.*").
		ColumnExpr(publishedPostCountExpr).
		Where(`EXISTS (SELECT 1 FROM post_tags pt3 JOIN posts p3 ON p3.id = pt3.post_id
			WHERE pt3.tag_id = t.id AND p3.status = 'published')`).
		OrderExpr("post_count DESC, t.name ASC")
	if limit > 0 {
		q = q.Limit(int64(limit))
	}
	err := q.Scan(ctx)
	return tags, err
}
