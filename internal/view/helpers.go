package view

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/harshadptl/gcore/internal/models"
	"github.com/harshadptl/gcore/web"
)

// asset adds a content-hash query string for cache busting.
func asset(path string) string { return path + "?v=" + web.AssetVersion() }

func postURL(p *models.Post) string     { return "/posts/" + url.PathEscape(p.Slug) }
func postEditURL(p *models.Post) string { return postURL(p) + "/edit" }
func userURL(u *models.User) string     { return "/u/" + url.PathEscape(u.Username) }
func tagURL(t models.Tag) string        { return "/tags/" + url.PathEscape(t.Slug) }

func itoa[T ~int | ~int64](n T) string { return strconv.FormatInt(int64(n), 10) }

// plural returns "1 like" / "3 likes".
func plural[T ~int | ~int64](n T, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return compact(int64(n)) + " " + word + "s"
}

// compact formats 1234 as 1.2k.
func compact(n int64) string {
	switch {
	case n >= 1_000_000:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/1e6), ".0") + "M"
	case n >= 10_000:
		return fmt.Sprintf("%dk", n/1000)
	case n >= 1_000:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/1e3), ".0") + "k"
	}
	return strconv.FormatInt(n, 10)
}

func formatDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format("Jan 2, 2006")
}

func isoDate(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// timeAgo renders "just now", "5 minutes ago", "3 days ago", or a date.
func timeAgo(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return plural(int(d.Minutes()), "minute") + " ago"
	case d < 24*time.Hour:
		return plural(int(d.Hours()), "hour") + " ago"
	case d < 7*24*time.Hour:
		return plural(int(d.Hours()/24), "day") + " ago"
	}
	return formatDate(t)
}

func flashesJSON(fs []Flash) string {
	b, _ := json.Marshal(fs)
	return string(b)
}

func flashVariant(kind string) string {
	switch kind {
	case "success":
		return "success"
	case "error", "danger":
		return "error"
	case "warning":
		return "warning"
	}
	return ""
}

func googleStartURL(next string) string {
	if next == "" || next == "/" {
		return "/auth/google"
	}
	return "/auth/google?next=" + url.QueryEscape(next)
}

func absURL(base, path string) string {
	if path == "" || strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	return base + path
}

// isActive reports whether the nav link for prefix should be highlighted.
func isActive(current, prefix string) bool {
	if prefix == "/" {
		return current == "/"
	}
	return current == prefix || strings.HasPrefix(current, prefix+"/")
}

func ariaCurrent(current, prefix string) string {
	if isActive(current, prefix) {
		return "page"
	}
	return ""
}

func dashboardFilterURL(status string) string {
	if status == "" {
		return "/dashboard"
	}
	return "/dashboard?status=" + status
}

func shortUA(ua string) string {
	switch {
	case ua == "":
		return "Unknown device"
	case strings.Contains(ua, "Firefox"):
		return "Firefox" + uaOS(ua)
	case strings.Contains(ua, "Edg/"):
		return "Edge" + uaOS(ua)
	case strings.Contains(ua, "Chrome"):
		return "Chrome" + uaOS(ua)
	case strings.Contains(ua, "Safari"):
		return "Safari" + uaOS(ua)
	}
	if len(ua) > 40 {
		return ua[:40] + "…"
	}
	return ua
}

func uaOS(ua string) string {
	switch {
	case strings.Contains(ua, "iPhone"), strings.Contains(ua, "iPad"):
		return " on iOS"
	case strings.Contains(ua, "Android"):
		return " on Android"
	case strings.Contains(ua, "Mac OS"):
		return " on macOS"
	case strings.Contains(ua, "Windows"):
		return " on Windows"
	case strings.Contains(ua, "Linux"):
		return " on Linux"
	}
	return ""
}

func liveURL(u *models.User) string { return "/live/" + url.PathEscape(u.Username) }

// splitRTMP splits a push URL at the last "/" into the server URL and the
// stream key, which is how OBS wants them.
func splitRTMP(pushURL string) (server, key string) {
	i := strings.LastIndex(pushURL, "/")
	if i < 0 {
		return pushURL, ""
	}
	return pushURL[:i], pushURL[i+1:]
}
