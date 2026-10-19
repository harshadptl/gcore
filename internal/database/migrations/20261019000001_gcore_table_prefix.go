package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

// gcoreTableRenames maps each table's current name to its gcore_-prefixed
// name, in an order safe to apply (parents before the children that
// reference them) and safe to reverse (children before parents).
var gcoreTableRenames = []struct {
	from, to string
}{
	{"users", "gcore_users"},
	{"posts", "gcore_posts"},
	{"tags", "gcore_tags"},
	{"post_tags", "gcore_post_tags"},
	{"comments", "gcore_comments"},
	{"likes", "gcore_likes"},
	{"bookmarks", "gcore_bookmarks"},
	{"sessions", "gcore_sessions"},
	{"live_streams", "gcore_live_streams"},
}

func init() {
	up := func(ctx context.Context, db *bun.DB) error {
		for _, r := range gcoreTableRenames {
			if _, err := db.ExecContext(ctx, `ALTER TABLE "`+r.from+`" RENAME TO "`+r.to+`"`); err != nil {
				return err
			}
		}
		return nil
	}

	down := func(ctx context.Context, db *bun.DB) error {
		for i := len(gcoreTableRenames) - 1; i >= 0; i-- {
			r := gcoreTableRenames[i]
			if _, err := db.ExecContext(ctx, `ALTER TABLE "`+r.to+`" RENAME TO "`+r.from+`"`); err != nil {
				return err
			}
		}
		return nil
	}

	Migrations.MustRegister(up, down)
}
