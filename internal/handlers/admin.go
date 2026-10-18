package handlers

import (
	"github.com/gofiber/fiber/v3"

	"github.com/harshadptl/gcore/internal/models"
	"github.com/harshadptl/gcore/internal/store"
	"github.com/harshadptl/gcore/internal/view"
)

// Admin shows site stats, users and recent comments.
func (h *Handler) Admin(c fiber.Ctx) error {
	stats, err := h.Store.SiteStats(c)
	if err != nil {
		return err
	}
	users, page, err := h.Store.ListUsers(c, store.NewPage(pageParam(c), 20))
	if err != nil {
		return err
	}
	comments, err := h.Store.RecentComments(c, 10)
	if err != nil {
		return err
	}
	b := h.base(c, "Admin")
	b.NoIndex = true
	return render(c, fiber.StatusOK, view.AdminPage(b, view.AdminData{
		Stats:    stats,
		Users:    users,
		Pager:    view.Pager{Page: page, Path: "/admin"},
		Comments: comments,
	}))
}

// SetUserRole promotes or demotes a user.
func (h *Handler) SetUserRole(c fiber.Ctx) error {
	id := fiber.Params[int64](c, "id")
	role := c.FormValue("role")
	if role != models.RoleAdmin && role != models.RoleUser {
		return fiber.NewError(fiber.StatusBadRequest, "Unknown role.")
	}
	if id == CurrentUser(c).ID {
		return c.Redirect().With("error", "You can't change your own role.").To("/admin")
	}
	target, err := h.Store.UserByID(c, id)
	if err != nil {
		return orNotFound(err)
	}
	if err := h.Store.SetRole(c, target.ID, role); err != nil {
		return err
	}
	return c.Redirect().With("success", target.Name+" is now "+role+".").To("/admin")
}
