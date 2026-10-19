package handlers

import (
	"net/url"
	"strings"

	"github.com/gofiber/fiber/v3"

	"github.com/harshadptl/gcore/internal/models"
	"github.com/harshadptl/gcore/internal/store"
	"github.com/harshadptl/gcore/internal/view"
)

// Home lists published posts, with optional ?q= search.
func (h *Handler) Home(c fiber.Ctx) error {
	query := strings.TrimSpace(c.Query("q"))
	if len(query) > 100 {
		query = query[:100]
	}
	page := store.NewPage(pageParam(c), h.Cfg.PostsPerPage)

	posts, page, err := h.Store.ListPosts(c, store.PostFilter{Status: models.StatusPublished, Query: query}, page)
	if err != nil {
		return err
	}
	tags, err := h.Store.PopularTags(c, 12)
	if err != nil {
		return err
	}

	var live []models.LiveStream
	if query == "" && page.Number == 1 {
		h.refreshLive(c)
		if live, err = h.Store.ListLive(c); err != nil {
			return err
		}
	}

	title := ""
	if query != "" {
		title = "Search: " + query
	}
	b := h.base(c, title)
	b.Description = "Stories, ideas and tutorials from the " + h.Cfg.AppName + " community."
	if query != "" || page.Number > 1 {
		b.NoIndex = true
	}
	return render(c, fiber.StatusOK, view.HomePage(b, view.HomeData{
		Posts:       posts,
		Pager:       view.Pager{Page: page, Path: "/", Params: url.Values{"q": {query}}},
		Query:       query,
		PopularTags: tags,
		Live:        live,
	}))
}

// Tags lists every tag in use.
func (h *Handler) Tags(c fiber.Ctx) error {
	tags, err := h.Store.PopularTags(c, 0)
	if err != nil {
		return err
	}
	return render(c, fiber.StatusOK, view.TagsPage(h.base(c, "Tags"), view.TagsData{Tags: tags}))
}

// Tag lists published posts with one tag.
func (h *Handler) Tag(c fiber.Ctx) error {
	tag, err := h.Store.TagBySlug(c, c.Params("slug"))
	if err != nil {
		return orNotFound(err)
	}
	page := store.NewPage(pageParam(c), h.Cfg.PostsPerPage)
	posts, page, err := h.Store.ListPosts(c, store.PostFilter{Status: models.StatusPublished, TagSlug: tag.Slug}, page)
	if err != nil {
		return err
	}
	b := h.base(c, "#"+tag.Name)
	b.Description = "Posts tagged " + tag.Name
	return render(c, fiber.StatusOK, view.TagPage(b, view.TagData{
		Tag:   tag,
		Posts: posts,
		Pager: view.Pager{Page: page, Path: c.Path()},
	}))
}

// Health reports whether the app and database are up.
func (h *Handler) Health(c fiber.Ctx) error {
	if err := h.Store.DB.PingContext(c); err != nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"status": "error", "db": err.Error()})
	}
	return c.JSON(fiber.Map{"status": "ok"})
}
