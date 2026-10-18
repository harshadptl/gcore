# Live Streaming Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let any signed-in user go live (OBS via RTMP/SRT, or the browser via WHIP) on one reusable Gcore stream, and let anyone watch at `/live/:username`. The blog stays as it is.

**Architecture:** A small stdlib `internal/gcore` REST client makes every account-level Gcore call on the server. A `live_streams` table (1:1 with `users`) caches each user's Gcore stream and its `active`/`live` status. Public reads call `h.refreshLive`, which makes one `GET /streaming/streams` at most once per TTL, under an in-process mutex. That refresh records the live→started_at / ended_at flips and auto-disarms abandoned armed streams. Playback is Gcore's hosted player in an `<iframe>`. The browser broadcaster is a vanilla JS WHIP client that POSTs SDP straight to Gcore.

**Tech Stack:** Go, Fiber v3, templ, Bun (SQLite + PostgreSQL), Oat CSS, vanilla JS, `net/http`.

**Spec:** `docs/superpowers/specs/2026-10-18-livestreaming-design.md` (approved; read it alongside this plan).

## Global Constraints

- No new Go dependencies (`go.mod` require block unchanged). No JS framework and no WHIP library.
- The Gcore API key never reaches the browser and never appears in rendered HTML, JSON, flash text, or error strings.
- One user can never see or control another user's stream. Push URLs and keys render only on the owner's `/dashboard/live`.
- Existing routes, middleware and tests keep passing unchanged (`go test ./...`, and `go test ./internal/store/...` against Postgres in CI).
- Gcore base URL `https://api.gcore.com`. Auth header `Authorization: APIKey <token>`. Endpoints: `POST /streaming/streams` `{"name":..., "active": false}`, `GET /streaming/streams`, `PATCH /streaming/streams/{id}` `{"active": bool}`, `DELETE /streaming/streams/{id}` (204).
- Config: `GCORE_API_KEY` (required when `APP_ENV=production`), `GCORE_API_BASE` (default `https://api.gcore.com`).
- Transport: `MaxIdleConnsPerHost: 20`, `IdleConnTimeout: 90 * time.Second`, built once in `gcore.New`. Add `Timeout: 10 * time.Second` on the `http.Client`.
- Status TTL: `liveSyncTTL = 5 * time.Second`. Abandon threshold: `models.AbandonAfter = 10 * time.Minute`. Watch-page poll interval: 10 s.
- CSP additions: `frame-src 'self' https://player.gvideo.co`, and add `https://whip.gvideo.co` to `connect-src`.
- **Conflict resolved (Rule 7):** the spec's Routes section says `/stream/start` stamps `started_at` and `/stream/stop` stamps `ended_at`. The later "Decisions from review" section says these track the `live` flip and are written by the sync code. **The decisions win.** `/stream/start` stamps `armed_at` only. `/stream/stop` sets `ended_at` only when it forces `live` from true to false.
- **Convention call:** the URL/text columns are `notnull, default ''`, the way every text column in `20261017000001_init.go` is. The spec listed them as nullable. Go reads both as `""`, so behaviour is the same.
- Templates: after any `.templ` edit run `go tool templ generate` and commit the `*_templ.go` files. CI fails on a stale diff.
- Commit messages end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Review Focus

1. **Double "Start" (two tabs or a double-click) on a first-ever stream.** It must not leave an orphaned Gcore stream or return a 500. `CreateLiveStream` uses `ON CONFLICT (user_id) DO NOTHING`, and the loser deletes the Gcore stream it just created. Tested in Task 3 (store) and Task 4 (the handler path, by pre-creating the row).
2. **Gcore is down or returns 5xx during a public read.** `/`, `/live/:u` and `/live/:u/status` still answer 200 from the cached DB state, and the failure is logged at warn. Tested in Task 5.
3. **A stream was deleted on the Gcore side, so its id is missing from the list.** The row is treated as `active=false, live=false`, with no crash and no retry loop. Tested in Task 5.
4. **The watch page or status JSON leaks secrets.** Anonymous `GET /live/:u` must not contain the push URL, the WHIP URL, the stream key, or the API key. Tested in Task 5.
5. **Bad title input:** whitespace only, more than 100 runes, or HTML. Trim it; reject more than 100 runes with a flash and leave the stream disarmed; templ escapes HTML. Tested in Task 4.

---

## File Structure

| File | Status | Responsibility |
|---|---|---|
| `internal/config/config.go` | modify | `GcoreAPIKey`, `GcoreAPIBase`, `GcoreEnabled()`, production validation |
| `.env.example`, `docker-compose.prod.yaml` | modify | document and require `GCORE_API_KEY` |
| `internal/gcore/client.go` | create | REST client: `Stream`, `APIError`, `New`, `Create`, `SetActive`, `List`, `Delete` |
| `internal/gcore/client_test.go` | create | httptest-backed client tests, plus a live smoke test that skips without a key |
| `internal/database/migrations/20261018000001_live_streams.go` | create | `live_streams` table |
| `internal/models/models.go` | modify | `LiveStream` model, `Apply`, `Abandoned`, `State`, registered in `All()` |
| `internal/models/models_test.go` | create | pure tests for flip/abandon logic |
| `internal/store/live_streams.go` | create | live_streams queries |
| `internal/store/store_test.go` | modify | `TestLiveStreamStore` |
| `internal/handlers/live.go` | create | `refreshLive`, `stopStream`, and the LiveDashboard, StartStream, StopStream, WatchLive and LiveStatus handlers |
| `internal/handlers/handler.go` | modify | `Gcore *gcore.Client`, `live liveSync` fields; construct the client in `New` |
| `internal/handlers/home.go`, `admin.go`, `users.go` | modify | Live-now strip, admin table and terminate, account-deletion cleanup |
| `internal/server/server.go` | modify | routes and CSP |
| `internal/server/server_test.go` | modify | fake Gcore server, `client.st`, live flow tests |
| `internal/view/live.templ` | create | `LiveDashboardPage`, `WatchPage`, `liveCard` |
| `internal/view/types.go` | modify | `LiveDashboardData`, `WatchData`, `HomeData.Live`, `AdminData.Live` |
| `internal/view/helpers.go` (+ new `helpers_test.go`) | modify | `liveURL`, `splitRTMP` |
| `internal/view/home.templ`, `admin.templ`, `layout.templ`, `dashboard.templ` | modify | strip, admin table, nav link |
| `web/static/js/whip-broadcast.js` | create | browser WHIP broadcaster |
| `web/static/js/app.js` | modify | watch-page status poll |
| `web/static/css/app.css` | modify | live badge and strip/player layout |

---

### Task 1: Config

**Files:**
- Modify: `internal/config/config.go` (struct fields, `Load`, `validate`, new method)
- Modify: `.env.example` (new `# ---- Gcore streaming ----` block after Google sign-in)
- Modify: `docker-compose.prod.yaml` (`GCORE_API_KEY: ${GCORE_API_KEY:?set GCORE_API_KEY in .env}` under `app.environment`)
- Test: `internal/config/config_test.go` (create)

**Interfaces:**
- Produces: `Config.GcoreAPIKey string`, `Config.GcoreAPIBase string` (trailing `/` trimmed), `func (c *Config) GcoreEnabled() bool` (key is not empty).

- [ ] **Step 1: Write the failing test**

```go
// Production must refuse to boot without a Gcore key. Otherwise every "Go live" fails at runtime instead of at deploy.
func TestProductionRequiresGcoreKey(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("SESSION_SECRET", strings.Repeat("x", 32))
	t.Setenv("GOOGLE_CLIENT_ID", "id"); t.Setenv("GOOGLE_CLIENT_SECRET", "s")
	t.Setenv("GCORE_API_KEY", "")
	_, err := config.Load()
	if err == nil || !strings.Contains(err.Error(), "GCORE_API_KEY") { t.Fatalf("want GCORE_API_KEY error, got %v", err) }
	t.Setenv("GCORE_API_KEY", "k")
	cfg, err := config.Load()
	if err != nil || cfg.GcoreAPIBase != "https://api.gcore.com" || !cfg.GcoreEnabled() { t.Fatalf("%v %+v", err, cfg) }
}
```

- [ ] **Step 2: Run** `go test ./internal/config/ -run TestProductionRequiresGcoreKey -v`. Expected: FAIL (compile error: no `GcoreAPIBase`).
- [ ] **Step 3: Implement.** Add the fields and `GcoreEnabled()`. Load them with `env("GCORE_API_KEY", "")` and `strings.TrimRight(env("GCORE_API_BASE", "https://api.gcore.com"), "/")`. In the `IsProduction()` branch of `validate`, add `errors.New("GCORE_API_KEY is required in production")`. Also document both vars in `.env.example` and the compose file.
- [ ] **Step 4: Run** `go test ./internal/config/ -v && go test ./...`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(config): add GCORE_API_KEY / GCORE_API_BASE`.

---

### Task 2: Gcore client and live smoke test

**Files:**
- Create: `internal/gcore/client.go`
- Test: `internal/gcore/client_test.go`

**Interfaces:**
- Produces:
  - `type Stream struct { ID int64; Active, Live bool; PushURL, PushURLSRT, PushURLWHIP, IframeURL, HLSCMAFURL string }` with JSON tags `id, active, live, push_url, push_url_srt, push_url_whip, iframe_url, hls_cmaf_url`.
  - `type APIError struct { Status int; Body string }`. `Error()` returns `"gcore: <status>: <body>"`, with the body trimmed to 300 bytes. It is shown to the user as a flash, so it must never contain the key.
  - `func New(baseURL, apiKey string) *Client` (owns one `*http.Client` with the transport from Global Constraints)
  - `func (c *Client) Create(ctx context.Context, name string) (Stream, error)`. POST, body `{"name":name,"active":false}`.
  - `func (c *Client) SetActive(ctx context.Context, id int64, active bool) (Stream, error)`. PATCH.
  - `func (c *Client) List(ctx context.Context) ([]Stream, error)`. GET, one page. Copy the spec's `ponytail:` pagination comment here.
  - `func (c *Client) Delete(ctx context.Context, id int64) error`. Accepts 200 or 204.
  - Any non-2xx response becomes `*APIError`.

- [ ] **Step 1: Write the failing tests** (an httptest server records the method, path, `Authorization` and body):
  - `TestClientSendsAPIKeyAndShapes`: `Create(ctx,"n")` sends `POST /streaming/streams`, header `Authorization: APIKey k`, body JSON `{"name":"n","active":false}`, and decodes `push_url_whip` and `iframe_url` from the reply. `SetActive(ctx, 7, true)` sends `PATCH /streaming/streams/7` with `{"active":true}`. `List` decodes a 2-element array. `Delete(ctx, 7)` sends `DELETE /streaming/streams/7`, and a 204 reply returns nil.
  - `TestClientErrorDoesNotLeakKey`: the server replies 403 with body `{"error":"quota"}`. Then `errors.As(err, &apiErr)` holds, `apiErr.Status == 403`, `err.Error()` contains `quota`, and it does **not** contain the key `"secret-key"`.
  - `TestLiveSmoke`: `t.Skip("set GCORE_SMOKE_KEY to run against the real account")` unless `GCORE_SMOKE_KEY` is set. It runs Create, then `SetActive(true)` and asserts `.Active`, then `SetActive(false)`, then a deferred `Delete`. This is the early check the spec asks for on the untested PATCH call.
- [ ] **Step 2: Run** `go test ./internal/gcore/ -v`. Expected: FAIL (package does not compile).
- [ ] **Step 3: Implement** `client.go` with one private `do(ctx, method, path string, body, out any) error` helper that all four methods share.
- [ ] **Step 4: Run** `go test ./internal/gcore/ -v`. Expected: the two tests PASS and `TestLiveSmoke` SKIPs. Then, if the key is available, **run once for real:** `GCORE_SMOKE_KEY=<key> go test ./internal/gcore/ -run TestLiveSmoke -v`. Expected: PASS. If the PATCH fails, stop and report: the whole go-live flow depends on it.
- [ ] **Step 5: Commit** `feat(gcore): stdlib REST client for Gcore streams`.

---

### Task 3: Migration, model, store

**Files:**
- Create: `internal/database/migrations/20261018000001_live_streams.go`
- Modify: `internal/models/models.go`
- Create: `internal/models/models_test.go`
- Create: `internal/store/live_streams.go`
- Modify: `internal/store/store_test.go`

**Interfaces:**
- Produces (models):
  - `const AbandonAfter = 10 * time.Minute`
  - `type LiveStream struct`: `bun:"table:live_streams,alias:ls"`. Fields: `UserID int64 (pk)`, `User *User (rel:belongs-to,join:user_id=id)`, `GcoreStreamID int64`, `Title`, `PushURL`, `PushURLSRT`, `PushURLWHIP`, `IframeURL string`, `Active`, `Live bool`, `ArmedAt`, `StartedAt`, `EndedAt time.Time (nullzero)`, `SyncedAt time.Time`, and the embedded `timestamps` plus a `BeforeAppendModel` hook. Give the URL fields explicit column tags (`push_url_srt`, `push_url_whip`, `iframe_url`) because bun's default snake-casing of `PushURLSRT` is wrong.
  - `func (s *LiveStream) Apply(active, live bool, now time.Time)`. Sets `StartedAt=now` when `live` flips false→true and `EndedAt=now` when it flips true→false. Sets `ArmedAt=now` when `active` flips false→true. Always sets `SyncedAt=now`.
  - `func (s *LiveStream) Abandoned(now time.Time) bool`. Returns `Active && !Live && now.Sub(latest(ArmedAt, EndedAt)) > AbandonAfter`.
  - `func (s *LiveStream) State() string` returns `"live"`, `"armed"` or `"off"`.
- Produces (store), all taking `ctx context.Context` first:
  - `LiveStreamByUser(ctx, userID int64) (*models.LiveStream, error)`. Returns `ErrNotFound` when no row exists.
  - `CreateLiveStream(ctx, ls *models.LiveStream) (created bool, err error)`. Uses `On("CONFLICT (user_id) DO NOTHING")`. `created` means RowsAffected == 1.
  - `ArmLiveStream(ctx, userID int64, title string, now time.Time) error`. Sets `active=true, armed_at=now, title`.
  - `DisarmLiveStream(ctx, userID int64, now time.Time) error`. Sets `active=false, live=false, ended_at = CASE WHEN live THEN now ELSE ended_at END`.
  - `SyncCandidates(ctx) ([]models.LiveStream, error)`. Rows where `active OR live`.
  - `SaveLiveStatus(ctx, ls *models.LiveStream) error`. Updates the columns `active, live, armed_at, started_at, ended_at, synced_at, updated_at`.
  - `ListLive(ctx) ([]models.LiveStream, error)`. Rows where `live`, with `Relation("User")`, ordered `started_at DESC`.

- [ ] **Step 1: Write failing tests.**
  - `models_test.go` → `TestLiveStreamFlips`. On a zero stream, `Apply(true,false,t0)` sets `ArmedAt==t0` with `StartedAt` zero. Then `Apply(true,true,t1)` sets `StartedAt==t1` and leaves `ArmedAt==t0`. Repeating `Apply(true,true,t2)` leaves `StartedAt==t1` (no restamp). `Apply(true,false,t3)` sets `EndedAt==t3`.
  - `TestLiveStreamAbandoned`. `Abandoned` is false at `ArmedAt+9m`, true at `ArmedAt+11m`, and always false when `Live`. Also: armed at t0 but `EndedAt` at t0+8m means not abandoned at t0+11m, because the clock restarts when live last went false (spec: "or since live last went false").
  - `store_test.go` → `TestLiveStreamStore`, after creating a user via `UpsertOAuthUser`:
    - `CreateLiveStream` returns `true`, and a second call for the same user returns `false, nil` (Review Focus 1).
    - `ArmLiveStream`, then `SyncCandidates` returns 1 row.
    - `ListLive` is empty until `Apply(true,true,now)` plus `SaveLiveStatus`. After that it returns the row with `User.Username` loaded.
    - `DisarmLiveStream` sets `ended_at` and `ListLive` is empty again.
    - `DeleteUser` makes `LiveStreamByUser` return `ErrNotFound` (cascade).
- [ ] **Step 2: Run** `go test ./internal/models/ ./internal/store/ -v`. Expected: FAIL (undefined).
- [ ] **Step 3: Implement.** Write the migration with its own snapshot struct (per the `migrations.go` doc). Use `user_id` as the PK and FK `("user_id") REFERENCES "users" ("id") ON DELETE CASCADE`. Columns: `gcore_stream_id bigint notnull`, the text columns `notnull default ''`, the bools `notnull default false`, the three nullable timestamps, `synced_at` / `created_at` / `updated_at` `notnull default current_timestamp`. The down migration drops the table. Then write the model (register it in `All()`) and `live_streams.go`.
- [ ] **Step 4: Run** `go test ./internal/models/ ./internal/store/ -v`. Expected: PASS. If Postgres is available, also run `make test-pg PG=...`, since `ON CONFLICT` and the boolean defaults must work on both databases.
- [ ] **Step 5: Commit** `feat(store): live_streams table, model and queries`.

---

### Task 4: Status sync, go-live control panel, start/stop

**Files:**
- Create: `internal/handlers/live.go`
- Modify: `internal/handlers/handler.go` (fields, and `h.Gcore = gcore.New(cfg.GcoreAPIBase, cfg.GcoreAPIKey)` in `New`)
- Create: `internal/view/live.templ` (this task adds `LiveDashboardPage`)
- Modify: `internal/view/types.go`, `internal/view/helpers.go`; create `internal/view/helpers_test.go`
- Modify: `internal/view/layout.templ` (add `<a role="menuitem" href="/dashboard/live">Go live</a>` after Dashboard in `userMenu`), `internal/view/dashboard.templ` (add an outline "Go live" button next to "New post")
- Modify: `internal/server/server.go` (routes), `internal/server/server_test.go`

**Interfaces:**
- Consumes: Task 1 `GcoreEnabled()`, Task 2 `*gcore.Client`, Task 3 store and model.
- Produces (handlers):
  - `type liveSync struct { mu sync.Mutex; last time.Time }` as `Handler.live`.
  - `func (h *Handler) refreshLive(ctx context.Context)`. Never returns an error, so callers never fail on it.
  - `func (h *Handler) invalidateLive()`. Zeroes `last` so the next read syncs.
  - `func (h *Handler) stopStream(ctx context.Context, ls *models.LiveStream) error`
  - Handlers: `LiveDashboard`, `StartStream`, `StopStream`.
- Produces (view):
  - `type LiveDashboardData struct { Stream *models.LiveStream; Configured bool; Title string }`
  - `func liveURL(u *models.User) string` returns `"/live/" + url.PathEscape(u.Username)`.
  - `func splitRTMP(pushURL string) (server, key string)` splits at the last `/`. OBS wants a server URL and a stream key separately.
- Routes (in `server.go`, under "Account"):
  - `app.Get("/dashboard/live", requireUser, handlers.NoStore, h.LiveDashboard)`
  - `app.Post("/stream/start", requireUser, writeLimiter, h.StartStream)`
  - `app.Post("/stream/stop", requireUser, h.StopStream)`

`refreshLive` algorithm (the signature doesn't pin this down):

```
lock mu; defer unlock
// ponytail: single-instance in-memory cache + mutex (copy spec comment verbatim)
if !GcoreEnabled() || time.Since(last) < liveSyncTTL { return }
last = now
rows := Store.SyncCandidates; if err or len(rows)==0 { return }   // zero Gcore calls when nobody is streaming
list, err := Gcore.List; if err { Log.Warn("gcore list failed", "err", err); return }  // serve cached DB state
byID := index list by ID
for each row:
    st, ok := byID[row.GcoreStreamID]      // missing => deleted on Gcore: treat as off
    row.Apply(ok && st.Active, ok && st.Live, now)
    if row.Abandoned(now) { if _, err := Gcore.SetActive(ctx,id,false); err == nil { row.Apply(false,false,now) } else warn }
    Store.SaveLiveStatus(row)  (warn on error)
```

`StartStream` flow:
1. Trim the `title` form field. If it is more than 100 runes, redirect `/dashboard/live` with error `"Keep the title under 100 characters."`.
2. If `!GcoreEnabled()`, error `"Live streaming isn't configured on this server."`.
3. `LiveStreamByUser`. On `ErrNotFound`: `gcore.Create(ctx, fmt.Sprintf("%s-%s-%d", AppName, Username, ID))`, then `CreateLiveStream` with the URLs from the response. If it returns `created=false`, `Gcore.Delete(newID)` (log an error on failure) and re-read the row.
4. `Gcore.SetActive(id, true)`. On error, redirect with error `err.Error()` and leave the row disarmed (spec: quota decision).
5. `ArmLiveStream(userID, title, now)`, then `invalidateLive()`, then redirect `/dashboard/live` with success `"You're armed. Start sending video to go live."`.

`StopStream`: if there is no row, redirect with info `"You're not streaming."`. Otherwise run `stopStream`, then redirect with success `"Stream stopped."`. `stopStream` is `Gcore.SetActive(false)` → `DisarmLiveStream` → `invalidateLive()`. Return the Gcore error without touching the DB, so the UI never claims "stopped" while Gcore is still armed.

`LiveDashboardPage` content: use the Oat components already in `dashboard.templ`, i.e. `page-head`, `card`, `badge`, `button outline small`.
- A state badge from `State()`.
- A title form that POSTs to `/stream/start` with `CSRFField`. The button reads "Start stream", or "Update title" when armed.
- A stop form with `id="stream-stop-form"`, shown when `Active`, that POSTs to `/stream/stop`.
- An OBS card with Server and Stream key from `splitRTMP(PushURL)`, plus the SRT URL, each with a `data-copy` button (existing `app.js` handler). Hide the key behind `<details>`.
- A "Watch page" link via `liveURL`.
- When `!Configured`, an `EmptyState` explaining that `GCORE_API_KEY` is missing.
- Task 6 adds the browser panel.

- [ ] **Step 1: Write failing tests.**
  - `helpers_test.go` → `TestSplitRTMP`: `"rtmp://h/in/123?tok"` gives `("rtmp://h/in", "123?tok")`.
  - In `server_test.go`, add a fake Gcore: `type fakeGcore struct{ mu sync.Mutex; streams map[int64]*gcore.Stream; nextID int64; calls []string /* "METHOD /path" */; failList, failDelete bool; patchErr string /* non-empty => 403 with this body */; onCreate func() }`. Add `func newFakeGcore(t *testing.T) (*fakeGcore, *httptest.Server)`. It serves the four endpoints and rejects any request without `Authorization: APIKey test-key` with a 401. It also needs `setLive(id int64, live bool)` and a `withGcore(url string) func(*config.Config)` mutator for `newApp`. Add `st *store.Store` to the test `client` struct so tests can assert on rows.
  - `TestGoLiveFlow` (signed in as grace):
    1. GET `/dashboard/live` is 200 and contains `Start stream`.
    2. POST `/stream/start` with title `My first stream` → 303 to `/dashboard/live`. The fake has 1 stream with `Active==true`, and the row has `Active`, `ArmedAt` set and `StartedAt` zero (Global Constraints conflict note).
    3. A second POST `/stream/start` makes no second `POST /streaming/streams` (reuse).
    4. POST `/stream/stop` → the fake stream `Active==false` and the row is not active.
  - `TestStartStreamValidatesTitle` (Review Focus 5): a 101-rune title gives a 303 with no Gcore calls and no row. A title `<b>x</b>` renders escaped (`&lt;b&gt;`) on `/dashboard/live`.
  - `TestStartStreamQuotaError`: the fake returns 403 `{"error":"quota exceeded"}` on PATCH. The flash shown on the next GET `/dashboard/live` contains `quota exceeded` and does not contain `test-key`, and the row is not active.
  - `TestStartStreamCreateRace` (Review Focus 1): set `fake.onCreate` to insert grace's row via `c.st.CreateLiveStream` with a different `GcoreStreamID`. That simulates the other tab winning between the lookup and the insert. Then POST `/stream/start`. Assert a 303 (not a 500), that the fake received `DELETE /streaming/streams/<id it just created>`, and that the row keeps the winner's `GcoreStreamID`.
  - `TestStartStreamWithoutGcoreConfigured`: default `newApp` (no key). POST `/stream/start` → flash containing `isn't configured`, and `GET /` stays 200.
- [ ] **Step 2: Run** `go test ./internal/view/ ./internal/server/ -v`. Expected: FAIL.
- [ ] **Step 3: Implement** `live.go`, the handler fields, the view data types, the helpers, `live.templ`, the nav link and the routes. Then run `go tool templ generate`.
- [ ] **Step 4: Run** `go vet ./... && go test ./...`. Expected: PASS, including all pre-existing tests.
- [ ] **Step 5: Commit** `feat(live): go-live control panel with start/stop`.

---

### Task 5: Public watch page, status poll, homepage strip, CSP

**Files:**
- Modify: `internal/handlers/live.go` (`WatchLive`, `LiveStatus`), `internal/handlers/home.go`
- Modify: `internal/view/live.templ` (`WatchPage`, `liveCard`), `internal/view/home.templ`, `internal/view/types.go`
- Modify: `internal/server/server.go` (routes, `contentSecurityPolicy`)
- Modify: `web/static/js/app.js`, `web/static/css/app.css`
- Test: `internal/server/server_test.go`

**Interfaces:**
- Consumes: `refreshLive`, `liveURL`, `Store.ListLive`, `Store.LiveStreamByUser`, `Store.UserByUsername`.
- Produces:
  - `type WatchData struct { Streamer *models.User; Stream *models.LiveStream }` and `func (d WatchData) IsLive() bool`.
  - `HomeData.Live []models.LiveStream`.
  - `templ liveCard(ls *models.LiveStream)`, used only by the home strip.
- Routes (Public pages block): `app.Get("/live/:username", h.WatchLive)`, `app.Get("/live/:username/status", h.LiveStatus)`. They get no limiter, because Gcore load is capped at one List per TTL whatever the traffic.

Behaviour:
- `WatchLive`: an unknown username returns `notFound()`. Call `refreshLive`. A missing row means not live. Render `WatchPage`, which shows the streamer's avatar, name and title. When live it shows `<div data-live-status={ liveURL(u)+"/status" }>` wrapping `<iframe src={ templ.URL(IframeURL) } allow="autoplay; fullscreen; picture-in-picture" allowfullscreen title=...>` (16:9 via CSS), plus a hidden `<p data-live-ended hidden>This stream has ended.</p>`. When not live it shows an `EmptyState` with `"<Name> isn't live right now"` and a link to their `/u/` profile. The page must render no push or WHIP URL.
- `LiveStatus`: an unknown username → `notFound()`. Otherwise call `refreshLive`, set `Cache-Control: no-store`, and return `c.JSON(fiber.Map{"live": bool})`.
- `Home`: call `refreshLive` and `ListLive` only when `query == "" && page.Number == 1` (same condition as the hero). The strip renders above `layout-with-aside` only when `len(d.Live) > 0`, with heading `Live now` and a grid of `liveCard` (avatar, name, title, a red `LIVE` badge, link to `liveURL`).
- `app.js`: if `[data-live-status]` exists, call `setInterval` every 10 s to `fetch(url, {headers: {'X-Requested-With': 'fetch'}})`. When the response is `!live`, remove the iframe, unhide `[data-live-ended]` and clear the interval. Ignore network errors, so a missed poll doesn't end the stream.
- CSP: add `"frame-src 'self' https://player.gvideo.co; "` and change `connect-src 'self'` to `connect-src 'self' https://whip.gvideo.co`.

- [ ] **Step 1: Write failing tests** in `server_test.go`. Set the fake's stream fields: `PushURL "rtmp://push/in/9?SECRETTOKEN"`, `PushURLWHIP "https://whip.gvideo.co/9_SECRETTOKEN/whip"`, `IframeURL "https://player.gvideo.co/streams/9"`.
  - `TestWatchLive`: grace starts streaming and `fake.setLive(id, true)`. Then anonymous GET `/live/grace` is 200, contains `player.gvideo.co/streams/9`, and contains **neither** `SECRETTOKEN` **nor** `test-key` (Review Focus 4). GET `/live/grace/status` returns `{"live":true}`. GET `/` contains `Live now` and `href="/live/grace"`. After POST `/stream/stop` (which invalidates the cache), `/live/grace/status` returns `{"live":false}`, and `/` no longer contains `Live now`.
  - `TestWatchLiveNotStreaming`: GET `/live/ada` (no row) is 200 and contains `isn't live right now`. GET `/live/nobody` and `/live/nobody/status` are 404.
  - `TestLiveSyncSurvivesGcoreOutage` (Review Focus 2): grace is armed, then `fake.failList = true`. GET `/`, `/live/grace` and `/live/grace/status` are all 200.
  - `TestLiveSyncStreamDeletedOnGcore` (Review Focus 3): grace is armed and live, then the stream is deleted from the fake's map. GET `/live/grace/status` returns `{"live":false}`, and the row is `Active==false`.
  - `TestAbandonedStreamAutoDisarms`: grace is armed (not live). Run `c.st.DB.NewUpdate()` to set `armed_at` to now minus 11 minutes, then GET `/live/grace` (first read after start, so the cache is invalidated). The fake receives `PATCH .../{id}` with `active:false`, and the row is not active.
  - `TestCSPAllowsGcorePlayer`: the `Content-Security-Policy` header of `GET /` contains `frame-src 'self' https://player.gvideo.co` and `connect-src 'self' https://whip.gvideo.co`.
- [ ] **Step 2: Run** `go test ./internal/server/ -v`. Expected: FAIL.
- [ ] **Step 3: Implement** the handlers, views, routes, CSP, JS and CSS above, then run `go tool templ generate`.
- [ ] **Step 4: Run** `go vet ./... && go test ./...`. Expected: PASS.
- [ ] **Step 5: Manual check** with `make dev` and a real key. Arm a stream from OBS, confirm `/` shows it under Live now, and confirm the iframe plays on `/live/<you>` with no CSP errors in the console. Stop OBS: within about 15 s the watch page should flip to "This stream has ended." without a reload.
- [ ] **Step 6: Commit** `feat(live): public watch page, status poll and Live now strip`.

---

### Task 6: Browser WHIP broadcaster

**Files:**
- Create: `web/static/js/whip-broadcast.js`
- Modify: `internal/view/live.templ` (browser panel in `LiveDashboardPage`)
- Test: `internal/server/server_test.go`

**Interfaces:**
- Consumes: `LiveStream.PushURLWHIP`, the stop form `#stream-stop-form` from Task 4.
- Produces: the panel markup contract the JS reads:
  - `<section data-whip data-whip-url={ PushURLWHIP }>` containing radios `name="whip-source"` with values `camera` and `screen`, `<video data-whip-preview muted playsinline autoplay>`, `<button data-whip-start>`, `<button data-whip-stop disabled>`, and `<p data-whip-status aria-live="polite">`.
  - It also carries `<script src={ asset("/static/js/whip-broadcast.js") } defer></script>`. Inline scripts are blocked by CSP.
  - The panel renders only when `Stream.Active && Stream.PushURLWHIP != ""`. You arm first with the normal Start form, then go live from the browser.

JS flow, in an IIFE with no globals, following the spec's four steps:
1. `getUserMedia({video:true,audio:true})` or `getDisplayMedia({video:true,audio:true})` depending on the radio. Show it in the preview.
2. `pc = new RTCPeerConnection()`, then `addTransceiver(track, {direction:'sendonly'})` for each track. Call `createOffer`, then `setLocalDescription`. Wait for `iceGatheringState === 'complete'`, with a 2 s timeout, since non-trickle WHIP needs the candidates in the offer.
3. `fetch(whipURL, {method:'POST', headers:{'Content-Type':'application/sdp'}, body: pc.localDescription.sdp})`. Anything other than 201/200 is an error shown in `[data-whip-status]`. Then `setRemoteDescription({type:'answer', sdp: await resp.text()})`.
4. Keep `new URL(resp.headers.get('Location'), whipURL)` when present. **Note:** cross-origin `Location` is only readable if Gcore sends `Access-Control-Expose-Headers: Location`. If it's null, Stop just closes the peer connection.
5. Stop (button, or the `ended` event of a track when the user stops sharing from the browser chrome): `DELETE` the resource URL if known (ignore errors), call `pc.close()`, stop every track, then `document.getElementById('stream-stop-form').requestSubmit()` to disarm via `/stream/stop`.

- [ ] **Step 1: Write the failing test** `TestWhipPanelOnlyWhenArmed`. GET `/dashboard/live` before start has no `data-whip`. After start it contains `data-whip-url="https://whip.gvideo.co/` and `whip-broadcast.js`. Anonymous GET `/static/js/whip-broadcast.js` is 200.
- [ ] **Step 2: Run** `go test ./internal/server/ -run TestWhipPanelOnlyWhenArmed -v`. Expected: FAIL.
- [ ] **Step 3: Implement** the panel and the JS, then run `go tool templ generate`.
- [ ] **Step 4: Run** `go test ./...`. Expected: PASS.
- [ ] **Step 5: Manual CORS check** (the risk flagged in the spec). With a real key and `make dev`:
  1. Arm, choose Camera, click Start.
  2. Confirm in DevTools that the WHIP POST succeeds with no CORS error, that `/live/<you>` shows the video, and that the response headers include `Access-Control-Expose-Headers` with `Location`.
  3. Confirm that no `Permissions-Policy` header blocks `camera` or `display-capture`.
  4. Click Stop and confirm the stream ends.
  - **If CORS blocks the POST:** stop and report. The documented fallback is an authenticated `POST /stream/whip` proxy that relays only SDP bytes to the owner's own `push_url_whip`. It is not built in this plan.
- [ ] **Step 6: Commit** `feat(live): browser WHIP broadcaster`.

---

### Task 7: Admin "Live now" table and terminate

**Files:**
- Modify: `internal/handlers/admin.go`, `internal/view/admin.templ`, `internal/view/types.go`, `internal/server/server.go`
- Test: `internal/server/server_test.go`

**Interfaces:**
- Consumes: `Store.ListLive`, `Store.LiveStreamByUser`, `h.stopStream`.
- Produces: `AdminData.Live []models.LiveStream`, `func (h *Handler) TerminateStream(c fiber.Ctx) error`, route `admin.Post("/live-streams/:user_id<int>/terminate", h.TerminateStream)`.

Behaviour: `Admin` adds `Live` from `ListLive` without calling `refreshLive`, so the admin page never waits on Gcore. `TerminateStream` mirrors `SetUserRole`:
1. `id := fiber.Params[int64](c, "user_id")`.
2. `LiveStreamByUser(id)`; on error, `orNotFound`.
3. `stopStream`. On a Gcore error, redirect `/admin` with error `err.Error()`.
4. Otherwise redirect `/admin` with success `"Stream stopped for <username>."` (load the username via `UserByID`).

In `admin.templ`, add a `<section class="mt-8" aria-labelledby="live-heading">` with `<h2 id="live-heading">Live now</h2>` before Users. Use the same `.table` markup with the columns User, Title, Started (`timeAgo(StartedAt)`), and Actions: a form with `data-confirm="Stop this stream?"` and a `button outline small data-variant="danger"` labelled "Stop". When the list is empty, show `<p class="text-light">Nobody is live.</p>`.

- [ ] **Step 1: Write failing tests.**
  - `TestAdminTerminatesStream`: grace starts and goes live (`setLive`). Admin GET `/admin/` contains `Live now` and `grace`. Admin POST `/admin/live-streams/<graceID>/terminate` gives a 303 to `/admin`, the fake stream `Active==false`, and the row is not live.
  - `TestTerminateRequiresAdmin`: grace POSTs to `/admin/live-streams/<adaID>/terminate` and gets 403.
  - `TestTerminateUnknown`: an admin POST for a user with no stream gets 404.
- [ ] **Step 2: Run** `go test ./internal/server/ -run Terminate -v`. Expected: FAIL.
- [ ] **Step 3: Implement**, then run `go tool templ generate`.
- [ ] **Step 4: Run** `go test ./...`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(admin): live-now table with terminate`.

---

### Task 8: Account deletion cleans up the Gcore stream

**Files:**
- Modify: `internal/handlers/users.go` (`DeleteAccount`, between the confirm check and `Store.DeleteUser`)
- Test: `internal/server/server_test.go`

**Interfaces:**
- Consumes: `Store.LiveStreamByUser`, `Gcore.Delete`.

Behaviour: if a row exists and `GcoreEnabled()`, call `Gcore.Delete(ctx, ls.GcoreStreamID)`. On error, `h.Log.Error("orphaned gcore stream", "user_id", u.ID, "gcore_stream_id", ls.GcoreStreamID, "err", err)` and **continue** (spec decision: user data removal wins). The row disappears through the FK cascade.

- [ ] **Step 1: Write failing tests.**
  - `TestDeleteAccountDeletesGcoreStream`: grace starts a stream, then POST `/settings/delete` with `confirm=grace` gives a 303 to `/`. The fake has a DELETE for the stream id and no longer holds it.
  - `TestDeleteAccountSurvivesGcoreFailure`: the fake returns 500 on DELETE. The account is still deleted (`c.st.UserByUsername("grace")` returns `ErrNotFound`).
- [ ] **Step 2: Run** `go test ./internal/server/ -run DeleteAccount -v`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go vet ./... && go test -race ./...`. Expected: PASS. Also run `go tool templ generate && git diff --exit-code -- '*_templ.go'`. Expected: no diff (mirrors CI).
- [ ] **Step 5: Commit** `feat(live): delete Gcore stream on account deletion`.

---

## Spec coverage check

| Spec section | Task |
|---|---|
| Gcore API shapes, auth, live PATCH smoke test | 2 |
| Data model `live_streams` | 3 |
| Status sync, TTL, filter-by-our-ids, auto-disarm after 10 min, started/ended on live flip | 3 (logic), 4 (`refreshLive`), 5 (tests) |
| `/dashboard/live`, `/stream/start`, `/stream/stop`, quota error shown, nav link | 4 |
| `/live/:username`, `/live/:username/status`, homepage strip | 5 |
| Browser WHIP + CORS risk + proxy fallback | 6 (fallback documented, not built) |
| Gcore client, pooled transport | 2 |
| Config, production requirement | 1 |
| CSP | 5 |
| Admin Live now + terminate | 7 |
| Account deletion, proceed on Gcore failure | 8 |
| Non-goals (VOD, chat, viewer counts, browse page) | not built, by design |
