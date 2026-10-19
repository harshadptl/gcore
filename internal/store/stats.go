package store

import (
	"context"

	"github.com/harshadptl/gcore/internal/models"
)

// SiteStats are headline numbers for the admin dashboard.
type SiteStats struct {
	Users      int
	Published  int
	Drafts     int
	Comments   int
	Likes      int
	TotalViews int64
}

// SiteStats counts everything in one round trip.
func (s *Store) SiteStats(ctx context.Context) (SiteStats, error) {
	var st SiteStats
	err := s.DB.NewSelect().
		ColumnExpr("(SELECT COUNT(*) FROM gcore_users) AS users").
		ColumnExpr("(SELECT COUNT(*) FROM gcore_posts WHERE status = ?) AS published", models.StatusPublished).
		ColumnExpr("(SELECT COUNT(*) FROM gcore_posts WHERE status = ?) AS drafts", models.StatusDraft).
		ColumnExpr("(SELECT COUNT(*) FROM gcore_comments) AS comments").
		ColumnExpr("(SELECT COUNT(*) FROM gcore_likes) AS likes").
		ColumnExpr("(SELECT COALESCE(SUM(views), 0) FROM gcore_posts) AS total_views").
		Scan(ctx, &st.Users, &st.Published, &st.Drafts, &st.Comments, &st.Likes, &st.TotalViews)
	return st, err
}

// AuthorStats are numbers shown on a writer's dashboard and profile.
type AuthorStats struct {
	Published int
	Drafts    int
	Views     int64
	Likes     int
	Comments  int
}

// AuthorStats aggregates numbers for one author.
func (s *Store) AuthorStats(ctx context.Context, authorID int64) (AuthorStats, error) {
	var st AuthorStats
	err := s.DB.NewSelect().
		ColumnExpr("(SELECT COUNT(*) FROM gcore_posts WHERE author_id = ? AND status = ?)", authorID, models.StatusPublished).
		ColumnExpr("(SELECT COUNT(*) FROM gcore_posts WHERE author_id = ? AND status = ?)", authorID, models.StatusDraft).
		ColumnExpr("(SELECT COALESCE(SUM(views), 0) FROM gcore_posts WHERE author_id = ?)", authorID).
		ColumnExpr("(SELECT COUNT(*) FROM gcore_likes JOIN gcore_posts posts ON posts.id = gcore_likes.post_id WHERE posts.author_id = ?)", authorID).
		ColumnExpr("(SELECT COUNT(*) FROM gcore_comments JOIN gcore_posts posts ON posts.id = gcore_comments.post_id WHERE posts.author_id = ?)", authorID).
		Scan(ctx, &st.Published, &st.Drafts, &st.Views, &st.Likes, &st.Comments)
	return st, err
}
