package store

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/harshadptl/gcore/internal/models"
)

// CommentsForPost returns a post's comments, oldest first.
func (s *Store) CommentsForPost(ctx context.Context, postID int64) ([]models.Comment, error) {
	var comments []models.Comment
	err := s.DB.NewSelect().Model(&comments).
		Relation("Author").
		Where("c.post_id = ?", postID).
		Order("c.created_at ASC", "c.id ASC").
		Scan(ctx)
	return comments, err
}

// CreateComment adds a comment.
func (s *Store) CreateComment(ctx context.Context, postID, authorID int64, body string) (*models.Comment, error) {
	c := &models.Comment{PostID: postID, AuthorID: authorID, Body: body}
	_, err := s.DB.NewInsert().Model(c).Returning("*").Exec(ctx)
	return c, err
}

// CommentByID returns a comment with its post loaded.
func (s *Store) CommentByID(ctx context.Context, id int64) (*models.Comment, error) {
	var c models.Comment
	err := s.DB.NewSelect().Model(&c).Relation("Post").Where("c.id = ?", id).Scan(ctx)
	if err != nil {
		return nil, notFound(err)
	}
	return &c, nil
}

// DeleteComment removes a comment.
func (s *Store) DeleteComment(ctx context.Context, id int64) error {
	_, err := s.DB.NewDelete().Model((*models.Comment)(nil)).Where("id = ?", id).Exec(ctx)
	return err
}

// RecentComments returns the newest comments site-wide (for moderation).
func (s *Store) RecentComments(ctx context.Context, limit int) ([]models.Comment, error) {
	var comments []models.Comment
	err := s.DB.NewSelect().Model(&comments).
		Relation("Author").
		Relation("Post", func(q *bun.SelectQuery) *bun.SelectQuery {
			return q.Column("id", "title", "slug")
		}).
		Order("c.created_at DESC").
		Limit(int64(limit)).
		Scan(ctx)
	return comments, err
}
