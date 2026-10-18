package handlers

import (
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/gofiber/fiber/v3"

	"github.com/harshadptl/gcore/internal/models"
	"github.com/harshadptl/gcore/internal/store"
	"github.com/harshadptl/gcore/internal/textutil"
	"github.com/harshadptl/gcore/internal/view"
)

const (
	maxTitleLen   = 200
	maxExcerptLen = 300
	maxContentLen = 200_000
	maxTags       = 5
)

// ShowPost renders a single post with its comments.
func (h *Handler) ShowPost(c fiber.Ctx) error {
	post, err := h.Store.PostBySlug(c, c.Params("slug"))
	if err != nil {
		return orNotFound(err)
	}
	user := CurrentUser(c)
	if !post.IsPublished() && !canEdit(user, post) {
		return notFound() // drafts are invisible to everyone else
	}

	if post.IsPublished() && (user == nil || user.ID != post.AuthorID) {
		if err := h.Store.IncrementViews(c, post.ID); err == nil {
			post.Views++
		}
	}

	comments, err := h.Store.CommentsForPost(c, post.ID)
	if err != nil {
		return err
	}

	data := view.PostData{
		Post:     post,
		Comments: comments,
		CanEdit:  canEdit(user, post),
		ShareURL: h.Cfg.BaseURL + "/posts/" + url.PathEscape(post.Slug),
	}
	if user != nil {
		if data.Liked, data.Saved, err = h.Store.Reactions(c, user.ID, post.ID); err != nil {
			return err
		}
	}
	if post.IsPublished() {
		more, _, err := h.Store.ListPosts(c, store.PostFilter{
			Status: models.StatusPublished, AuthorID: post.AuthorID, ExcludeID: post.ID,
		}, store.NewPage(1, 3))
		if err != nil {
			return err
		}
		data.More = more
	}

	b := h.base(c, post.Title)
	b.Description = post.Excerpt
	b.OGType = "article"
	if post.CoverImage != "" {
		b.OGImage = post.CoverImage
	}
	b.NoIndex = !post.IsPublished()
	return render(c, fiber.StatusOK, view.PostPage(b, data))
}

// NewPost shows an empty editor.
func (h *Handler) NewPost(c fiber.Ctx) error {
	b := h.base(c, "New post")
	b.NoIndex = true
	return render(c, fiber.StatusOK, view.EditorPage(b, view.EditorData{
		Form: view.PostForm{Status: models.StatusDraft},
	}))
}

// CreatePost validates the editor form and inserts a post.
func (h *Handler) CreatePost(c fiber.Ctx) error {
	user := CurrentUser(c)
	form := readPostForm(c)
	in, errs := h.validatePost(form)
	if len(errs) == 0 {
		if err := h.attachCover(c, &in, errs); err != nil {
			return err
		}
	}
	if len(errs) > 0 {
		b := h.base(c, "New post")
		return render(c, fiber.StatusUnprocessableEntity, view.EditorPage(b, view.EditorData{Form: form, Errors: errs}))
	}

	post, err := h.Store.CreatePost(c, user.ID, in)
	if err != nil {
		return err
	}
	msg := "Draft saved."
	if post.IsPublished() {
		msg = "Your post is live!"
	}
	return c.Redirect().With("success", msg).To("/posts/" + url.PathEscape(post.Slug))
}

// EditPost shows the editor for an existing post.
func (h *Handler) EditPost(c fiber.Ctx) error {
	post, err := h.editablePost(c)
	if err != nil {
		return err
	}
	b := h.base(c, "Edit: "+post.Title)
	b.NoIndex = true
	return render(c, fiber.StatusOK, view.EditorPage(b, view.EditorData{
		Post: post,
		Form: view.PostForm{
			Title:      post.Title,
			Slug:       post.Slug,
			Excerpt:    post.Excerpt,
			Content:    post.Content,
			CoverImage: post.CoverImage,
			Status:     post.Status,
			Tags:       strings.Join(post.TagNames(), ", "),
		},
	}))
}

// UpdatePost saves the editor form for an existing post.
func (h *Handler) UpdatePost(c fiber.Ctx) error {
	post, err := h.editablePost(c)
	if err != nil {
		return err
	}
	form := readPostForm(c)
	in, errs := h.validatePost(form)
	if len(errs) == 0 {
		if err := h.attachCover(c, &in, errs); err != nil {
			return err
		}
	}
	if len(errs) > 0 {
		b := h.base(c, "Edit: "+post.Title)
		return render(c, fiber.StatusUnprocessableEntity, view.EditorPage(b, view.EditorData{Post: post, Form: form, Errors: errs}))
	}

	if err := h.Store.UpdatePost(c, post, in); err != nil {
		return err
	}
	return c.Redirect().With("success", "Changes saved.").To("/posts/" + url.PathEscape(post.Slug))
}

// DeletePost removes a post.
func (h *Handler) DeletePost(c fiber.Ctx) error {
	post, err := h.editablePost(c)
	if err != nil {
		return err
	}
	if err := h.Store.DeletePost(c, post.ID); err != nil {
		return err
	}
	return c.Redirect().With("success", "Post deleted.").To(formNext(c, "/dashboard"))
}

// Preview renders Markdown to sanitized HTML for the editor's preview tab.
func (h *Handler) Preview(c fiber.Ctx) error {
	content := c.FormValue("content")
	if len(content) > maxContentLen {
		return fiber.ErrRequestEntityTooLarge
	}
	html, err := h.Markdown.Render(content)
	if err != nil {
		return err
	}
	if strings.TrimSpace(html) == "" {
		html = "<p><em>Nothing to preview yet.</em></p>"
	}
	c.Set(fiber.HeaderContentType, fiber.MIMETextHTMLCharsetUTF8)
	return c.SendString(html)
}

// editablePost loads the :slug post and checks the user may edit it.
func (h *Handler) editablePost(c fiber.Ctx) (*models.Post, error) {
	post, err := h.Store.PostBySlug(c, c.Params("slug"))
	if err != nil {
		return nil, orNotFound(err)
	}
	if !canEdit(CurrentUser(c), post) {
		if post.IsPublished() {
			return nil, fiber.ErrForbidden
		}
		return nil, fiber.ErrNotFound
	}
	return post, nil
}

func readPostForm(c fiber.Ctx) view.PostForm {
	return view.PostForm{
		Title:      strings.TrimSpace(c.FormValue("title")),
		Slug:       strings.TrimSpace(c.FormValue("slug")),
		Excerpt:    strings.TrimSpace(c.FormValue("excerpt")),
		Content:    strings.ReplaceAll(c.FormValue("content"), "\r\n", "\n"),
		CoverImage: strings.TrimSpace(c.FormValue("cover_image")),
		Status:     c.FormValue("status"),
		Tags:       c.FormValue("tags"),
	}
}

// validatePost checks the form and converts it into store input.
func (h *Handler) validatePost(f view.PostForm) (store.PostInput, map[string]string) {
	errs := map[string]string{}

	switch n := utf8.RuneCountInString(f.Title); {
	case n == 0:
		errs["title"] = "Give your post a title."
	case n > maxTitleLen:
		errs["title"] = "Keep the title under 200 characters."
	}
	if f.Status != models.StatusDraft && f.Status != models.StatusPublished {
		f.Status = models.StatusDraft
	}
	if f.Status == models.StatusPublished && strings.TrimSpace(f.Content) == "" {
		errs["content"] = "Write something before publishing."
	}
	if len(f.Content) > maxContentLen {
		errs["content"] = "This post is too long."
	}
	if utf8.RuneCountInString(f.Excerpt) > maxExcerptLen {
		errs["excerpt"] = "Keep the summary under 300 characters."
	}
	if f.Slug != "" && textutil.Slugify(f.Slug) == "" {
		errs["slug"] = "Use letters, numbers and dashes."
	}
	if f.CoverImage != "" && !validImageURL(f.CoverImage) {
		errs["cover_image"] = "Use an https:// image URL or upload a file."
	}
	tags := textutil.ParseTags(f.Tags, maxTags+1)
	if len(tags) > maxTags {
		errs["tags"] = "Use at most 5 tags."
	}

	var html string
	if len(f.Content) <= maxContentLen {
		var err error
		html, err = h.Markdown.Render(f.Content)
		if err != nil {
			errs["content"] = "Could not render this Markdown."
		}
	}
	plain := h.Markdown.PlainText(html)
	excerpt := f.Excerpt
	if excerpt == "" {
		excerpt = textutil.Truncate(plain, 200)
	}

	return store.PostInput{
		Title:          f.Title,
		Slug:           f.Slug,
		Excerpt:        excerpt,
		Content:        f.Content,
		ContentHTML:    html,
		CoverImage:     f.CoverImage,
		Status:         f.Status,
		ReadingMinutes: textutil.ReadingMinutes(plain),
		Tags:           tags,
	}, errs
}

func validImageURL(s string) bool {
	if (strings.HasPrefix(s, "/uploads/") || strings.HasPrefix(s, "/static/")) && !strings.Contains(s, "..") {
		return true
	}
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != ""
}
