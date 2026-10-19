package handlers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gofiber/fiber/v3"

	"github.com/harshadptl/gcore/internal/gcore"
	"github.com/harshadptl/gcore/internal/models"
	"github.com/harshadptl/gcore/internal/store"
	"github.com/harshadptl/gcore/internal/view"
)

// liveSyncTTL bounds how often we poll Gcore for stream status.
const liveSyncTTL = 5 * time.Second

const maxStreamTitle = 100

// liveSync throttles status syncs.
type liveSync struct {
	mu   sync.Mutex
	last time.Time
}

// refreshLive reconciles active/live streams with Gcore. It never fails the
// caller: on any error it logs and leaves the cached DB state in place.
func (h *Handler) refreshLive(ctx context.Context) {
	h.live.mu.Lock() // held across List on purpose: at most one List per TTL
	defer h.live.mu.Unlock()
	// ponytail: single-instance in-memory cache + mutex; if this ever runs
	// as >1 process, move the TTL check to the DB (e.g. synced_at column,
	// already there) or add a shared cache.
	if !h.Cfg.GcoreEnabled() || time.Since(h.live.last) < liveSyncTTL {
		return
	}
	now := time.Now()
	h.live.last = now
	rows, err := h.Store.SyncCandidates(ctx)
	if err != nil {
		h.Log.Warn("live sync candidates failed", "err", err)
		return
	}
	if len(rows) == 0 {
		return // nobody is streaming: no Gcore call
	}
	list, err := h.Gcore.List(ctx)
	if err != nil {
		h.Log.Warn("gcore list failed", "err", err)
		return
	}
	byID := make(map[int64]gcore.Stream, len(list))
	for _, s := range list {
		byID[s.ID] = s
	}
	for i := range rows {
		row := &rows[i]
		st, ok := byID[row.GcoreStreamID] // missing => deleted on Gcore: off
		row.Apply(ok && st.Active, ok && st.Live, now)
		if row.Abandoned(now) {
			if _, err := h.Gcore.SetActive(ctx, row.GcoreStreamID, false); err != nil {
				h.Log.Warn("gcore auto-disarm failed", "stream", row.GcoreStreamID, "err", err)
			} else {
				row.Apply(false, false, now)
			}
		}
		if err := h.Store.SaveLiveStatus(ctx, row); err != nil {
			h.Log.Warn("save live status failed", "user", row.UserID, "err", err)
		}
	}
}

// invalidateLive makes the next refreshLive hit Gcore.
func (h *Handler) invalidateLive() {
	h.live.mu.Lock()
	h.live.last = time.Time{}
	h.live.mu.Unlock()
}

// stopStream disarms on Gcore first; the DB is untouched if Gcore fails so
// the UI never claims "stopped" while Gcore is still armed.
func (h *Handler) stopStream(ctx context.Context, ls *models.LiveStream) error {
	if _, err := h.Gcore.SetActive(ctx, ls.GcoreStreamID, false); err != nil {
		return err
	}
	if err := h.Store.DisarmLiveStream(ctx, ls.UserID, time.Now()); err != nil {
		return err
	}
	h.invalidateLive()
	return nil
}

// LiveDashboard is the owner's go-live control panel.
func (h *Handler) LiveDashboard(c fiber.Ctx) error {
	h.refreshLive(c)
	data := view.LiveDashboardData{Configured: h.Cfg.GcoreEnabled()}
	ls, err := h.Store.LiveStreamByUser(c, CurrentUser(c).ID)
	switch {
	case err == nil:
		data.Stream, data.Title = ls, ls.Title
	case !errors.Is(err, store.ErrNotFound):
		return err
	}
	b := h.base(c, "Go live")
	b.NoIndex = true
	return render(c, fiber.StatusOK, view.LiveDashboardPage(b, data))
}

// StartStream creates the user's Gcore stream on first use, then arms it.
func (h *Handler) StartStream(c fiber.Ctx) error {
	const back = "/dashboard/live"
	u := CurrentUser(c)
	title := strings.TrimSpace(c.FormValue("title"))
	if utf8.RuneCountInString(title) > maxStreamTitle {
		return c.Redirect().With("error", "Keep the title under 100 characters.").To(back)
	}
	if !h.Cfg.GcoreEnabled() {
		return c.Redirect().With("error", "Live streaming isn't configured on this server.").To(back)
	}

	ls, err := h.Store.LiveStreamByUser(c, u.ID)
	if errors.Is(err, store.ErrNotFound) {
		var s gcore.Stream
		s, err = h.Gcore.Create(c, fmt.Sprintf("%s-%s-%d", h.Cfg.AppName, u.Username, u.ID))
		if err != nil {
			return c.Redirect().With("error", err.Error()).To(back)
		}
		var created bool
		created, err = h.Store.CreateLiveStream(c, &models.LiveStream{
			UserID: u.ID, GcoreStreamID: s.ID, Title: title,
			PushURL: s.PushURL, PushURLSRT: s.PushURLSRT, PushURLWHIP: s.PushURLWHIP, IframeURL: s.IframeURL,
		})
		if err == nil && !created {
			// Lost a race with another tab: drop our stream, use the winner's.
			if derr := h.Gcore.Delete(c, s.ID); derr != nil {
				h.Log.Error("gcore delete of duplicate stream failed", "stream", s.ID, "err", derr)
			}
		}
		if err != nil {
			return err
		}
		ls, err = h.Store.LiveStreamByUser(c, u.ID)
	}
	if err != nil {
		return err
	}

	if _, err := h.Gcore.SetActive(c, ls.GcoreStreamID, true); err != nil {
		return c.Redirect().With("error", err.Error()).To(back)
	}
	if err := h.Store.ArmLiveStream(c, u.ID, title, time.Now()); err != nil {
		return err
	}
	h.invalidateLive()
	return c.Redirect().With("success", "You're armed. Start sending video to go live.").To(back)
}

// StopStream disarms the user's stream.
func (h *Handler) StopStream(c fiber.Ctx) error {
	const back = "/dashboard/live"
	ls, err := h.Store.LiveStreamByUser(c, CurrentUser(c).ID)
	if errors.Is(err, store.ErrNotFound) {
		return c.Redirect().With("info", "You're not streaming.").To(back)
	}
	if err != nil {
		return err
	}
	if err := h.stopStream(c, ls); err != nil {
		return c.Redirect().With("error", err.Error()).To(back)
	}
	return c.Redirect().With("success", "Stream stopped.").To(back)
}

// watchTarget resolves :username and syncs state; stream is nil when the
// user has never armed one.
func (h *Handler) watchTarget(c fiber.Ctx) (*models.User, *models.LiveStream, error) {
	u, err := h.Store.UserByUsername(c, c.Params("username"))
	if err != nil {
		return nil, nil, orNotFound(err)
	}
	h.refreshLive(c)
	ls, err := h.Store.LiveStreamByUser(c, u.ID)
	if errors.Is(err, store.ErrNotFound) {
		return u, nil, nil
	}
	return u, ls, err
}

func (h *Handler) WatchLive(c fiber.Ctx) error {
	u, ls, err := h.watchTarget(c)
	if err != nil {
		return err
	}
	b := h.base(c, u.Name+" live")
	b.NoIndex = true
	return render(c, fiber.StatusOK, view.WatchPage(b, view.WatchData{Streamer: u, Stream: ls}))
}

func (h *Handler) LiveStatus(c fiber.Ctx) error {
	_, ls, err := h.watchTarget(c)
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	return c.JSON(fiber.Map{"live": ls != nil && ls.Live})
}
