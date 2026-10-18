// Package view contains templ components and the data types they render.
package view

import (
	"net/url"
	"strconv"

	"github.com/harshadptl/gcore/internal/models"
	"github.com/harshadptl/gcore/internal/store"
)

// Flash is a one-time notification shown after a redirect.
type Flash struct {
	Kind    string // success | error | warning | info
	Message string
}

// Base carries data every page needs (layout, nav, SEO).
type Base struct {
	SiteName    string
	BaseURL     string
	Title       string
	Description string
	OGImage     string
	OGType      string // "website" or "article"
	Canonical   string
	Path        string
	User        *models.User
	CSRF        string
	Flashes     []Flash
	Year        int
	NoIndex     bool
}

// PageTitle is the <title> text.
func (b Base) PageTitle() string {
	if b.Title == "" {
		return b.SiteName
	}
	return b.Title + " · " + b.SiteName
}

// Pager renders pagination links for a store.Page.
type Pager struct {
	store.Page
	Path   string
	Params url.Values // query params to preserve (e.g. q, status)
}

// URL returns the link to page n.
func (p Pager) URL(n int) string {
	v := url.Values{}
	for k, vals := range p.Params {
		for _, val := range vals {
			if val != "" {
				v.Add(k, val)
			}
		}
	}
	if n > 1 {
		v.Set("page", strconv.Itoa(n))
	}
	if len(v) == 0 {
		return p.Path
	}
	return p.Path + "?" + v.Encode()
}

// Window returns page numbers to show; 0 means an ellipsis.
func (p Pager) Window() []int {
	total, cur := p.Pages(), p.Number
	var out []int
	for i := 1; i <= total; i++ {
		if i == 1 || i == total || (i >= cur-1 && i <= cur+1) {
			out = append(out, i)
		} else if len(out) > 0 && out[len(out)-1] != 0 {
			out = append(out, 0)
		}
	}
	return out
}

// HomeData is the front page / search results.
type HomeData struct {
	Posts       []models.Post
	Pager       Pager
	Query       string
	PopularTags []models.Tag
}

// TagData lists posts for one tag.
type TagData struct {
	Tag   *models.Tag
	Posts []models.Post
	Pager Pager
}

// TagsData lists every tag.
type TagsData struct {
	Tags []models.Tag
}

// PostData is a single article page.
type PostData struct {
	Post     *models.Post
	Comments []models.Comment
	Liked    bool
	Saved    bool
	CanEdit  bool
	More     []models.Post // more from the same author
	ShareURL string
}

// PostForm holds the editor's raw input.
type PostForm struct {
	Title, Slug, Excerpt, Content, CoverImage, Status, Tags string
}

// EditorData drives the create/edit form.
type EditorData struct {
	Post   *models.Post // nil when creating
	Form   PostForm
	Errors map[string]string
}

// IsNew reports whether the editor is creating a post.
func (e EditorData) IsNew() bool { return e.Post == nil }

// DashboardData is a writer's post list.
type DashboardData struct {
	Posts  []models.Post
	Pager  Pager
	Stats  store.AuthorStats
	Status string // filter: "", draft, published
}

// SavedData lists bookmarked posts.
type SavedData struct {
	Posts []models.Post
	Pager Pager
}

// ProfileData is a public author page.
type ProfileData struct {
	Profile *models.User
	Posts   []models.Post
	Pager   Pager
	Stats   store.AuthorStats
	IsSelf  bool
}

// ProfileForm holds editable account fields.
type ProfileForm struct {
	Name, Username, Bio string
}

// SettingsData drives the account settings page.
type SettingsData struct {
	Form             ProfileForm
	Errors           map[string]string
	Sessions         []models.Session
	CurrentSessionID string
}

// LoginData drives the sign-in page.
type LoginData struct {
	Next          string
	GoogleEnabled bool
	DevLogin      bool
}

// AdminData is the admin overview.
type AdminData struct {
	Stats    store.SiteStats
	Users    []models.User
	Pager    Pager
	Comments []models.Comment
}

// ErrorData is shown for 4xx/5xx pages.
type ErrorData struct {
	Code    int
	Title   string
	Message string
}
