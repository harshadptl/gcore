# Live streaming — design

## Intent

Turn the existing Go/Fiber blog into a blog *and* livestreaming app. Any
signed-in (Google OAuth) user can go live; anyone can watch a live stream
without signing in. Gcore's Streaming API does ingest, transcode, and
playback; we only manage the Gcore stream resource per user and the
pages around it.

## Goals

- A signed-in user can start a stream (RTMP via OBS/Streamlabs, or
  straight from the browser with camera/screen) and stop it.
- Each user has exactly one Gcore stream resource, reused across
  sessions. One Gcore resource per user automatically gives "one active
  stream per user" for free — there's nothing else for a second stream
  to collide with.
- Anyone, logged in or not, can see which streams are currently live on
  the homepage and watch one at `/live/:username` with no login.
- The blog (posts, tags, comments, likes) is untouched.

## Non-goals (explicit scope cuts, my call — flag if you want these)

- No VOD/replay. When a stream ends, the watch page just says so.
- No live chat, no viewer counts, no websockets.
- No dedicated `/live` browse-all page — the homepage "Live now" strip
  is the only discovery surface for v1.
- No stream moderation UI for admins (existing admin role is untouched).

## Anti-goals (would make this a failure even if it "works")

- The Gcore account API key must never reach the browser. All
  account-level Gcore REST calls (create/patch/list/delete stream) are
  server-side only.
- One user must never be able to see or control another user's stream
  resource.
- Nothing here breaks the existing blog routes, CSP, or middleware.

## Constraints

- Go, Fiber v3, templ, Bun/SQLite — match existing conventions in
  `internal/handlers`, `internal/store`, `internal/view` (one file per
  concern, same naming).
- No new Go dependencies: `net/http` is enough for the Gcore REST client.
  No JS framework: the browser broadcaster is vanilla JS using
  `RTCPeerConnection` + `fetch`, no WHIP client library.
- Single server instance assumed (matches current deploy). An in-memory
  cache with a mutex is fine; no Redis.

## Gcore API, confirmed live against the account

- Base URL: `https://api.gcore.com`
- Auth: `Authorization: APIKey <token>` (verified: 200 on a real call)
- `POST /streaming/streams` body `{"name": "...", "active": false}` →
  200, full stream object (verified live, then deleted the test stream)
- `GET /streaming/streams` → array of all stream objects on the account
  (not scoped to "ours" — we must filter by the IDs we know about)
- `GET /streaming/streams/{id}` → single stream object (same shape;
  assumed from the list endpoint's shape, not separately tested)
- `PATCH /streaming/streams/{id}` body `{"active": true|false}` → arms
  the stream to accept ingest, or disarms it. **Not live-tested yet** —
  strongly implied by Gcore's own tutorial (its reuse example shows this
  call) but the builder should smoke-test this specific call early,
  since the whole "go live" flow depends on it.
- `DELETE /streaming/streams/{id}` → 204 (verified live)
- Fields we use from the stream object: `id`, `live` (bool, actually
  receiving video), `active` (bool, armed to receive), `push_url`,
  `push_url_srt`, `push_url_whip` (full WHIP ingest URL, token embedded
  in the path — no separate auth needed for the browser to POST here),
  `iframe_url` (hosted player embed), `hls_cmaf_url`.

## Data model

New table `live_streams`, 1:1 with `users`:

```
user_id          PK, FK -> users.id
gcore_stream_id  bigint, not null
title            text, not null default ''
push_url         text
push_url_srt     text
push_url_whip    text
iframe_url       text
active           bool, not null default false   -- cached from Gcore
live             bool, not null default false    -- cached from Gcore
armed_at         nullable timestamp   -- when active last flipped true
started_at       nullable timestamp
ended_at         nullable timestamp
synced_at        timestamp   -- last time we refreshed active/live from Gcore
created_at / updated_at      -- existing `timestamps` embed, same as other models
```

Row is created lazily on a user's first "go live"; reused every time
after.

## Status sync (no cron, no webhook, no background worker)

A single in-process cache, refreshed on demand:

```
# ponytail: single-instance in-memory cache + mutex; if this ever runs
# as >1 process, move the TTL check to the DB (e.g. synced_at column,
# already there) or add a shared cache.
```

Any read that needs current live/active status (homepage strip, the
watch page, the status-poll endpoint) calls a `refreshIfStale(ttl)`
helper first: if `now - synced_at > ttl` (suggest 5–10s) for the rows in
question, do one `GET /streaming/streams`, match entries by
`gcore_stream_id` against our known IDs (ignore any account streams that
aren't ours — the account may have other test streams on it, confirmed
live), update the matching `live_streams` rows, bump `synced_at`. Then
serve from the DB.

```
# ponytail: GET /streaming/streams returns one page; if the Gcore
# account ever holds enough streams to paginate, this stops seeing all
# of them. Upgrade to a pagination loop if/when that's true — not
# expected for this app's scale.
```

## Routes

- `GET /` — homepage gains a "Live now" strip above the post list:
  cards for every `live_streams` row where `live = true` (after a
  stale-refresh), each linking to `/live/:username`.
- `GET /dashboard/live` (signed in) — control panel: current state (not
  started / armed / live), a title field, the RTMP/SRT push URL + key
  for OBS (copy-to-clipboard, matches existing UI patterns), and a
  "Go live from browser" panel (camera/screen toggle, preview, Start /
  Stop).
- `POST /stream/start` (signed in) — creates the Gcore stream if the
  user has none yet, else reuses it; `PATCH active:true`; saves title;
  stamps `armed_at`.
- `POST /stream/stop` (signed in) — `PATCH active:false`. (`started_at`
  / `ended_at` are written by the status sync when `live` flips; see
  Decisions from review.)
- `GET /live/:username` — public, no auth/CSRF. If not live (after a
  stale-refresh), renders "not live right now". If live, embeds Gcore's
  `iframe_url` in an `<iframe>` — no HLS player of our own to build or
  ship.
- `GET /live/:username/status` — public JSON `{"live": bool}`, polled
  by a small JS snippet every ~10s on the watch page; flips the page to
  "stream ended" without a full reload when it goes false.

## Browser broadcaster (WHIP)

New static file, `web/static/js/whip-broadcast.js`:

1. `getUserMedia` (camera+mic) or `getDisplayMedia` (screen share),
   user's toggle choice.
2. `new RTCPeerConnection()`, add the tracks, create an offer,
   `setLocalDescription`.
3. `fetch(push_url_whip, { method: "POST", headers: {"Content-Type":
   "application/sdp"}, body: offer.sdp })` — straight from the browser
   to Gcore, no proxy through our server. The response is the SDP
   answer; `setRemoteDescription` it.
4. The WHIP response's `Location` header is the per-session resource
   URL; the Stop button sends `DELETE` there to end the WebRTC session,
   and separately calls our own `POST /stream/stop` to disarm the Gcore
   stream.

**Open risk, flagged for the builder:** this assumes Gcore's WHIP
endpoint allows cross-origin `fetch` from the browser (the WHIP spec
expects this, but it wasn't verified live). If CORS blocks it, the
fallback is a thin server-side proxy route that relays the SDP
offer/answer bytes — it only forwards bytes, never the account API key
(the WHIP URL's token is already stream-scoped), so it's a small
addition if needed.

## Gcore client

`internal/gcore/client.go` — stdlib `net/http`, no SDK:

- `Create(ctx, name string) (Stream, error)`
- `SetActive(ctx, id int64, active bool) (Stream, error)`
- `List(ctx) ([]Stream, error)`
- `Delete(ctx, id int64) error`

`Stream` is a small struct with just the fields we use (listed above).

The client uses one shared `*http.Client` (package-level, built once in
`gcore.New`), not `http.DefaultClient` per call. Every request goes to
the same host (`api.gcore.com`), so the transport is tuned for that:

```go
&http.Transport{
    MaxIdleConnsPerHost: 20, // default is 2 — too low for one busy host
    IdleConnTimeout:     90 * time.Second,
}
```

Plain connection pooling via Go's built-in keep-alive reuse — no new
dependency.

## Config

`internal/config.Config` gains `GcoreAPIKey` (env `GCORE_API_KEY`) and
`GcoreAPIBase` (env `GCORE_API_BASE`, default `https://api.gcore.com`).
In production, require `GcoreAPIKey` to be set, same pattern as the
existing Google OAuth validation.

## CSP (internal/server/server.go)

The current policy has no `frame-src`, so it falls back to `default-src
'self'` — which blocks embedding Gcore's player iframe. Add:

- `frame-src 'self' https://player.gvideo.co` — the iframe embed domain
  (confirmed live from `iframe_url`).
- `connect-src` gains `https://whip.gvideo.co` — for the browser's WHIP
  POST/DELETE.

## Admin

The existing admin page (`internal/handlers/admin.go`, `admin.templ`)
gains a "Live now" table alongside its existing users/comments tables:
username, title, started_at, a "Stop" button per row. Reuses the
existing `admin := app.Group("/admin", requireUser, h.RequireAdmin,
...)` group — no new middleware.

- `POST /admin/live-streams/:user_id<int>/terminate` — same effect as
  the streamer's own `/stream/stop` (`PATCH active:false`, stamp
  `ended_at`), callable by an admin on any user's stream. Mirrors
  `SetUserRole`'s shape: look up the target, act, redirect to `/admin`
  with a flash message.

## Account deletion

Existing `DeleteAccount` handler should also call `gcore.Delete` for the
user's stream, if one exists, so deleting an account doesn't leave an
orphaned Gcore resource. Builder's call on exact placement.

## Decisions from review

- **Who can go live:** any signed-in Google user, as requested. No
  allowlist. Known exposure: Gcore usage is billed to the account for
  anyone who signs up. Add a gate later if that becomes a problem.
- **Gcore quota errors** (my call): if `PATCH active:true` or create
  fails (e.g. concurrent-stream quota), `/stream/start` shows the Gcore
  error message on `/dashboard/live` and leaves the row disarmed. No
  pre-emptive capacity check.
- **Abandoned armed streams** (my call): `refreshIfStale` auto-disarms
  any stream that is `active && !live` for more than 10 minutes since it
  was armed (or since `live` last went false). 10 minutes gives an OBS
  user time to set up. Needs an `armed_at` column. No `beforeunload`
  beacon.
- **Timestamps** (my call): `started_at` / `ended_at` track the `live`
  flip (video actually flowing), written by the sync code. `armed_at`
  tracks the `active` flip.
- **Account deletion when Gcore delete fails** (my call): delete the
  account anyway and log the orphaned `gcore_stream_id` with `slog` at
  error level. User data removal wins over a leftover Gcore resource.

## Left to the builder

- Exact TTL for the status cache (suggested 5–10s).
- UI styling/copy — match existing Oat CSS components.
- Live-testing the `PATCH active` call and the browser-to-WHIP CORS
  behavior, both flagged above as unverified assumptions.
- Whether to add a nav link to `/dashboard/live` in the existing layout
  header (yes, obviously needed — just noting it's not spelled out
  route-by-route above).
