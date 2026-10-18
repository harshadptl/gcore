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

// OAuthProfile is the identity returned by an OAuth provider.
type OAuthProfile struct {
	Subject   string // stable provider user ID ("sub")
	Email     string
	Name      string
	AvatarURL string
}

// UpsertOAuthUser finds the user for profile (by provider ID, then by email)
// or creates one. The avatar is refreshed on every login; the display name is
// only taken from the provider on sign-up (users can change it in settings).
// When makeAdmin is true the user is promoted to admin.
func (s *Store) UpsertOAuthUser(ctx context.Context, p OAuthProfile, makeAdmin bool) (*models.User, error) {
	email := strings.ToLower(strings.TrimSpace(p.Email))
	if p.Subject == "" || email == "" {
		return nil, fmt.Errorf("profile is missing subject or email")
	}

	var user models.User
	err := s.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		err := tx.NewSelect().Model(&user).
			Where("google_id = ?", p.Subject).
			WhereOr("email = ?", email).
			OrderExpr("CASE WHEN google_id = ? THEN 0 ELSE 1 END", p.Subject).
			Limit(1).
			Scan(ctx)
		now := time.Now().UTC()

		if err == nil { // existing user: refresh profile data
			user.GoogleID = p.Subject
			user.Email = email
			if user.Name == "" && p.Name != "" {
				user.Name = p.Name
			}
			if p.AvatarURL != "" {
				user.AvatarURL = p.AvatarURL
			}
			if makeAdmin {
				user.Role = models.RoleAdmin
			}
			user.LastLoginAt = now
			_, err = tx.NewUpdate().Model(&user).
				Column("google_id", "email", "name", "avatar_url", "role", "last_login_at", "updated_at").
				WherePK().Exec(ctx)
			return err
		}
		if notFound(err) != ErrNotFound {
			return err
		}

		username, err := uniqueUsername(ctx, tx, email)
		if err != nil {
			return err
		}
		name := p.Name
		if name == "" {
			name = username
		}
		user = models.User{
			GoogleID:    p.Subject,
			Email:       email,
			Name:        name,
			Username:    username,
			AvatarURL:   p.AvatarURL,
			Role:        models.RoleUser,
			LastLoginAt: now,
		}
		if makeAdmin {
			user.Role = models.RoleAdmin
		}
		_, err = tx.NewInsert().Model(&user).Returning("*").Exec(ctx)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func uniqueUsername(ctx context.Context, db bun.IDB, email string) (string, error) {
	base := textutil.Slugify(strings.SplitN(email, "@", 2)[0])
	if base == "" {
		base = "user"
	}
	if len(base) > 24 {
		base = base[:24]
	}
	candidate := base
	for i := 2; i < 1000; i++ {
		exists, err := db.NewSelect().Model((*models.User)(nil)).Where("username = ?", candidate).Exists(ctx)
		if err != nil {
			return "", err
		}
		if !exists && !reservedUsernames[candidate] {
			return candidate, nil
		}
		candidate = fmt.Sprintf("%s%d", base, i)
	}
	return "", fmt.Errorf("could not find a free username for %q", base)
}

var reservedUsernames = map[string]bool{
	"admin": true, "root": true, "api": true, "settings": true, "dashboard": true,
}

// UserByID returns the user with id.
func (s *Store) UserByID(ctx context.Context, id int64) (*models.User, error) {
	var u models.User
	if err := s.DB.NewSelect().Model(&u).Where("u.id = ?", id).Scan(ctx); err != nil {
		return nil, notFound(err)
	}
	return &u, nil
}

// UserByUsername returns the user with username.
func (s *Store) UserByUsername(ctx context.Context, username string) (*models.User, error) {
	var u models.User
	if err := s.DB.NewSelect().Model(&u).Where("u.username = ?", strings.ToLower(username)).Scan(ctx); err != nil {
		return nil, notFound(err)
	}
	return &u, nil
}

// UsernameTaken reports whether username belongs to someone other than exceptID.
func (s *Store) UsernameTaken(ctx context.Context, username string, exceptID int64) (bool, error) {
	if reservedUsernames[username] {
		return true, nil
	}
	return s.DB.NewSelect().Model((*models.User)(nil)).
		Where("username = ?", username).Where("id <> ?", exceptID).Exists(ctx)
}

// UpdateProfile saves the editable profile fields.
func (s *Store) UpdateProfile(ctx context.Context, u *models.User) error {
	_, err := s.DB.NewUpdate().Model(u).Column("name", "username", "bio", "updated_at").WherePK().Exec(ctx)
	return err
}

// SetRole changes a user's role.
func (s *Store) SetRole(ctx context.Context, userID int64, role string) error {
	_, err := s.DB.NewUpdate().Model((*models.User)(nil)).
		Set("role = ?", role).Set("updated_at = ?", time.Now().UTC()).
		Where("id = ?", userID).Exec(ctx)
	return err
}

// DeleteUser removes a user and (via ON DELETE CASCADE) everything they own.
func (s *Store) DeleteUser(ctx context.Context, userID int64) error {
	_, err := s.DB.NewDelete().Model((*models.User)(nil)).Where("id = ?", userID).Exec(ctx)
	return err
}

// ListUsers returns users with their post counts, newest first.
func (s *Store) ListUsers(ctx context.Context, page Page) ([]models.User, Page, error) {
	var users []models.User
	total, err := s.DB.NewSelect().Model(&users).
		ColumnExpr("u.*").
		ColumnExpr("(SELECT COUNT(*) FROM posts WHERE posts.author_id = u.id) AS post_count").
		Order("u.created_at DESC").
		Limit(page.Limit()).Offset(page.Offset()).
		ScanAndCount(ctx)
	page.Total = int(total)
	return users, page, err
}
