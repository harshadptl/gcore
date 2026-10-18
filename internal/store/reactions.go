package store

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/harshadptl/gcore/internal/models"
)

// ToggleLike likes or unlikes a post and returns the new state and count.
func (s *Store) ToggleLike(ctx context.Context, userID, postID int64) (liked bool, count int, err error) {
	return s.toggle(ctx, &models.Like{UserID: userID, PostID: postID}, (*models.Like)(nil), userID, postID)
}

// ToggleBookmark saves or un-saves a post and returns the new state and count.
func (s *Store) ToggleBookmark(ctx context.Context, userID, postID int64) (saved bool, count int, err error) {
	return s.toggle(ctx, &models.Bookmark{UserID: userID, PostID: postID}, (*models.Bookmark)(nil), userID, postID)
}

func (s *Store) toggle(ctx context.Context, row, model any, userID, postID int64) (on bool, count int, err error) {
	err = s.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		res, err := tx.NewDelete().Model(model).
			Where("user_id = ?", userID).Where("post_id = ?", postID).Exec(ctx)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			if _, err := tx.NewInsert().Model(row).On("CONFLICT DO NOTHING").Returning("NULL").Exec(ctx); err != nil {
				return err
			}
			on = true
		}
		n, err := tx.NewSelect().Model(model).Where("post_id = ?", postID).Count(ctx)
		count = int(n)
		return err
	})
	return on, count, err
}

// Reactions reports whether userID liked and/or bookmarked postID.
func (s *Store) Reactions(ctx context.Context, userID, postID int64) (liked, saved bool, err error) {
	liked, err = s.DB.NewSelect().Model((*models.Like)(nil)).
		Where("user_id = ?", userID).Where("post_id = ?", postID).Exists(ctx)
	if err != nil {
		return
	}
	saved, err = s.DB.NewSelect().Model((*models.Bookmark)(nil)).
		Where("user_id = ?", userID).Where("post_id = ?", postID).Exists(ctx)
	return
}
