package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"time"

	"github.com/harshadptl/gcore/internal/models"
)

// NewToken returns a random URL-safe token with 256 bits of entropy.
func NewToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b) // crypto/rand.Read never returns an error
	return base64.RawURLEncoding.EncodeToString(b)
}

// HashToken returns the storage key for a session token.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// CreateSession starts a login session and returns the raw token for the
// cookie. Only its hash is stored.
func (s *Store) CreateSession(ctx context.Context, userID int64, userAgent, ip string, ttl time.Duration) (string, *models.Session, error) {
	token := NewToken()
	now := time.Now().UTC()
	if len(userAgent) > 255 {
		userAgent = userAgent[:255]
	}
	sess := &models.Session{
		ID:         HashToken(token),
		UserID:     userID,
		CSRFToken:  NewToken(),
		UserAgent:  userAgent,
		IP:         ip,
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  now.Add(ttl),
	}
	_, err := s.DB.NewInsert().Model(sess).Exec(ctx)
	return token, sess, err
}

// SessionByToken returns a non-expired session (with its user) for token.
func (s *Store) SessionByToken(ctx context.Context, token string) (*models.Session, error) {
	var sess models.Session
	err := s.DB.NewSelect().Model(&sess).
		Relation("User").
		Where("s.id = ?", HashToken(token)).
		Where("s.expires_at > ?", time.Now().UTC()).
		Scan(ctx)
	if err != nil {
		return nil, notFound(err)
	}
	return &sess, nil
}

// TouchSession slides the session's expiry forward.
func (s *Store) TouchSession(ctx context.Context, sess *models.Session, ttl time.Duration) error {
	now := time.Now().UTC()
	sess.LastSeenAt = now
	sess.ExpiresAt = now.Add(ttl)
	_, err := s.DB.NewUpdate().Model(sess).Column("last_seen_at", "expires_at").WherePK().Exec(ctx)
	return err
}

// DeleteSession ends a single session.
func (s *Store) DeleteSession(ctx context.Context, id string) error {
	_, err := s.DB.NewDelete().Model((*models.Session)(nil)).Where("id = ?", id).Exec(ctx)
	return err
}

// DeleteOtherSessions signs a user out everywhere except keepID.
func (s *Store) DeleteOtherSessions(ctx context.Context, userID int64, keepID string) (int64, error) {
	res, err := s.DB.NewDelete().Model((*models.Session)(nil)).
		Where("user_id = ?", userID).Where("id <> ?", keepID).Exec(ctx)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// SessionsForUser lists a user's active sessions, most recent first.
func (s *Store) SessionsForUser(ctx context.Context, userID int64) ([]models.Session, error) {
	var sessions []models.Session
	err := s.DB.NewSelect().Model(&sessions).
		Where("s.user_id = ?", userID).
		Where("s.expires_at > ?", time.Now().UTC()).
		Order("s.last_seen_at DESC").
		Scan(ctx)
	return sessions, err
}

// DeleteExpiredSessions removes stale sessions; call it periodically.
func (s *Store) DeleteExpiredSessions(ctx context.Context) (int64, error) {
	res, err := s.DB.NewDelete().Model((*models.Session)(nil)).
		Where("expires_at <= ?", time.Now().UTC()).Exec(ctx)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
