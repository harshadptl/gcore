# gcore

A production-minded Go boilerplate for a multi-author blog:

- **[Fiber v3](https://gofiber.io)** — HTTP server, routing, middleware
- **[Bun](https://bun.uptrace.dev)** — SQL toolkit and migrations, on **PostgreSQL** or **SQLite** (pure Go, no CGO)
- **[templ](https://templ.guide)** — type-safe, compiled HTML components
- **[Oat UI](https://oat.ink)** — semantic, class-light CSS/JS components (vendored, no CDN)
- **Sign in with Google** — OAuth 2.0 authorization-code flow with PKCE and signed state

Server-rendered HTML that works without JavaScript, with small progressive enhancements on top.

## Features

**Writing**
- Markdown editor with live server-side preview (GitHub-flavoured: tables, task lists, fenced code)
- Drafts and publishing, custom slugs (stable when titles change), summaries (auto-generated if blank)
- Tags with chip input, cover images by URL or upload (type sniffed from bytes, size-limited)
- Reading-time estimate, view counter, Ctrl/⌘+S to save, unsaved-changes warning

**Reading**
- Home feed with search and pagination, tag pages, author profiles
- Comments, likes and a personal reading list (bookmarks) — instant via `fetch()`, plain forms without JS
- RSS feed (`/feed.xml`), `sitemap.xml`, `robots.txt`, Open Graph tags
- Light/dark theme (follows the OS, with a toggle), responsive down to phones

**Accounts & admin**
- Google sign-in, profile settings (name, username, bio), list and revoke active sessions, delete account
- Writer dashboard with stats and post management
- Admin area: site stats, promote/demote users, moderate comments. `ADMIN_EMAILS` bootstraps the first admins
- Dev-only password-less login for trying things out without Google credentials

**Engineering**
- Server-side sessions in the database (only token *hashes* stored), sliding expiry, hourly cleanup
- CSRF: per-session synchronizer token **and** Fiber's tokenless cross-origin protection
- Security headers via Helmet with a strict CSP (no inline scripts), rate limiting on auth and writes
- Markdown → HTML sanitized with bluemonday; templ escapes everything else
- Versioned migrations (`migrate up/down/status`), demo seed data
- Embedded static assets with content-hash cache busting → single binary deploys
- Graceful shutdown, structured logging (`log/slog`), health check endpoint and CLI
- Tests for the store (SQLite + Postgres), HTTP flows, and helpers; GitHub Actions CI
- Dockerfile (distroless, non-root) and Docker Compose with PostgreSQL 18

## Quick start

Requires **Go 1.26+**. No database server needed — SQLite is the default.

```bash
make setup     # go mod tidy (writes go.sum), creates .env, generates templates
make seed      # optional: demo users, posts and comments
make dev       # live reload at http://localhost:7331
```

Open <http://localhost:7331>, click **Sign in**, and use the **Developer login** form
(enabled by `DEV_LOGIN=true` in `.env`). Sign in as `admin@example.com` to get the admin role.

Without `make`: `go mod tidy && cp .env.example .env && go tool templ generate && go run ./cmd/app`.

> The templ CLI is pinned in `go.mod` as a Go tool, so `go tool templ …` always matches the
> runtime library version. No separate install needed.

## Google sign-in setup

1. Open [Google Cloud Console → APIs & Services → Credentials](https://console.cloud.google.com/apis/credentials).
2. Configure the **OAuth consent screen** (External, app name, support email). The app only
   requests the `openid`, `email` and `profile` scopes, which don't need verification.
3. **Create credentials → OAuth client ID → Web application.**
4. Add an **Authorized redirect URI** for each environment:
   - `http://localhost:3000/auth/google/callback` (development; the session cookie is shared with the `:7331` live-reload proxy)
   - `https://your-domain.com/auth/google/callback` (production)
5. Put the values in `.env`:

   ```dotenv
   GOOGLE_CLIENT_ID=1234-abc.apps.googleusercontent.com
   GOOGLE_CLIENT_SECRET=GOCSPX-...
   ADMIN_EMAILS=you@gmail.com
   ```

The redirect URI is derived from `BASE_URL` (override with `GOOGLE_REDIRECT_URL`) and must match the console exactly.

**How the flow works** (`internal/handlers/auth.go`): `/auth/google` creates a random `state`
and PKCE verifier, stores them in a short-lived HMAC-signed cookie, and redirects to Google.
The callback checks the state, exchanges the code with the verifier, fetches the OpenID userinfo,
requires a verified email, then creates or updates the user and starts a fresh session.
Accounts are matched by Google's stable `sub` ID, falling back to email.

## Using PostgreSQL

Set `DATABASE_URL` and restart; migrations run automatically (`AUTO_MIGRATE=true`):

```dotenv
DATABASE_URL=postgres://blog:blog@localhost:5432/blog?sslmode=disable
```

Or run everything in Docker:

```bash
cp .env.example .env          # set SESSION_SECRET and GOOGLE_* for production
docker compose up --build     # app on :3000, Postgres 18 on :5432
docker compose exec app /app/app seed   # optional demo content
```

Compose defaults to `APP_ENV=development` so the dev login works out of the box; set
`APP_ENV=production` in `.env` for a real deployment.

## Commands

```text
make setup           First run: modules, .env, templates
make dev             Live reload (templ watch + proxy on :7331)
make run             Run once
make build           Production binary → bin/app
make test            Tests on in-memory SQLite
make test-pg PG=...  Same tests on PostgreSQL (wipes that database's public schema!)
make migrate         Apply migrations            (app migrate up)
make migrate-down    Roll back the last group    (app migrate down)
make migrate-status  Show migrations             (app migrate status)
make seed            Demo content                (app seed)
make fmt / vet       Format / static checks
make rename MODULE=github.com/you/blog   Rename the Go module everywhere
```

The binary itself: `app [serve|migrate up|migrate down|migrate status|seed|healthcheck]`.

## Project layout

```text
cmd/app/                  entrypoint: serve, migrate, seed, healthcheck
internal/
  config/                 env/.env → typed Config (validated; stricter in production)
  database/               open Bun (pgx or pure-Go SQLite), migrator helpers
    migrations/           versioned migrations (<timestamp>_<name>.go)
  models/                 Bun models: User, Post, Tag, PostTag, Comment, Like, Bookmark, Session
  store/                  all SQL lives here (posts, users, sessions, reactions, stats) + tests
  auth/                   Google OAuth client, HMAC signer, safe redirect helper
  handlers/               HTTP handlers, middleware (session, CSRF, guards), error pages
  server/                 Fiber app: middleware stack and routes + HTTP tests
  view/                   templ components (*.templ) and their data types
  markdown/               goldmark (GFM) + bluemonday sanitizer
  textutil/               slugs, excerpts, reading time, tag parsing
  seed/                   demo content
web/
  static/css/app.css      theme tokens + layout on top of Oat
  static/js/app.js        progressive enhancement (toasts, likes, editor, theme)
  static/vendor/oat/      Oat UI v0.8.3 (MIT)
  embed.go                embeds static/ into the binary
```

## Routes

| Method | Path | |
| --- | --- | --- |
| GET | `/`, `/?q=` | Latest posts, search |
| GET | `/posts/:slug` | Post with comments |
| GET | `/tags`, `/tags/:slug` | Tag index, posts by tag |
| GET | `/u/:username` | Author profile |
| GET | `/feed.xml`, `/sitemap.xml`, `/robots.txt`, `/healthz` | Feeds & ops |
| GET | `/login`, `/auth/google`, `/auth/google/callback` | Sign-in |
| POST | `/auth/dev-login`, `/logout` | Dev login, sign out |
| GET/POST | `/posts/new`, `/posts` | New post |
| GET/POST | `/posts/:slug/edit`, `/posts/:slug` | Edit post |
| POST | `/posts/:slug/delete`, `/like`, `/bookmark`, `/comments` | Post actions |
| POST | `/comments/:id/delete`, `/preview` | Comment moderation, Markdown preview |
| GET | `/dashboard`, `/dashboard/saved` | Your posts, reading list |
| GET/POST | `/settings`, `/settings/sessions/revoke`, `/settings/delete` | Account |
| GET/POST | `/admin`, `/admin/users/:id/role` | Admin |

## Common tasks

**Add a page.** Add a `templ` component in `internal/view`, a handler in `internal/handlers`
that builds `h.base(c, "Title")` and calls `render(...)`, then register the route in
`internal/server/server.go`. Every form needs `@CSRFField(b.CSRF)`.

**Add a migration.** Create `internal/database/migrations/20261101000000_add_featured.go`:

```go
package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(func(ctx context.Context, db *bun.DB) error {
		_, err := db.ExecContext(ctx, `ALTER TABLE posts ADD COLUMN featured BOOLEAN NOT NULL DEFAULT FALSE`)
		return err
	}, func(ctx context.Context, db *bun.DB) error {
		_, err := db.ExecContext(ctx, `ALTER TABLE posts DROP COLUMN featured`)
		return err
	})
}
```

Then add the field to the model (`Featured bool \`bun:",notnull"\``) and run `make migrate`.
Migrations use their own snapshot structs or SQL rather than `internal/models`, so old
migrations keep producing the same schema as models evolve.

**Restyle.** Oat styles plain HTML; `web/static/css/app.css` starts with brand tokens
(`--primary`, `--background`, `--font-serif`, …) that override Oat's variables. Change those
first. Components follow Oat's conventions: `<article class="card">`, `<button data-variant="danger">`,
`<span class="badge" data-variant="success">`, `<div role="alert">`, `<ot-tabs>`, `<ot-dropdown>`,
`<dialog>` with `commandfor`. See <https://oat.ink>.

**Use the current user** in a handler: `handlers.CurrentUser(c)` (nil when signed out). Protect
routes with `h.RequireUser` / `h.RequireAdmin`.

## Production checklist

- `APP_ENV=production` (JSON logs, HSTS, longer static caching; refuses to start without the next two)
- `SESSION_SECRET` — 32+ random characters (`openssl rand -base64 48`)
- `GOOGLE_CLIENT_ID` / `GOOGLE_CLIENT_SECRET`, and the production redirect URI in Google Cloud
- `BASE_URL=https://…` — enables `Secure` + `__Host-` cookies
- PostgreSQL `DATABASE_URL`
- Deploying with Docker Compose: use `docker-compose.prod.yaml`, not `docker-compose.yml` — the
  latter is dev-only (default Postgres creds, published DB port, `APP_ENV=development`). Run
  `docker compose -f docker-compose.prod.yaml up -d --build` once `.env` has the values above plus
  `POSTGRES_USER`/`POSTGRES_PASSWORD`/`POSTGRES_DB`.
- Behind a proxy/load balancer: `TRUST_PROXY=true` and `TRUSTED_PROXIES=<cidr>` for real client IPs
- Persist `UPLOAD_DIR` (a volume), or swap `saveImage` in `internal/handlers/uploads.go` for object storage
- Commit `go.sum` (created by `make setup`) so CI and Docker builds are reproducible

## Notes

- Generated `*_templ.go` files are committed so `go build` works right after cloning; CI checks they're current.
- SQLite runs through [ncruces/go-sqlite3](https://github.com/ncruces/go-sqlite3) (WebAssembly, no CGO), so
  `CGO_ENABLED=0` builds and cross-compiles work.
- Oat UI is vendored in `web/static/vendor/oat` — update by replacing `oat.min.css`/`oat.min.js` from
  `npm pack @knadh/oat`.
