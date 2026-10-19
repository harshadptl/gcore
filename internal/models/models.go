// Package models defines the Bun models that map to database tables.
//
// The schema itself is created by migrations (internal/database/migrations),
// not from these structs, so it's safe to evolve models alongside new
// migrations.
package models

import (
	"context"
	"strings"
	"time"

	"github.com/uptrace/bun"
)

// Role values for User.Role.
const (
	RoleUser  = "user"
	RoleAdmin = "admin"
)

// Status values for Post.Status.
const (
	StatusDraft     = "draft"
	StatusPublished = "published"
)

// timestamps is embedded by models that track creation/update times.
type timestamps struct {
	CreatedAt time.Time `bun:",nullzero,notnull,default:current_timestamp"`
	UpdatedAt time.Time `bun:",nullzero,notnull,default:current_timestamp"`
}

func (t *timestamps) touch(query bun.Query) {
	now := time.Now().UTC()
	switch query.(type) {
	case *bun.InsertQuery:
		if t.CreatedAt.IsZero() {
			t.CreatedAt = now
		}
		t.UpdatedAt = now
	case *bun.UpdateQuery:
		t.UpdatedAt = now
	}
}

// User is a person who signed in with Google.
type User struct {
	bun.BaseModel `bun:"table:users,alias:u"`

	ID          int64     `bun:",pk,autoincrement"`
	GoogleID    string    `bun:",unique,notnull"`
	Email       string    `bun:",unique,notnull"`
	Name        string    `bun:",notnull"`
	Username    string    `bun:",unique,notnull"`
	AvatarURL   string    `bun:",notnull"`
	Bio         string    `bun:",notnull"`
	Role        string    `bun:",notnull"`
	LastLoginAt time.Time `bun:",nullzero"`
	timestamps

	// Aggregates filled by some queries.
	PostCount int `bun:"post_count,scanonly"`
}

var _ bun.BeforeAppendModelHook = (*User)(nil)

// BeforeAppendModel keeps timestamps current.
func (u *User) BeforeAppendModel(_ context.Context, q bun.Query) error {
	u.touch(q)
	return nil
}

// IsAdmin reports whether the user has the admin role.
func (u *User) IsAdmin() bool { return u != nil && u.Role == RoleAdmin }

// Initials returns up to two initials for avatar placeholders.
func (u *User) Initials() string {
	if u == nil {
		return "?"
	}
	var out []rune
	prevSpace := true
	for _, r := range u.Name {
		if r == ' ' {
			prevSpace = true
			continue
		}
		if prevSpace {
			out = append(out, r)
			if len(out) == 2 {
				break
			}
		}
		prevSpace = false
	}
	if len(out) == 0 {
		return "?"
	}
	return strings.ToUpper(string(out))
}

// Post is a blog article written in Markdown.
type Post struct {
	bun.BaseModel `bun:"table:posts,alias:p"`

	ID             int64     `bun:",pk,autoincrement"`
	AuthorID       int64     `bun:",notnull"`
	Author         *User     `bun:"rel:belongs-to,join:author_id=id"`
	Title          string    `bun:",notnull"`
	Slug           string    `bun:",unique,notnull"`
	Excerpt        string    `bun:",notnull"`
	Content        string    `bun:",notnull"`             // Markdown source
	ContentHTML    string    `bun:"content_html,notnull"` // sanitized HTML, rendered on save
	CoverImage     string    `bun:",notnull"`
	Status         string    `bun:",notnull"`
	ReadingMinutes int       `bun:",notnull"`
	Views          int64     `bun:",notnull"`
	PublishedAt    time.Time `bun:",nullzero"`
	timestamps

	Tags []Tag `bun:"m2m:post_tags,join:Post=Tag"`

	// Aggregates filled by list/detail queries.
	LikeCount    int `bun:"like_count,scanonly"`
	CommentCount int `bun:"comment_count,scanonly"`
}

var _ bun.BeforeAppendModelHook = (*Post)(nil)

// BeforeAppendModel keeps timestamps current.
func (p *Post) BeforeAppendModel(_ context.Context, q bun.Query) error {
	p.touch(q)
	return nil
}

// IsPublished reports whether the post is publicly visible.
func (p *Post) IsPublished() bool { return p.Status == StatusPublished }

// DisplayDate is the date shown to readers.
func (p *Post) DisplayDate() time.Time {
	if !p.PublishedAt.IsZero() {
		return p.PublishedAt
	}
	return p.CreatedAt
}

// TagNames returns the post's tag names.
func (p *Post) TagNames() []string {
	names := make([]string, 0, len(p.Tags))
	for _, t := range p.Tags {
		names = append(names, t.Name)
	}
	return names
}

// Tag groups posts by topic.
type Tag struct {
	bun.BaseModel `bun:"table:tags,alias:t"`

	ID        int64     `bun:",pk,autoincrement"`
	Name      string    `bun:",notnull"`
	Slug      string    `bun:",unique,notnull"`
	CreatedAt time.Time `bun:",nullzero,notnull,default:current_timestamp"`

	PostCount int `bun:"post_count,scanonly"`
}

// PostTag is the join table for the posts <-> tags many-to-many relation.
type PostTag struct {
	bun.BaseModel `bun:"table:post_tags,alias:pt"`

	PostID int64 `bun:",pk"`
	Post   *Post `bun:"rel:belongs-to,join:post_id=id"`
	TagID  int64 `bun:",pk"`
	Tag    *Tag  `bun:"rel:belongs-to,join:tag_id=id"`
}

// Comment is a reader's reply on a post.
type Comment struct {
	bun.BaseModel `bun:"table:comments,alias:c"`

	ID       int64  `bun:",pk,autoincrement"`
	PostID   int64  `bun:",notnull"`
	Post     *Post  `bun:"rel:belongs-to,join:post_id=id"`
	AuthorID int64  `bun:",notnull"`
	Author   *User  `bun:"rel:belongs-to,join:author_id=id"`
	Body     string `bun:",notnull"`
	timestamps
}

var _ bun.BeforeAppendModelHook = (*Comment)(nil)

// BeforeAppendModel keeps timestamps current.
func (c *Comment) BeforeAppendModel(_ context.Context, q bun.Query) error {
	c.touch(q)
	return nil
}

// Like records that a user liked a post.
type Like struct {
	bun.BaseModel `bun:"table:likes,alias:l"`

	UserID    int64     `bun:",pk"`
	PostID    int64     `bun:",pk"`
	CreatedAt time.Time `bun:",nullzero,notnull,default:current_timestamp"`
}

// Bookmark records that a user saved a post to read later.
type Bookmark struct {
	bun.BaseModel `bun:"table:bookmarks,alias:b"`

	UserID    int64     `bun:",pk"`
	PostID    int64     `bun:",pk"`
	CreatedAt time.Time `bun:",nullzero,notnull,default:current_timestamp"`
}

// Session is a server-side login session. ID is the SHA-256 hash of the
// token stored in the user's cookie, so a database leak does not leak
// usable session tokens.
type Session struct {
	bun.BaseModel `bun:"table:sessions,alias:s"`

	ID         string    `bun:",pk"`
	UserID     int64     `bun:",notnull"`
	User       *User     `bun:"rel:belongs-to,join:user_id=id"`
	CSRFToken  string    `bun:"csrf_token,notnull"`
	UserAgent  string    `bun:",notnull"`
	IP         string    `bun:"ip,notnull"`
	CreatedAt  time.Time `bun:",nullzero,notnull,default:current_timestamp"`
	LastSeenAt time.Time `bun:",nullzero,notnull,default:current_timestamp"`
	ExpiresAt  time.Time `bun:",notnull"`
}

// All returns every model, for registration with bun.DB.
// AbandonAfter is how long an armed, non-live stream may sit idle before the
// syncer disarms it.
const AbandonAfter = 10 * time.Minute

const (
	StreamLive  = "live"
	StreamArmed = "armed"
	StreamOff   = "off"
)

type LiveStream struct {
	bun.BaseModel `bun:"table:live_streams,alias:ls"`

	UserID        int64     `bun:",pk"`
	User          *User     `bun:"rel:belongs-to,join:user_id=id"`
	GcoreStreamID int64     `bun:"gcore_stream_id,notnull"`
	Title         string    `bun:",notnull"`
	PushURL       string    `bun:"push_url,notnull"`
	PushURLSRT    string    `bun:"push_url_srt,notnull"`
	PushURLWHIP   string    `bun:"push_url_whip,notnull"`
	IframeURL     string    `bun:"iframe_url,notnull"`
	Active        bool      `bun:",notnull"`
	Live          bool      `bun:",notnull"`
	ArmedAt       time.Time `bun:",nullzero"`
	StartedAt     time.Time `bun:",nullzero"`
	EndedAt       time.Time `bun:",nullzero"`
	SyncedAt      time.Time `bun:",nullzero,notnull,default:current_timestamp"`
	timestamps
}

var _ bun.BeforeAppendModelHook = (*LiveStream)(nil)

func (s *LiveStream) BeforeAppendModel(_ context.Context, q bun.Query) error {
	s.touch(q)
	return nil
}

// Apply records the observed Gcore state, stamping the transition times.
func (s *LiveStream) Apply(active, live bool, now time.Time) {
	if live && !s.Live {
		s.StartedAt = now
	}
	if !live && s.Live {
		s.EndedAt = now
	}
	if active && !s.Active {
		s.ArmedAt = now
	}
	s.Active, s.Live, s.SyncedAt = active, live, now
}

// Abandoned reports whether an armed stream never went live (or stopped being
// live) for longer than AbandonAfter.
func (s *LiveStream) Abandoned(now time.Time) bool {
	since := s.ArmedAt
	if s.EndedAt.After(since) {
		since = s.EndedAt
	}
	return s.Active && !s.Live && now.Sub(since) > AbandonAfter
}

func (s *LiveStream) State() string {
	switch {
	case s.Live:
		return StreamLive
	case s.Active:
		return StreamArmed
	}
	return StreamOff
}

func All() []any {
	return []any{
		(*PostTag)(nil), // m2m join models must be registered first
		(*User)(nil),
		(*Post)(nil),
		(*Tag)(nil),
		(*Comment)(nil),
		(*Like)(nil),
		(*Bookmark)(nil),
		(*Session)(nil),
		(*LiveStream)(nil),
	}
}
