// Package seed fills a fresh database with demo users and posts.
package seed

import (
	"context"
	"fmt"

	"github.com/harshadptl/gcore/internal/markdown"
	"github.com/harshadptl/gcore/internal/models"
	"github.com/harshadptl/gcore/internal/store"
	"github.com/harshadptl/gcore/internal/textutil"
)

type demoPost struct {
	title, cover, content string
	tags                  []string
	draft                 bool
}

// Run inserts demo content. It refuses to run if posts already exist.
func Run(ctx context.Context, st *store.Store, adminEmail string) error {
	n, err := st.DB.NewSelect().Model((*models.Post)(nil)).Count(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("database already has %d posts; seed only runs on an empty database", n)
	}
	if adminEmail == "" {
		adminEmail = "admin@example.com"
	}

	ada, err := st.UpsertOAuthUser(ctx, store.OAuthProfile{Subject: "dev:" + adminEmail, Email: adminEmail, Name: "Ada Admin"}, true)
	if err != nil {
		return err
	}
	ada.Bio = "Runs this place. Writes about Go, databases and the web."
	if err := st.UpdateProfile(ctx, ada); err != nil {
		return err
	}
	grace, err := st.UpsertOAuthUser(ctx, store.OAuthProfile{Subject: "dev:grace@example.com", Email: "grace@example.com", Name: "Grace Writer"}, false)
	if err != nil {
		return err
	}
	grace.Bio = "Front-end tinkerer. CSS is my love language."
	if err := st.UpdateProfile(ctx, grace); err != nil {
		return err
	}

	md := markdown.New()
	authors := []*models.User{ada, grace}
	var created []*models.Post
	for i, p := range demoPosts {
		html, err := md.Render(p.content)
		if err != nil {
			return err
		}
		plain := md.PlainText(html)
		status := models.StatusPublished
		if p.draft {
			status = models.StatusDraft
		}
		post, err := st.CreatePost(ctx, authors[i%len(authors)].ID, store.PostInput{
			Title:          p.title,
			Excerpt:        textutil.Truncate(plain, 180),
			Content:        p.content,
			ContentHTML:    html,
			CoverImage:     p.cover,
			Status:         status,
			ReadingMinutes: textutil.ReadingMinutes(plain),
			Tags:           p.tags,
		})
		if err != nil {
			return fmt.Errorf("seed post %q: %w", p.title, err)
		}
		created = append(created, post)
	}

	if _, err := st.CreateComment(ctx, created[0].ID, grace.ID, "This is exactly the starter I was looking for. The SQLite default makes trying it out painless!"); err != nil {
		return err
	}
	if _, err := st.CreateComment(ctx, created[0].ID, ada.ID, "Thanks Grace! Switch DATABASE_URL to Postgres when you deploy."); err != nil {
		return err
	}
	if _, _, err := st.ToggleLike(ctx, grace.ID, created[0].ID); err != nil {
		return err
	}
	if _, _, err := st.ToggleLike(ctx, ada.ID, created[1].ID); err != nil {
		return err
	}
	_, _, err = st.ToggleBookmark(ctx, ada.ID, created[1].ID)
	return err
}

var demoPosts = []demoPost{
	{
		title: "Welcome to gcore",
		cover: "/static/img/cover-welcome.svg",
		tags:  []string{"Announcements", "Go"},
		content: `Welcome! This post was created by ` + "`go run ./cmd/app seed`" + `.

gcore is a starter kit for content sites built with:

- **[Fiber v3](https://gofiber.io)** for HTTP routing and middleware
- **[Bun](https://bun.uptrace.dev)** as the SQL toolkit (PostgreSQL or SQLite)
- **[templ](https://templ.guide)** for type-safe HTML components
- **[Oat UI](https://oat.ink)** for semantic, class-light styling
- **Google sign-in** with OAuth 2.0 + PKCE

## What's included

| Feature | Where to look |
| --- | --- |
| Posts with Markdown, drafts & tags | ` + "`internal/handlers/posts.go`" + ` |
| Comments, likes & reading list | ` + "`internal/handlers/comments.go`" + ` |
| Sessions & CSRF | ` + "`internal/handlers/middleware.go`" + ` |
| Migrations | ` + "`internal/database/migrations`" + ` |

> Tip: open the editor with **New post** and try the *Preview* tab.

- [x] Server-rendered, works without JavaScript
- [x] Progressive enhancement where it helps
- [ ] Your next feature
`,
	},
	{
		title: "Styling with semantic HTML and Oat UI",
		cover: "/static/img/cover-oat.svg",
		tags:  []string{"CSS", "Design"},
		content: `Oat styles plain HTML elements, so templates stay readable.

A button is just a ` + "`<button>`" + `, a card is ` + "`<article class=\"card\">`" + `, and alerts use ` + "`role=\"alert\"`" + `.

` + "```html" + `
<article class="card">
  <header><h3>Hello</h3></header>
  <p>Card body.</p>
  <footer><button data-variant="primary">Save</button></footer>
</article>
` + "```" + `

Dark mode comes for free through CSS ` + "`light-dark()`" + `. Use the moon icon in the top bar to toggle it.
`,
	},
	{
		title: "Type-safe templates with templ",
		cover: "/static/img/cover-templ.svg",
		tags:  []string{"Go", "templ"},
		content: `templ compiles ` + "`.templ`" + ` files into Go functions, so a typo in a field name is a compile error, not a blank page in production.

` + "```go" + `
templ Hello(name string) {
	<p>Hello, { name }!</p>
}
` + "```" + `

Run ` + "`make generate`" + ` (or ` + "`go tool templ generate`" + `) after editing templates. ` + "`make dev`" + ` watches and regenerates for you.
`,
	},
	{
		title:   "Notes for an upcoming post",
		tags:    []string{"Go"},
		draft:   true,
		content: "This one is a **draft**: only its author (and admins) can see it.",
	},
}
