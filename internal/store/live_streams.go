package store

import (
	"context"
	"time"

	"github.com/harshadptl/gcore/internal/models"
)

// LiveStreamByUser returns the user's stream or ErrNotFound.
func (s *Store) LiveStreamByUser(ctx context.Context, userID int64) (*models.LiveStream, error) {
	ls := new(models.LiveStream)
	if err := s.DB.NewSelect().Model(ls).Where("ls.user_id = ?", userID).Scan(ctx); err != nil {
		return nil, notFound(err)
	}
	return ls, nil
}

// CreateLiveStream inserts ls unless the user already has one; created
// reports whether a row was inserted.
func (s *Store) CreateLiveStream(ctx context.Context, ls *models.LiveStream) (created bool, err error) {
	res, err := s.DB.NewInsert().Model(ls).On("CONFLICT (user_id) DO NOTHING").Exec(ctx)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// ReplaceLiveStreamGcore points a user's row at a new Gcore stream (ls.UserID
// selects the row; its Gcore id and URLs are written).
func (s *Store) ReplaceLiveStreamGcore(ctx context.Context, ls *models.LiveStream) error {
	_, err := s.DB.NewUpdate().Model(ls).
		Column("gcore_stream_id", "push_url", "push_url_srt", "push_url_whip", "iframe_url").
		WherePK().Exec(ctx)
	return err
}

// ArmLiveStream marks the stream as wanted-on.
func (s *Store) ArmLiveStream(ctx context.Context, userID int64, title string, now time.Time) error {
	_, err := s.DB.NewUpdate().Model((*models.LiveStream)(nil)).
		Set("active = ?", true).Set("armed_at = ?", now).Set("title = ?", title).
		Set("updated_at = ?", now).
		Where("user_id = ?", userID).Exec(ctx)
	return err
}

// DisarmLiveStream turns the stream off, stamping ended_at if it was live.
func (s *Store) DisarmLiveStream(ctx context.Context, userID int64, now time.Time) error {
	_, err := s.DB.NewUpdate().Model((*models.LiveStream)(nil)).
		Set("active = ?", false).Set("live = ?", false).
		Set("ended_at = CASE WHEN live THEN ? ELSE ended_at END", now).
		Set("updated_at = ?", now).
		Where("user_id = ?", userID).Exec(ctx)
	return err
}

// SyncCandidates returns streams the syncer must reconcile with Gcore.
func (s *Store) SyncCandidates(ctx context.Context) ([]models.LiveStream, error) {
	var out []models.LiveStream
	err := s.DB.NewSelect().Model(&out).Where("active OR live").Scan(ctx)
	return out, err
}

// SaveLiveStatus persists the status columns after Apply.
func (s *Store) SaveLiveStatus(ctx context.Context, ls *models.LiveStream) error {
	_, err := s.DB.NewUpdate().Model(ls).
		Column("active", "live", "armed_at", "started_at", "ended_at", "synced_at", "updated_at").
		WherePK().Exec(ctx)
	return err
}

// ListLive returns currently live streams with their owners, newest first.
func (s *Store) ListLive(ctx context.Context) ([]models.LiveStream, error) {
	var out []models.LiveStream
	err := s.DB.NewSelect().Model(&out).Relation("User").
		Where("ls.live").OrderExpr("ls.started_at DESC").Scan(ctx)
	return out, err
}
