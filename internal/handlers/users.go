package handlers

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/gofiber/fiber/v3"

	"github.com/harshadptl/gcore/internal/models"
	"github.com/harshadptl/gcore/internal/store"
	"github.com/harshadptl/gcore/internal/view"
)

var usernameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{2,29}$`)

// Profile is an author's public page.
func (h *Handler) Profile(c fiber.Ctx) error {
	profile, err := h.Store.UserByUsername(c, c.Params("username"))
	if err != nil {
		return orNotFound(err)
	}
	page := store.NewPage(pageParam(c), h.Cfg.PostsPerPage)
	posts, page, err := h.Store.ListPosts(c, store.PostFilter{Status: models.StatusPublished, AuthorID: profile.ID}, page)
	if err != nil {
		return err
	}
	stats, err := h.Store.AuthorStats(c, profile.ID)
	if err != nil {
		return err
	}
	me := CurrentUser(c)
	b := h.base(c, profile.Name)
	b.Description = profile.Bio
	b.OGImage = profile.AvatarURL
	return render(c, fiber.StatusOK, view.ProfilePage(b, view.ProfileData{
		Profile: profile,
		Posts:   posts,
		Pager:   view.Pager{Page: page, Path: c.Path()},
		Stats:   stats,
		IsSelf:  me != nil && me.ID == profile.ID,
	}))
}

// Settings shows the account settings form.
func (h *Handler) Settings(c fiber.Ctx) error {
	u := CurrentUser(c)
	return h.renderSettings(c, fiber.StatusOK, view.ProfileForm{Name: u.Name, Username: u.Username, Bio: u.Bio}, nil)
}

func (h *Handler) renderSettings(c fiber.Ctx, status int, form view.ProfileForm, errs map[string]string) error {
	u := CurrentUser(c)
	sessions, err := h.Store.SessionsForUser(c, u.ID)
	if err != nil {
		return err
	}
	b := h.base(c, "Settings")
	b.NoIndex = true
	return render(c, status, view.SettingsPage(b, view.SettingsData{
		Form:             form,
		Errors:           errs,
		Sessions:         sessions,
		CurrentSessionID: currentSession(c).ID,
	}))
}

// UpdateSettings saves name, username and bio.
func (h *Handler) UpdateSettings(c fiber.Ctx) error {
	u := CurrentUser(c)
	form := view.ProfileForm{
		Name:     strings.TrimSpace(c.FormValue("name")),
		Username: strings.ToLower(strings.TrimSpace(c.FormValue("username"))),
		Bio:      strings.TrimSpace(c.FormValue("bio")),
	}
	errs := map[string]string{}
	if n := utf8.RuneCountInString(form.Name); n == 0 || n > 60 {
		errs["name"] = "Enter a name between 1 and 60 characters."
	}
	if !usernameRe.MatchString(form.Username) {
		errs["username"] = "3–30 characters: lowercase letters, numbers, dashes or underscores."
	} else if taken, err := h.Store.UsernameTaken(c, form.Username, u.ID); err != nil {
		return err
	} else if taken {
		errs["username"] = "That username is taken."
	}
	if utf8.RuneCountInString(form.Bio) > 280 {
		errs["bio"] = "Keep your bio under 280 characters."
	}
	if len(errs) > 0 {
		return h.renderSettings(c, fiber.StatusUnprocessableEntity, form, errs)
	}

	u.Name, u.Username, u.Bio = form.Name, form.Username, form.Bio
	if err := h.Store.UpdateProfile(c, u); err != nil {
		return err
	}
	return c.Redirect().With("success", "Profile updated.").To("/settings")
}

// RevokeOtherSessions signs the user out of every other device.
func (h *Handler) RevokeOtherSessions(c fiber.Ctx) error {
	n, err := h.Store.DeleteOtherSessions(c, CurrentUser(c).ID, currentSession(c).ID)
	if err != nil {
		return err
	}
	return c.Redirect().With("success", fmt.Sprintf("Signed out of %d other session(s).", n)).To("/settings#tab=sessions")
}

// DeleteAccount permanently deletes the user and all their content.
func (h *Handler) DeleteAccount(c fiber.Ctx) error {
	u := CurrentUser(c)
	if strings.TrimSpace(c.FormValue("confirm")) != u.Username {
		return c.Redirect().With("error", "Type your username exactly to confirm deletion.").To("/settings#tab=danger")
	}
	if err := h.Store.DeleteUser(c, u.ID); err != nil {
		return err
	}
	h.clearSessionCookie(c)
	h.Log.Info("account deleted", "user_id", u.ID)
	return c.Redirect().With("success", "Your account and posts have been deleted.").To("/")
}

// Dashboard lists the current user's posts.
func (h *Handler) Dashboard(c fiber.Ctx) error {
	u := CurrentUser(c)
	status := c.Query("status")
	if status != models.StatusDraft && status != models.StatusPublished {
		status = ""
	}
	page := store.NewPage(pageParam(c), 15)
	posts, page, err := h.Store.ListPosts(c, store.PostFilter{AuthorID: u.ID, Status: status}, page)
	if err != nil {
		return err
	}
	stats, err := h.Store.AuthorStats(c, u.ID)
	if err != nil {
		return err
	}
	b := h.base(c, "Dashboard")
	b.NoIndex = true
	return render(c, fiber.StatusOK, view.DashboardPage(b, view.DashboardData{
		Posts:  posts,
		Pager:  view.Pager{Page: page, Path: "/dashboard", Params: map[string][]string{"status": {status}}},
		Stats:  stats,
		Status: status,
	}))
}

// Saved lists bookmarked posts.
func (h *Handler) Saved(c fiber.Ctx) error {
	u := CurrentUser(c)
	page := store.NewPage(pageParam(c), h.Cfg.PostsPerPage)
	posts, page, err := h.Store.ListPosts(c, store.PostFilter{Status: models.StatusPublished, BookmarkedBy: u.ID}, page)
	if err != nil {
		return err
	}
	b := h.base(c, "Reading list")
	b.NoIndex = true
	return render(c, fiber.StatusOK, view.SavedPage(b, view.SavedData{
		Posts: posts,
		Pager: view.Pager{Page: page, Path: "/dashboard/saved"},
	}))
}
